package core

import (
	"context"
	"testing"

	"github.com/agentanycast/agentanycast-node/internal/adapter"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

// mockProtocol is a test ProtocolAdapter.
type mockProtocol struct {
	name string
}

func (m *mockProtocol) Name() string { return m.name }
func (m *mockProtocol) Ingest(_ context.Context, raw []byte, _ map[string]string) (*envelope.Envelope, error) {
	return envelope.New(envelope.TypeCustom, "", raw), nil
}
func (m *mockProtocol) Emit(_ context.Context, env *envelope.Envelope) ([]byte, error) {
	return env.Payload, nil
}
func (m *mockProtocol) Endpoints() []adapter.Endpoint { return nil }

// mockTransport is a test TransportAdapter.
type mockTransport struct {
	name    string
	prefix  string
	sent    []*envelope.Envelope
	inCh    chan adapter.InboundMessage
}

func newMockTransport(name, prefix string) *mockTransport {
	return &mockTransport{
		name:   name,
		prefix: prefix,
		inCh:   make(chan adapter.InboundMessage, 8),
	}
}

func (m *mockTransport) Name() string { return m.name }
func (m *mockTransport) Send(_ context.Context, _ string, env *envelope.Envelope) error {
	m.sent = append(m.sent, env)
	return nil
}
func (m *mockTransport) Subscribe(_ context.Context) (<-chan adapter.InboundMessage, error) {
	return m.inCh, nil
}
func (m *mockTransport) MatchesTarget(target string) bool {
	if m.prefix == "" {
		return true
	}
	return len(target) >= len(m.prefix) && target[:len(m.prefix)] == m.prefix
}
func (m *mockTransport) LocalAddrs() []string { return nil }
func (m *mockTransport) Close() error          { return nil }

func TestRegisterAndLookup(t *testing.T) {
	c := New(Config{})

	p := &mockProtocol{name: "test"}
	c.RegisterProtocol(p)

	got, ok := c.Protocol("test")
	if !ok {
		t.Fatal("expected protocol to be registered")
	}
	if got.Name() != "test" {
		t.Fatalf("expected 'test', got %q", got.Name())
	}

	_, ok = c.Protocol("missing")
	if ok {
		t.Fatal("expected missing protocol to not be found")
	}
}

func TestSendRoutesToCorrectTransport(t *testing.T) {
	c := New(Config{})

	http := newMockTransport("http", "http")
	p2p := newMockTransport("p2p", "12D3")
	c.RegisterTransport(http)
	c.RegisterTransport(p2p)

	// Send to HTTP target.
	env1 := envelope.New(envelope.TypeTask, "http://example.com", []byte("http-data"))
	if err := c.Send(context.Background(), env1); err != nil {
		t.Fatal(err)
	}
	if len(http.sent) != 1 {
		t.Fatalf("expected 1 HTTP send, got %d", len(http.sent))
	}
	if len(p2p.sent) != 0 {
		t.Fatalf("expected 0 P2P sends, got %d", len(p2p.sent))
	}

	// Send to P2P target.
	env2 := envelope.New(envelope.TypeTask, "12D3KooWPeer", []byte("p2p-data"))
	if err := c.Send(context.Background(), env2); err != nil {
		t.Fatal(err)
	}
	if len(p2p.sent) != 1 {
		t.Fatalf("expected 1 P2P send, got %d", len(p2p.sent))
	}
}

func TestSendNoMatchingTransport(t *testing.T) {
	c := New(Config{})
	c.RegisterTransport(newMockTransport("http", "http"))

	env := envelope.New(envelope.TypeTask, "nats://subject", []byte("data"))
	err := c.Send(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for unmatched target")
	}
}

func TestProtocolAndTransportNames(t *testing.T) {
	c := New(Config{})
	c.RegisterProtocol(&mockProtocol{name: "a2a"})
	c.RegisterProtocol(&mockProtocol{name: "anp"})
	c.RegisterTransport(newMockTransport("libp2p", "12D3"))
	c.RegisterTransport(newMockTransport("http", "http"))

	pNames := c.ProtocolNames()
	if len(pNames) != 2 {
		t.Fatalf("expected 2 protocols, got %d", len(pNames))
	}

	tNames := c.TransportNames()
	if len(tNames) != 2 {
		t.Fatalf("expected 2 transports, got %d", len(tNames))
	}
}
