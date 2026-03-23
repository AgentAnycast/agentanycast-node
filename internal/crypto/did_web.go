// Package crypto provides did:web DID method support.
//
// This is a thin wrapper around the agentanycast-identity package, re-exporting
// types and delegating function calls for did:web operations.
package crypto

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/AgentAnycast/agentanycast-identity"
)

// DIDDocument represents a minimal W3C DID Document.
// Re-exported from the identity package.
type DIDDocument = identity.DIDDocument

// VerificationMethod represents a verification method in a DID Document.
// Re-exported from the identity package.
type VerificationMethod = identity.VerificationMethod

// Errors for did:web operations. Re-exported from the identity package.
var (
	ErrInvalidDIDWeb    = identity.ErrInvalidDIDWeb
	ErrDIDWebResolve    = identity.ErrDIDWebResolve
	ErrDIDDocMismatch   = identity.ErrDIDDocMismatch
	ErrNoEd25519Key     = identity.ErrNoEd25519Key
	ErrInvalidMultibase = identity.ErrInvalidMultibase
)

// didWebHTTPClient is a shared HTTP client with a reasonable timeout for DID Web resolution.
var didWebHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
}

// DIDWebToURL converts a did:web identifier to its HTTPS resolution URL.
func DIDWebToURL(didWeb string) (string, error) {
	return identity.DIDWebToURL(didWeb)
}

// GenerateDIDWeb creates a did:web identifier string from a domain and optional path segments.
func GenerateDIDWeb(domain string, path ...string) string {
	return identity.GenerateDIDWeb(domain, path...)
}

// BuildDIDDocument creates a W3C DID Document for the given did:web and Ed25519 public key.
func BuildDIDDocument(didWeb string, pubKeyRaw []byte) *DIDDocument {
	return identity.BuildDIDDocument(didWeb, pubKeyRaw)
}

// ResolveDIDWeb fetches and parses a DID Document from the did:web URL.
// Resolved documents are cached for 5 minutes to reduce HTTP requests.
func ResolveDIDWeb(ctx context.Context, didWeb string) (*DIDDocument, error) {
	return identity.ResolveDIDWeb(ctx, didWeb)
}

// resolveDIDWebFromURL fetches and validates a DID Document from a given URL.
// Extracted for testability (allows using httptest servers).
func resolveDIDWebFromURL(ctx context.Context, didWeb, resolveURL string) (*DIDDocument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolveURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create request: %w", ErrDIDWebResolve, err)
	}
	req.Header.Set("Accept", "application/did+ld+json, application/json")

	resp, err := didWebHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: HTTP GET %s: %w", ErrDIDWebResolve, resolveURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d from %s", ErrDIDWebResolve, resp.StatusCode, resolveURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB limit
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %w", ErrDIDWebResolve, err)
	}

	var doc DIDDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: parse JSON: %w", ErrDIDWebResolve, err)
	}

	if doc.ID != didWeb {
		return nil, fmt.Errorf("%w: expected %s, got %s", ErrDIDDocMismatch, didWeb, doc.ID)
	}

	return &doc, nil
}

// ExtractEd25519Key extracts the first Ed25519 public key from a DID Document.
func ExtractEd25519Key(doc *DIDDocument) ([]byte, error) {
	return identity.ExtractEd25519Key(doc)
}
