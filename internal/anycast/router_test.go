package anycast

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// mockDiscovery is a test double for DiscoveryProvider.
type mockDiscovery struct {
	discoverFunc    func(ctx context.Context, skillID string) ([]AgentEndpoint, error)
	registerFunc    func(ctx context.Context, peerID string, skills []SkillInfo, agentName, agentDesc string) error
	unregisterFunc  func(ctx context.Context, peerID string, skillIDs []string) error
}

func (m *mockDiscovery) DiscoverBySkill(ctx context.Context, skillID string) ([]AgentEndpoint, error) {
	if m.discoverFunc != nil {
		return m.discoverFunc(ctx, skillID)
	}
	return nil, nil
}

func (m *mockDiscovery) RegisterSkills(ctx context.Context, peerID string, skills []SkillInfo, agentName, agentDesc string) error {
	if m.registerFunc != nil {
		return m.registerFunc(ctx, peerID, skills, agentName, agentDesc)
	}
	return nil
}

func (m *mockDiscovery) UnregisterSkills(ctx context.Context, peerID string, skillIDs []string) error {
	if m.unregisterFunc != nil {
		return m.unregisterFunc(ctx, peerID, skillIDs)
	}
	return nil
}

func (m *mockDiscovery) Close() error { return nil }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRouterResolveFromDiscovery(t *testing.T) {
	endpoints := []AgentEndpoint{
		{PeerID: peer.ID("peer-1"), AgentName: "Agent A"},
		{PeerID: peer.ID("peer-2"), AgentName: "Agent B"},
	}

	discovery := &mockDiscovery{
		discoverFunc: func(_ context.Context, skillID string) ([]AgentEndpoint, error) {
			if skillID == "summarize" {
				return endpoints, nil
			}
			return nil, nil
		},
	}

	router := NewRouter(RouterConfig{
		Discovery: discovery,
		Strategy:  &RandomStrategy{},
		CacheTTL:  5 * time.Second,
		Logger:    testLogger(),
	})

	peerID, err := router.Resolve(context.Background(), "summarize")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if peerID != peer.ID("peer-1") && peerID != peer.ID("peer-2") {
		t.Fatalf("unexpected peer: %s", peerID)
	}
}

func TestRouterResolveUsesCache(t *testing.T) {
	callCount := 0
	discovery := &mockDiscovery{
		discoverFunc: func(_ context.Context, _ string) ([]AgentEndpoint, error) {
			callCount++
			return []AgentEndpoint{{PeerID: peer.ID("peer-1")}}, nil
		},
	}

	router := NewRouter(RouterConfig{
		Discovery: discovery,
		CacheTTL:  5 * time.Second,
		Logger:    testLogger(),
	})

	// First call populates cache.
	_, err := router.Resolve(context.Background(), "echo")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}

	// Second call should use cache.
	_, err = router.Resolve(context.Background(), "echo")
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}

	if callCount != 1 {
		t.Fatalf("expected discovery called once, got %d", callCount)
	}
}

func TestRouterResolveNoAgents(t *testing.T) {
	discovery := &mockDiscovery{
		discoverFunc: func(_ context.Context, _ string) ([]AgentEndpoint, error) {
			return nil, nil
		},
	}

	router := NewRouter(RouterConfig{
		Discovery: discovery,
		Logger:    testLogger(),
	})

	_, err := router.Resolve(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for no agents")
	}
}

func TestRouterResolveDiscoveryError(t *testing.T) {
	discovery := &mockDiscovery{
		discoverFunc: func(_ context.Context, _ string) ([]AgentEndpoint, error) {
			return nil, errors.New("network error")
		},
	}

	router := NewRouter(RouterConfig{
		Discovery: discovery,
		Logger:    testLogger(),
	})

	_, err := router.Resolve(context.Background(), "echo")
	if err == nil {
		t.Fatal("expected error from discovery failure")
	}
}

