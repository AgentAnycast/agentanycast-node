package crypto

import (
	"crypto/rand"
	"encoding/json"
	"testing"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func generateTestKeyAndDID(t *testing.T) (libp2pcrypto.PrivKey, string) {
	t.Helper()
	priv, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatalf("derive PeerID: %v", err)
	}
	did, err := PeerIDToDIDKey(pid)
	if err != nil {
		t.Fatalf("PeerIDToDIDKey: %v", err)
	}
	return priv, did
}

func TestIssueSkillCredential_Valid(t *testing.T) {
	priv, did := generateTestKeyAndDID(t)

	skills := []string{"text-summarization", "translation", "code-review"}
	vc, err := IssueSkillCredential(priv, did, did, skills)
	if err != nil {
		t.Fatalf("IssueSkillCredential: %v", err)
	}

	if vc.Issuer != did {
		t.Errorf("Issuer = %q, want %q", vc.Issuer, did)
	}
	if vc.CredentialSubject.ID != did {
		t.Errorf("Subject.ID = %q, want %q", vc.CredentialSubject.ID, did)
	}
	if vc.CredentialSubject.Type != "AgentCapability" {
		t.Errorf("Subject.Type = %q, want AgentCapability", vc.CredentialSubject.Type)
	}
	if len(vc.CredentialSubject.Capabilities) != 3 {
		t.Errorf("expected 3 capabilities, got %d", len(vc.CredentialSubject.Capabilities))
	}
	if vc.Proof == nil {
		t.Fatal("expected non-nil proof")
	}
	if vc.Proof.Type != "Ed25519Signature2020" {
		t.Errorf("Proof.Type = %q, want Ed25519Signature2020", vc.Proof.Type)
	}
	if vc.Proof.ProofPurpose != "assertionMethod" {
		t.Errorf("Proof.ProofPurpose = %q, want assertionMethod", vc.Proof.ProofPurpose)
	}
	if vc.Proof.ProofValue == "" {
		t.Error("expected non-empty proof value")
	}
	if len(vc.Context) != 2 {
		t.Errorf("expected 2 context entries, got %d", len(vc.Context))
	}
	if len(vc.Type) != 2 {
		t.Errorf("expected 2 type entries, got %d", len(vc.Type))
	}
}

func TestVerifyCredential_Valid(t *testing.T) {
	priv, did := generateTestKeyAndDID(t)

	vc, err := IssueSkillCredential(priv, did, did, []string{"translation"})
	if err != nil {
		t.Fatalf("IssueSkillCredential: %v", err)
	}

	if err := VerifyCredential(vc); err != nil {
		t.Fatalf("VerifyCredential: %v", err)
	}
}

func TestVerifyCredential_TamperedSubject(t *testing.T) {
	priv, did := generateTestKeyAndDID(t)

	vc, err := IssueSkillCredential(priv, did, did, []string{"translation"})
	if err != nil {
		t.Fatalf("IssueSkillCredential: %v", err)
	}

	// Tamper with the credential.
	vc.CredentialSubject.Capabilities = []string{"admin-access"}

	if err := VerifyCredential(vc); err == nil {
		t.Fatal("expected verification to fail for tampered credential")
	}
}

func TestVerifyCredential_TamperedIssuer(t *testing.T) {
	priv, did := generateTestKeyAndDID(t)
	_, otherDID := generateTestKeyAndDID(t)

	vc, err := IssueSkillCredential(priv, did, did, []string{"translation"})
	if err != nil {
		t.Fatalf("IssueSkillCredential: %v", err)
	}

	// Replace issuer with a different DID (signature won't match).
	vc.Issuer = otherDID
	vc.Proof.VerificationMethod = otherDID + "#key-1"

	if err := VerifyCredential(vc); err == nil {
		t.Fatal("expected verification to fail for wrong issuer key")
	}
}

func TestVerifyCredential_NoProof(t *testing.T) {
	vc := &VerifiableCredential{
		Issuer: "did:key:z1234",
	}

	err := VerifyCredential(vc)
	if err != ErrVCInvalidProof {
		t.Errorf("expected ErrVCInvalidProof, got %v", err)
	}
}

