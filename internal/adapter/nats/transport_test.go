package nats

import (
	"context"
	"log/slog"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/agentanycast/agentanycast-node/internal/adapter"
	"github.com/agentanycast/agentanycast-node/internal/config"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

// startTestServer starts an embedded NATS server on a random port and
// returns the server and its client URL.
func startTestServer(t *testing.T) (*natsserver.Server, string) {
	t.Helper()
	opts := &natsserver.Options{
		Host:   "127.0.0.1",
		Port:   -1, // random available port
		NoLog:  true,
		NoSigs: true,
	}
	ns, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("create nats server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready for connections")
	}
	t.Cleanup(ns.Shutdown)
	return ns, ns.ClientURL()
}

// mockProtocol is a minimal ProtocolAdapter for testing. Ingest and
// Emit are identity functions over the raw bytes.
type mockProtocol struct{}

func (m *mockProtocol) Name() string { return "mock" }

func (m *mockProtocol) Ingest(_ context.Context, raw []byte, metadata map[string]string) (*envelope.Envelope, error) {
	env := envelope.New(envelope.TypeTask, "", raw)
	for k, v := range metadata {
		env.SetMeta(k, v)
	}
	return env, nil
}

func (m *mockProtocol) Emit(_ context.Context, env *envelope.Envelope) ([]byte, error) {
	return env.Payload, nil
}

func (m *mockProtocol) Endpoints() []adapter.Endpoint { return nil }

func newTestTransport(t *testing.T, brokerURL, localID string) *Transport {
	t.Helper()
	cfg := config.NATSConfig{
		Enabled:       true,
		Broker:        brokerURL,
		SubjectPrefix: "agent.",
	}
	logger := slog.Default()
	tr, err := New(context.Background(), cfg, localID, &mockProtocol{}, logger)
	if err != nil {
		t.Fatalf("create nats transport for %s: %v", localID, err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

func TestNATSSendReceive(t *testing.T) {
	_, brokerURL := startTestServer(t)

	trA := newTestTransport(t, brokerURL, "peerA")
	trB := newTestTransport(t, brokerURL, "peerB")

	chB, err := trB.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Send from A to B's subject.
	payload := []byte("hello from A")
	env := envelope.New(envelope.TypeTask, "nats://agent.peerB", payload)
	if err := trA.Send(context.Background(), "nats://agent.peerB", env); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Wait for B to receive.
	select {
	case msg := <-chB:
		if string(msg.Envelope.Payload) != "hello from A" {
			t.Errorf("payload = %q, want %q", msg.Envelope.Payload, "hello from A")
		}
		if msg.Source != "agent.peerB" {
			t.Errorf("source = %q, want %q", msg.Source, "agent.peerB")
		}
		if msg.Envelope.Meta("transport") != "nats" {
			t.Errorf("transport meta = %q, want %q", msg.Envelope.Meta("transport"), "nats")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestNATSMatchesTarget(t *testing.T) {
	tr := &Transport{}

	tests := []struct {
		target string
		want   bool
	}{
		{"nats://agent.peer123", true},
		{"nats://some.subject", true},
		{"nats://", true},
		{"http://example.com", false},
		{"12D3KooWDpJ7As7BWAwRMfu1VU2WCqNjvq387JEYKDBj4kx6nXTN", false},
		{"", false},
	}

	for _, tt := range tests {
		got := tr.MatchesTarget(tt.target)
		if got != tt.want {
			t.Errorf("MatchesTarget(%q) = %v, want %v", tt.target, got, tt.want)
		}
	}
}

func TestNATSClose(t *testing.T) {
	_, brokerURL := startTestServer(t)

	cfg := config.NATSConfig{
		Enabled:       true,
		Broker:        brokerURL,
		SubjectPrefix: "agent.",
	}
	logger := slog.Default()
	tr, err := New(context.Background(), cfg, "peerClose", &mockProtocol{}, logger)
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}

	ch, err := tr.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Close should shut down cleanly.
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Channel should be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}

	// Double close should not panic.
	if err := tr.Close(); err != nil {
		t.Fatalf("double close: %v", err)
	}
}

func TestNATSFullChannelDrop(t *testing.T) {
	_, brokerURL := startTestServer(t)

	cfg := config.NATSConfig{
		Enabled:       true,
		Broker:        brokerURL,
		SubjectPrefix: "test.",
	}
	logger := slog.Default()
	tr, err := New(context.Background(), cfg, "peerFull", &mockProtocol{}, logger)
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })

	// Fill the inbound channel completely.
	for i := 0; i < cap(tr.inCh); i++ {
		tr.inCh <- adapter.InboundMessage{
			Envelope: envelope.New(envelope.TypeTask, "", []byte("filler")),
		}
	}

	// Send a message — it should be dropped without blocking or panicking.
	env := envelope.New(envelope.TypeTask, "nats://test.peerFull", []byte("overflow"))
	if err := tr.Send(context.Background(), "nats://test.peerFull", env); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Give NATS a moment to deliver the callback.
	time.Sleep(200 * time.Millisecond)

	// Drain the channel — we should get exactly cap(inCh) messages (all fillers).
	drained := 0
	for {
		select {
		case <-tr.inCh:
			drained++
		default:
			goto done
		}
	}
done:
	if drained != 256 {
		t.Errorf("drained %d messages, want %d (overflow should have been dropped)", drained, 256)
	}
}

func TestNATSName(t *testing.T) {
	tr := &Transport{}
	if tr.Name() != "nats" {
		t.Fatalf("expected 'nats', got %q", tr.Name())
	}
}

func TestNATSSendEmptySubject(t *testing.T) {
	_, brokerURL := startTestServer(t)
	tr := newTestTransport(t, brokerURL, "peerEmpty")

	env := envelope.New(envelope.TypeTask, "nats://", []byte("test"))
	err := tr.Send(context.Background(), "nats://", env)
	if err == nil {
		t.Fatal("expected error for empty subject, got nil")
	}
}
