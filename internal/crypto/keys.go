// Package crypto handles Ed25519 key generation, loading, and persistence.
//
// This package is a thin wrapper around the agentanycast-identity package,
// adapting its standard library types to the libp2p crypto types used
// throughout the node codebase.
package crypto

import (
	"crypto/ed25519"
	"fmt"

	"github.com/AgentAnycast/agentanycast-identity"
	"github.com/libp2p/go-libp2p/core/crypto"
)

// LoadOrGenerateKey loads an Ed25519 private key from path.
// If the file does not exist, it generates a new key and saves it.
func LoadOrGenerateKey(path string) (crypto.PrivKey, error) {
	stdKey, err := identity.LoadOrGenerateKey(path)
	if err != nil {
		return nil, err
	}

	return stdKeyToLibp2p(stdKey)
}

// stdKeyToLibp2p converts a standard library ed25519.PrivateKey to a libp2p PrivKey.
func stdKeyToLibp2p(stdKey ed25519.PrivateKey) (crypto.PrivKey, error) {
	// libp2p's UnmarshalEd25519PrivateKey expects the 64-byte seed+pubkey format,
	// which is exactly what ed25519.PrivateKey is.
	priv, err := crypto.UnmarshalEd25519PrivateKey(stdKey)
	if err != nil {
		return nil, fmt.Errorf("convert ed25519 key to libp2p: %w", err)
	}
	return priv, nil
}
