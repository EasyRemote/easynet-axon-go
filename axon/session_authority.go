package axon

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SessionAuthority is issuer-signed authority for an authenticated
// interactive user session. It is not DelegationProof: the issuer key signs
// it, so it proves session issuer policy, not user-private-key delegation.
type SessionAuthority struct {
	IssuerURA   string
	SubjectURA  string
	SessionID   string
	Scopes      []string
	Audiences   []string
	IssuedAtMS  int64
	ExpiresAtMS int64
	Signature   []byte
}

type SessionAuthorityRaw struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

type sessionAuthorityPayload struct {
	IssuerURA   string   `json:"issuer_ura"`
	SubjectURA  string   `json:"subject_ura"`
	SessionID   string   `json:"session_id"`
	Scopes      []string `json:"scopes"`
	Audiences   []string `json:"audiences"`
	IssuedAtMS  int64    `json:"issued_at_ms"`
	ExpiresAtMS int64    `json:"expires_at_ms"`
}

// CanonicalPayload returns the key-sorted JSON bytes signed by the issuer.
// This mirrors the downstream admission verifier's canonical JSON contract.
func (a *SessionAuthority) CanonicalPayload() ([]byte, error) {
	if a == nil {
		return nil, errors.New("session authority: nil authority")
	}
	payload := sessionAuthorityPayload{
		IssuerURA:   a.IssuerURA,
		SubjectURA:  a.SubjectURA,
		SessionID:   a.SessionID,
		Scopes:      a.Scopes,
		Audiences:   a.Audiences,
		IssuedAtMS:  a.IssuedAtMS,
		ExpiresAtMS: a.ExpiresAtMS,
	}
	return canonicalAuthorityJSONBytes(&payload)
}

func (a *SessionAuthority) Sign(priv ed25519.PrivateKey) error {
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("session authority: signing key wrong size, want %d got %d",
			ed25519.PrivateKeySize, len(priv))
	}
	payload, err := a.CanonicalPayload()
	if err != nil {
		return fmt.Errorf("session authority: marshal payload: %w", err)
	}
	a.Signature = ed25519.Sign(priv, payload)
	return nil
}

func (a *SessionAuthority) Verify(pub ed25519.PublicKey) error {
	if a == nil {
		return errors.New("session authority: nil authority")
	}
	if len(a.Signature) == 0 {
		return errors.New("session authority: signature missing")
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("session authority: verify key wrong size, want %d got %d",
			ed25519.PublicKeySize, len(pub))
	}
	payload, err := a.CanonicalPayload()
	if err != nil {
		return fmt.Errorf("session authority: marshal payload: %w", err)
	}
	if !ed25519.Verify(pub, payload, a.Signature) {
		return errors.New("session authority: signature does not verify")
	}
	return nil
}

func (a *SessionAuthority) MarshalRaw() ([]byte, error) {
	payload, err := a.CanonicalPayload()
	if err != nil {
		return nil, err
	}
	if len(a.Signature) == 0 {
		return nil, errors.New("session authority: cannot marshal unsigned authority")
	}
	raw := SessionAuthorityRaw{
		Payload:   payload,
		Signature: base64.StdEncoding.EncodeToString(a.Signature),
	}
	return json.Marshal(&raw)
}

func UnmarshalRawSessionAuthority(data []byte) (*SessionAuthority, error) {
	var raw SessionAuthorityRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("session authority: parse raw: %w", err)
	}
	var payload sessionAuthorityPayload
	if err := json.Unmarshal(raw.Payload, &payload); err != nil {
		return nil, fmt.Errorf("session authority: parse payload: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(raw.Signature)
	if err != nil {
		return nil, fmt.Errorf("session authority: decode signature: %w", err)
	}
	return &SessionAuthority{
		IssuerURA:   payload.IssuerURA,
		SubjectURA:  payload.SubjectURA,
		SessionID:   payload.SessionID,
		Scopes:      payload.Scopes,
		Audiences:   payload.Audiences,
		IssuedAtMS:  payload.IssuedAtMS,
		ExpiresAtMS: payload.ExpiresAtMS,
		Signature:   sig,
	}, nil
}

func (a *SessionAuthority) MatchesScope(ability string) bool {
	if a == nil {
		return false
	}
	for _, scope := range a.Scopes {
		if MatchScopePattern(scope, ability) {
			return true
		}
	}
	return false
}

func (a *SessionAuthority) MatchesAudience(callee string) bool {
	if a == nil {
		return false
	}
	for _, audience := range a.Audiences {
		if audience == "*" || audience == callee || StrictPrefixMatch(audience, callee) {
			return true
		}
	}
	return false
}

func (a *SessionAuthority) IsExpired(now time.Time) bool {
	if a == nil {
		return true
	}
	return now.UnixMilli() >= a.ExpiresAtMS
}

func MatchScopePattern(pattern, ability string) bool {
	switch {
	case pattern == "*":
		return true
	case len(pattern) >= 2 && pattern[len(pattern)-1] == '*':
		return len(ability) >= len(pattern)-1 &&
			ability[:len(pattern)-1] == pattern[:len(pattern)-1]
	default:
		return pattern == ability
	}
}

func StrictPrefixMatch(prefix, candidate string) bool {
	if len(prefix) == 0 || prefix[len(prefix)-1] != '/' {
		return false
	}
	if len(candidate) < len(prefix) {
		return false
	}
	return candidate[:len(prefix)] == prefix
}
