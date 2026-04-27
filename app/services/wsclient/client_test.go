package wsclient

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"hostlink/app/services/localtaskstore"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hostlink/app/services/agentstate"
	"hostlink/internal/wsprotocol"
)

func TestClientSendsHelloAndMarksActiveAfterHelloAck(t *testing.T) {
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	written := conn.waitForWrite(t)
	if written.Type != wsprotocol.TypeAgentHello {
		t.Fatalf("written type = %q, want %q", written.Type, wsprotocol.TypeAgentHello)
	}
	if written.AgentID != "agent_ws_test" {
		t.Fatalf("written agent_id = %q", written.AgentID)
	}
	if len(written.Payload) != 0 {
		t.Fatalf("hello payload = %#v, want empty object", written.Payload)
	}

	conn.readCh <- wsprotocol.Envelope{
		ProtocolVersion: wsprotocol.ProtocolVersion,
		MessageID:       "msg_ack",
		Type:            wsprotocol.TypeAgentHelloAck,
		AgentID:         "agent_ws_test",
		SentAt:          time.Now().UTC().Format(time.RFC3339),
		Payload: payloadMap(t, wsprotocol.BuildAck(wsprotocol.AckOptions{
			AckedMessageID: written.MessageID,
			AckedType:      wsprotocol.TypeAgentHello,
		})),
	}

	waitFor(t, func() bool { return client.IsActive() }, "client to become active")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientDialUsesSignedUpgradeHeaders(t *testing.T) {
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer)

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	conn.waitForWrite(t)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	for _, header := range []string{"X-Agent-ID", "X-Timestamp", "X-Nonce", "X-Signature"} {
		if dialer.headers.Get(header) == "" {
			t.Fatalf("expected signed upgrade header %s", header)
		}
	}
	if dialer.headers.Get("X-Agent-ID") != "agent_ws_test" {
		t.Fatalf("X-Agent-ID = %q", dialer.headers.Get("X-Agent-ID"))
	}
}

