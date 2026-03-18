package anycast

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"testing"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func genPeerID(t *testing.T) peer.ID {
	t.Helper()
	priv, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatalf("derive PeerID: %v", err)
	}
	return pid
}

// compositeTestProvider is a simple mock for composite discovery tests.
type compositeTestProvider struct {
	discoverResult  []AgentEndpoint
	discoverErr     error
	registerErr     error
	unregisterErr   error
	closeErr        error
	registerCalled  bool
	unregisterCalled bool
	closed          bool
}

func (p *compositeTestProvider) DiscoverBySkill(_ context.Context, _ string) ([]AgentEndpoint, error) {
	return p.discoverResult, p.discoverErr
}

func (p *compositeTestProvider) RegisterSkills(_ context.Context, _ string, _ []SkillInfo, _, _ string) error {
	p.registerCalled = true
	return p.registerErr
}

func (p *compositeTestProvider) UnregisterSkills(_ context.Context, _ string, _ []string) error {
	p.unregisterCalled = true
	return p.unregisterErr
}

func (p *compositeTestProvider) Close() error {
	p.closed = true
	return p.closeErr
}

func TestCompositeDiscovery_MergesResults(t *testing.T) {
	pid1 := genPeerID(t)
	pid2 := genPeerID(t)

	p1 := &compositeTestProvider{
		discoverResult: []AgentEndpoint{{PeerID: pid1, AgentName: "Agent A"}},
	}
	p2 := &compositeTestProvider{
		discoverResult: []AgentEndpoint{{PeerID: pid2, AgentName: "Agent B"}},
	}

	comp := NewCompositeDiscovery(slog.Default(), p1, p2)
	eps, err := comp.DiscoverBySkill(context.Background(), "test-skill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(eps))
	}
}

func TestCompositeDiscovery_DeduplicatesByPeerID(t *testing.T) {
	pid := genPeerID(t)

	p1 := &compositeTestProvider{
		discoverResult: []AgentEndpoint{{PeerID: pid, AgentName: "From P1"}},
	}
	p2 := &compositeTestProvider{
		discoverResult: []AgentEndpoint{{PeerID: pid, AgentName: "From P2"}},
	}

	comp := NewCompositeDiscovery(slog.Default(), p1, p2)
	eps, err := comp.DiscoverBySkill(context.Background(), "test-skill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("expected 1 deduplicated endpoint, got %d", len(eps))
	}
}

func TestCompositeDiscovery_PartialFailure(t *testing.T) {
	pid := genPeerID(t)

	good := &compositeTestProvider{
		discoverResult: []AgentEndpoint{{PeerID: pid, AgentName: "OK"}},
	}
	bad := &compositeTestProvider{
		discoverErr: errors.New("connection refused"),
	}

	comp := NewCompositeDiscovery(slog.Default(), bad, good)
	eps, err := comp.DiscoverBySkill(context.Background(), "test-skill")
	if err != nil {
		t.Fatalf("expected success despite partial failure, got: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
}

func TestCompositeDiscovery_AllFail(t *testing.T) {
	bad1 := &compositeTestProvider{discoverErr: errors.New("fail1")}
	bad2 := &compositeTestProvider{discoverErr: errors.New("fail2")}

	comp := NewCompositeDiscovery(slog.Default(), bad1, bad2)
	_, err := comp.DiscoverBySkill(context.Background(), "test-skill")
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestCompositeDiscovery_RegisterAcrossAll(t *testing.T) {
	p1 := &compositeTestProvider{}
	p2 := &compositeTestProvider{}

	comp := NewCompositeDiscovery(slog.Default(), p1, p2)
	err := comp.RegisterSkills(context.Background(), "peer1", []SkillInfo{{SkillID: "s1"}}, "Agent", "Desc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p1.registerCalled || !p2.registerCalled {
		t.Error("expected RegisterSkills called on all providers")
	}
}

func TestCompositeDiscovery_CloseAll(t *testing.T) {
	p1 := &compositeTestProvider{}
	p2 := &compositeTestProvider{}

	comp := NewCompositeDiscovery(slog.Default(), p1, p2)
	err := comp.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p1.closed || !p2.closed {
		t.Error("expected Close called on all providers")
	}
}

// ── RegisterSkills error path tests ─────────────────────────────────

func TestCompositeDiscovery_RegisterPartialFailure(t *testing.T) {
	// One provider fails, one succeeds — overall should succeed (anyOK semantics).
	good := &compositeTestProvider{}
	bad := &compositeTestProvider{registerErr: errors.New("provider down")}

	comp := NewCompositeDiscovery(slog.Default(), bad, good)
	err := comp.RegisterSkills(context.Background(), "peer1", []SkillInfo{{SkillID: "s1"}}, "Agent", "Desc")
	if err != nil {
		t.Fatalf("expected success when at least one provider succeeds, got: %v", err)
	}
	if !good.registerCalled || !bad.registerCalled {
		t.Error("expected RegisterSkills called on all providers")
	}
}

func TestCompositeDiscovery_RegisterAllFail(t *testing.T) {
	bad1 := &compositeTestProvider{registerErr: errors.New("fail1")}
	bad2 := &compositeTestProvider{registerErr: errors.New("fail2")}

	comp := NewCompositeDiscovery(slog.Default(), bad1, bad2)
	err := comp.RegisterSkills(context.Background(), "peer1", []SkillInfo{{SkillID: "s1"}}, "Agent", "Desc")
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

// ── UnregisterSkills tests ──────────────────────────────────────────

func TestCompositeDiscovery_UnregisterAcrossAll(t *testing.T) {
	p1 := &compositeTestProvider{}
	p2 := &compositeTestProvider{}

	comp := NewCompositeDiscovery(slog.Default(), p1, p2)
	err := comp.UnregisterSkills(context.Background(), "peer1", []string{"s1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p1.unregisterCalled || !p2.unregisterCalled {
		t.Error("expected UnregisterSkills called on all providers")
	}
}

func TestCompositeDiscovery_UnregisterPartialFailure(t *testing.T) {
	good := &compositeTestProvider{}
	bad := &compositeTestProvider{unregisterErr: errors.New("provider down")}

	comp := NewCompositeDiscovery(slog.Default(), bad, good)
	err := comp.UnregisterSkills(context.Background(), "peer1", []string{"s1"})
	if err != nil {
		t.Fatalf("expected success when at least one provider succeeds, got: %v", err)
	}
}

func TestCompositeDiscovery_UnregisterAllFail(t *testing.T) {
	bad1 := &compositeTestProvider{unregisterErr: errors.New("fail1")}
	bad2 := &compositeTestProvider{unregisterErr: errors.New("fail2")}

	comp := NewCompositeDiscovery(slog.Default(), bad1, bad2)
	err := comp.UnregisterSkills(context.Background(), "peer1", []string{"s1"})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

// ── Close error tests ───────────────────────────────────────────────

func TestCompositeDiscovery_ClosePartialFailure(t *testing.T) {
	good := &compositeTestProvider{}
	bad := &compositeTestProvider{closeErr: errors.New("close failed")}

	comp := NewCompositeDiscovery(slog.Default(), bad, good)
	err := comp.Close()
	// Close returns firstErr, doesn't use anyOK semantics.
	if err == nil {
		t.Fatal("expected error when a provider fails to close")
	}
	if !good.closed || !bad.closed {
		t.Error("expected Close called on all providers even if one fails")
	}
}