func TestVerifyCredential_WrongProofType(t *testing.T) {
	vc := &VerifiableCredential{
		Issuer: "did:key:z1234",
		Proof: &Proof{
			Type: "RsaSignature2018",
		},
	}

	err := VerifyCredential(vc)
	if err == nil {
		t.Fatal("expected error for unsupported proof type")
	}
}

func TestVerifiableCredential_JSONRoundTrip(t *testing.T) {
	priv, did := generateTestKeyAndDID(t)

	vc, err := IssueSkillCredential(priv, did, did, []string{"text-summarization", "translation"})
	if err != nil {
		t.Fatalf("IssueSkillCredential: %v", err)
	}

	data, err := json.Marshal(vc)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var parsed VerifiableCredential
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// Verify the round-tripped credential.
	if err := VerifyCredential(&parsed); err != nil {
		t.Fatalf("VerifyCredential after round-trip: %v", err)
	}

	if parsed.Issuer != vc.Issuer {
		t.Errorf("Issuer mismatch: %q != %q", parsed.Issuer, vc.Issuer)
	}
	if parsed.CredentialSubject.ID != vc.CredentialSubject.ID {
		t.Errorf("Subject.ID mismatch: %q != %q", parsed.CredentialSubject.ID, vc.CredentialSubject.ID)
	}
	if len(parsed.CredentialSubject.Capabilities) != len(vc.CredentialSubject.Capabilities) {
		t.Errorf("capabilities length mismatch: %d != %d",
			len(parsed.CredentialSubject.Capabilities), len(vc.CredentialSubject.Capabilities))
	}
}

func TestIssueSkillCredential_DifferentIssuerAndSubject(t *testing.T) {
	issuerPriv, issuerDID := generateTestKeyAndDID(t)
	_, subjectDID := generateTestKeyAndDID(t)

	vc, err := IssueSkillCredential(issuerPriv, issuerDID, subjectDID, []string{"code-review"})
	if err != nil {
		t.Fatalf("IssueSkillCredential: %v", err)
	}

	if vc.Issuer != issuerDID {
		t.Errorf("Issuer = %q, want %q", vc.Issuer, issuerDID)
	}
	if vc.CredentialSubject.ID != subjectDID {
		t.Errorf("Subject.ID = %q, want %q", vc.CredentialSubject.ID, subjectDID)
	}

	// Verification should pass using issuer's key.
	if err := VerifyCredential(vc); err != nil {
		t.Fatalf("VerifyCredential: %v", err)
	}
}

func TestCanonicalJSON_Deterministic(t *testing.T) {
	// Verify that canonicalJSON produces the same output for equivalent input.
	input := map[string]any{
		"z_field": "last",
		"a_field": "first",
		"m_field": map[string]any{
			"b": 2,
			"a": 1,
		},
	}

	out1, err := canonicalJSON(input)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	out2, err := canonicalJSON(input)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}

	if string(out1) != string(out2) {
		t.Errorf("canonicalJSON not deterministic:\n  %s\n  %s", out1, out2)
	}

	// Verify keys are sorted.
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(out1, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The JSON output should have a_field before m_field before z_field.
	s := string(out1)
	aIdx := len(s)
	mIdx := len(s)
	zIdx := len(s)
	for i := 0; i < len(s); i++ {
		if s[i:i+1] == "a" && i+7 < len(s) && s[i:i+7] == "a_field" {
			aIdx = i
			break
		}
	}
	for i := 0; i < len(s); i++ {
		if s[i:i+1] == "m" && i+7 < len(s) && s[i:i+7] == "m_field" {
			mIdx = i
			break
		}
	}
	for i := 0; i < len(s); i++ {
		if s[i:i+1] == "z" && i+7 < len(s) && s[i:i+7] == "z_field" {
			zIdx = i
			break
		}
	}
	if !(aIdx < mIdx && mIdx < zIdx) {
		t.Errorf("keys not sorted in output: %s", s)
	}
}