func TestClientHandlesAckWithoutTaskSideEffects(t *testing.T) {
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	conn.waitForWrite(t)
	conn.readCh <- wsprotocol.Envelope{
		ProtocolVersion: wsprotocol.ProtocolVersion,
		MessageID:       "msg_generic_ack",
		Type:            wsprotocol.TypeAck,
		AgentID:         "agent_ws_test",
		SentAt:          time.Now().UTC().Format(time.RFC3339),
		Payload: payloadMap(t, wsprotocol.BuildAck(wsprotocol.AckOptions{
			AckedMessageID: "msg_other",
			AckedType:      wsprotocol.TypeAck,
		})),
	}

	waitFor(t, func() bool { return client.LastAck() != nil }, "ack to be recorded")
	if client.LastAck().AckedMessageID != "msg_other" {
		t.Fatalf("last ack = %#v", client.LastAck())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientAckRemovesResultMessageFromOutbox(t *testing.T) {
	store := newClientTestStore(t)
	requireNoError(t, store.AppendOutputChunk(localtaskstore.OutputChunk{
		MessageID:          "msg-output-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Stream:             "stdout",
		Sequence:           1,
		Payload:            "hello\n",
		ByteCount:          6,
	}))
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithResultOutbox(store))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	conn.waitForWrite(t)
	conn.readCh <- ackEnvelope("msg_ack", "msg-output-1", wsprotocol.TypeTaskOutput)

	waitFor(t, func() bool {
		messages, err := store.UnackedMessages()
		return err == nil && len(messages) == 0
	}, "ack to remove outbox message")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientReplaysUnackedMessagesAfterHelloAck(t *testing.T) {
	store := newClientTestStore(t)
	requireNoError(t, store.AppendOutputChunk(localtaskstore.OutputChunk{
		MessageID:          "msg-output-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Stream:             "stdout",
		Sequence:           1,
		Payload:            "hello\n",
		ByteCount:          6,
	}))
	requireNoError(t, store.RecordFinal(localtaskstore.FinalResult{
		MessageID:          "msg-final-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Status:             "completed",
		ExitCode:           0,
		Payload:            `{"status":"completed","exit_code":0,"output_truncated":false,"error_truncated":false}`,
	}))
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithResultOutbox(store))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- wsprotocol.Envelope{
		ProtocolVersion: wsprotocol.ProtocolVersion,
		MessageID:       "msg_hello_ack",
		Type:            wsprotocol.TypeAgentHelloAck,
		AgentID:         "agent_ws_test",
		SentAt:          time.Now().UTC().Format(time.RFC3339),
		Payload: payloadMap(t, wsprotocol.BuildAck(wsprotocol.AckOptions{
			AckedMessageID: hello.MessageID,
			AckedType:      wsprotocol.TypeAgentHello,
		})),
	}

	output := conn.waitForWrite(t)
	final := conn.waitForWrite(t)
	if output.MessageID != "msg-output-1" || output.Type != wsprotocol.TypeTaskOutput {
		t.Fatalf("first replay = %#v", output)
	}
	if final.MessageID != "msg-final-1" || final.Type != wsprotocol.TypeTaskFinal {
		t.Fatalf("second replay = %#v", final)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientRetryableErrorKeepsConnectionAndOutboxMessage(t *testing.T) {
	store := newClientTestStore(t)
	requireNoError(t, store.AppendOutputChunk(localtaskstore.OutputChunk{
		MessageID:          "msg-output-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Stream:             "stdout",
		Sequence:           1,
		Payload:            "hello\n",
		ByteCount:          6,
	}))
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithResultOutbox(store))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	conn.waitForWrite(t)
	conn.readCh <- wsprotocol.Envelope{
		ProtocolVersion: wsprotocol.ProtocolVersion,
		MessageID:       "msg_error",
		Type:            wsprotocol.TypeError,
		AgentID:         "agent_ws_test",
		SentAt:          time.Now().UTC().Format(time.RFC3339),
		Payload: payloadMap(t, wsprotocol.BuildError(wsprotocol.ErrorOptions{
			Code:                    "output_sequence_gap",
			Message:                 "expected sequence 2",
			Retryable:               true,
			RelatedMessageID:        "msg-output-1",
			HighestAcceptedSequence: intValuePtr(1),
		})),
	}

	waitFor(t, func() bool { return !conn.closed() }, "connection to remain open")
	messages, err := store.UnackedMessages()
	if err != nil {
		t.Fatalf("unacked messages: %v", err)
	}
	if len(messages) != 1 || messages[0].MessageID != "msg-output-1" {
		t.Fatalf("messages = %#v", messages)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientErrorMessageTriggersReconnect(t *testing.T) {
	first := newFakeConn()
	second := newFakeConn()
	dialer := &fakeDialer{conns: []*fakeConn{first, second}}
	sleeps := make(chan time.Duration, 2)
	client := newTestClient(t, dialer, WithSleepFunc(func(ctx context.Context, d time.Duration) error {
		sleeps <- d
		return nil
	}))

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	first.waitForWrite(t)
	first.readCh <- wsprotocol.Envelope{
		ProtocolVersion: wsprotocol.ProtocolVersion,
		MessageID:       "msg_error",
		Type:            wsprotocol.TypeError,
		AgentID:         "agent_ws_test",
		SentAt:          time.Now().UTC().Format(time.RFC3339),
		Payload: payloadMap(t, wsprotocol.BuildError(wsprotocol.ErrorOptions{
			Code:             "expected_agent_hello",
			Message:          "first message must be agent.hello",
			RelatedMessageID: "msg_hello",
		})),
	}

	select {
	case <-sleeps:
	case <-time.After(time.Second):
		t.Fatal("expected reconnect backoff sleep")
	}
	second.waitForWrite(t)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestNewFailsForMissingAgentState(t *testing.T) {
	state := agentstate.New(t.TempDir())

	_, err := New(Config{
		URL:            "ws://example.test/api/v1/agents/ws",
		AgentState:     state,
		PrivateKeyPath: saveTestPrivateKey(t, t.TempDir()),
	})

	if err == nil || !errors.Is(err, ErrAgentNotRegistered) {
		t.Fatalf("New error = %v, want ErrAgentNotRegistered", err)
	}
}

func TestNewFailsForMissingPrivateKey(t *testing.T) {
	state := agentstate.New(t.TempDir())
	if err := state.SetAgentID("agent_ws_test"); err != nil {
		t.Fatalf("set agent ID: %v", err)
	}

	_, err := New(Config{
		URL:            "ws://example.test/api/v1/agents/ws",
		AgentState:     state,
		PrivateKeyPath: filepath.Join(t.TempDir(), "missing.key"),
	})

	if err == nil {
		t.Fatal("expected missing private key error")
	}
}

func TestReconnectsAfterServerClose(t *testing.T) {
	first := newFakeConn()
	second := newFakeConn()
	dialer := &fakeDialer{conns: []*fakeConn{first, second}}
	sleeps := make(chan time.Duration, 2)
	client := newTestClient(t, dialer, WithSleepFunc(func(ctx context.Context, d time.Duration) error {
		sleeps <- d
		return nil
	}))

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	first.waitForWrite(t)
	first.readErr <- errors.New("server closed")
	select {
	case <-sleeps:
	case <-time.After(time.Second):
		t.Fatal("expected reconnect backoff sleep")
	}
	second.waitForWrite(t)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestReconnectsAfterPingFailure(t *testing.T) {
	first := newFakeConn()
	first.pingErr = errors.New("ping failed")
	second := newFakeConn()
	dialer := &fakeDialer{conns: []*fakeConn{first, second}}
	client := newTestClient(t, dialer,
		WithPingInterval(time.Millisecond),
		WithSleepFunc(func(ctx context.Context, d time.Duration) error { return nil }),
	)

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	first.waitForWrite(t)
	second.waitForWrite(t)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !first.closed() {
		t.Fatal("expected first connection to be closed after ping failure")
	}
}

type clientOption func(*Config)

func newTestClient(t *testing.T, dialer Dialer, opts ...clientOption) *Client {
	t.Helper()
	state := agentstate.New(t.TempDir())
	if err := state.SetAgentID("agent_ws_test"); err != nil {
		t.Fatalf("set agent ID: %v", err)
	}
	cfg := Config{
		URL:            "ws://example.test/api/v1/agents/ws",
		AgentState:     state,
		PrivateKeyPath: saveTestPrivateKey(t, t.TempDir()),
		Dialer:         dialer,
		ReconnectMin:   time.Millisecond,
		ReconnectMax:   10 * time.Millisecond,
		PingInterval:   time.Hour,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	return client
}

func WithSleepFunc(fn SleepFunc) clientOption {
	return func(cfg *Config) { cfg.SleepFunc = fn }
}

func WithPingInterval(d time.Duration) clientOption {
	return func(cfg *Config) { cfg.PingInterval = d }
}

func WithResultOutbox(outbox localtaskstore.ResultOutbox) clientOption {
	return func(cfg *Config) { cfg.ResultOutbox = outbox }
}

type fakeDialer struct {
	mu      sync.Mutex
	conn    *fakeConn
	conns   []*fakeConn
	headers http.Header
	calls   int
}

func (d *fakeDialer) Dial(ctx context.Context, url string, headers http.Header) (Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	d.headers = headers.Clone()
	if len(d.conns) > 0 {
		conn := d.conns[0]
		d.conns = d.conns[1:]
		return conn, nil
	}
	return d.conn, nil
}

type fakeConn struct {
	readCh  chan wsprotocol.Envelope
	readErr chan error
	writeCh chan wsprotocol.Envelope
	pingErr error
	mu      sync.Mutex
	closedV bool
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		readCh:  make(chan wsprotocol.Envelope, 4),
		readErr: make(chan error, 4),
		writeCh: make(chan wsprotocol.Envelope, 4),
	}
}

func (c *fakeConn) WriteEnvelope(ctx context.Context, env wsprotocol.Envelope) error {
	select {
	case c.writeCh <- env:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeConn) ReadEnvelope(ctx context.Context) (wsprotocol.Envelope, error) {
	select {
	case env := <-c.readCh:
		return env, nil
	case err := <-c.readErr:
		return wsprotocol.Envelope{}, err
	case <-ctx.Done():
		return wsprotocol.Envelope{}, ctx.Err()
	}
}

func (c *fakeConn) Ping(ctx context.Context) error {
	return c.pingErr
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closedV = true
	return nil
}

func (c *fakeConn) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closedV
}

func (c *fakeConn) waitForWrite(t *testing.T) wsprotocol.Envelope {
	t.Helper()
	select {
	case env := <-c.writeCh:
		return env
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for written envelope")
		return wsprotocol.Envelope{}
	}
}

func payloadMap(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return payload
}

func waitFor(t *testing.T, check func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func ackEnvelope(messageID, ackedMessageID string, ackedType wsprotocol.MessageType) wsprotocol.Envelope {
	return wsprotocol.Envelope{
		ProtocolVersion: wsprotocol.ProtocolVersion,
		MessageID:       messageID,
		Type:            wsprotocol.TypeAck,
		AgentID:         "agent_ws_test",
		SentAt:          time.Now().UTC().Format(time.RFC3339),
		Payload: map[string]any{
			"acked_message_id": ackedMessageID,
			"acked_type":       string(ackedType),
		},
	}
}

func newClientTestStore(t *testing.T) *localtaskstore.Store {
	t.Helper()
	store, err := localtaskstore.New(localtaskstore.Config{
		Path:                 filepath.Join(t.TempDir(), "task_store.db"),
		SpoolCapBytes:        1024 * 1024,
		TerminalReserveBytes: 1024,
	})
	if err != nil {
		t.Fatalf("new local task store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func intValuePtr(value int) *int {
	return &value
}

func saveTestPrivateKey(t *testing.T, dir string) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyPath := filepath.Join(dir, "agent.key")
	file, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	defer file.Close()
	if err := pem.Encode(file, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return keyPath
}
