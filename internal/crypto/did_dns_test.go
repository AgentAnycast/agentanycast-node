package crypto

import (
	"context"
	"crypto/rand"
	"net"
	"testing"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// mockDNSResolver is a test double for DNS lookups.
type mockDNSResolver struct {
	records map[string][]string
	err     error
}

func (m *mockDNSResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.records[name], nil
}

func testDIDKey(t *testing.T) (string, peer.ID) {
	t.Helper()
	priv, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatalf("derive PeerID: %v", err)
	}
	didKey, err := PeerIDToDIDKey(pid)
	if err != nil {
		t.Fatalf("PeerIDToDIDKey: %v", err)
	}
	return didKey, pid
}

func TestParseDIDDNSDomain(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"did:dns:example.com", "example.com", false},
		{"did:dns:sub.example.com", "sub.example.com", false},
		{"did:dns:", "", true},
		{"did:web:example.com", "", true},
		{"did:dns:bad domain", "", true},
	}

	for _, tt := range tests {
		got, err := parseDIDDNSDomain(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseDIDDNSDomain(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseDIDDNSDomain(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractDIDKeyFromTXT(t *testing.T) {
	didKey := "did:key:z6Mkf5rGMoatrSj1f4CyvuHBeXJELe9RPdzo2PKGNCKVtZxP"

	tests := []struct {
		record string
		want   string
	}{
		{"did=" + didKey, didKey},
		{didKey, didKey},
		{"  did=" + didKey + "  ", didKey},
		{"something-else", ""},
		{"did=not-a-did-key", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := extractDIDKeyFromTXT(tt.record)
		if got != tt.want {
			t.Errorf("extractDIDKeyFromTXT(%q) = %q, want %q", tt.record, got, tt.want)
		}
	}
}

func TestResolveDIDDNS_SingleEntry(t *testing.T) {
	didKey, pid := testDIDKey(t)

	resolver := &mockDNSResolver{
		records: map[string][]string{
			"_did.example.com": {"did=" + didKey},
		},
	}

	entries, err := resolveDIDDNSWithResolver(context.Background(), "did:dns:example.com", resolver)
	if err != nil {
		t.Fatalf("resolveDIDDNSWithResolver: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].DIDKey != didKey {
		t.Errorf("DIDKey = %q, want %q", entries[0].DIDKey, didKey)
	}
	if entries[0].PeerID != pid.String() {
		t.Errorf("PeerID = %q, want %q", entries[0].PeerID, pid.String())
	}
}

func TestResolveDIDDNS_MultipleEntries(t *testing.T) {
	didKey1, _ := testDIDKey(t)
	didKey2, _ := testDIDKey(t)

	resolver := &mockDNSResolver{
		records: map[string][]string{
			"_did.example.com": {
				"did=" + didKey1,
				didKey2, // bare format
			},
		},
	}

	entries, err := resolveDIDDNSWithResolver(context.Background(), "did:dns:example.com", resolver)
	if err != nil {
		t.Fatalf("resolveDIDDNSWithResolver: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestResolveDIDDNS_NoRecords(t *testing.T) {
	resolver := &mockDNSResolver{
		records: map[string][]string{
			"_did.example.com": {},
		},
	}

	entries, err := resolveDIDDNSWithResolver(context.Background(), "did:dns:example.com", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestResolveDIDDNS_SkipsInvalidRecords(t *testing.T) {
	didKey, _ := testDIDKey(t)

	resolver := &mockDNSResolver{
		records: map[string][]string{
			"_did.example.com": {
				"not-a-did-record",
				"did=" + didKey,
				"v=spf1 include:example.com",
			},
		},
	}

	entries, err := resolveDIDDNSWithResolver(context.Background(), "did:dns:example.com", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (skipping invalid), got %d", len(entries))
	}
}

func TestResolveDIDDNS_InvalidDID(t *testing.T) {
	resolver := &mockDNSResolver{}

	_, err := resolveDIDDNSWithResolver(context.Background(), "did:web:example.com", resolver)
	if err == nil {
		t.Fatal("expected error for non-did:dns input")
	}
}

func TestResolveDIDDNS_LookupError(t *testing.T) {
	resolver := &mockDNSResolver{
		err: &net.DNSError{Err: "no such host", Name: "_did.example.com"},
	}

	_, err := resolveDIDDNSWithResolver(context.Background(), "did:dns:example.com", resolver)
	if err == nil {
		t.Fatal("expected error for DNS lookup failure")
	}
}
