// Package crypto provides did:web DID method support.
//
// did:web maps a DID to a JSON-LD document hosted on an HTTPS server.
//
//	did:web:example.com:agents:myagent -> https://example.com/agents/myagent/did.json
//	did:web:example.com                -> https://example.com/.well-known/did.json
package crypto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mr-tron/base58"
)

// didWebHTTPClient is a shared HTTP client with a reasonable timeout for DID Web resolution.
var didWebHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
}

// didDocCacheTTL is the time-to-live for cached DID documents.
const didDocCacheTTL = 5 * time.Minute

// didDocCacheInstance is the global DID document cache.
var didDocCacheInstance = &didDocCache{
	entries: make(map[string]*cachedDoc),
}

// didDocCache caches resolved DID documents with a TTL to reduce HTTP requests.
type didDocCache struct {
	mu      sync.RWMutex
	entries map[string]*cachedDoc
}

// cachedDoc holds a cached DID document and its expiration time.
type cachedDoc struct {
	doc       *DIDDocument
	expiresAt time.Time
}

// get returns a cached document if it exists and has not expired.
func (c *didDocCache) get(did string) (*DIDDocument, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[did]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.doc, true
}

// set stores a document in the cache with the configured TTL.
func (c *didDocCache) set(did string, doc *DIDDocument) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[did] = &cachedDoc{
		doc:       doc,
		expiresAt: time.Now().Add(didDocCacheTTL),
	}
}

// Errors for did:web operations.
var (
	ErrInvalidDIDWeb    = errors.New("invalid did:web format")
	ErrDIDWebResolve    = errors.New("did:web resolution failed")
	ErrDIDDocMismatch   = errors.New("DID document ID does not match requested DID")
	ErrNoEd25519Key     = errors.New("no Ed25519VerificationKey2020 found in DID document")
	ErrInvalidMultibase = errors.New("invalid multibase-encoded key")
)

// DIDDocument represents a minimal W3C DID Document.
type DIDDocument struct {
	Context            []string             `json:"@context"`
	ID                 string               `json:"id"`
	VerificationMethod []VerificationMethod `json:"verificationMethod"`
	Authentication     []string             `json:"authentication"`
	AssertionMethod    []string             `json:"assertionMethod,omitempty"`
}

// VerificationMethod represents a verification method in a DID Document.
type VerificationMethod struct {
	ID                 string `json:"id"`
	Type               string `json:"type"`
	Controller         string `json:"controller"`
	PublicKeyMultibase string `json:"publicKeyMultibase"`
}

// DIDWebToURL converts a did:web identifier to its HTTPS resolution URL.
//
// Examples:
//
//	did:web:example.com              -> https://example.com/.well-known/did.json
//	did:web:example.com:agents:alice -> https://example.com/agents/alice/did.json
//	did:web:example.com%3A8443       -> https://example.com:8443/.well-known/did.json
func DIDWebToURL(didWeb string) (string, error) {
	if !strings.HasPrefix(didWeb, "did:web:") {
		return "", fmt.Errorf("%w: missing did:web: prefix", ErrInvalidDIDWeb)
	}

	remainder := didWeb[len("did:web:"):]
	if remainder == "" {
		return "", fmt.Errorf("%w: empty domain", ErrInvalidDIDWeb)
	}

	parts := strings.Split(remainder, ":")

	// URL-decode the domain part (e.g., example.com%3A8443 -> example.com:8443).
	domain, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", fmt.Errorf("%w: invalid domain encoding: %w", ErrInvalidDIDWeb, err)
	}

	var path string
	if len(parts) > 1 {
		// Join remaining path segments.
		decodedParts := make([]string, len(parts)-1)
		for i, p := range parts[1:] {
			decoded, err := url.PathUnescape(p)
			if err != nil {
				return "", fmt.Errorf("%w: invalid path encoding: %w", ErrInvalidDIDWeb, err)
			}
			decodedParts[i] = decoded
		}
		path = "/" + strings.Join(decodedParts, "/") + "/did.json"
	} else {
		path = "/.well-known/did.json"
	}

	return "https://" + domain + path, nil
}

