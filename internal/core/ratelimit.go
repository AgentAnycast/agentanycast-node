package core

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/agentanycast/agentanycast-node/internal/config"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

// RateLimiter enforces per-source rate limits using a token bucket algorithm.
// Limiters are lazily created per source identity and evicted after a period
// of inactivity.
type RateLimiter struct {
	mu         sync.Mutex
	limiters   map[string]*limiterEntry
	defaultRPS int
	overrides  map[string]int // source identity -> RPS
	logger     *slog.Logger
	stopCh     chan struct{}
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter creates a rate limiter with default and per-source limits.
func NewRateLimiter(cfg config.RateLimitConfig, logger *slog.Logger) *RateLimiter {
	if logger == nil {
		logger = slog.Default()
	}

	defaultRPS := cfg.DefaultRPS
	if defaultRPS <= 0 {
		defaultRPS = 100
	}

	overrides := make(map[string]int)
	for _, o := range cfg.Overrides {
		overrides[o.Source] = o.RPS
	}

	rl := &RateLimiter{
		limiters:   make(map[string]*limiterEntry),
		defaultRPS: defaultRPS,
		overrides:  overrides,
		logger:     logger.With("component", "ratelimit"),
		stopCh:     make(chan struct{}),
	}

	// Start cleanup goroutine to evict entries not seen for 10 minutes.
	go rl.cleanup()

	return rl
}

// Allow returns true if the source is within its rate limit.
func (rl *RateLimiter) Allow(source envelope.AgentIdentity) bool {
	sourceID := source.DIDKey
	if sourceID == "" {
		sourceID = source.PeerID
	}
	if sourceID == "" {
		return true // Unknown source — allow.
	}

	rl.mu.Lock()
	entry, ok := rl.limiters[sourceID]
	if !ok {
		rps := rl.defaultRPS
		if override, exists := rl.overrides[sourceID]; exists {
			rps = override
		}
		entry = &limiterEntry{
			limiter: rate.NewLimiter(rate.Limit(rps), rps), // burst = rps
		}
		rl.limiters[sourceID] = entry
	}
	entry.lastSeen = time.Now()
	rl.mu.Unlock()

	allowed := entry.limiter.Allow()
	if !allowed {
		rl.logger.Info("rate limit exceeded", "source", sourceID)
	}
	return allowed
}

// Stop terminates the background cleanup goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// AllowN returns a rate-limit error if the source exceeds its limit.
// This is a convenience wrapper that returns an error instead of a bool.
func (rl *RateLimiter) AllowN(source envelope.AgentIdentity) error {
	if !rl.Allow(source) {
		sourceID := source.DIDKey
		if sourceID == "" {
			sourceID = source.PeerID
		}
		return fmt.Errorf("rate limit exceeded for %s", sourceID)
	}
	return nil
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			threshold := time.Now().Add(-10 * time.Minute)
			for k, v := range rl.limiters {
				if v.lastSeen.Before(threshold) {
					delete(rl.limiters, k)
				}
			}
			rl.mu.Unlock()
		case <-rl.stopCh:
			return
		}
	}
}
