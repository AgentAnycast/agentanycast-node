package crypto

import (
	"crypto/rand"
	"testing"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mr-tron/base58"
)

func generateTestPeerID(t *testing.T) peer.ID {
	t.Helper()
	priv, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatalf("derive PeerID: %v", err)
	}
	return pid
}

func TestPeerIDToDIDKey(t *testing.T) {
	pid := generateTestPeerID(t)

	did, err := PeerIDToDIDKey(pid)
	if err != nil {
		t.Fatalf("PeerIDToDIDKey: %v", err)
	}

	if did == "" {
		t.Fatal("expected non-empty did:key")
	}
	if did[:8] != "did:key:" {
		t.Errorf("expected did:key: prefix, got %q", did[:8])
	}
	if did[8] != 'z' {
		t.Errorf("expected multibase base58btc prefix 'z', got %c", did[8])
	}
}

func TestDIDKeyToPeerID(t *testing.T) {
	pid := generateTestPeerID(t)

	did, err := PeerIDToDIDKey(pid)
	if err != nil {
		t.Fatalf("PeerIDToDIDKey: %v", err)
	}

	recovered, err := DIDKeyToPeerID(did)
	if err != nil {
		t.Fatalf("DIDKeyToPeerID: %v", err)
	}

	if pid != recovered {
		t.Errorf("round-trip failed: %s != %s", pid, recovered)
	}
}

func TestRoundTripMultipleKeys(t *testing.T) {
	for i := 0; i < 20; i++ {
		pid := generateTestPeerID(t)

		did, err := PeerIDToDIDKey(pid)
		if err != nil {
			t.Fatalf("iteration %d: PeerIDToDIDKey: %v", i, err)
		}

		recovered, err := DIDKeyToPeerID(did)
		if err != nil {
			t.Fatalf("iteration %d: DIDKeyToPeerID: %v", i, err)
		}

		if pid != recovered {
			t.Errorf("iteration %d: round-trip mismatch: %s != %s", i, pid, recovered)
		}
	}
}

func TestDIDKeyToPeerID_InvalidPrefix(t *testing.T) {
	_, err := DIDKeyToPeerID("not-a-did")
	if err == nil {
		t.Fatal("expected error for invalid prefix")
	}
}

func TestDIDKeyToPeerID_TooShort(t *testing.T) {
	_, err := DIDKeyToPeerID("did:key:z1234")
	if err == nil {
		t.Fatal("expected error for too-short payload")
	}
}

func TestDIDKeyToPeerID_WrongMulticodec(t *testing.T) {
	// Encode with a wrong multicodec prefix (0x00 0x01 instead of 0xed 0x01).
	fakePayload := make([]byte, 34)
	fakePayload[0] = 0x00
	fakePayload[1] = 0x01
	encoded := "did:key:z" + base58.Encode(fakePayload)

	_, err := DIDKeyToPeerID(encoded)
	if err == nil {
		t.Fatal("expected error for wrong multicodec prefix")
	}
}
