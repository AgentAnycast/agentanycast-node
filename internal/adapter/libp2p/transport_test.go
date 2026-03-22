package libp2p

import (
	"testing"
)

func TestMatchesTarget(t *testing.T) {
	// Transport with nil host — MatchesTarget only parses PeerID, no network needed.
	tr := &Transport{}

	tests := []struct {
		target string
		want   bool
	}{
		// Valid PeerIDs (base58btc multihash).
		{"12D3KooWDpJ7As7BWAwRMfu1VU2WCqNjvq387JEYKDBj4kx6nXTN", true},
		{"QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N", true},

		// Invalid — not a PeerID.
		{"http://example.com", false},
		{"nats://broker:4222", false},
		{"translate", false},
		{"", false},
	}

	for _, tt := range tests {
		got := tr.MatchesTarget(tt.target)
		if got != tt.want {
			t.Errorf("MatchesTarget(%q) = %v, want %v", tt.target, got, tt.want)
		}
	}
}

func TestName(t *testing.T) {
	tr := &Transport{}
	if tr.Name() != "libp2p" {
		t.Fatalf("expected 'libp2p', got %q", tr.Name())
	}
}
