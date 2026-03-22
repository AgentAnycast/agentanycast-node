// Package core — crypto.go implements envelope-level end-to-end encryption
// using NaCl box (X25519 + XSalsa20-Poly1305). Ed25519 keys are converted
// to X25519 for the Diffie-Hellman key exchange.
package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"fmt"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/nacl/box"
)

// EncryptPayload encrypts data for a specific recipient using NaCl box.
// Converts Ed25519 keys to X25519 for Diffie-Hellman key exchange.
// Returns encrypted data with a 24-byte nonce prepended.
func EncryptPayload(plaintext []byte, senderPrivate ed25519.PrivateKey, recipientPublic ed25519.PublicKey) ([]byte, error) {
	// Convert Ed25519 keys to X25519.
	senderX25519 := ed25519PrivateToX25519(senderPrivate)
	recipientX25519, err := ed25519PublicToX25519(recipientPublic)
	if err != nil {
		return nil, fmt.Errorf("convert recipient public key: %w", err)
	}

	// Generate random nonce.
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	// Encrypt with NaCl box.
	var senderKey [32]byte
	var recipientKey [32]byte
	copy(senderKey[:], senderX25519)
	copy(recipientKey[:], recipientX25519)

	encrypted := box.Seal(nonce[:], plaintext, &nonce, &recipientKey, &senderKey)
	return encrypted, nil
}

// DecryptPayload decrypts data from a specific sender.
// Expects a 24-byte nonce prepended to the ciphertext (as produced by EncryptPayload).
func DecryptPayload(encrypted []byte, senderPublic ed25519.PublicKey, recipientPrivate ed25519.PrivateKey) ([]byte, error) {
	if len(encrypted) < 24 {
		return nil, fmt.Errorf("ciphertext too short: need at least 24 bytes, got %d", len(encrypted))
	}

	var nonce [24]byte
	copy(nonce[:], encrypted[:24])
	ciphertext := encrypted[24:]

	senderX25519, err := ed25519PublicToX25519(senderPublic)
	if err != nil {
		return nil, fmt.Errorf("convert sender public key: %w", err)
	}
	recipientX25519 := ed25519PrivateToX25519(recipientPrivate)

	var senderKey [32]byte
	var recipientKey [32]byte
	copy(senderKey[:], senderX25519)
	copy(recipientKey[:], recipientX25519)

	plaintext, ok := box.Open(nil, ciphertext, &nonce, &senderKey, &recipientKey)
	if !ok {
		return nil, fmt.Errorf("decryption failed: authentication error")
	}
	return plaintext, nil
}

// ed25519PrivateToX25519 converts an Ed25519 private key to an X25519
// private key by hashing the seed with SHA-512 and clamping per RFC 7748.
func ed25519PrivateToX25519(priv ed25519.PrivateKey) []byte {
	h := sha512.Sum512(priv.Seed())
	// Clamp per RFC 7748.
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64
	return h[:32]
}

// ed25519PublicToX25519 converts an Ed25519 public key to an X25519 public
// key using the birational map from the Edwards curve to the Montgomery curve.
func ed25519PublicToX25519(pub ed25519.PublicKey) ([]byte, error) {
	p, err := new(edwards25519.Point).SetBytes(pub)
	if err != nil {
		return nil, fmt.Errorf("invalid ed25519 public key: %w", err)
	}
	return p.BytesMontgomery(), nil
}
