package axon

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestDelegationProofSignMarshalVerifyRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	proof := &DelegationProof{
		IssuerURA:   "easynet:///r/realm/user/alice",
		SubjectURA:  "easynet:///r/realm/user/alice",
		CallerURA:   "easynet:///r/realm/authority",
		Audience:    "easynet:///r/realm/",
		Scopes:      []string{"runtime.*"},
		IssuedAtMS:  10,
		ExpiresAtMS: 20,
	}
	if err := proof.Sign(priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := proof.MarshalRaw()
	if err != nil {
		t.Fatalf("MarshalRaw: %v", err)
	}
	decoded, err := UnmarshalRawDelegationProof(raw)
	if err != nil {
		t.Fatalf("UnmarshalRawDelegationProof: %v", err)
	}
	if decoded.SubjectURA != proof.SubjectURA || decoded.CallerURA != proof.CallerURA {
		t.Fatalf("decoded binding changed: %+v", decoded)
	}
	if err := decoded.Verify(pub); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDelegationProofCanonicalPayloadSortsKeysForDaemonAdmission(t *testing.T) {
	proof := &DelegationProof{
		IssuerURA:   "easynet:///r/realm/user/alice",
		SubjectURA:  "easynet:///r/realm/user/alice",
		CallerURA:   "easynet:///r/realm/authority",
		Audience:    "easynet:///r/realm/",
		Scopes:      []string{"runtime.*"},
		IssuedAtMS:  10,
		ExpiresAtMS: 20,
	}

	payload, err := proof.CanonicalPayload()
	if err != nil {
		t.Fatalf("CanonicalPayload: %v", err)
	}
	want := `{"audience":"easynet:///r/realm/","caller_ura":"easynet:///r/realm/authority","expires_at_ms":20,"issued_at_ms":10,"issuer_ura":"easynet:///r/realm/user/alice","scopes":["runtime.*"],"subject_ura":"easynet:///r/realm/user/alice"}`
	if string(payload) != want {
		t.Fatalf("canonical payload = %s, want %s", payload, want)
	}
}

func TestDelegationProofRejectsTamper(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	proof := &DelegationProof{
		IssuerURA:   "easynet:///r/realm/user/alice",
		SubjectURA:  "easynet:///r/realm/user/alice",
		CallerURA:   "easynet:///r/realm/authority",
		Audience:    "*",
		Scopes:      []string{"provider.inspect"},
		IssuedAtMS:  10,
		ExpiresAtMS: 20,
	}
	if err := proof.Sign(priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	proof.CallerURA = "easynet:///r/realm/device/dev-1"
	if err := proof.Verify(pub); err == nil {
		t.Fatal("tampered payload must not verify")
	}
}

func TestDelegationProofScopeAudienceAndExpiry(t *testing.T) {
	proof := &DelegationProof{
		Scopes:      []string{"agent.l*", "runtime.forward"},
		Audience:    "easynet:///r/acme/",
		ExpiresAtMS: 1000,
	}
	if !proof.MatchesScope("agent.list") || !proof.MatchesScope("agent.l") {
		t.Fatal("trailing wildcard should match prefix")
	}
	if proof.MatchesScope("agent.start") {
		t.Fatal("trailing wildcard should not match other prefix")
	}
	if !proof.MatchesAudience("easynet:///r/acme/agent/alice.bot") {
		t.Fatal("slash-terminated audience prefix should match")
	}
	if proof.MatchesAudience("easynet:///r/acmeother/agent/alice.bot") {
		t.Fatal("non-boundary audience prefix must not match")
	}
	if proof.IsExpired(time.UnixMilli(999)) {
		t.Fatal("proof should still be valid before expiry")
	}
	if !proof.IsExpired(time.UnixMilli(1000)) {
		t.Fatal("proof should expire at expires_at_ms")
	}
}
