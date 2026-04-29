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
	"hostlink/app/services/rollout"
	"hostlink/domain/task"
	"hostlink/internal/telemetry/telemetrytest"
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
	for _, key := range []string{"running_task", "received_not_started", "unacked_finals", "unacked_output", "spool_status", "client_version", "capabilities"} {
		if _, ok := written.Payload[key]; !ok {
			t.Fatalf("hello payload missing key %q", key)
		}
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

func TestClientHelloPayloadAdvertisesRolloutCapabilities(t *testing.T) {
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithResultsEnabled(true), WithDeliveryEnabled(false))

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	written := conn.waitForWrite(t)
	capabilities, ok := written.Payload["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %#v", written.Payload["capabilities"])
	}
	if capabilities["results_enabled"] != true || capabilities["delivery_enabled"] != false {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientHelloAckUpdatesPollingCoordinator(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	coordinator := rollout.NewCoordinatorWithClock(true, 5*time.Second, func() time.Time { return now })
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithDeliveryEnabled(true), WithDeliveryCoordinator(coordinator))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelopeWithDirectives(hello.MessageID, wsprotocol.HelloAckPayload{DeliveryEnabled: true})
	waitFor(t, func() bool { return !coordinator.ShouldPoll() }, "polling to pause after delivery-enabled hello ack")

	conn.readErr <- errors.New("server closed")
	waitFor(t, func() bool { return !client.IsActive() }, "client to mark inactive after disconnect")
	if coordinator.ShouldPoll() {
		t.Fatal("polling resumed before fallback threshold elapsed")
	}
	now = now.Add(6 * time.Second)
	if !coordinator.ShouldPoll() {
		t.Fatal("polling did not resume after fallback threshold elapsed")
	}
}

func TestClientReconnectAttemptEmitsTelemetry(t *testing.T) {
	telemetryPath := filepath.Join(t.TempDir(), "hostlink-ws-telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)
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

	entries := telemetryEntries(t, telemetryPath)
	reconnectMetric := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.agent_ws.reconnect.attempts"
	})
	if reconnectMetric["agent_id"] != "agent_ws_test" {
		t.Fatalf("reconnect metric = %#v", reconnectMetric)
	}
}

func TestClientSessionLifecycleEmitsTelemetry(t *testing.T) {
	telemetryPath := filepath.Join(t.TempDir(), "hostlink-ws-telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer)

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelope(hello.MessageID)
	waitFor(t, func() bool { return client.IsActive() }, "client to become active")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	entries := telemetryEntries(t, telemetryPath)
	activated := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["event"] == "hostlink.agent_ws.session.activated"
	})
	opened := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.agent_ws.connections.opened"
	})
	connected := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.agent_ws.connection.active" && entry["value"] == float64(1)
	})
	disconnected := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["event"] == "hostlink.agent_ws.session.disconnected"
	})
	closed := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.agent_ws.connections.closed"
	})

	if activated["agent_id"] != "agent_ws_test" {
		t.Fatalf("activated entry = %#v", activated)
	}
	if opened["agent_id"] != "agent_ws_test" {
		t.Fatalf("opened metric = %#v", opened)
	}
	if connected["agent_id"] != "agent_ws_test" {
		t.Fatalf("connected metric = %#v", connected)
	}
	if disconnected["agent_id"] != "agent_ws_test" {
		t.Fatalf("disconnected entry = %#v", disconnected)
	}
	if closed["agent_id"] != "agent_ws_test" {
		t.Fatalf("closed metric = %#v", closed)
	}
}

func TestClientDeliveryDisabledHelloAckLeavesPollingEnabled(t *testing.T) {
	coordinator := rollout.NewCoordinator(true, 30*time.Second)
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithDeliveryEnabled(true), WithDeliveryCoordinator(coordinator))

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelopeWithDirectives(hello.MessageID, wsprotocol.HelloAckPayload{DeliveryEnabled: false})
	waitFor(t, func() bool { return client.IsActive() }, "client to become active")
	if !coordinator.ShouldPoll() {
		t.Fatal("polling paused despite delivery-disabled hello ack")
	}
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