// GenerateDIDWeb creates a did:web identifier string from a domain and optional path segments.
//
// The domain may include a port (e.g., "example.com:8443"), which is percent-encoded.
// Path segments are joined with ":" separators.
func GenerateDIDWeb(domain string, path ...string) string {
	// Percent-encode colons in domain (for port numbers).
	encoded := strings.Replace(domain, ":", "%3A", 1)

	if len(path) == 0 {
		return "did:web:" + encoded
	}

	return "did:web:" + encoded + ":" + strings.Join(path, ":")
}

// BuildDIDDocument creates a W3C DID Document for the given did:web and Ed25519 public key.
func BuildDIDDocument(didWeb string, pubKeyRaw []byte) *DIDDocument {
	// Encode as multibase base58btc: "z" + base58btc(multicodec_ed25519_prefix + rawKey).
	mcBytes := make([]byte, 0, len(ed25519MulticodecPrefix)+len(pubKeyRaw))
	mcBytes = append(mcBytes, ed25519MulticodecPrefix...)
	mcBytes = append(mcBytes, pubKeyRaw...)
	multibaseKey := "z" + base58.Encode(mcBytes)

	keyID := didWeb + "#key-1"

	return &DIDDocument{
		Context: []string{
			"https://www.w3.org/ns/did/v1",
			"https://w3id.org/security/suites/ed2020/v1",
		},
		ID: didWeb,
		VerificationMethod: []VerificationMethod{
			{
				ID:                 keyID,
				Type:               "Ed25519VerificationKey2020",
				Controller:         didWeb,
				PublicKeyMultibase: multibaseKey,
			},
		},
		Authentication:  []string{keyID},
		AssertionMethod: []string{keyID},
	}
}

// ResolveDIDWeb fetches and parses a DID Document from the did:web URL.
// Resolved documents are cached for 5 minutes to reduce HTTP requests.
func ResolveDIDWeb(ctx context.Context, didWeb string) (*DIDDocument, error) {
	// Check the cache first.
	if doc, ok := didDocCacheInstance.get(didWeb); ok {
		return doc, nil
	}

	resolveURL, err := DIDWebToURL(didWeb)
	if err != nil {
		return nil, err
	}

	doc, err := resolveDIDWebFromURL(ctx, didWeb, resolveURL)
	if err != nil {
		return nil, err
	}

	// Cache the successfully resolved document.
	didDocCacheInstance.set(didWeb, doc)

	return doc, nil
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
// It looks for a verification method with type "Ed25519VerificationKey2020" and
// decodes the publicKeyMultibase field (base58btc with multicodec prefix).
func ExtractEd25519Key(doc *DIDDocument) ([]byte, error) {
	for _, vm := range doc.VerificationMethod {
		if vm.Type != "Ed25519VerificationKey2020" {
			continue
		}
		if vm.PublicKeyMultibase == "" || vm.PublicKeyMultibase[0] != 'z' {
			return nil, fmt.Errorf("%w: expected 'z' (base58btc) prefix", ErrInvalidMultibase)
		}

		decoded, err := base58.Decode(vm.PublicKeyMultibase[1:])
		if err != nil {
			return nil, fmt.Errorf("%w: base58 decode: %w", ErrInvalidMultibase, err)
		}

		// Verify and strip the Ed25519 multicodec prefix (0xed 0x01).
		if len(decoded) != len(ed25519MulticodecPrefix)+32 {
			return nil, fmt.Errorf("%w: decoded key has wrong length (got %d, want %d)", ErrInvalidMultibase, len(decoded), len(ed25519MulticodecPrefix)+32)
		}
		if !bytes.HasPrefix(decoded, ed25519MulticodecPrefix) {
			return nil, fmt.Errorf("%w: unexpected multicodec prefix", ErrInvalidMultibase)
		}

		return decoded[len(ed25519MulticodecPrefix):], nil
	}

	return nil, ErrNoEd25519Key
}
