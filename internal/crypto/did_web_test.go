package crypto

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
)

func TestDIDWebToURL_DomainOnly(t *testing.T) {
	u, err := DIDWebToURL("did:web:example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "https://example.com/.well-known/did.json"
	if u != expected {
		t.Errorf("got %q, want %q", u, expected)
	}
}

func TestDIDWebToURL_DomainWithPath(t *testing.T) {
	u, err := DIDWebToURL("did:web:example.com:agents:alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "https://example.com/agents/alice/did.json"
	if u != expected {
		t.Errorf("got %q, want %q", u, expected)
	}
}

func TestDIDWebToURL_DomainWithPort(t *testing.T) {
	u, err := DIDWebToURL("did:web:example.com%3A8443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "https://example.com:8443/.well-known/did.json"
	if u != expected {
		t.Errorf("got %q, want %q", u, expected)
	}
}

func TestDIDWebToURL_DomainWithPortAndPath(t *testing.T) {
	u, err := DIDWebToURL("did:web:example.com%3A8443:agents:bob")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "https://example.com:8443/agents/bob/did.json"
	if u != expected {
		t.Errorf("got %q, want %q", u, expected)
	}
}

func TestDIDWebToURL_InvalidPrefix(t *testing.T) {
	_, err := DIDWebToURL("did:key:z1234")
	if err == nil {
		t.Fatal("expected error for non-did:web prefix")
	}
}

func TestDIDWebToURL_EmptyDomain(t *testing.T) {
	_, err := DIDWebToURL("did:web:")
	if err == nil {
		t.Fatal("expected error for empty domain")
	}
}

func TestGenerateDIDWeb_DomainOnly(t *testing.T) {
	did := GenerateDIDWeb("example.com")
	if did != "did:web:example.com" {
		t.Errorf("got %q, want %q", did, "did:web:example.com")
	}
}

func TestGenerateDIDWeb_WithPath(t *testing.T) {
	did := GenerateDIDWeb("example.com", "agents", "myagent")
	if did != "did:web:example.com:agents:myagent" {
		t.Errorf("got %q, want %q", did, "did:web:example.com:agents:myagent")
	}
}

func TestGenerateDIDWeb_WithPort(t *testing.T) {
	did := GenerateDIDWeb("example.com:8443")
	if did != "did:web:example.com%3A8443" {
		t.Errorf("got %q, want %q", did, "did:web:example.com%3A8443")
	}
}

func TestGenerateDIDWeb_RoundTrip(t *testing.T) {
	tests := []struct {
		domain string
		path   []string
	}{
		{"example.com", nil},
		{"example.com", []string{"agents", "alice"}},
		{"example.com:8443", nil},
		{"example.com:8443", []string{"org", "dept", "agent"}},
	}

	for _, tt := range tests {
		did := GenerateDIDWeb(tt.domain, tt.path...)
		u, err := DIDWebToURL(did)
		if err != nil {
			t.Errorf("GenerateDIDWeb(%q, %v) -> DIDWebToURL error: %v", tt.domain, tt.path, err)
			continue
		}
		if u == "" {
			t.Errorf("GenerateDIDWeb(%q, %v) -> empty URL", tt.domain, tt.path)
		}
	}
}

func TestBuildDIDDocument_Valid(t *testing.T) {
	priv, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub := priv.GetPublic()
	raw, err := pub.Raw()
	if err != nil {
		t.Fatalf("get raw key: %v", err)
	}

	didWeb := "did:web:example.com:agents:test"
	doc := BuildDIDDocument(didWeb, raw)

	if doc.ID != didWeb {
		t.Errorf("doc.ID = %q, want %q", doc.ID, didWeb)
	}
	if len(doc.Context) != 2 {
		t.Errorf("expected 2 context entries, got %d", len(doc.Context))
	}
	if len(doc.VerificationMethod) != 1 {
		t.Fatalf("expected 1 verification method, got %d", len(doc.VerificationMethod))
	}
	vm := doc.VerificationMethod[0]
	if vm.Type != "Ed25519VerificationKey2020" {
		t.Errorf("vm.Type = %q, want Ed25519VerificationKey2020", vm.Type)
	}
	if vm.Controller != didWeb {
		t.Errorf("vm.Controller = %q, want %q", vm.Controller, didWeb)
	}
	if vm.PublicKeyMultibase == "" || vm.PublicKeyMultibase[0] != 'z' {
		t.Errorf("expected multibase base58btc key starting with 'z', got %q", vm.PublicKeyMultibase)
	}

	// Verify JSON serialization.
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal DID document: %v", err)
	}
	var parsed DIDDocument
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal DID document: %v", err)
	}
	if parsed.ID != didWeb {
		t.Errorf("round-trip: ID = %q, want %q", parsed.ID, didWeb)
	}
}

func TestExtractEd25519Key_RoundTrip(t *testing.T) {
	priv, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	raw, err := priv.GetPublic().Raw()
	if err != nil {
		t.Fatalf("get raw key: %v", err)
	}

	doc := BuildDIDDocument("did:web:example.com", raw)

	extracted, err := ExtractEd25519Key(doc)
	if err != nil {
		t.Fatalf("ExtractEd25519Key: %v", err)
	}

	if len(extracted) != len(raw) {
		t.Fatalf("key length mismatch: got %d, want %d", len(extracted), len(raw))
	}
	for i := range raw {
		if extracted[i] != raw[i] {
			t.Fatalf("key mismatch at byte %d", i)
		}
	}
}

func TestExtractEd25519Key_NoKey(t *testing.T) {
	doc := &DIDDocument{
		VerificationMethod: []VerificationMethod{
			{Type: "JsonWebKey2020", PublicKeyMultibase: "z1234"},
		},
	}
	_, err := ExtractEd25519Key(doc)
	if err != ErrNoEd25519Key {
		t.Errorf("expected ErrNoEd25519Key, got %v", err)
	}
}

func TestResolveDIDWeb_HTTPTest(t *testing.T) {
	priv, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	raw, err := priv.GetPublic().Raw()
	if err != nil {
		t.Fatalf("get raw key: %v", err)
	}

	// We use a fixed DID for the document served by the test server.
	// The actual resolution uses resolveDIDWebFromURL to bypass HTTPS requirement.
	testDID := "did:web:testserver.local"
	doc := BuildDIDDocument(testDID, raw)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/did.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/did+ld+json")
		json.NewEncoder(w).Encode(doc)
	}))
	defer srv.Close()

	ctx := context.Background()
	resolved, err := resolveDIDWebFromURL(ctx, testDID, srv.URL+"/.well-known/did.json")
	if err != nil {
		t.Fatalf("resolveDIDWebFromURL: %v", err)
	}

	if resolved.ID != testDID {
		t.Errorf("resolved ID = %q, want %q", resolved.ID, testDID)
	}

	// Verify we can extract the key from the resolved document.
	extracted, err := ExtractEd25519Key(resolved)
	if err != nil {
		t.Fatalf("ExtractEd25519Key from resolved doc: %v", err)
	}
	if len(extracted) != 32 {
		t.Errorf("expected 32-byte Ed25519 key, got %d bytes", len(extracted))
	}
}

func TestResolveDIDWeb_IDMismatch(t *testing.T) {
	doc := &DIDDocument{ID: "did:web:wrong.example.com"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(doc)
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := resolveDIDWebFromURL(ctx, "did:web:expected.example.com", srv.URL+"/.well-known/did.json")
	if err == nil {
		t.Fatal("expected error for DID document ID mismatch")
	}
}

func TestResolveDIDWeb_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	ctx := context.Background()
	_, err := resolveDIDWebFromURL(ctx, "did:web:test", srv.URL+"/.well-known/did.json")
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
}
