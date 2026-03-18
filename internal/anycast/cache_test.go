package anycast

import (
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestRouteCacheGetPut(t *testing.T) {
	cache := NewRouteCache(1 * time.Second)

	endpoints := []AgentEndpoint{
		{PeerID: peer.ID("peer-1"), AgentName: "Agent A"},
		{PeerID: peer.ID("peer-2"), AgentName: "Agent B"},
	}

	// Miss on empty cache.
	if got := cache.Get("summarize"); got != nil {
		t.Fatalf("expected nil on empty cache, got %v", got)
	}

	cache.Put("summarize", endpoints)

	got := cache.Get("summarize")
	if len(got) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(got))
	}
	if got[0].AgentName != "Agent A" {
		t.Fatalf("expected Agent A, got %s", got[0].AgentName)
	}
}

func TestRouteCacheTTLExpiration(t *testing.T) {
	cache := NewRouteCache(50 * time.Millisecond)

	cache.Put("echo", []AgentEndpoint{
		{PeerID: peer.ID("peer-1")},
	})

	// Should be available immediately.
	if got := cache.Get("echo"); len(got) != 1 {
		t.Fatal("expected 1 endpoint before expiry")
	}

	time.Sleep(60 * time.Millisecond)

	// Should be expired.
	if got := cache.Get("echo"); got != nil {
		t.Fatal("expected nil after TTL expiry")
	}
}

func TestRouteCacheInvalidate(t *testing.T) {
	cache := NewRouteCache(5 * time.Second)

	cache.Put("skill-a", []AgentEndpoint{{PeerID: peer.ID("p1")}})
	cache.Put("skill-b", []AgentEndpoint{{PeerID: peer.ID("p2")}})

	cache.Invalidate("skill-a")

	if got := cache.Get("skill-a"); got != nil {
		t.Fatal("expected nil after invalidate")
	}
	if got := cache.Get("skill-b"); len(got) != 1 {
		t.Fatal("expected skill-b to still be cached")
	}
}

func TestRouteCacheClear(t *testing.T) {
	cache := NewRouteCache(5 * time.Second)

	cache.Put("skill-a", []AgentEndpoint{{PeerID: peer.ID("p1")}})
	cache.Put("skill-b", []AgentEndpoint{{PeerID: peer.ID("p2")}})

	cache.Clear()

	if got := cache.Get("skill-a"); got != nil {
		t.Fatal("expected nil after clear")
	}
	if got := cache.Get("skill-b"); got != nil {
		t.Fatal("expected nil after clear")
	}
}

func TestRouteCacheConcurrency(t *testing.T) {
	cache := NewRouteCache(1 * time.Second)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			cache.Put("echo", []AgentEndpoint{{PeerID: peer.ID("p1")}})
		}()
		go func() {
			defer wg.Done()
			_ = cache.Get("echo")
		}()
		go func() {
			defer wg.Done()
			cache.Invalidate("echo")
		}()
	}
	wg.Wait()
}
