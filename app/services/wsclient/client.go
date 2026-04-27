package wsclient

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"hostlink/app/services/agentstate"
	"hostlink/app/services/requestsigner"
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

type Config struct {
	URL            string
	AgentState     *agentstate.AgentState
	PrivateKeyPath string
	Dialer         Dialer
	ReconnectMin   time.Duration
	ReconnectMax   time.Duration
	PingInterval   time.Duration
	SleepFunc      SleepFunc
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

	mu      sync.RWMutex
	active  bool
	lastAck *wsprotocol.AckPayload
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
	defer conn.Close()

	hello := c.buildHello()
	if err := conn.WriteEnvelope(ctx, hello); err != nil {
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
			if err := conn.Ping(ctx); err != nil {
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
			}
			c.setLastAck(&ack)
		case wsprotocol.TypeAck:
			ack, err := wsprotocol.DecodePayload[wsprotocol.AckPayload](env)
			if err != nil {
				return err
			}
			c.setLastAck(&ack)
		case wsprotocol.TypeError:
			return fmt.Errorf("websocket protocol error: %s", env.MessageID)
		default:
			return fmt.Errorf("unsupported inbound websocket message type: %s", env.Type)
		}
	}
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

func (c *Client) setActive(active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = active
}

func (c *Client) setLastAck(ack *wsprotocol.AckPayload) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastAck = ack
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
