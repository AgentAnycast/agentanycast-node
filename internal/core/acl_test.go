package core

import (
	"testing"

	"github.com/agentanycast/agentanycast-node/internal/config"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

func TestACLAllowAll(t *testing.T) {
	rules := []config.ACLRule{
		{Source: "*", Skill: "*", Allow: true},
	}
	acl := NewACLManager(rules, nil)

	err := acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:z123"}, "translate")
	if err != nil {
		t.Fatalf("expected access allowed, got: %v", err)
	}
}

func TestACLDenySpecificSkill(t *testing.T) {
	rules := []config.ACLRule{
		{Source: "*", Skill: "admin", Allow: false},
		{Source: "*", Skill: "*", Allow: true},
	}
	acl := NewACLManager(rules, nil)

	// Admin skill should be denied.
	err := acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:z123"}, "admin")
	if err == nil {
		t.Fatal("expected access denied for admin skill")
	}

	// Other skills should be allowed.
	err = acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:z123"}, "translate")
	if err != nil {
		t.Fatalf("expected access allowed for translate, got: %v", err)
	}
}

func TestACLGlobMatch(t *testing.T) {
	rules := []config.ACLRule{
		{Source: "did:web:trusted.com:*", Skill: "*", Allow: true},
		{Source: "*", Skill: "*", Allow: false},
	}
	acl := NewACLManager(rules, nil)

	// Trusted source should be allowed.
	err := acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:web:trusted.com:agents:bot1"}, "translate")
	if err != nil {
		t.Fatalf("expected access allowed for trusted source, got: %v", err)
	}

	// Untrusted source should be denied.
	err = acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:z456"}, "translate")
	if err == nil {
		t.Fatal("expected access denied for untrusted source")
	}
}

func TestACLFirstMatchWins(t *testing.T) {
	rules := []config.ACLRule{
		{Source: "did:key:special", Skill: "admin", Allow: true},
		{Source: "*", Skill: "admin", Allow: false},
		{Source: "*", Skill: "*", Allow: true},
	}
	acl := NewACLManager(rules, nil)

	// Special user can access admin.
	err := acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:special"}, "admin")
	if err != nil {
		t.Fatalf("expected special user to access admin, got: %v", err)
	}

	// Regular user cannot access admin.
	err = acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:regular"}, "admin")
	if err == nil {
		t.Fatal("expected regular user to be denied admin access")
	}

	// Regular user can access other skills.
	err = acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:regular"}, "translate")
	if err != nil {
		t.Fatalf("expected regular user to access translate, got: %v", err)
	}
}

func TestACLNoRulesAllowAll(t *testing.T) {
	acl := NewACLManager(nil, nil)

	err := acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:anyone"}, "any-skill")
	if err != nil {
		t.Fatalf("expected access allowed when no rules configured, got: %v", err)
	}
}

func TestACLDefaultDeny(t *testing.T) {
	rules := []config.ACLRule{
		{Source: "did:key:allowed", Skill: "translate", Allow: true},
	}
	acl := NewACLManager(rules, nil)

	// Matching rule allows access.
	err := acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:allowed"}, "translate")
	if err != nil {
		t.Fatalf("expected access allowed, got: %v", err)
	}

	// Non-matching source: default deny.
	err = acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:other"}, "translate")
	if err == nil {
		t.Fatal("expected default deny for non-matching source")
	}

	// Non-matching skill: default deny.
	err = acl.CheckAccess(envelope.AgentIdentity{DIDKey: "did:key:allowed"}, "admin")
	if err == nil {
		t.Fatal("expected default deny for non-matching skill")
	}
}

func TestACLFallbackToPeerID(t *testing.T) {
	rules := []config.ACLRule{
		{Source: "12D3KooW*", Skill: "*", Allow: true},
	}
	acl := NewACLManager(rules, nil)

	// When DIDKey is empty, should fall back to PeerID.
	err := acl.CheckAccess(envelope.AgentIdentity{PeerID: "12D3KooWPeer"}, "translate")
	if err != nil {
		t.Fatalf("expected access allowed via PeerID fallback, got: %v", err)
	}
}
