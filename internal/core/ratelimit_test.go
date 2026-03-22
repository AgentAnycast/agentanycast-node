package core

import (
	"testing"

	"github.com/agentanycast/agentanycast-node/internal/config"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

func TestRateLimiterAllow(t *testing.T) {
	cfg := config.RateLimitConfig{DefaultRPS: 100}
	rl := NewRateLimiter(cfg, nil)
	defer rl.Stop()

	source := envelope.AgentIdentity{DIDKey: "did:key:z123"}

	// First request should always be allowed.
	if !rl.Allow(source) {
		t.Fatal("expected first request to be allowed")
	}
}

func TestRateLimiterExceed(t *testing.T) {
	// Set a very low rate limit to trigger denial.
	cfg := config.RateLimitConfig{DefaultRPS: 1}
	rl := NewRateLimiter(cfg, nil)
	defer rl.Stop()

	source := envelope.AgentIdentity{DIDKey: "did:key:z123"}

	// First request uses the burst token.
	if !rl.Allow(source) {
		t.Fatal("expected first request to be allowed")
	}

	// Second request should exceed the limit (burst=1, no time passed).
	if rl.Allow(source) {
		t.Fatal("expected second request to be denied")
	}
}

func TestRateLimiterOverride(t *testing.T) {
	cfg := config.RateLimitConfig{
		DefaultRPS: 1,
		Overrides: []config.RateLimitRule{
			{Source: "did:key:vip", RPS: 1000},
		},
	}
	rl := NewRateLimiter(cfg, nil)
	defer rl.Stop()

	vip := envelope.AgentIdentity{DIDKey: "did:key:vip"}
	regular := envelope.AgentIdentity{DIDKey: "did:key:regular"}

	// VIP can make many requests.
	for i := 0; i < 100; i++ {
		if !rl.Allow(vip) {
			t.Fatalf("expected VIP request %d to be allowed", i)
		}
	}

	// Regular user hits the limit after burst.
	rl.Allow(regular) // consume burst
	if rl.Allow(regular) {
		t.Fatal("expected regular user to be rate limited")
	}
}

func TestRateLimiterUnknownSource(t *testing.T) {
	cfg := config.RateLimitConfig{DefaultRPS: 1}
	rl := NewRateLimiter(cfg, nil)
	defer rl.Stop()

	// Empty identity should always be allowed.
	empty := envelope.AgentIdentity{}
	for i := 0; i < 10; i++ {
		if !rl.Allow(empty) {
			t.Fatalf("expected unknown source request %d to be allowed", i)
		}
	}
}

func TestRateLimiterAllowN(t *testing.T) {
	cfg := config.RateLimitConfig{DefaultRPS: 1}
	rl := NewRateLimiter(cfg, nil)
	defer rl.Stop()

	source := envelope.AgentIdentity{DIDKey: "did:key:z123"}

	// First call succeeds.
	if err := rl.AllowN(source); err != nil {
		t.Fatalf("expected first AllowN to succeed, got: %v", err)
	}

	// Second call returns error.
	if err := rl.AllowN(source); err == nil {
		t.Fatal("expected AllowN to return error when rate limited")
	}
}
