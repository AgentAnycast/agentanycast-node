package anycast

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// Router resolves skill-based addresses to concrete peer endpoints.
type Router struct {
	discovery DiscoveryProvider
	strategy  RoutingStrategy
	cache     *RouteCache
	logger    *slog.Logger
}

// RouterConfig holds configuration for the Anycast Router.
type RouterConfig struct {
	Discovery DiscoveryProvider
	Strategy  RoutingStrategy
	CacheTTL  time.Duration
	Logger    *slog.Logger
}

// NewRouter creates a new Anycast Router.
func NewRouter(cfg RouterConfig) *Router {
	if cfg.Strategy == nil {
		cfg.Strategy = &RandomStrategy{}
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 30 * time.Second
	}

	return &Router{
		discovery: cfg.Discovery,
		strategy:  cfg.Strategy,
		cache:     NewRouteCache(cfg.CacheTTL),
		logger:    cfg.Logger,
	}
}

// Resolve finds a target peer for the given skill.
// It checks the cache first, then queries the discovery provider.
func (r *Router) Resolve(ctx context.Context, skillID string) (peer.ID, error) {
	// Check cache first.
	if endpoints := r.cache.Get(skillID); len(endpoints) > 0 {
		selected, err := r.strategy.Select(ctx, endpoints)
		if err == nil {
			r.logger.Debug("route resolved from cache",
				"skill_id", skillID,
				"target", selected.PeerID,
			)
			return selected.PeerID, nil
		}
	}

	// Query discovery provider.
	endpoints, err := r.discovery.DiscoverBySkill(ctx, skillID)
	if err != nil {
		return "", fmt.Errorf("discover skill %q: %w", skillID, err)
	}
	if len(endpoints) == 0 {
		return "", fmt.Errorf("no agents found for skill %q", skillID)
	}

	// Cache the results.
	r.cache.Put(skillID, endpoints)

	// Select a target.
	selected, err := r.strategy.Select(ctx, endpoints)
	if err != nil {
		return "", fmt.Errorf("select target for skill %q: %w", skillID, err)
	}

	r.logger.Info("route resolved",
		"skill_id", skillID,
		"target", selected.PeerID,
		"candidates", len(endpoints),
	)
	return selected.PeerID, nil
}

// Discover returns all agents that offer the given skill (for the SDK's discover() API).
func (r *Router) Discover(ctx context.Context, skillID string) ([]AgentEndpoint, error) {
	endpoints, err := r.discovery.DiscoverBySkill(ctx, skillID)
	if err != nil {
		return nil, fmt.Errorf("discover skill %q: %w", skillID, err)
	}

	// Update cache as a side effect.
	if len(endpoints) > 0 {
		r.cache.Put(skillID, endpoints)
	}

	return endpoints, nil
}

// RegisterSkills registers the local node's skills with the discovery provider.
func (r *Router) RegisterSkills(ctx context.Context, peerID string, skills []SkillInfo, agentName, agentDesc string) error {
	return r.discovery.RegisterSkills(ctx, peerID, skills, agentName, agentDesc)
}

// UnregisterSkills removes the local node's skills.
func (r *Router) UnregisterSkills(ctx context.Context, peerID string, skillIDs []string) error {
	return r.discovery.UnregisterSkills(ctx, peerID, skillIDs)
}

// InvalidateCache clears cached routes for a specific skill or all skills.
func (r *Router) InvalidateCache(skillID string) {
	if skillID == "" {
		r.cache.Clear()
	} else {
		r.cache.Invalidate(skillID)
	}
}

// Close releases resources.
func (r *Router) Close() error {
	return r.discovery.Close()
}
