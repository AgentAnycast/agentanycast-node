package node

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
)

// TestConnectionUsesNoiseProtocol verifies that connections between two
// AgentAnycast hosts negotiate the Noise_XX security protocol.
func TestConnectionUsesNoiseProtocol(t *testing.T) {
	host1 := newTestHost(t)
	defer host1.Close()
	host2 := newTestHost(t)
	defer host2.Close()

	// Connect host1 → host2.
	host2Info := host2.Peerstore().PeerInfo(host2.ID())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := host1.Connect(ctx, host2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Verify the connection on host1's side uses Noise.
	conns := host1.Network().ConnsToPeer(host2.ID())
	if len(conns) == 0 {
		t.Fatal("no connections to host2")
	}
	sec := conns[0].ConnState().Security
	if sec != noise.ID {
		t.Fatalf("connection security = %q, want %q (Noise)", sec, noise.ID)
	}

	// Verify the reverse direction.
	conns2 := host2.Network().ConnsToPeer(host1.ID())
	if len(conns2) == 0 {
		t.Fatal("no connections from host2 to host1")
	}
	sec2 := conns2[0].ConnState().Security
	if sec2 != noise.ID {
		t.Fatalf("reverse connection security = %q, want %q (Noise)", sec2, noise.ID)
	}
}

// TestPlaintextConnectionRejected verifies that a plaintext-only (insecure)
// host cannot connect to a Noise-only AgentAnycast host, ensuring that
// unencrypted communication is impossible.
func TestPlaintextConnectionRejected(t *testing.T) {
	// Standard Noise-only host.
	noiseHost := newTestHost(t)
	defer noiseHost.Close()

	// Create a plaintext (insecure) host.
	insecureKey, _, err := libp2pcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	insecureHost, err := libp2p.New(
		libp2p.Identity(insecureKey),
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
		libp2p.NoSecurity,
	)
	if err != nil {
		t.Fatalf("create insecure host: %v", err)
	}
	defer insecureHost.Close()

	// Attempt to connect the insecure host to the Noise host.
	noiseInfo := peer.AddrInfo{
		ID:    noiseHost.ID(),
		Addrs: noiseHost.Addrs(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = insecureHost.Connect(ctx, noiseInfo)
	if err == nil {
		t.Fatal("insecure host connected to Noise host — expected failure")
	}

	// Verify no connections were established.
	conns := noiseHost.Network().ConnsToPeer(insecureHost.ID())
	if len(conns) > 0 {
		t.Fatalf("Noise host has %d connections to insecure host — expected 0", len(conns))
	}
}
