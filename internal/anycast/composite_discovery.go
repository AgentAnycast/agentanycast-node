package anycast

import (
	"context"
	"log/slog"
	"sync"
)

// CompositeDiscovery wraps multiple DiscoveryProviders and merges their results.
// It queries all providers concurrently, deduplicating by PeerID (first seen wins).
// This enables gradual migration from Relay Registry to DHT while maintaining
// backward compatibility.
type CompositeDiscovery struct {
	providers []DiscoveryProvider
	logger    *slog.Logger
}

// NewCompositeDiscovery creates a composite discovery that queries all given providers.
func NewCompositeDiscovery(logger *slog.Logger, providers ...DiscoveryProvider) *CompositeDiscovery {
	return &CompositeDiscovery{
		providers: providers,
		logger:    logger,
	}
}

func (c *CompositeDiscovery) DiscoverBySkill(ctx context.Context, skillID string) ([]AgentEndpoint, error) {
	type result struct {
		endpoints []AgentEndpoint
		err       error
	}

	results := make([]result, len(c.providers))
	var wg sync.WaitGroup
	for i, p := range c.providers {
		wg.Add(1)
		go func(idx int, prov DiscoveryProvider) {
			defer wg.Done()
			eps, err := prov.DiscoverBySkill(ctx, skillID)
			results[idx] = result{endpoints: eps, err: err}
		}(i, p)
	}
	wg.Wait()

	// Merge and deduplicate by PeerID.
	seen := make(map[string]bool)
	var merged []AgentEndpoint
	var lastErr error

	for _, r := range results {
		if r.err != nil {
			lastErr = r.err
			continue
		}
		for _, ep := range r.endpoints {
			key := ep.PeerID.String()
			if !seen[key] {
				seen[key] = true
				merged = append(merged, ep)
			}
		}
	}

	// Return results if we have any, even if some providers failed.
	if len(merged) > 0 {
		return merged, nil
	}
	// If all providers failed or returned empty, propagate the last error.
	if lastErr != nil {
		return nil, lastErr
	}
	return merged, nil
}

func (c *CompositeDiscovery) RegisterSkills(ctx context.Context, peerID string, skills []SkillInfo, agentName, agentDesc string) error {
	var firstErr error
	anyOK := false
	for _, p := range c.providers {
		if err := p.RegisterSkills(ctx, peerID, skills, agentName, agentDesc); err != nil {
			c.logger.Warn("composite: provider registration failed", "error", err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			anyOK = true
		}
	}
	// Succeed if at least one provider accepted the registration.
	if anyOK {
		return nil
	}
	return firstErr
}

func (c *CompositeDiscovery) UnregisterSkills(ctx context.Context, peerID string, skillIDs []string) error {
	var firstErr error
	anyOK := false
	for _, p := range c.providers {
		if err := p.UnregisterSkills(ctx, peerID, skillIDs); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			anyOK = true
		}
	}
	if anyOK {
		return nil
	}
	return firstErr
}

func (c *CompositeDiscovery) Close() error {
	var firstErr error
	for _, p := range c.providers {
		if err := p.Close(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
