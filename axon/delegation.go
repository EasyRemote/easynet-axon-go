package axon

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// DelegationProof is the SDK-owned Go shape for RFC-001 delegated authority.
// The Ed25519 signature is over CanonicalPayload.
type DelegationProof struct {
	IssuerURA   string
	SubjectURA  string
	CallerURA   string
	Audience    string
	Scopes      []string
	IssuedAtMS  int64
	ExpiresAtMS int64
	Signature   []byte
}

type DelegationProofRaw struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

type delegationPayload struct {
	IssuerURA   string   `json:"issuer_ura"`
	SubjectURA  string   `json:"subject_ura"`
	CallerURA   string   `json:"caller_ura"`
	Audience    string   `json:"audience"`
	Scopes      []string `json:"scopes"`
	IssuedAtMS  int64    `json:"issued_at_ms"`
	ExpiresAtMS int64    `json:"expires_at_ms"`
}

func (p *DelegationProof) CanonicalPayload() ([]byte, error) {
	if p == nil {
		return nil, errors.New("delegation: nil proof")
	}
	payload := delegationPayload{
		IssuerURA:   p.IssuerURA,
		SubjectURA:  p.SubjectURA,
		CallerURA:   p.CallerURA,
		Audience:    p.Audience,
		Scopes:      p.Scopes,
		IssuedAtMS:  p.IssuedAtMS,
		ExpiresAtMS: p.ExpiresAtMS,
	}
	return canonicalAuthorityJSONBytes(&payload)
}

func (p *DelegationProof) Sign(priv ed25519.PrivateKey) error {
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("delegation: signing key wrong size, want %d got %d",
			ed25519.PrivateKeySize, len(priv))
	}
	payload, err := p.CanonicalPayload()
	if err != nil {
		return fmt.Errorf("delegation: marshal payload: %w", err)
	}
	p.Signature = ed25519.Sign(priv, payload)
	return nil
}

func (p *DelegationProof) Verify(pub ed25519.PublicKey) error {
	if p == nil {
		return errors.New("delegation: nil proof")
	}
	if len(p.Signature) == 0 {
		return errors.New("delegation: signature missing")
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("delegation: verify key wrong size, want %d got %d",
			ed25519.PublicKeySize, len(pub))
	}
	payload, err := p.CanonicalPayload()
	if err != nil {
		return fmt.Errorf("delegation: marshal payload: %w", err)
	}
	if !ed25519.Verify(pub, payload, p.Signature) {
		return errors.New("delegation: signature does not verify")
	}
	return nil
}

func (p *DelegationProof) MarshalRaw() ([]byte, error) {
	payload, err := p.CanonicalPayload()
	if err != nil {
		return nil, err
	}
	if len(p.Signature) == 0 {
		return nil, errors.New("delegation: cannot marshal unsigned proof")
	}
	raw := DelegationProofRaw{
		Payload:   payload,
		Signature: base64.StdEncoding.EncodeToString(p.Signature),
	}
	return json.Marshal(&raw)
}

func UnmarshalRawDelegationProof(data []byte) (*DelegationProof, error) {
	var raw DelegationProofRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("delegation: parse raw: %w", err)
	}
	var payload delegationPayload
	if err := json.Unmarshal(raw.Payload, &payload); err != nil {
		return nil, fmt.Errorf("delegation: parse payload: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(raw.Signature)
	if err != nil {
		return nil, fmt.Errorf("delegation: decode signature: %w", err)
	}
	return &DelegationProof{
		IssuerURA:   payload.IssuerURA,
		SubjectURA:  payload.SubjectURA,
		CallerURA:   payload.CallerURA,
		Audience:    payload.Audience,
		Scopes:      payload.Scopes,
		IssuedAtMS:  payload.IssuedAtMS,
		ExpiresAtMS: payload.ExpiresAtMS,
		Signature:   sig,
	}, nil
}

func (p *DelegationProof) MatchesScope(ability string) bool {
	if p == nil {
		return false
	}
	for _, scope := range p.Scopes {
		if MatchScopePattern(scope, ability) {
			return true
		}
	}
	return false
}

func (p *DelegationProof) MatchesAudience(callee string) bool {
	if p == nil {
		return false
	}
	return p.Audience == "*" || p.Audience == callee || StrictPrefixMatch(p.Audience, callee)
}

func (p *DelegationProof) IsExpired(now time.Time) bool {
	if p == nil {
		return true
	}
	return now.UnixMilli() >= p.ExpiresAtMS
}
