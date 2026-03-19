// Package crypto provides Verifiable Credentials (W3C VC Data Model v2.0) for
// agent capability attestation.
//
// This implementation supports self-issued credentials where an agent attests
// its own capabilities (skills), signed with the agent's Ed25519 key. The
// credential format follows the W3C VC Data Model with Ed25519Signature2020
// proof suite.
package crypto

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/mr-tron/base58"
)

// Errors for verifiable credential operations.
var (
	ErrVCInvalidProof    = errors.New("invalid or missing proof on verifiable credential")
	ErrVCSignatureFailed = errors.New("credential signature verification failed")
	ErrVCIssuerKey       = errors.New("cannot extract public key from issuer DID")
)

// VerifiableCredential represents a W3C Verifiable Credential.
type VerifiableCredential struct {
	Context           []string          `json:"@context"`
	Type              []string          `json:"type"`
	Issuer            string            `json:"issuer"`
	IssuanceDate      string            `json:"issuanceDate"`
	CredentialSubject CredentialSubject `json:"credentialSubject"`
	Proof             *Proof            `json:"proof,omitempty"`
}

// CredentialSubject describes the entity and capabilities being attested.
type CredentialSubject struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Capabilities []string `json:"capabilities"`
}

// Proof contains the cryptographic proof for the credential.
type Proof struct {
	Type               string `json:"type"`
	Created            string `json:"created"`
	VerificationMethod string `json:"verificationMethod"`
	ProofPurpose       string `json:"proofPurpose"`
	ProofValue         string `json:"proofValue"`
}

// IssueSkillCredential creates a self-issued Verifiable Credential attesting
// that the subject agent possesses the given skill capabilities.
//
// The credential is signed with the issuer's Ed25519 private key. Both issuerDID
// and subjectDID should be valid DID identifiers (did:key, did:web, etc.).
func IssueSkillCredential(privKey libp2pcrypto.PrivKey, issuerDID, subjectDID string, skills []string) (*VerifiableCredential, error) {
	now := time.Now().UTC()

	vc := &VerifiableCredential{
		Context: []string{
			"https://www.w3.org/2018/credentials/v1",
			"https://w3id.org/security/suites/ed2020/v1",
		},
		Type:         []string{"VerifiableCredential", "AgentCapabilityCredential"},
		Issuer:       issuerDID,
		IssuanceDate: now.Format(time.RFC3339),
		CredentialSubject: CredentialSubject{
			ID:           subjectDID,
			Type:         "AgentCapability",
			Capabilities: skills,
		},
	}

	// Serialize to canonical JSON (sorted keys) for signing.
	canonical, err := canonicalJSON(vc)
	if err != nil {
		return nil, fmt.Errorf("serialize credential for signing: %w", err)
	}

	sig, err := privKey.Sign(canonical)
	if err != nil {
		return nil, fmt.Errorf("sign credential: %w", err)
	}

	vc.Proof = &Proof{
		Type:               "Ed25519Signature2020",
		Created:            now.Format(time.RFC3339),
		VerificationMethod: issuerDID + "#key-1",
		ProofPurpose:       "assertionMethod",
		ProofValue:         base58.Encode(sig),
	}

	return vc, nil
}

// VerifyCredential verifies the Ed25519 signature on a Verifiable Credential.
//
// It extracts the issuer's public key from the issuer DID (supports did:key),
// reconstructs the canonical credential payload, and checks the signature.
func VerifyCredential(vc *VerifiableCredential) error {
	if vc.Proof == nil {
		return ErrVCInvalidProof
	}
	if vc.Proof.Type != "Ed25519Signature2020" {
		return fmt.Errorf("%w: unsupported proof type %q", ErrVCInvalidProof, vc.Proof.Type)
	}

	// Extract issuer public key from DID.
	pubKey, err := resolveIssuerPublicKey(vc.Issuer)
	if err != nil {
		return err
	}

	// Decode the proof signature.
	sig, err := base58.Decode(vc.Proof.ProofValue)
	if err != nil {
		return fmt.Errorf("%w: decode proof value: %v", ErrVCInvalidProof, err)
	}

	// Reconstruct the credential without proof for verification.
	vcCopy := *vc
	vcCopy.Proof = nil

	canonical, err := canonicalJSON(&vcCopy)
	if err != nil {
		return fmt.Errorf("serialize credential for verification: %w", err)
	}

	ok, err := pubKey.Verify(canonical, sig)
	if err != nil {
		return fmt.Errorf("%w: verify signature: %v", ErrVCSignatureFailed, err)
	}
	if !ok {
		return ErrVCSignatureFailed
	}

	return nil
}

// resolveIssuerPublicKey extracts the Ed25519 public key from an issuer DID.
// Currently supports did:key. For did:web, the caller should resolve the DID
// document separately and use ExtractEd25519Key.
func resolveIssuerPublicKey(issuerDID string) (libp2pcrypto.PubKey, error) {
	if strings.HasPrefix(issuerDID, "did:key:") {
		pid, err := DIDKeyToPeerID(issuerDID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrVCIssuerKey, err)
		}
		pub, err := pid.ExtractPublicKey()
		if err != nil {
			return nil, fmt.Errorf("%w: extract public key from PeerID: %v", ErrVCIssuerKey, err)
		}
		return pub, nil
	}

	return nil, fmt.Errorf("%w: unsupported DID method for issuer %q (only did:key supported for verification)", ErrVCIssuerKey, issuerDID)
}

// canonicalJSON produces a deterministic JSON serialization with sorted keys.
// This ensures consistent signing and verification across implementations.
func canonicalJSON(v any) ([]byte, error) {
	// Marshal to JSON, then unmarshal into a generic structure to sort keys.
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
	var buf strings.Builder
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
	return []byte(buf.String()), nil
}
