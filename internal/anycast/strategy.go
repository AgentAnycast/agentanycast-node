package anycast

import (
	"context"
	"errors"
	"math/rand/v2"
)

// ErrNoCandidates is returned when no candidate agents are available for routing.
var ErrNoCandidates = errors.New("no candidate agents available")

// RoutingStrategy defines how a target is selected from a set of candidates.
// v0.2: RandomStrategy
// future: LatencyStrategy, WeightedStrategy, RoundRobinStrategy
type RoutingStrategy interface {
	// Select picks one endpoint from the candidate list.
	Select(ctx context.Context, candidates []AgentEndpoint) (AgentEndpoint, error)
}

// RandomStrategy selects a random candidate.
type RandomStrategy struct{}

func (s *RandomStrategy) Select(_ context.Context, candidates []AgentEndpoint) (AgentEndpoint, error) {
	if len(candidates) == 0 {
		return AgentEndpoint{}, ErrNoCandidates
	}
	return candidates[rand.IntN(len(candidates))], nil
}
