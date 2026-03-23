// Package crypto provides Verifiable Credentials (W3C VC Data Model v2.0) for
// agent capability attestation.
//
// This is a thin wrapper around the agentanycast-identity package, adapting
// between libp2p crypto types and the identity package's standard library types.
package crypto

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/AgentAnycast/agentanycast-identity"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
)

// Errors for verifiable credential operations. Re-exported from identity.
var (
	ErrVCInvalidProof    = identity.ErrVCInvalidProof
	ErrVCSignatureFailed = identity.ErrVCSignatureFailed
	ErrVCIssuerKey       = identity.ErrVCIssuerKey
)

// VerifiableCredential represents a W3C Verifiable Credential.
// Re-exported from the identity package.
type VerifiableCredential = identity.VerifiableCredential

// CredentialSubject describes the entity and capabilities being attested.
// Re-exported from the identity package.
type CredentialSubject = identity.CredentialSubject

// Proof contains the cryptographic proof for the credential.
// Re-exported from the identity package.
type Proof = identity.Proof

// DIDResolver resolves a DID to its public key bytes. Implementations handle
// different DID methods (did:key, did:web, etc.).
type DIDResolver interface {
	ResolvePublicKey(ctx context.Context, did string) ([]byte, error)
}

// DefaultDIDResolver resolves did:key DIDs by extracting the public key from
// the DID itself. This is the default resolver used when none is provided.
type DefaultDIDResolver struct{}

// ResolvePublicKey extracts the Ed25519 public key from a did:key DID.
func (r *DefaultDIDResolver) ResolvePublicKey(_ context.Context, did string) ([]byte, error) {
	if strings.HasPrefix(did, "did:key:") {
		pid, err := DIDKeyToPeerID(did)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrVCIssuerKey, err)
		}
		pub, err := pid.ExtractPublicKey()
		if err != nil {
			return nil, fmt.Errorf("%w: extract public key from PeerID: %w", ErrVCIssuerKey, err)
		}
		raw, err := libp2pcrypto.MarshalPublicKey(pub)
		if err != nil {
			return nil, fmt.Errorf("%w: marshal public key: %w", ErrVCIssuerKey, err)
		}
		return raw, nil
	}
	return nil, fmt.Errorf("%w: unsupported DID method for issuer %q (only did:key supported for verification)", ErrVCIssuerKey, did)
}

// IssueSkillCredential creates a self-issued Verifiable Credential attesting
// that the subject agent possesses the given skill capabilities.
//
// The credential is signed with the issuer's Ed25519 private key. Both issuerDID
// and subjectDID should be valid DID identifiers (did:key, did:web, etc.).
func IssueSkillCredential(privKey libp2pcrypto.PrivKey, issuerDID, subjectDID string, skills []string) (*VerifiableCredential, error) {
	raw, err := privKey.Raw()
	if err != nil {
		return nil, fmt.Errorf("extract raw private key: %w", err)
	}

	stdKey := ed25519.PrivateKey(raw)
	return identity.IssueSkillCredential(stdKey, issuerDID, subjectDID, skills)
}

// vcDIDResolverAdapter adapts the node's DIDResolver interface to the identity
// package's VCDIDResolver interface.
type vcDIDResolverAdapter struct {
	inner DIDResolver
}

func (a *vcDIDResolverAdapter) ResolvePublicKey(ctx context.Context, did string) (ed25519.PublicKey, error) {
	raw, err := a.inner.ResolvePublicKey(ctx, did)
	if err != nil {
		return nil, err
	}
	// The raw bytes from the node's DIDResolver are libp2p-marshaled public keys.
	pub, err := libp2pcrypto.UnmarshalPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal public key: %w", err)
	}
	rawKey, err := pub.Raw()
	if err != nil {
		return nil, fmt.Errorf("extract raw public key: %w", err)
	}
	return ed25519.PublicKey(rawKey), nil
}

// VerifyCredential verifies the Ed25519 signature on a Verifiable Credential.
//
// It extracts the issuer's public key from the issuer DID (supports did:key),
// reconstructs the canonical credential payload, and checks the signature.
//
// An optional DIDResolver can be provided for pluggable DID resolution. When
// resolver is nil, DefaultDIDResolver is used (did:key only).
//
// Known limitations:
//   - No expiration checking: credentials without an expirationDate are accepted
//     indefinitely, and any expirationDate field present in the credential is not validated.
//   - No revocation support: there is no credential status or revocation list check.
//   - Only did:key issuers (with default resolver): verification requires the issuer DID
//     to use the did:key method; did:web and other methods require a custom DIDResolver.
//   - Canonical JSON caveat: the canonicalization uses sorted-key JSON marshaling,
//     which may not be fully compatible with other VC implementations that use
//     JSON-LD canonicalization (e.g., URDNA2015).
func VerifyCredential(vc *VerifiableCredential, resolver ...DIDResolver) error {
	if len(resolver) > 0 && resolver[0] != nil {
		adapted := &vcDIDResolverAdapter{inner: resolver[0]}
		return identity.VerifyCredential(vc, adapted)
	}
	return identity.VerifyCredential(vc)
}

// canonicalJSON produces a deterministic JSON serialization with sorted keys.
// This ensures consistent signing and verification across implementations.
// Kept for test compatibility within this package.
func canonicalJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, err
	}

	sorted := sortKeys(generic)
	return json.Marshal(sorted)
}

// sortKeys recursively sorts map keys for deterministic JSON output.
func sortKeys(v any) any {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		sorted := make(orderedMap, 0, len(val))
		for _, k := range keys {
			sorted = append(sorted, orderedEntry{Key: k, Value: sortKeys(val[k])})
		}
		return sorted
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = sortKeys(item)
		}
		return result
	default:
		return v
	}
}

// orderedMap preserves insertion order during JSON marshaling.
type orderedMap []orderedEntry

type orderedEntry struct {
	Key   string
	Value any
}

func (m orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, entry := range m {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(entry.Key)
		if err != nil {
			return nil, err
		}
		val, err := json.Marshal(entry.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
