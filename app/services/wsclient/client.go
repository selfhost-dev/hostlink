package wsclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hostlink/app/services/localtaskstore"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"hostlink/app/services/agentstate"
	"hostlink/app/services/requestsigner"
	"hostlink/domain/task"
	"hostlink/internal/wsprotocol"
)

var ErrAgentNotRegistered = errors.New("agent not registered: missing agent ID")

type Dialer interface {
	Dial(ctx context.Context, url string, headers http.Header) (Conn, error)
}

type Conn interface {
	WriteEnvelope(ctx context.Context, env wsprotocol.Envelope) error
	ReadEnvelope(ctx context.Context) (wsprotocol.Envelope, error)
	Ping(ctx context.Context) error
	Close() error
}

type SleepFunc func(context.Context, time.Duration) error

type TaskEnqueuer interface {
	Enqueue(context.Context, task.Task) error
}

type Config struct {
	URL            string
	AgentState     *agentstate.AgentState
	PrivateKeyPath string
	Dialer         Dialer
	ReconnectMin   time.Duration
	ReconnectMax   time.Duration
	PingInterval   time.Duration
	SleepFunc      SleepFunc
	ResultOutbox   localtaskstore.ResultOutbox
	ReceiptStore   localtaskstore.ReceiptStore
	TaskEnqueuer   TaskEnqueuer
}

type Client struct {
	url          string
	agentID      string
	signer       *requestsigner.RequestSigner
	dialer       Dialer
	reconnectMin time.Duration
	reconnectMax time.Duration
	pingInterval time.Duration
	sleep        SleepFunc

	mu       sync.RWMutex
	writeMu  sync.Mutex
	active   bool
	lastAck  *wsprotocol.AckPayload
	conn     Conn
	outbox   localtaskstore.ResultOutbox
	receipts localtaskstore.ReceiptStore
	enqueuer TaskEnqueuer
}

func New(cfg Config) (*Client, error) {
	if cfg.AgentState == nil {
		return nil, ErrAgentNotRegistered
	}
	agentID := cfg.AgentState.GetAgentID()
	if agentID == "" {
		return nil, ErrAgentNotRegistered
	}
	signer, err := requestsigner.New(cfg.PrivateKeyPath, agentID)
	if err != nil {
		return nil, fmt.Errorf("create request signer: %w", err)
	}
	if cfg.Dialer == nil {
		cfg.Dialer = DefaultDialer{}
	}
	if cfg.ReconnectMin == 0 {
		cfg.ReconnectMin = time.Second
	}
	if cfg.ReconnectMax == 0 {
		cfg.ReconnectMax = 5 * time.Minute
	}
	if cfg.PingInterval == 0 {
		cfg.PingInterval = 30 * time.Second
	}
	if cfg.SleepFunc == nil {
		cfg.SleepFunc = sleepContext
	}

	return &Client{
		url:          cfg.URL,
		agentID:      agentID,
		signer:       signer,
		dialer:       cfg.Dialer,
		reconnectMin: cfg.ReconnectMin,
		reconnectMax: cfg.ReconnectMax,
		pingInterval: cfg.PingInterval,
		sleep:        cfg.SleepFunc,
		outbox:       cfg.ResultOutbox,
		receipts:     cfg.ReceiptStore,
		enqueuer:     cfg.TaskEnqueuer,
	}, nil
}

func (c *Client) Start(ctx context.Context) error {
	backoff := c.reconnectMin
	for {
		if ctx.Err() != nil {
			return nil
		}

		err := c.runOnce(ctx)
		c.setActive(false)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			backoff = c.reconnectMin
			continue
		}

		delay := jitter(backoff)
		if err := c.sleep(ctx, delay); err != nil {
			return nil
		}
		backoff *= 2
		if backoff > c.reconnectMax {
			backoff = c.reconnectMax
		}
	}
}

func (c *Client) IsActive() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active
}

func (c *Client) LastAck() *wsprotocol.AckPayload {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.lastAck == nil {
		return nil
	}
	ack := *c.lastAck
	return &ack
}

