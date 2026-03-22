package core

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func generateTestKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	return pub, priv
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	senderPub, senderPriv := generateTestKeyPair(t)
	recipientPub, recipientPriv := generateTestKeyPair(t)

	plaintext := []byte("hello, encrypted world!")

	encrypted, err := EncryptPayload(plaintext, senderPriv, recipientPub)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Encrypted data must be longer than plaintext (nonce + overhead).
	if len(encrypted) <= len(plaintext) {
		t.Fatalf("encrypted data too short: %d bytes", len(encrypted))
	}

	decrypted, err := DecryptPayload(encrypted, senderPub, recipientPriv)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("decrypted text does not match: got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptWithWrongKey(t *testing.T) {
	_, senderPriv := generateTestKeyPair(t)
	recipientPub, _ := generateTestKeyPair(t)
	wrongPub, wrongPriv := generateTestKeyPair(t)

	plaintext := []byte("secret message")

	encrypted, err := EncryptPayload(plaintext, senderPriv, recipientPub)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Attempt decryption with the wrong private key.
	_, err = DecryptPayload(encrypted, wrongPub, wrongPriv)
	if err == nil {
		t.Fatal("expected decryption to fail with wrong key")
	}
}

func TestEncryptEmptyPayload(t *testing.T) {
	senderPub, senderPriv := generateTestKeyPair(t)
	recipientPub, recipientPriv := generateTestKeyPair(t)

	plaintext := []byte{}

	encrypted, err := EncryptPayload(plaintext, senderPriv, recipientPub)
	if err != nil {
		t.Fatalf("encrypt empty payload: %v", err)
	}

	decrypted, err := DecryptPayload(encrypted, senderPub, recipientPriv)
	if err != nil {
		t.Fatalf("decrypt empty payload: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("decrypted empty payload mismatch: got %q", decrypted)
	}
}

func TestEncryptLargePayload(t *testing.T) {
	senderPub, senderPriv := generateTestKeyPair(t)
	recipientPub, recipientPriv := generateTestKeyPair(t)

	// 1 MB payload.
	plaintext := make([]byte, 1<<20)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatalf("generate random payload: %v", err)
	}

	encrypted, err := EncryptPayload(plaintext, senderPriv, recipientPub)
	if err != nil {
		t.Fatalf("encrypt large payload: %v", err)
	}

	decrypted, err := DecryptPayload(encrypted, senderPub, recipientPriv)
	if err != nil {
		t.Fatalf("decrypt large payload: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatal("decrypted large payload does not match original")
	}
}

func TestDecryptTooShort(t *testing.T) {
	_, recipientPriv := generateTestKeyPair(t)
	senderPub, _ := generateTestKeyPair(t)

	// Data shorter than the 24-byte nonce.
	_, err := DecryptPayload([]byte("short"), senderPub, recipientPriv)
	if err == nil {
		t.Fatal("expected error for short ciphertext")
	}
}
