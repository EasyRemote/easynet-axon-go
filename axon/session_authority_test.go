package axon

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestSessionAuthoritySignMarshalVerifyRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	authority := &SessionAuthority{
		IssuerURA:   "easynet:///r/realm/authority",
		SubjectURA:  "easynet:///r/realm/user/alice",
		SessionID:   "sess-1",
		Scopes:      []string{"provider.*", "session.attach"},
		Audiences:   []string{"easynet:///r/realm/device/dev-1"},
		IssuedAtMS:  10,
		ExpiresAtMS: 20,
	}
	if err := authority.Sign(priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := authority.MarshalRaw()
	if err != nil {
		t.Fatalf("MarshalRaw: %v", err)
	}
	decoded, err := UnmarshalRawSessionAuthority(raw)
	if err != nil {
		t.Fatalf("UnmarshalRawSessionAuthority: %v", err)
	}
	if decoded.SubjectURA != authority.SubjectURA || decoded.IssuerURA != authority.IssuerURA {
		t.Fatalf("decoded binding changed: %+v", decoded)
	}
	if err := decoded.Verify(pub); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSessionAuthorityCanonicalPayloadSortsKeysForDaemonAdmission(t *testing.T) {
	authority := &SessionAuthority{
		IssuerURA:   "easynet:///r/realm/authority",
		SubjectURA:  "easynet:///r/realm/user/alice",
		SessionID:   "sess-1",
		Scopes:      []string{"provider.*", "session.attach"},
		Audiences:   []string{"easynet:///r/realm/device/dev-1"},
		IssuedAtMS:  10,
		ExpiresAtMS: 20,
	}

	payload, err := authority.CanonicalPayload()
	if err != nil {
		t.Fatalf("CanonicalPayload: %v", err)
	}
	want := `{"audiences":["easynet:///r/realm/device/dev-1"],"expires_at_ms":20,"issued_at_ms":10,"issuer_ura":"easynet:///r/realm/authority","scopes":["provider.*","session.attach"],"session_id":"sess-1","subject_ura":"easynet:///r/realm/user/alice"}`
	if string(payload) != want {
		t.Fatalf("canonical payload = %s, want %s", payload, want)
	}
}

func TestSessionAuthorityRejectsTamper(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	authority := &SessionAuthority{
		IssuerURA:   "easynet:///r/realm/authority",
		SubjectURA:  "easynet:///r/realm/user/alice",
		SessionID:   "sess-1",
		Scopes:      []string{"provider.inspect"},
		Audiences:   []string{"*"},
		IssuedAtMS:  10,
		ExpiresAtMS: 20,
	}
	if err := authority.Sign(priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	authority.SubjectURA = "easynet:///r/realm/user/bob"
	if err := authority.Verify(pub); err == nil {
		t.Fatal("tampered payload must not verify")
	}
}

func TestSessionAuthorityScopeAudienceAndExpiry(t *testing.T) {
	authority := &SessionAuthority{
		Scopes:      []string{"provider.*", "session.attach"},
		Audiences:   []string{"easynet:///r/realm/device/", "easynet:///r/realm/authority"},
		ExpiresAtMS: 1000,
	}
	if !authority.MatchesScope("provider.inspect") {
		t.Fatal("wildcard scope should match")
	}
	if authority.MatchesScope("providers.inspect") {
		t.Fatal("wildcard scope must be prefix exact")
	}
	if !authority.MatchesAudience("easynet:///r/realm/device/dev-1") {
		t.Fatal("slash-terminated audience prefix should match")
	}
	if authority.MatchesAudience("easynet:///r/realm/deviceops/dev-1") {
		t.Fatal("non-boundary audience prefix must not match")
	}
	if authority.IsExpired(time.UnixMilli(999)) {
		t.Fatal("authority should still be valid before expiry")
	}
	if !authority.IsExpired(time.UnixMilli(1000)) {
		t.Fatal("authority should expire at expires_at_ms")
	}
}
