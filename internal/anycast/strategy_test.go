package anycast

import (
	"context"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestRandomStrategySelect(t *testing.T) {
	s := &RandomStrategy{}
	ctx := context.Background()

	candidates := []AgentEndpoint{
		{PeerID: peer.ID("peer-1"), AgentName: "A"},
		{PeerID: peer.ID("peer-2"), AgentName: "B"},
		{PeerID: peer.ID("peer-3"), AgentName: "C"},
	}

	// Run multiple times to exercise randomness (should never error).
	seen := make(map[peer.ID]bool)
	for i := 0; i < 100; i++ {
		selected, err := s.Select(ctx, candidates)
		if err != nil {
			t.Fatalf("Select returned error: %v", err)
		}
		seen[selected.PeerID] = true
	}

	// With 100 iterations over 3 candidates, we should see all of them.
	if len(seen) != 3 {
		t.Fatalf("expected all 3 candidates to be selected at least once, saw %d", len(seen))
	}
}

func TestRandomStrategyEmptyCandidates(t *testing.T) {
	s := &RandomStrategy{}
	ctx := context.Background()

	_, err := s.Select(ctx, nil)
	if err != ErrNoCandidates {
		t.Fatalf("expected ErrNoCandidates, got %v", err)
	}

	_, err = s.Select(ctx, []AgentEndpoint{})
	if err != ErrNoCandidates {
		t.Fatalf("expected ErrNoCandidates for empty slice, got %v", err)
	}
}

func TestRandomStrategySingleCandidate(t *testing.T) {
	s := &RandomStrategy{}
	ctx := context.Background()

	single := []AgentEndpoint{{PeerID: peer.ID("only-one"), AgentName: "Sole"}}
	selected, err := s.Select(ctx, single)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected.PeerID != peer.ID("only-one") {
		t.Fatalf("expected only-one, got %s", selected.PeerID)
	}
}