func (c *Client) runOnce(ctx context.Context) error {
	headers, err := c.signer.SignHeaders()
	if err != nil {
		return err
	}
	conn, err := c.dialer.Dial(ctx, c.url, headers)
	if err != nil {
		return err
	}
	c.setConn(conn)
	defer conn.Close()
	defer c.setConn(nil)

	hello := c.buildHello()
	if err := c.writeEnvelope(ctx, conn, hello); err != nil {
		return err
	}

	readErr := make(chan error, 1)
	go func() { readErr <- c.readLoop(ctx, conn, hello.MessageID) }()

	if c.pingInterval <= 0 {
		return <-readErr
	}
	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-readErr:
			return err
		case <-ticker.C:
			if err := c.ping(ctx, conn); err != nil {
				_ = conn.Close()
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (c *Client) readLoop(ctx context.Context, conn Conn, helloMessageID string) error {
	for {
		env, err := conn.ReadEnvelope(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := env.Validate(c.agentID); err != nil {
			return err
		}

		switch env.Type {
		case wsprotocol.TypeAgentHelloAck:
			ack, err := wsprotocol.DecodePayload[wsprotocol.AckPayload](env)
			if err != nil {
				return err
			}
			if ack.AckedMessageID == helloMessageID {
				c.setActive(true)
				if err := c.replayUnacked(ctx, conn); err != nil {
					return err
				}
			}
			c.setLastAck(&ack)
		case wsprotocol.TypeAck:
			ack, err := wsprotocol.DecodePayload[wsprotocol.AckPayload](env)
			if err != nil {
				return err
			}
			if c.outbox != nil && ack.AckedMessageID != "" {
				if err := c.outbox.AckMessage(ack.AckedMessageID); err != nil {
					return err
				}
			}
			c.setLastAck(&ack)
		case wsprotocol.TypeError:
			payload, err := wsprotocol.DecodePayload[wsprotocol.ErrorPayload](env)
			if err != nil {
				return err
			}
			if payload.Retryable {
				continue
			}
			return fmt.Errorf("websocket protocol error: %s", env.MessageID)
		case wsprotocol.TypeTaskDeliver:
			if err := c.receiveTaskDeliver(ctx, conn, env); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported inbound websocket message type: %s", env.Type)
		}
	}
}

func (c *Client) receiveTaskDeliver(ctx context.Context, conn Conn, env wsprotocol.Envelope) error {
	if c.receipts == nil {
		return fmt.Errorf("receipt store is not configured")
	}
	payload, err := wsprotocol.DecodePayload[wsprotocol.TaskDeliverPayload](env)
	if err != nil {
		return err
	}
	if err := payload.Validate(); err != nil {
		return err
	}

	previous, err := c.receipts.TaskState(env.TaskID, env.ExecutionAttemptID)
	if err != nil {
		return err
	}
	state, err := c.receipts.RecordReceived(localtaskstore.TaskReceipt{
		TaskID:             env.TaskID,
		ExecutionAttemptID: env.ExecutionAttemptID,
	})
	if err != nil {
		return err
	}
	if previous.Exists && previous.Status == localtaskstore.TaskStatusRunning {
		return c.writeEnvelope(ctx, conn, c.buildTaskStateEnvelope(wsprotocol.TypeTaskStarted, env.TaskID, env.ExecutionAttemptID))
	}
	if previous.Exists && (previous.Status == localtaskstore.TaskStatusFinal || previous.Status == localtaskstore.TaskStatusInterrupted) {
		replayed, err := c.replayTaskFinal(ctx, conn, env.TaskID, env.ExecutionAttemptID)
		if err != nil {
			return err
		}
		if replayed {
			return nil
		}
	}
	if err := c.writeEnvelope(ctx, conn, c.buildTaskStateEnvelope(wsprotocol.TypeTaskReceived, env.TaskID, env.ExecutionAttemptID)); err != nil {
		return err
	}
	if !previous.Exists && state.Status == localtaskstore.TaskStatusReceived && c.enqueuer != nil {
		return c.enqueuer.Enqueue(ctx, task.Task{
			ID:                 env.TaskID,
			ExecutionAttemptID: env.ExecutionAttemptID,
			Command:            payload.Command,
			Status:             "pending",
			Priority:           payload.Priority,
		})
	}
	return nil
}

func (c *Client) replayTaskFinal(ctx context.Context, conn Conn, taskID, executionAttemptID string) (bool, error) {
	if c.outbox == nil {
		return false, nil
	}
	messages, err := c.outbox.UnackedMessages()
	if err != nil {
		return false, err
	}
	for _, message := range messages {
		if message.TaskID == taskID && message.ExecutionAttemptID == executionAttemptID && message.Type == localtaskstore.OutboxMessageTypeFinal {
			return true, c.writeEnvelope(ctx, conn, envelopeFromOutboxMessage(c.agentID, message))
		}
	}
	return false, nil
}

func (c *Client) SendOutput(ctx context.Context, chunk localtaskstore.OutputChunk) error {
	if c.outbox == nil {
		return fmt.Errorf("result outbox is not configured")
	}
	if err := c.outbox.AppendOutputChunk(chunk); err != nil {
		return err
	}
	return c.sendIfActive(ctx, envelopeFromOutboxMessage(c.agentID, localtaskstore.OutboxMessage{
		MessageID:          chunk.MessageID,
		TaskID:             chunk.TaskID,
		ExecutionAttemptID: chunk.ExecutionAttemptID,
		Type:               localtaskstore.OutboxMessageTypeOutput,
		Stream:             chunk.Stream,
		Sequence:           chunk.Sequence,
		Payload:            chunk.Payload,
		ByteCount:          chunk.ByteCount,
	}))
}

func (c *Client) SendFinal(ctx context.Context, result localtaskstore.FinalResult) error {
	if c.outbox == nil {
		return fmt.Errorf("result outbox is not configured")
	}
	if err := c.outbox.RecordFinal(result); err != nil {
		return err
	}
	err := c.sendIfActive(ctx, envelopeFromOutboxMessage(c.agentID, localtaskstore.OutboxMessage{
		MessageID:          result.MessageID,
		TaskID:             result.TaskID,
		ExecutionAttemptID: result.ExecutionAttemptID,
		Type:               localtaskstore.OutboxMessageTypeFinal,
		Payload:            result.Payload,
		ByteCount:          int64(len(result.Payload)),
	}))
	if err != nil {
		return err
	}
	if !c.IsActive() {
		return fmt.Errorf("websocket result channel is inactive")
	}
	return nil
}

func (c *Client) SendStarted(ctx context.Context, receipt localtaskstore.TaskReceipt) error {
	return c.sendIfActive(ctx, c.buildTaskStateEnvelope(wsprotocol.TypeTaskStarted, receipt.TaskID, receipt.ExecutionAttemptID))
}

func (c *Client) RecordStarted(ctx context.Context, receipt localtaskstore.TaskReceipt) error {
	if c.receipts == nil {
		return fmt.Errorf("receipt store cannot record started state")
	}
	return c.receipts.RecordStarted(receipt.TaskID, receipt.ExecutionAttemptID)
}

func (c *Client) buildHello() wsprotocol.Envelope {
	return wsprotocol.Envelope{
		ProtocolVersion: wsprotocol.ProtocolVersion,
		MessageID:       fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Type:            wsprotocol.TypeAgentHello,
		AgentID:         c.agentID,
		SentAt:          time.Now().UTC().Format(time.RFC3339),
		Payload:         map[string]any{},
	}
}

func (c *Client) buildTaskStateEnvelope(messageType wsprotocol.MessageType, taskID, executionAttemptID string) wsprotocol.Envelope {
	return wsprotocol.Envelope{
		ProtocolVersion:    wsprotocol.ProtocolVersion,
		MessageID:          fmt.Sprintf("msg_%s_%s_%d", taskID, messageType, time.Now().UnixNano()),
		Type:               messageType,
		AgentID:            c.agentID,
		TaskID:             taskID,
		ExecutionAttemptID: executionAttemptID,
		SentAt:             time.Now().UTC().Format(time.RFC3339),
		Payload:            map[string]any{},
	}
}

func (c *Client) setActive(active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = active
}

func (c *Client) setConn(conn Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
}

func (c *Client) setLastAck(ack *wsprotocol.AckPayload) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastAck = ack
}

func (c *Client) sendIfActive(ctx context.Context, env wsprotocol.Envelope) error {
	c.mu.RLock()
	conn := c.conn
	active := c.active
	c.mu.RUnlock()
	if conn == nil || !active {
		return nil
	}
	return c.writeEnvelope(ctx, conn, env)
}

func (c *Client) replayUnacked(ctx context.Context, conn Conn) error {
	if c.outbox == nil {
		return nil
	}
	messages, err := c.outbox.UnackedMessages()
	if err != nil {
		return err
	}
	for _, message := range messages {
		if err := c.writeEnvelope(ctx, conn, envelopeFromOutboxMessage(c.agentID, message)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) writeEnvelope(ctx context.Context, conn Conn, env wsprotocol.Envelope) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.WriteEnvelope(ctx, env)
}

func (c *Client) ping(ctx context.Context, conn Conn) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.Ping(ctx)
}

func envelopeFromOutboxMessage(agentID string, message localtaskstore.OutboxMessage) wsprotocol.Envelope {
	now := time.Now().UTC().Format(time.RFC3339)
	if message.Type == localtaskstore.OutboxMessageTypeOutput {
		sequence := int(message.Sequence)
		return wsprotocol.Envelope{
			ProtocolVersion:    wsprotocol.ProtocolVersion,
			MessageID:          message.MessageID,
			Type:               wsprotocol.TypeTaskOutput,
			AgentID:            agentID,
			TaskID:             message.TaskID,
			ExecutionAttemptID: message.ExecutionAttemptID,
			Sequence:           &sequence,
			SentAt:             now,
			Payload: map[string]any{
				"stream":     message.Stream,
				"data":       message.Payload,
				"byte_count": message.ByteCount,
			},
		}
	}

	payload := map[string]any{}
	if message.Payload != "" {
		_ = json.Unmarshal([]byte(message.Payload), &payload)
	}
	return wsprotocol.Envelope{
		ProtocolVersion:    wsprotocol.ProtocolVersion,
		MessageID:          message.MessageID,
		Type:               wsprotocol.TypeTaskFinal,
		AgentID:            agentID,
		TaskID:             message.TaskID,
		ExecutionAttemptID: message.ExecutionAttemptID,
		SentAt:             now,
		Payload:            payload,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	delta := d / 4
	if delta <= 0 {
		return d
	}
	return d - delta + time.Duration(rand.Int64N(int64(delta*2)))
}