func TestRouterDiscover(t *testing.T) {
	endpoints := []AgentEndpoint{
		{PeerID: peer.ID("peer-1")},
		{PeerID: peer.ID("peer-2")},
	}

	discovery := &mockDiscovery{
		discoverFunc: func(_ context.Context, _ string) ([]AgentEndpoint, error) {
			return endpoints, nil
		},
	}

	router := NewRouter(RouterConfig{
		Discovery: discovery,
		Logger:    testLogger(),
	})

	results, err := router.Discover(context.Background(), "translate")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestRouterInvalidateCache(t *testing.T) {
	callCount := 0
	discovery := &mockDiscovery{
		discoverFunc: func(_ context.Context, _ string) ([]AgentEndpoint, error) {
			callCount++
			return []AgentEndpoint{{PeerID: peer.ID("peer-1")}}, nil
		},
	}

	router := NewRouter(RouterConfig{
		Discovery: discovery,
		CacheTTL:  5 * time.Second,
		Logger:    testLogger(),
	})

	_, _ = router.Resolve(context.Background(), "echo")
	router.InvalidateCache("echo")
	_, _ = router.Resolve(context.Background(), "echo")

	if callCount != 2 {
		t.Fatalf("expected 2 discovery calls after invalidation, got %d", callCount)
	}
}

func TestRouterInvalidateCacheAll(t *testing.T) {
	callCount := 0
	discovery := &mockDiscovery{
		discoverFunc: func(_ context.Context, _ string) ([]AgentEndpoint, error) {
			callCount++
			return []AgentEndpoint{{PeerID: peer.ID("peer-1")}}, nil
		},
	}

	router := NewRouter(RouterConfig{
		Discovery: discovery,
		CacheTTL:  5 * time.Second,
		Logger:    testLogger(),
	})

	_, _ = router.Resolve(context.Background(), "echo")
	_, _ = router.Resolve(context.Background(), "translate")
	router.InvalidateCache("") // clear all

	_, _ = router.Resolve(context.Background(), "echo")
	_, _ = router.Resolve(context.Background(), "translate")

	if callCount != 4 {
		t.Fatalf("expected 4 discovery calls after full invalidation, got %d", callCount)
	}
}

func TestRouterDefaultStrategy(t *testing.T) {
	discovery := &mockDiscovery{
		discoverFunc: func(_ context.Context, _ string) ([]AgentEndpoint, error) {
			return []AgentEndpoint{{PeerID: peer.ID("peer-1")}}, nil
		},
	}

	// No Strategy provided — should default to RandomStrategy.
	router := NewRouter(RouterConfig{
		Discovery: discovery,
		Logger:    testLogger(),
	})

	peerID, err := router.Resolve(context.Background(), "echo")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if peerID != peer.ID("peer-1") {
		t.Fatalf("unexpected peer: %s", peerID)
	}
}

func TestRouterRegisterUnregister(t *testing.T) {
	var registeredSkills []SkillInfo
	var unregisteredIDs []string

	discovery := &mockDiscovery{
		registerFunc: func(_ context.Context, _ string, skills []SkillInfo, _, _ string) error {
			registeredSkills = skills
			return nil
		},
		unregisterFunc: func(_ context.Context, _ string, skillIDs []string) error {
			unregisteredIDs = skillIDs
			return nil
		},
	}

	router := NewRouter(RouterConfig{
		Discovery: discovery,
		Logger:    testLogger(),
	})

	skills := []SkillInfo{{SkillID: "echo", Description: "Echo back"}}
	err := router.RegisterSkills(context.Background(), "peer-1", skills, "Agent", "Desc")
	if err != nil {
		t.Fatalf("RegisterSkills: %v", err)
	}
	if len(registeredSkills) != 1 || registeredSkills[0].SkillID != "echo" {
		t.Fatalf("unexpected registered skills: %v", registeredSkills)
	}

	err = router.UnregisterSkills(context.Background(), "peer-1", []string{"echo"})
	if err != nil {
		t.Fatalf("UnregisterSkills: %v", err)
	}
	if len(unregisteredIDs) != 1 || unregisteredIDs[0] != "echo" {
		t.Fatalf("unexpected unregistered IDs: %v", unregisteredIDs)
	}
}
