// Package crypto handles Ed25519 key generation, loading, and persistence.
package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
)

// LoadOrGenerateKey loads an Ed25519 private key from path.
// If the file does not exist, it generates a new key and saves it.
func LoadOrGenerateKey(path string) (crypto.PrivKey, error) {
	if path == "" {
		return generateKey()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return generateAndSaveKey(path)
		}
		return nil, fmt.Errorf("read key file: %w", err)
	}

	key, err := crypto.UnmarshalPrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("unmarshal private key: %w", err)
	}
	return key, nil
}

func generateKey() (crypto.PrivKey, error) {
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	return priv, nil
}

func generateAndSaveKey(path string) (crypto.PrivKey, error) {
	priv, err := generateKey()
	if err != nil {
		return nil, err
	}

	data, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, fmt.Errorf("write key file: %w", err)
	}

	return priv, nil
}
