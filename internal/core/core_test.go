package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentanycast/agentanycast-node/internal/adapter"
	"github.com/agentanycast/agentanycast-node/internal/config"
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

func TestRouteWithACLRateLimitAudit(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

	c := New(Config{})
	tr := newMockTransport("test", "")
	c.RegisterTransport(tr)

	// Setup ACL: allow did:key:alice for skill "echo", deny everything else.
	acl := NewACLManager([]config.ACLRule{
		{Source: "did:key:alice", Skill: "echo", Allow: true},
		{Source: "*", Skill: "*", Allow: false},
	}, nil)
	c.SetACLManager(acl)

	// Setup rate limiter.
	rl := NewRateLimiter(config.RateLimitConfig{DefaultRPS: 1000}, nil)
	defer rl.Stop()
	c.SetRateLimiter(rl)

	// Setup audit logger.
	al, err := NewAuditLogger(auditPath, nil)
	if err != nil {
		t.Fatalf("create audit logger: %v", err)
	}
	defer al.Close()
	c.SetAuditLogger(al)

	ctx := context.Background()

	// Test 1: Allowed access — transport receives envelope, audit logs "allow".
	envAllow := envelope.New(envelope.TypeTask, "target-peer", []byte("hello"))
	envAllow.Source = envelope.AgentIdentity{DIDKey: "did:key:alice"}
	envAllow.SetMeta(envelope.MetaKeySkill, "echo")

	if err := c.Route(ctx, envAllow); err != nil {
		t.Fatalf("Route allowed should succeed: %v", err)
	}
	if len(tr.sent) != 1 {
		t.Fatalf("expected 1 transport send, got %d", len(tr.sent))
	}

	// Test 2: ACL denied — transport NOT called, returns error.
	envDeny := envelope.New(envelope.TypeTask, "target-peer", []byte("denied"))
	envDeny.Source = envelope.AgentIdentity{DIDKey: "did:key:bob"}
	envDeny.SetMeta(envelope.MetaKeySkill, "echo")

	if err := c.Route(ctx, envDeny); err == nil {
		t.Fatal("Route denied should return error")
	}
	if len(tr.sent) != 1 {
		t.Fatalf("expected transport NOT called on deny, got %d sends", len(tr.sent))
	}

	// Test 3: Rate limited — exceeding the limiter returns error.
	rlStrict := NewRateLimiter(config.RateLimitConfig{DefaultRPS: 1}, nil)
	defer rlStrict.Stop()
	c.SetRateLimiter(rlStrict)

	// First call should succeed (within rate limit).
	envRL1 := envelope.New(envelope.TypeTask, "target-peer", []byte("rl1"))
	envRL1.Source = envelope.AgentIdentity{DIDKey: "did:key:alice"}
	envRL1.SetMeta(envelope.MetaKeySkill, "echo")
	_ = c.Route(ctx, envRL1)

	// Rapid subsequent calls should eventually be rate limited.
	rateLimited := false
	for i := 0; i < 100; i++ {
		envRL := envelope.New(envelope.TypeTask, "target-peer", []byte("rl-burst"))
		envRL.Source = envelope.AgentIdentity{DIDKey: "did:key:alice"}
		envRL.SetMeta(envelope.MetaKeySkill, "echo")
		if err := c.Route(ctx, envRL); err != nil {
			rateLimited = true
			break
		}
	}
	if !rateLimited {
		t.Fatal("expected rate limiting to trigger")
	}

	// Verify audit log was written.
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("audit log is empty")
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
