// Package crypto provides DID (Decentralized Identifier) utilities for
// bidirectional conversion between libp2p PeerIDs and W3C did:key identifiers.
//
// This is a thin wrapper around the agentanycast-identity package that adapts
// between libp2p peer.ID types and standard library ed25519 types.
package crypto

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/AgentAnycast/agentanycast-identity"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ErrUnsupportedKeyType is returned when the PeerID does not use an Ed25519 key.
var ErrUnsupportedKeyType = errors.New("only Ed25519 keys are supported for did:key conversion")

// ErrInvalidDIDKey is returned when the provided did:key string is malformed.
var ErrInvalidDIDKey = errors.New("invalid did:key format")

// PeerIDToDIDKey converts a libp2p PeerID (backed by an Ed25519 key) to a
// W3C did:key string. Returns an error if the PeerID does not use Ed25519.
func PeerIDToDIDKey(pid peer.ID) (string, error) {
	pub, err := pid.ExtractPublicKey()
	if err != nil {
		return "", fmt.Errorf("extract public key from PeerID: %w", err)
	}

	if pub.Type() != crypto.Ed25519 {
		return "", ErrUnsupportedKeyType
	}

	raw, err := pub.Raw()
	if err != nil {
		return "", fmt.Errorf("extract raw public key bytes: %w", err)
	}

	return identity.PubKeyToDIDKey(ed25519.PublicKey(raw))
}

// DIDKeyToPeerID converts a W3C did:key string (Ed25519) back to a libp2p PeerID.
func DIDKeyToPeerID(didKey string) (peer.ID, error) {
	pub, err := identity.DIDKeyToPubKey(didKey)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidDIDKey, err)
	}

	libp2pPub, err := crypto.UnmarshalEd25519PublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("unmarshal Ed25519 public key: %w", err)
	}

	pid, err := peer.IDFromPublicKey(libp2pPub)
	if err != nil {
		return "", fmt.Errorf("derive PeerID from public key: %w", err)
	}

	return pid, nil
}
