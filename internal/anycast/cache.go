package anycast

import (
	"sync"
	"time"
)

// RouteCache is a TTL-based cache for discovery results.
type RouteCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	endpoints []AgentEndpoint
	expiresAt time.Time
}

// NewRouteCache creates a new cache with the given TTL.
func NewRouteCache(ttl time.Duration) *RouteCache {
	return &RouteCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
}

// Get returns cached endpoints for the given skill, or nil if not cached or expired.
func (c *RouteCache) Get(skillID string) []AgentEndpoint {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[skillID]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil
	}
	return entry.endpoints
}

// Put stores endpoints in the cache.
func (c *RouteCache) Put(skillID string, endpoints []AgentEndpoint) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[skillID] = &cacheEntry{
		endpoints: endpoints,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate removes a specific skill from the cache.
func (c *RouteCache) Invalidate(skillID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, skillID)
}

// Clear removes all cache entries.
func (c *RouteCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
}