func TestClientReceivesTaskDeliverStoresAcksAndQueues(t *testing.T) {
	store := newClientTestStore(t)
	enqueuer := &fakeTaskEnqueuer{}
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithReceiptStore(store), WithTaskEnqueuer(enqueuer))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelope(hello.MessageID)
	conn.readCh <- deliverEnvelope("msg_deliver", "task-1", "attempt-1", "printf hi", 2)

	received := conn.waitForWrite(t)
	if received.Type != wsprotocol.TypeTaskReceived {
		t.Fatalf("received type = %q, want %q", received.Type, wsprotocol.TypeTaskReceived)
	}
	if received.TaskID != "task-1" || received.ExecutionAttemptID != "attempt-1" {
		t.Fatalf("received envelope = %#v", received)
	}
	state, err := store.TaskState("task-1", "attempt-1")
	requireNoError(t, err)
	if !state.Exists || state.Status != localtaskstore.TaskStatusReceived {
		t.Fatalf("state = %#v", state)
	}
	waitFor(t, func() bool { return len(enqueuer.tasks()) == 1 }, "task to be queued")
	queued := enqueuer.tasks()[0]
	if queued.ID != "task-1" || queued.ExecutionAttemptID != "attempt-1" || queued.Command != "printf hi" || queued.Priority != 2 {
		t.Fatalf("queued task = %#v", queued)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientSendStartedPersistsRunningStateAndSendsStarted(t *testing.T) {
	store := newClientTestStore(t)
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithReceiptStore(store))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelope(hello.MessageID)
	waitFor(t, func() bool { return client.IsActive() }, "client to become active")
	requireNoError(t, client.SendStarted(context.Background(), localtaskstore.TaskReceipt{TaskID: "task-1", ExecutionAttemptID: "attempt-1"}))

	started := conn.waitForWrite(t)
	if started.Type != wsprotocol.TypeTaskStarted {
		t.Fatalf("started type = %q", started.Type)
	}
	state, err := store.TaskState("task-1", "attempt-1")
	requireNoError(t, err)
	if state.Status != localtaskstore.TaskStatusRunning {
		t.Fatalf("state = %#v", state)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientDeliveryOnlySendsStartedButFallsBackToHTTPFinal(t *testing.T) {
	store := newClientTestStore(t)
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithReceiptStore(store), WithDeliveryEnabled(true), WithResultsEnabled(false))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelopeWithDirectives(hello.MessageID, wsprotocol.HelloAckPayload{DeliveryEnabled: true})
	waitFor(t, func() bool { return client.IsActive() }, "client to become active")
	receipt := localtaskstore.TaskReceipt{TaskID: "task-1", ExecutionAttemptID: "attempt-1"}
	requireNoError(t, client.RecordStarted(context.Background(), receipt))
	requireNoError(t, client.SendStarted(context.Background(), receipt))
	started := conn.waitForWrite(t)
	if started.Type != wsprotocol.TypeTaskStarted {
		t.Fatalf("started type = %q", started.Type)
	}

	err := client.SendFinal(context.Background(), localtaskstore.FinalResult{TaskID: "task-1", ExecutionAttemptID: "attempt-1", Status: "completed"})
	if err == nil {
		t.Fatal("expected disabled result channel to force HTTP final fallback")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientDuplicateTaskDeliverReacksWithoutDuplicateQueue(t *testing.T) {
	telemetryPath := filepath.Join(t.TempDir(), "hostlink-ws-telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)
	store := newClientTestStore(t)
	requireNoError(t, store.RecordStarted("task-1", "attempt-1"))
	enqueuer := &fakeTaskEnqueuer{}
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithReceiptStore(store), WithTaskEnqueuer(enqueuer))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelope(hello.MessageID)
	conn.readCh <- deliverEnvelope("msg_deliver", "task-1", "attempt-1", "printf hi", 2)

	started := conn.waitForWrite(t)
	if started.Type != wsprotocol.TypeTaskStarted {
		t.Fatalf("started type = %q", started.Type)
	}
	if len(enqueuer.tasks()) != 0 {
		t.Fatalf("queued tasks = %#v, want none", enqueuer.tasks())
	}
	entries := telemetryEntries(t, telemetryPath)
	duplicate := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["event"] == "hostlink.agent_ws.task.deliver.duplicate"
	})
	duplicateMetric := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.agent_ws.delivery.duplicates"
	})
	if duplicate["task_id"] != "task-1" || duplicate["execution_attempt_id"] != "attempt-1" {
		t.Fatalf("duplicate entry = %#v", duplicate)
	}
	if duplicateMetric["task_id"] != "task-1" {
		t.Fatalf("duplicate metric = %#v", duplicateMetric)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientReceivedDuplicateTaskDeliverReacksWithoutDuplicateQueue(t *testing.T) {
	telemetryPath := filepath.Join(t.TempDir(), "hostlink-ws-telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)
	store := newClientTestStore(t)
	_, err := store.RecordReceived(localtaskstore.TaskReceipt{TaskID: "task-1", ExecutionAttemptID: "attempt-1"})
	requireNoError(t, err)
	enqueuer := &fakeTaskEnqueuer{}
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithReceiptStore(store), WithTaskEnqueuer(enqueuer))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelope(hello.MessageID)
	conn.readCh <- deliverEnvelope("msg_deliver", "task-1", "attempt-1", "printf hi", 2)

	received := conn.waitForWrite(t)
	if received.Type != wsprotocol.TypeTaskReceived {
		t.Fatalf("received type = %q", received.Type)
	}
	if len(enqueuer.tasks()) != 0 {
		t.Fatalf("queued tasks = %#v, want none", enqueuer.tasks())
	}
	entries := telemetryEntries(t, telemetryPath)
	duplicate := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["event"] == "hostlink.agent_ws.task.deliver.duplicate"
	})
	duplicateMetric := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.agent_ws.delivery.duplicates"
	})
	if duplicate["state"] != localtaskstore.TaskStatusReceived {
		t.Fatalf("duplicate entry = %#v", duplicate)
	}
	if duplicateMetric["task_id"] != "task-1" {
		t.Fatalf("duplicate metric = %#v", duplicateMetric)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientFinalDuplicateTaskDeliverResendsUnackedFinalWithoutQueue(t *testing.T) {
	store := newClientTestStore(t)
	requireNoError(t, store.RecordFinal(localtaskstore.FinalResult{
		MessageID:          "msg-final-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Status:             "completed",
		ExitCode:           0,
		Payload:            `{"status":"completed","exit_code":0,"output_truncated":false,"error_truncated":false}`,
	}))
	enqueuer := &fakeTaskEnqueuer{}
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithReceiptStore(store), WithResultOutbox(store), WithTaskEnqueuer(enqueuer))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelope(hello.MessageID)
	replayed := conn.waitForWrite(t)
	if replayed.Type != wsprotocol.TypeTaskFinal {
		t.Fatalf("hello replay type = %q", replayed.Type)
	}
	conn.readCh <- deliverEnvelope("msg_deliver", "task-1", "attempt-1", "printf hi", 2)

	final := conn.waitForWrite(t)
	if final.Type != wsprotocol.TypeTaskFinal || final.MessageID != "msg-final-1" {
		t.Fatalf("final duplicate response = %#v", final)
	}
	if len(enqueuer.tasks()) != 0 {
		t.Fatalf("queued tasks = %#v, want none", enqueuer.tasks())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientHelloAckAcknowledgedFinalsEmitOutboxAcknowledgementTelemetry(t *testing.T) {
	telemetryPath := filepath.Join(t.TempDir(), "hostlink-ws-telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)
	store := newClientTestStore(t)
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
	conn.readCh <- helloAckEnvelopeWithDirectives(hello.MessageID, wsprotocol.HelloAckPayload{
		AcknowledgedFinalMessageIDs: []string{"msg-final-1"},
	})

	waitFor(t, func() bool {
		messages, err := store.UnackedMessages()
		return err == nil && len(messages) == 0
	}, "hello ack to remove final outbox message")
	entries := telemetryEntries(t, telemetryPath)
	acknowledged := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["event"] == "hostlink.agent_ws.outbox.acknowledged" && entry["acked_message_id"] == "msg-final-1"
	})
	if acknowledged["acked_type"] != string(wsprotocol.TypeTaskFinal) || acknowledged["task_id"] != "task-1" {
		t.Fatalf("acknowledged event = %#v", acknowledged)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientAckRemovesResultMessageFromOutbox(t *testing.T) {
	telemetryPath := filepath.Join(t.TempDir(), "hostlink-ws-telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)
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
	entries := telemetryEntries(t, telemetryPath)
	ackMetric := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.local_store.outbox.pending_messages" && entry["value"] == float64(0)
	})
	finalPendingAck := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["event"] == "hostlink.agent_ws.outbox.acknowledged" && entry["acked_message_id"] == "msg-output-1"
	})
	if ackMetric["metric_name"] != "hostlink.local_store.outbox.pending_messages" {
		t.Fatalf("ack metric = %#v", ackMetric)
	}
	if finalPendingAck["task_id"] != "task-1" || finalPendingAck["execution_attempt_id"] != "attempt-1" {
		t.Fatalf("ack event = %#v", finalPendingAck)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientReplaysUnackedMessagesAfterHelloAck(t *testing.T) {
	telemetryPath := filepath.Join(t.TempDir(), "hostlink-ws-telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)
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
	entries := telemetryEntries(t, telemetryPath)
	outputResend := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.agent_ws.outbox.resends" && entry["message_id"] == "msg-output-1"
	})
	finalResend := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.agent_ws.outbox.resends" && entry["message_id"] == "msg-final-1"
	})
	if outputResend["message_type"] != string(wsprotocol.TypeTaskOutput) {
		t.Fatalf("output resend = %#v", outputResend)
	}
	if finalResend["message_type"] != string(wsprotocol.TypeTaskFinal) {
		t.Fatalf("final resend = %#v", finalResend)
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

func TestClientRetryableOutputGapReplaysFromHighestAcceptedSequence(t *testing.T) {
	store := newClientTestStore(t)
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithResultOutbox(store))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelopeWithDirectives(hello.MessageID, wsprotocol.HelloAckPayload{OutputReplay: []wsprotocol.OutputReplayDirective{}})
	waitFor(t, func() bool { return client.IsActive() }, "client to become active")
	requireNoError(t, client.SendOutput(context.Background(), localtaskstore.OutputChunk{
		MessageID: "msg-output-1", TaskID: "task-1", ExecutionAttemptID: "attempt-1", Stream: "stdout", Sequence: 1, Payload: "hello", ByteCount: 5,
	}))
	requireNoError(t, client.SendOutput(context.Background(), localtaskstore.OutputChunk{
		MessageID: "msg-output-2", TaskID: "task-1", ExecutionAttemptID: "attempt-1", Stream: "stdout", Sequence: 2, Payload: "world", ByteCount: 5,
	}))
	_ = conn.waitForWrite(t)
	_ = conn.waitForWrite(t)

	zero := 0
	conn.readCh <- wsprotocol.Envelope{
		ProtocolVersion: wsprotocol.ProtocolVersion,
		MessageID:       "msg_error",
		Type:            wsprotocol.TypeError,
		AgentID:         "agent_ws_test",
		SentAt:          time.Now().UTC().Format(time.RFC3339),
		Payload: payloadMap(t, wsprotocol.BuildError(wsprotocol.ErrorOptions{
			Code:                    "output_sequence_gap",
			Message:                 "expected sequence 1",
			Retryable:               true,
			RelatedMessageID:        "msg-output-2",
			HighestAcceptedSequence: &zero,
		})),
	}

	replayedFirst := conn.waitForWrite(t)
	replayedSecond := conn.waitForWrite(t)
	if replayedFirst.MessageID != "msg-output-1" || replayedSecond.MessageID != "msg-output-2" {
		t.Fatalf("replayed messages = %#v then %#v", replayedFirst, replayedSecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientDuplicateOutputChaosSendsChunkTwice(t *testing.T) {
	t.Setenv("HOSTLINK_WS_CHAOS_DUPLICATE_OUTPUT", "true")
	store := newClientTestStore(t)
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithResultOutbox(store))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelopeWithDirectives(hello.MessageID, wsprotocol.HelloAckPayload{OutputReplay: []wsprotocol.OutputReplayDirective{}})
	waitFor(t, func() bool { return client.IsActive() }, "client to become active")
	requireNoError(t, client.SendOutput(context.Background(), localtaskstore.OutputChunk{
		MessageID: "msg-output-1", TaskID: "task-1", ExecutionAttemptID: "attempt-1", Stream: "stdout", Sequence: 1, Payload: "hello", ByteCount: 5,
	}))

	first := conn.waitForWrite(t)
	duplicate := conn.waitForWrite(t)
	if first.MessageID != "msg-output-1" || duplicate.MessageID != "msg-output-1" {
		t.Fatalf("duplicate output writes = %#v then %#v", first, duplicate)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestClientDropOutputSequenceChaosStoresButDoesNotSendOnce(t *testing.T) {
	t.Setenv("HOSTLINK_WS_CHAOS_DROP_OUTPUT_SEQUENCE", "1")
	store := newClientTestStore(t)
	conn := newFakeConn()
	dialer := &fakeDialer{conn: conn}
	client := newTestClient(t, dialer, WithResultOutbox(store))

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Start(runCtx) }()

	hello := conn.waitForWrite(t)
	conn.readCh <- helloAckEnvelopeWithDirectives(hello.MessageID, wsprotocol.HelloAckPayload{OutputReplay: []wsprotocol.OutputReplayDirective{}})
	waitFor(t, func() bool { return client.IsActive() }, "client to become active")
	requireNoError(t, client.SendOutput(context.Background(), localtaskstore.OutputChunk{
		MessageID: "msg-output-1", TaskID: "task-1", ExecutionAttemptID: "attempt-1", Stream: "stdout", Sequence: 1, Payload: "hello", ByteCount: 5,
	}))

	select {
	case written := <-conn.writeCh:
		t.Fatalf("unexpected output write = %#v", written)
	case <-time.After(20 * time.Millisecond):
	}
	messages, err := store.UnackedMessages()
	requireNoError(t, err)
	if len(messages) != 1 || messages[0].MessageID != "msg-output-1" {
		t.Fatalf("stored messages = %#v", messages)
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
		URL:             "ws://example.test/api/v1/agents/ws",
		AgentState:      state,
		PrivateKeyPath:  saveTestPrivateKey(t, t.TempDir()),
		Dialer:          dialer,
		ReconnectMin:    time.Millisecond,
		ReconnectMax:    10 * time.Millisecond,
		PingInterval:    time.Hour,
		ResultsEnabled:  true,
		DeliveryEnabled: true,
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

func WithReceiptStore(store localtaskstore.ReceiptStore) clientOption {
	return func(cfg *Config) { cfg.ReceiptStore = store }
}

func WithTaskEnqueuer(enqueuer TaskEnqueuer) clientOption {
	return func(cfg *Config) { cfg.TaskEnqueuer = enqueuer }
}

func WithResultsEnabled(enabled bool) clientOption {
	return func(cfg *Config) { cfg.ResultsEnabled = enabled }
}

func WithDeliveryEnabled(enabled bool) clientOption {
	return func(cfg *Config) { cfg.DeliveryEnabled = enabled }
}

func WithDeliveryCoordinator(coordinator DeliveryCoordinator) clientOption {
	return func(cfg *Config) { cfg.DeliveryCoordinator = coordinator }
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

func helloAckEnvelope(ackedMessageID string) wsprotocol.Envelope {
	return wsprotocol.Envelope{
		ProtocolVersion: wsprotocol.ProtocolVersion,
		MessageID:       "msg_hello_ack",
		Type:            wsprotocol.TypeAgentHelloAck,
		AgentID:         "agent_ws_test",
		SentAt:          time.Now().UTC().Format(time.RFC3339),
		Payload: payloadMapForTest(wsprotocol.BuildAck(wsprotocol.AckOptions{
			AckedMessageID: ackedMessageID,
			AckedType:      wsprotocol.TypeAgentHello,
		})),
	}
}

func helloAckEnvelopeWithDirectives(ackedMessageID string, payload wsprotocol.HelloAckPayload) wsprotocol.Envelope {
	payload.AckedMessageID = ackedMessageID
	payload.AckedType = wsprotocol.TypeAgentHello
	return wsprotocol.Envelope{
		ProtocolVersion: wsprotocol.ProtocolVersion,
		MessageID:       "msg_hello_ack",
		Type:            wsprotocol.TypeAgentHelloAck,
		AgentID:         "agent_ws_test",
		SentAt:          time.Now().UTC().Format(time.RFC3339),
		Payload:         payloadMapForTest(payload),
	}
}

func deliverEnvelope(messageID, taskID, attemptID, command string, priority int) wsprotocol.Envelope {
	return wsprotocol.Envelope{
		ProtocolVersion:    wsprotocol.ProtocolVersion,
		MessageID:          messageID,
		Type:               wsprotocol.TypeTaskDeliver,
		AgentID:            "agent_ws_test",
		TaskID:             taskID,
		ExecutionAttemptID: attemptID,
		SentAt:             time.Now().UTC().Format(time.RFC3339),
		Payload: map[string]any{
			"command":  command,
			"priority": priority,
		},
	}
}

func payloadMapForTest(value any) map[string]any {
	data, _ := json.Marshal(value)
	var payload map[string]any
	_ = json.Unmarshal(data, &payload)
	return payload
}

type fakeTaskEnqueuer struct {
	mu      sync.Mutex
	queued  []task.Task
	enqueue error
}

func (f *fakeTaskEnqueuer) Enqueue(ctx context.Context, t task.Task) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queued = append(f.queued, t)
	return f.enqueue
}

func (f *fakeTaskEnqueuer) tasks() []task.Task {
	f.mu.Lock()
	defer f.mu.Unlock()
	tasks := make([]task.Task, len(f.queued))
	copy(tasks, f.queued)
	return tasks
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

func telemetryEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	return telemetrytest.ReadEntries(t, path)
}

func findTelemetryEntry(entries []map[string]any, match func(map[string]any) bool) map[string]any {
	return telemetrytest.FindEntry(entries, match)
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
