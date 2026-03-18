// Package crypto provides DID (Decentralized Identifier) utilities for
// bidirectional conversion between libp2p PeerIDs and W3C did:key identifiers.
//
// The did:key method encodes a public key as:
//
//	did:key:z<multibase-base58btc(multicodec-prefix + raw-public-key)>
//
// For Ed25519 keys the multicodec prefix is 0xed01 (varint-encoded 0xed).
package crypto

import (
	"errors"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mr-tron/base58"
)

// ed25519MulticodecPrefix is the varint-encoded multicodec for Ed25519 public keys (0xed).
var ed25519MulticodecPrefix = []byte{0xed, 0x01}

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

	// Prepend the Ed25519 multicodec prefix.
	mcBytes := make([]byte, 0, len(ed25519MulticodecPrefix)+len(raw))
	mcBytes = append(mcBytes, ed25519MulticodecPrefix...)
	mcBytes = append(mcBytes, raw...)

	// Encode with multibase base58btc (prefix 'z').
	encoded := "z" + base58.Encode(mcBytes)

	return "did:key:" + encoded, nil
}

// DIDKeyToPeerID converts a W3C did:key string (Ed25519) back to a libp2p PeerID.
func DIDKeyToPeerID(didKey string) (peer.ID, error) {
	if !strings.HasPrefix(didKey, "did:key:z") {
		return "", ErrInvalidDIDKey
	}

	// Strip "did:key:z" prefix to get the base58btc-encoded payload.
	encoded := didKey[len("did:key:z"):]

	decoded, err := base58.Decode(encoded)
	if err != nil {
		return "", fmt.Errorf("base58 decode did:key: %w", err)
	}

	// Verify and strip the Ed25519 multicodec prefix.
	if len(decoded) < len(ed25519MulticodecPrefix)+32 {
		return "", fmt.Errorf("%w: payload too short", ErrInvalidDIDKey)
	}
	for i, b := range ed25519MulticodecPrefix {
		if decoded[i] != b {
			return "", fmt.Errorf("%w: unexpected multicodec prefix (only Ed25519 supported)", ErrInvalidDIDKey)
		}
	}

	rawPub := decoded[len(ed25519MulticodecPrefix):]

	pub, err := crypto.UnmarshalEd25519PublicKey(rawPub)
	if err != nil {
		return "", fmt.Errorf("unmarshal Ed25519 public key: %w", err)
	}

	pid, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("derive PeerID from public key: %w", err)
	}

	return pid, nil
}
