package axon

// Portable JSON bundle format for RFC 001 v2 receipts and invocation
// envelopes (Go).
//
// Byte-identical to Rust sdk/rust/src/invocation/bundle.rs and Python
// sdk/python/axon_sdk/invocation/bundle.py. Every SDK reads/writes
// the same shape so a verifier built from any SDK can ingest a bundle
// produced by any other.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ── Identity JSON ────────────────────────────────────────────────────

// IdentityJSON — common shape for AgentIdentity and SubjectIdentity.
type IdentityJSON struct {
	URA     string `json:"ura"`
	Profile string `json:"profile"`
}

// FromAgent builds an IdentityJSON from an AgentIdentity.
func IdentityFromAgent(a AgentIdentity) IdentityJSON {
	return IdentityJSON{URA: a.URA, Profile: string(a.Profile)}
}

// FromSubject builds an IdentityJSON from a SubjectIdentity.
func IdentityFromSubject(s SubjectIdentity) IdentityJSON {
	return IdentityJSON{URA: s.URA, Profile: string(s.Profile)}
}

// ToAgent converts back to an AgentIdentity.
func (i IdentityJSON) ToAgent() (AgentIdentity, error) {
	p, err := ParseUraProfile(i.Profile)
	if err != nil {
		return AgentIdentity{}, err
	}
	return AgentIdentity{URA: i.URA, Profile: p}, nil
}

// ToSubject converts back to a SubjectIdentity.
func (i IdentityJSON) ToSubject() (SubjectIdentity, error) {
	p, err := ParseUraProfile(i.Profile)
	if err != nil {
		return SubjectIdentity{}, err
	}
	return SubjectIdentity{URA: i.URA, Profile: p}, nil
}

type EntityRefJSON struct {
	Kind    string `json:"kind"`
	URA     string `json:"ura"`
	Profile string `json:"profile"`
}

func EntityRefToJSON(entity EntityRef) EntityRefJSON {
	return EntityRefJSON{
		Kind:    entity.Kind.String(),
		URA:     entity.URA,
		Profile: string(entity.Profile),
	}
}

func (e EntityRefJSON) ToEntityRef() (EntityRef, error) {
	kind, err := ParseEntityRefKind(e.Kind)
	if err != nil {
		return EntityRef{}, err
	}
	profile, err := ParseUraProfile(e.Profile)
	if err != nil {
		return EntityRef{}, err
	}
	return EntityRef{Kind: kind, URA: e.URA, Profile: profile}, nil
}

// ── Causal context JSON ──────────────────────────────────────────────

// CausalJSON — tagged union of all four causal forms.
type CausalJSON struct {
	Form           string           `json:"form"`
	ReceiptHashHex string           `json:"receipt_hash_hex,omitempty"`
	ReceiptURA     string           `json:"receipt_ura,omitempty"`
	Prior          []ReceiptRefJSON `json:"prior,omitempty"`
	RootHex        string           `json:"root_hex,omitempty"`
	ProofURA       string           `json:"proof_ura,omitempty"`
}

// ReceiptRefJSON — serialisation of a ReceiptRef.
type ReceiptRefJSON struct {
	ReceiptHashHex string `json:"receipt_hash_hex"`
	ReceiptURA     string `json:"receipt_ura"`
}

// AuthorityJSON — RFC 001 §A14 authority-binding wire shape. Mirrors the
// Rust bundle's AuthorityJson: `form` is the compound tag
// "<relation>+<evidence>" (e.g. "self+identity",
// "delegated_by+delegation", "session_of+session") — a JSON `tag`
// cannot express two independent axes as one flat discriminator
// without either a nested object or a compound tag string, and a
// compound tag string is the smaller wire change relative to the
// pre-redesign flat-enum JSON shape. Bootstrap is a SEPARATE JSON
// shape (BootstrapJSON), not part of AuthorityJSON. The
// `signature_hex` is the Ed25519 delegation/session signature,
// hex-encoded.
type AuthorityJSON struct {
	Form         string   `json:"form"`
	AuthorityURA string   `json:"authority_ura,omitempty"`
	IssuerURA    string   `json:"issuer_ura,omitempty"`
	Audience     string   `json:"audience,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	IssuedAtMs   int64    `json:"issued_at_ms,omitempty,string"`
	ExpiresAtMs  int64    `json:"expires_at_ms,omitempty,string"`
	SignatureHex string   `json:"signature_hex,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	Audiences    []string `json:"audiences,omitempty"`
}

// BootstrapJSON — JSON projection of the admission-plane
// BootstrapBinding. Kept as a SEPARATE type from AuthorityJSON,
// mirroring BootstrapBinding's separation from AuthorityBinding.
type BootstrapJSON struct {
	PrincipalURA string `json:"principal_ura"`
	Realm        string `json:"realm"`
	Ability      string `json:"ability"`
}

// AuthorityOrBootstrapJSON — JSON projection of AuthorityOrBootstrap,
// the proof envelope's binding slot, whichever plane it carries.
// Mirrors the Rust untagged AuthorityOrBootstrapJson: exactly one of
// Authority / Bootstrap is set.
type AuthorityOrBootstrapJSON struct {
	Authority *AuthorityJSON `json:"-"`
	Bootstrap *BootstrapJSON `json:"-"`
}

// MarshalJSON emits whichever of Authority/Bootstrap is set, untagged
// (mirrors serde's `#[serde(untagged)]`). A zero value (neither set)
// marshals as an empty AuthorityJSON, matching the pre-existing
// zero-value-marshals-harmlessly behavior of the flat AuthorityJSON
// type this replaces.
func (a AuthorityOrBootstrapJSON) MarshalJSON() ([]byte, error) {
	if a.Bootstrap != nil {
		return json.Marshal(a.Bootstrap)
	}
	if a.Authority != nil {
		return json.Marshal(a.Authority)
	}
	return json.Marshal(&AuthorityJSON{})
}

// UnmarshalJSON detects Bootstrap (has "principal_ura"/"realm"/"ability"
// and no "form") vs. Authority (has "form") shape and rejects
// noncanonical fields instead of letting encoding/json silently drop
// them. This keeps the Go SDK aligned with the fail-closed
// receipt/bundle parsers in the other language SDKs.
func (a *AuthorityOrBootstrapJSON) UnmarshalJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	if _, hasForm := object["form"]; !hasForm {
		allowed := map[string]struct{}{"principal_ura": {}, "realm": {}, "ability": {}}
		for field := range object {
			if _, ok := allowed[field]; !ok {
				return ErrInvalidArgument(fmt.Sprintf("authority contains noncanonical field %s", field))
			}
		}
		var bootstrap BootstrapJSON
		if err := json.Unmarshal(data, &bootstrap); err != nil {
			return err
		}
		*a = AuthorityOrBootstrapJSON{Bootstrap: &bootstrap}
		return nil
	}
	var authority AuthorityJSON
	if err := json.Unmarshal(data, &authority); err != nil {
		return err
	}
	*a = AuthorityOrBootstrapJSON{Authority: &authority}
	return nil
}

// AuthorityOrBootstrapJSONFromBinding encodes an AuthorityOrBootstrap
// domain value into its JSON projection.
func AuthorityOrBootstrapJSONFromBinding(binding AuthorityOrBootstrap) (AuthorityOrBootstrapJSON, error) {
	if binding.IsBootstrap {
		b := binding.Bootstrap
		if b == nil {
			return AuthorityOrBootstrapJSON{}, ErrInvalidArgument("authority_bootstrap_missing")
		}
		return AuthorityOrBootstrapJSON{Bootstrap: &BootstrapJSON{
			PrincipalURA: b.PrincipalURA,
			Realm:        b.Realm,
			Ability:      b.Ability,
		}}, nil
	}
	if binding.Binding == nil {
		return AuthorityOrBootstrapJSON{}, ErrInvalidArgument("authority_binding_missing")
	}
	encoded, err := AuthorityFromBinding(*binding.Binding)
	if err != nil {
		return AuthorityOrBootstrapJSON{}, err
	}
	return AuthorityOrBootstrapJSON{Authority: &encoded}, nil
}

// ToBinding parses an AuthorityOrBootstrapJSON into an
// AuthorityOrBootstrap.
func (a AuthorityOrBootstrapJSON) ToBinding() (AuthorityOrBootstrap, error) {
	if a.Bootstrap != nil {
		if strings.TrimSpace(a.Bootstrap.PrincipalURA) == "" ||
			strings.TrimSpace(a.Bootstrap.Realm) == "" ||
			strings.TrimSpace(a.Bootstrap.Ability) == "" {
			return AuthorityOrBootstrap{}, ErrInvalidArgument("authority_bootstrap_incomplete")
		}
		return AuthorityOrBootstrapFromBootstrap(BootstrapAuthority(
			a.Bootstrap.PrincipalURA, a.Bootstrap.Realm, a.Bootstrap.Ability,
		)), nil
	}
	if a.Authority == nil {
		return AuthorityOrBootstrap{}, ErrInvalidArgument("authority_or_bootstrap_missing")
	}
	binding, err := a.Authority.ToBinding()
	if err != nil {
		return AuthorityOrBootstrap{}, err
	}
	return AuthorityOrBootstrapFromBinding(binding), nil
}

// UnmarshalJSON rejects noncanonical authority fields instead of letting
// encoding/json silently drop them. This keeps the Go SDK aligned with the
// fail-closed receipt/bundle parsers in the other language SDKs.
func (a *AuthorityJSON) UnmarshalJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	formRaw, ok := object["form"]
	if !ok {
		return ErrInvalidArgument("authority.form_missing")
	}
	var form string
	if err := json.Unmarshal(formRaw, &form); err != nil {
		return err
	}
	allowed, ok := authorityJSONAllowedFields(form)
	if !ok {
		return ErrInvalidArgument(fmt.Sprintf("unknown_authority_form:%s", form))
	}
	for field := range object {
		if _, ok := allowed[field]; !ok {
			return ErrInvalidArgument(fmt.Sprintf("authority contains noncanonical field %s", field))
		}
	}
	type authorityJSONAlias AuthorityJSON
	var decoded authorityJSONAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*a = AuthorityJSON(decoded)
	return nil
}

func authorityJSONAllowedFields(form string) (map[string]struct{}, bool) {
	fields := func(names ...string) map[string]struct{} {
		out := make(map[string]struct{}, len(names))
		for _, name := range names {
			out[name] = struct{}{}
		}
		return out
	}
	switch form {
	case "self+identity":
		return fields("form", "authority_ura"), true
	case "delegated_by+delegation":
		return fields(
			"form",
			"authority_ura",
			"issuer_ura",
			"audience",
			"scopes",
			"issued_at_ms",
			"expires_at_ms",
			"signature_hex",
		), true
	case "session_of+session":
		return fields(
			"form",
			"authority_ura",
			"issuer_ura",
			"session_id",
			"scopes",
			"audiences",
			"issued_at_ms",
			"expires_at_ms",
			"signature_hex",
		), true
	default:
		return nil, false
	}
}

// AuthorityFromBinding encodes explicit authority evidence without filling
// missing fields.
func AuthorityFromBinding(a AuthorityBinding) (AuthorityJSON, error) {
	switch {
	case a.Relation == AuthorityRelationSelf && a.EvidenceKind == AuthorityEvidenceIdentity:
		if strings.TrimSpace(a.Authority.URA) == "" {
			return AuthorityJSON{}, ErrInvalidArgument("authority_self_principal_missing")
		}
		return AuthorityJSON{Form: "self+identity", AuthorityURA: a.Authority.URA}, nil
	case a.Relation == AuthorityRelationDelegatedBy && a.EvidenceKind == AuthorityEvidenceDelegation:
		d := a.Delegation
		if d == nil {
			return AuthorityJSON{}, ErrInvalidArgument("authority_delegation_missing")
		}
		return AuthorityJSON{
			Form:         "delegated_by+delegation",
			AuthorityURA: a.Authority.URA,
			IssuerURA:    d.Issuer.URA,
			Audience:     d.Audience,
			Scopes:       d.Scopes,
			IssuedAtMs:   d.IssuedAtMs,
			ExpiresAtMs:  d.ExpiresAtMs,
			SignatureHex: hex.EncodeToString(d.Signature),
		}, nil
	case a.Relation == AuthorityRelationSessionOf && a.EvidenceKind == AuthorityEvidenceSession:
		s := a.Session
		if s == nil {
			return AuthorityJSON{}, ErrInvalidArgument("authority_session_missing")
		}
		return AuthorityJSON{
			Form:         "session_of+session",
			AuthorityURA: a.Authority.URA,
			IssuerURA:    s.Issuer.URA,
			SessionID:    s.SessionID,
			Scopes:       s.Scopes,
			Audiences:    s.Audiences,
			IssuedAtMs:   s.IssuedAtMs,
			ExpiresAtMs:  s.ExpiresAtMs,
			SignatureHex: hex.EncodeToString(s.Signature),
		}, nil
	default:
		return AuthorityJSON{}, ErrInvalidArgument("authority_binding_missing")
	}
}

// ToBinding parses an AuthorityJSON into an AuthorityBinding.
func (a AuthorityJSON) ToBinding() (AuthorityBinding, error) {
	switch a.Form {
	case "self+identity":
		if strings.TrimSpace(a.AuthorityURA) == "" {
			return AuthorityBinding{}, ErrInvalidArgument("authority_self_principal_missing")
		}
		return SelfAuthority(a.AuthorityURA), nil
	case "delegated_by+delegation":
		sig, err := hex.DecodeString(a.SignatureHex)
		if err != nil {
			return AuthorityBinding{}, ErrInvalidArgument(fmt.Sprintf("bad_hex:authority.signature_hex:%s", err))
		}
		if strings.TrimSpace(a.AuthorityURA) == "" ||
			strings.TrimSpace(a.IssuerURA) == "" ||
			strings.TrimSpace(a.Audience) == "" ||
			len(a.Scopes) == 0 ||
			len(sig) == 0 {
			return AuthorityBinding{}, ErrInvalidArgument("authority_delegation_incomplete")
		}
		return DelegatedAuthority(
			AgentIdentity{URA: a.AuthorityURA, Profile: ProfileStrictV2},
			DelegationEvidence{
				Issuer:      AgentIdentity{URA: a.IssuerURA, Profile: ProfileStrictV2},
				Audience:    a.Audience,
				Scopes:      a.Scopes,
				IssuedAtMs:  a.IssuedAtMs,
				ExpiresAtMs: a.ExpiresAtMs,
				Signature:   sig,
			},
		), nil
	case "session_of+session":
		sig, err := hex.DecodeString(a.SignatureHex)
		if err != nil {
			return AuthorityBinding{}, ErrInvalidArgument(fmt.Sprintf("bad_hex:authority.signature_hex:%s", err))
		}
		if strings.TrimSpace(a.AuthorityURA) == "" ||
			strings.TrimSpace(a.IssuerURA) == "" ||
			strings.TrimSpace(a.SessionID) == "" ||
			len(a.Scopes) == 0 ||
			len(a.Audiences) == 0 ||
			len(sig) == 0 {
			return AuthorityBinding{}, ErrInvalidArgument("authority_session_incomplete")
		}
		return SessionAuthority(
			AgentIdentity{URA: a.AuthorityURA, Profile: ProfileStrictV2},
			SessionEvidence{
				Issuer:      AgentIdentity{URA: a.IssuerURA, Profile: ProfileStrictV2},
				SessionID:   a.SessionID,
				Scopes:      a.Scopes,
				Audiences:   a.Audiences,
				IssuedAtMs:  a.IssuedAtMs,
				ExpiresAtMs: a.ExpiresAtMs,
				Signature:   sig,
			},
		), nil
	default:
		return AuthorityBinding{}, ErrInvalidArgument(fmt.Sprintf("unknown_authority_form:%s", a.Form))
	}
}

type InvocationAuthorityProofJSON struct {
	ProofType          string                    `json:"proof_type"`
	Binding            *AuthorityOrBootstrapJSON `json:"binding,omitempty"`
	ProofPayloadHex    string                    `json:"proof_payload_hex"`
	ProofHashHex       string                    `json:"proof_hash_hex"`
	Issuer             *IdentityJSON             `json:"issuer,omitempty"`
	SignatureHex       string                    `json:"signature_hex"`
	SignatureAlg       string                    `json:"signature_alg"`
	SignatureKeyIDHint string                    `json:"signature_key_id_hint"`
	AdmissionHook      string                    `json:"admission_hook"`
}

func AuthorityProofFromProof(
	proof InvocationAuthorityProof,
) (InvocationAuthorityProofJSON, error) {
	var binding *AuthorityOrBootstrapJSON
	if proof.Binding != nil {
		encoded, err := AuthorityOrBootstrapJSONFromBinding(*proof.Binding)
		if err != nil {
			return InvocationAuthorityProofJSON{}, err
		}
		binding = &encoded
	}
	var issuer *IdentityJSON
	if proof.Issuer != nil {
		encoded := IdentityFromAgent(*proof.Issuer)
		issuer = &encoded
	}
	out := InvocationAuthorityProofJSON{
		ProofType:       proof.ProofType,
		Binding:         binding,
		ProofPayloadHex: hex.EncodeToString(proof.ProofPayload),
		ProofHashHex:    hex.EncodeToString(proof.ProofHash[:]),
		Issuer:          issuer,
		AdmissionHook:   proof.AdmissionHook,
	}
	if proof.Signature != nil {
		out.SignatureHex = hex.EncodeToString(proof.Signature.Signature)
		out.SignatureAlg = proof.Signature.Algorithm
		out.SignatureKeyIDHint = proof.Signature.KeyIDHint
	}
	return out, nil
}

// ToProof converts the portable JSON projection back into the canonical
// authority-proof domain object. Canonical proof bytes remain owned by the
// axiom module.
func (p InvocationAuthorityProofJSON) ToProof() (InvocationAuthorityProof, error) {
	proofHash, err := hexTo32(p.ProofHashHex)
	if err != nil {
		return InvocationAuthorityProof{}, err
	}
	proofPayload, err := hex.DecodeString(p.ProofPayloadHex)
	if err != nil {
		return InvocationAuthorityProof{}, ErrInvalidArgument(fmt.Sprintf("bad_authority_proof_payload_hex:%s", err))
	}
	var binding *AuthorityOrBootstrap
	if p.Binding != nil {
		decoded, err := p.Binding.ToBinding()
		if err != nil {
			return InvocationAuthorityProof{}, err
		}
		binding = &decoded
	}
	var issuer *AgentIdentity
	if p.Issuer != nil {
		decoded, err := p.Issuer.ToAgent()
		if err != nil {
			return InvocationAuthorityProof{}, err
		}
		issuer = &decoded
	}
	var signature *CalleeSignature
	if strings.TrimSpace(p.SignatureHex) != "" {
		decoded, err := hex.DecodeString(p.SignatureHex)
		if err != nil {
			return InvocationAuthorityProof{}, ErrInvalidArgument(fmt.Sprintf("bad_authority_proof_signature_hex:%s", err))
		}
		signature = &CalleeSignature{
			Algorithm: p.SignatureAlg,
			Signature: decoded,
			KeyIDHint: p.SignatureKeyIDHint,
		}
	}
	return InvocationAuthorityProof{
		ProofType:     p.ProofType,
		Binding:       binding,
		ProofPayload:  proofPayload,
		ProofHash:     proofHash,
		Issuer:        issuer,
		Signature:     signature,
		AdmissionHook: p.AdmissionHook,
	}, nil
}

// CausalFromCtx encodes a CausalContext into a CausalJSON.
func CausalFromCtx(c CausalContext) CausalJSON {
	switch c.Form {
	case CausalNone:
		return CausalJSON{Form: "none"}
	case CausalScalar:
		if c.Scalar == nil {
			return CausalJSON{Form: "none"}
		}
		return CausalJSON{
			Form:           "scalar",
			ReceiptHashHex: hex.EncodeToString(c.Scalar.ReceiptHash[:]),
			ReceiptURA:     c.Scalar.ReceiptURA,
		}
	case CausalList:
		prior := make([]ReceiptRefJSON, 0, len(c.List))
		for _, r := range c.List {
			prior = append(prior, ReceiptRefJSON{
				ReceiptHashHex: hex.EncodeToString(r.ReceiptHash[:]),
				ReceiptURA:     r.ReceiptURA,
			})
		}
		return CausalJSON{Form: "list", Prior: prior}
	case CausalMerkle:
		return CausalJSON{
			Form:     "merkle",
			RootHex:  hex.EncodeToString(c.MerkleRoot[:]),
			ProofURA: c.MerkleProofURA,
		}
	}
	return CausalJSON{Form: "none"}
}

// ToCtx parses a CausalJSON into a CausalContext.
func (c CausalJSON) ToCtx() (CausalContext, error) {
	switch c.Form {
	case "none":
		return CausalNoneCtx(), nil
	case "scalar":
		h, err := hexTo32(c.ReceiptHashHex)
		if err != nil {
			return CausalContext{}, err
		}
		return CausalScalarCtx(ReceiptRef{ReceiptHash: h, ReceiptURA: c.ReceiptURA}), nil
	case "list":
		refs := make([]ReceiptRef, 0, len(c.Prior))
		for _, p := range c.Prior {
			h, err := hexTo32(p.ReceiptHashHex)
			if err != nil {
				return CausalContext{}, err
			}
			refs = append(refs, ReceiptRef{ReceiptHash: h, ReceiptURA: p.ReceiptURA})
		}
		return CausalListCtx(refs), nil
	case "merkle":
		root, err := hexTo32(c.RootHex)
		if err != nil {
			return CausalContext{}, err
		}
		return CausalMerkleCtx(root, c.ProofURA), nil
	default:
		return CausalContext{}, ErrInvalidArgument(fmt.Sprintf("unknown_causal_form:%s", c.Form))
	}
}

// ── Invocation + receipt JSON ────────────────────────────────────────

// InvocationJSON is the wire shape for an envelope + caller signature.
type InvocationJSON struct {
	InvocationID       string       `json:"invocation_id"`
	Caller             IdentityJSON `json:"caller"`
	Callee             IdentityJSON `json:"callee"`
	Subject            IdentityJSON `json:"subject"`
	Ability            string       `json:"ability"`
	ArgsDigestHex      string       `json:"args_digest_hex"`
	InvocationNonceHex string       `json:"invocation_nonce_hex"`
	CausalContext      CausalJSON   `json:"causal_context"`
	CallerSignatureHex string       `json:"caller_signature_hex"`
	CallerSignatureAlg string       `json:"caller_signature_alg"`
}

var invocationJSONFields = map[string]struct{}{
	"invocation_id":        {},
	"caller":               {},
	"callee":               {},
	"subject":              {},
	"ability":              {},
	"args_digest_hex":      {},
	"invocation_nonce_hex": {},
	"causal_context":       {},
	"caller_signature_hex": {},
	"caller_signature_alg": {},
}

func (i *InvocationJSON) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := invocationJSONFields[key]; !ok {
			return ErrInvalidArgument(fmt.Sprintf("unknown_invocation_json_field:%s", key))
		}
	}

	type invocationJSONAlias InvocationJSON
	var aux invocationJSONAlias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*i = InvocationJSON(aux)
	return nil
}

func NewDescriptorBoundInvocationJSON(invocationID string, env DescriptorBoundEnvelope, sig CallerSignature) InvocationJSON {
	return NewInvocationJSON(invocationID, env.Envelope(), sig)
}

// NewInvocationJSON builds an InvocationJSON from a signed envelope.
func NewInvocationJSON(invocationID string, env InvocationEnvelope, sig CallerSignature) InvocationJSON {
	return InvocationJSON{
		InvocationID:       invocationID,
		Caller:             IdentityFromAgent(env.Caller),
		Callee:             IdentityFromAgent(env.Callee),
		Subject:            IdentityFromSubject(env.Subject),
		Ability:            env.Ability,
		ArgsDigestHex:      hex.EncodeToString(env.ArgsDigest[:]),
		InvocationNonceHex: hex.EncodeToString(env.InvocationNonce[:]),
		CausalContext:      CausalFromCtx(env.CausalContext),
		CallerSignatureHex: hex.EncodeToString(sig.Signature),
		CallerSignatureAlg: sig.Algorithm,
	}
}

// ToEnvelope parses back into (envelope, signature).
func (i InvocationJSON) ToEnvelope() (InvocationEnvelope, CallerSignature, error) {
	caller, err := i.Caller.ToAgent()
	if err != nil {
		return InvocationEnvelope{}, CallerSignature{}, err
	}
	callee, err := i.Callee.ToAgent()
	if err != nil {
		return InvocationEnvelope{}, CallerSignature{}, err
	}
	subject, err := i.Subject.ToSubject()
	if err != nil {
		return InvocationEnvelope{}, CallerSignature{}, err
	}
	args, err := hexTo32(i.ArgsDigestHex)
	if err != nil {
		return InvocationEnvelope{}, CallerSignature{}, err
	}
	nonce, err := hexTo16(i.InvocationNonceHex)
	if err != nil {
		return InvocationEnvelope{}, CallerSignature{}, err
	}
	ctx, err := i.CausalContext.ToCtx()
	if err != nil {
		return InvocationEnvelope{}, CallerSignature{}, err
	}
	sigBytes, err := hex.DecodeString(i.CallerSignatureHex)
	if err != nil {
		return InvocationEnvelope{}, CallerSignature{}, ErrInvalidArgument(fmt.Sprintf("bad_caller_sig_hex:%s", err))
	}
	return InvocationEnvelope{
			Caller: caller, Callee: callee, Subject: subject,
			Ability: i.Ability, ArgsDigest: args, InvocationNonce: nonce,
			CausalContext: ctx,
		},
		CallerSignature{Algorithm: i.CallerSignatureAlg, Signature: sigBytes},
		nil
}

func (i InvocationJSON) ToDescriptorBoundEnvelope() (*DescriptorBoundEnvelope, CallerSignature, error) {
	if _, err := ParseAbilityDescriptorRef(i.Ability); err != nil {
		return nil, CallerSignature{}, nil
	}
	env, sig, err := i.ToEnvelope()
	if err != nil {
		return nil, CallerSignature{}, err
	}
	descriptorBound, err := NewDescriptorBoundEnvelope(env)
	if err != nil {
		return nil, CallerSignature{}, err
	}
	return &descriptorBound, sig, nil
}

// JSONU64 is the portable bundle representation for protocol uint64 counters.
// Writers and readers use canonical decimal JSON strings only.
type JSONU64 uint64

func (v JSONU64) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(strconv.FormatUint(uint64(v), 10))), nil
}

func (v *JSONU64) UnmarshalJSON(data []byte) error {
	value, err := parseJSONU64(data)
	if err != nil {
		return err
	}
	*v = JSONU64(value)
	return nil
}

type JSONU32 uint32

func (v JSONU32) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(strconv.FormatUint(uint64(v), 10))), nil
}

func (v *JSONU32) UnmarshalJSON(data []byte) error {
	value, err := parseJSONU64(data)
	if err != nil {
		return err
	}
	if value > uint64(^uint32(0)) {
		return ErrInvalidArgument(fmt.Sprintf("u32_out_of_range:%d", value))
	}
	*v = JSONU32(value)
	return nil
}

// ReceiptJSON is the wire shape for a receipt + callee signature.
type ReceiptJSON struct {
	Index              uint64                       `json:"index,string"`
	InvocationID       string                       `json:"invocation_id"`
	ReceiptType        string                       `json:"receipt_type"`
	State              string                       `json:"state"`
	TimestampUnixMs    int64                        `json:"timestamp_unix_ms,string"`
	PrevReceiptHashHex string                       `json:"prev_receipt_hash_hex"`
	PayloadSha256Hex   string                       `json:"payload_sha256_hex"`
	Reason             string                       `json:"reason"`
	CleanupComplete    bool                         `json:"cleanup_complete"`
	CallerBinding      IdentityJSON                 `json:"caller_binding"`
	CalleeBinding      IdentityJSON                 `json:"callee_binding"`
	SubjectBinding     IdentityJSON                 `json:"subject_binding"`
	InvocationNonceHex string                       `json:"invocation_nonce_hex"`
	CausalBinding      CausalJSON                   `json:"causal_binding"`
	AbilityBinding     string                       `json:"ability_binding"`
	AuthorityBinding   AuthorityOrBootstrapJSON     `json:"authority_binding"`
	UsageTokensIn      JSONU64                      `json:"usage_tokens_in"`
	UsageTokensOut     JSONU64                      `json:"usage_tokens_out"`
	UsageDurationMs    JSONU64                      `json:"usage_duration_ms"`
	UsageExternalCalls JSONU32                      `json:"usage_external_calls"`
	SubjectRef         EntityRefJSON                `json:"subject_ref"`
	DescriptorVersion  string                       `json:"descriptor_version"`
	SchemaHashHex      string                       `json:"schema_hash_hex"`
	ImplHashHex        string                       `json:"impl_hash_hex"`
	RuntimeEnv         string                       `json:"runtime_env"`
	AuthorityProof     InvocationAuthorityProofJSON `json:"authority_proof"`
	InputHashHex       string                       `json:"input_hash_hex"`
	OutputHashHex      string                       `json:"output_hash_hex"`
	ParentReceipts     []ReceiptRefJSON             `json:"parent_receipts"`
	CalleeSignatureHex string                       `json:"callee_signature_hex"`
	CalleeSignatureAlg string                       `json:"callee_signature_alg"`
}

// InvocationReceiptJSON is the additive full-domain projection around the
// stable ReceiptJSON bundle contract. Keeping optional domain fields here
// preserves source compatibility for external ReceiptJSON struct literals.
// Its JSON representation is flattened to the portable cross-language shape.
type InvocationReceiptJSON struct {
	Receipt                  ReceiptJSON   `json:"-"`
	SelfHashHex              *string       `json:"self_hash_hex,omitempty"`
	PayloadHex               *string       `json:"payload_hex,omitempty"`
	PayloadContentType       string        `json:"payload_content_type,omitempty"`
	ChildInvocationID        string        `json:"child_invocation_id,omitempty"`
	CalleeSignatureKeyIDHint string        `json:"callee_signature_key_id_hint,omitempty"`
	SignerBinding            *IdentityJSON `json:"signer_binding,omitempty"`
	HostAttestationHex       string        `json:"host_attestation_hex,omitempty"`
}

// MarshalJSON flattens the stable receipt and the additive domain fields.
func (r InvocationReceiptJSON) MarshalJSON() ([]byte, error) {
	base, err := json.Marshal(r.Receipt)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(base, &object); err != nil {
		return nil, err
	}
	extension, err := json.Marshal(struct {
		SelfHashHex              *string       `json:"self_hash_hex,omitempty"`
		PayloadHex               *string       `json:"payload_hex,omitempty"`
		PayloadContentType       string        `json:"payload_content_type,omitempty"`
		ChildInvocationID        string        `json:"child_invocation_id,omitempty"`
		CalleeSignatureKeyIDHint string        `json:"callee_signature_key_id_hint,omitempty"`
		SignerBinding            *IdentityJSON `json:"signer_binding,omitempty"`
		HostAttestationHex       string        `json:"host_attestation_hex,omitempty"`
	}{
		SelfHashHex:              r.SelfHashHex,
		PayloadHex:               r.PayloadHex,
		PayloadContentType:       r.PayloadContentType,
		ChildInvocationID:        r.ChildInvocationID,
		CalleeSignatureKeyIDHint: r.CalleeSignatureKeyIDHint,
		SignerBinding:            r.SignerBinding,
		HostAttestationHex:       r.HostAttestationHex,
	})
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(extension, &fields); err != nil {
		return nil, err
	}
	for key, value := range fields {
		object[key] = value
	}
	return json.Marshal(object)
}

// UnmarshalJSON reads the flattened full-domain shape without teaching the
// stable ReceiptJSON type about additive fields.
func (r *InvocationReceiptJSON) UnmarshalJSON(data []byte) error {
	var receipt ReceiptJSON
	if err := json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	var extension struct {
		SelfHashHex              *string       `json:"self_hash_hex,omitempty"`
		PayloadHex               *string       `json:"payload_hex,omitempty"`
		PayloadContentType       string        `json:"payload_content_type,omitempty"`
		ChildInvocationID        string        `json:"child_invocation_id,omitempty"`
		CalleeSignatureKeyIDHint string        `json:"callee_signature_key_id_hint,omitempty"`
		SignerBinding            *IdentityJSON `json:"signer_binding,omitempty"`
		HostAttestationHex       string        `json:"host_attestation_hex,omitempty"`
	}
	if err := json.Unmarshal(data, &extension); err != nil {
		return err
	}
	*r = InvocationReceiptJSON{
		Receipt:                  receipt,
		SelfHashHex:              extension.SelfHashHex,
		PayloadHex:               extension.PayloadHex,
		PayloadContentType:       extension.PayloadContentType,
		ChildInvocationID:        extension.ChildInvocationID,
		CalleeSignatureKeyIDHint: extension.CalleeSignatureKeyIDHint,
		SignerBinding:            extension.SignerBinding,
		HostAttestationHex:       extension.HostAttestationHex,
	}
	return nil
}

// UnmarshalJSON decodes a receipt and rejects a document that omits any usage
// counter. The DEC-010 contract is "zero is a fact, absence is not": a zero
// usage tail is a deliberate signed value, but a receipt that simply leaves the
// counters out is malformed. Go's default decoder would silently coerce the
// missing fields to zero, so we presence-check the four usage keys here to keep
// the read side symmetric with the Rust / Python / Swift decoders.
func (r *ReceiptJSON) UnmarshalJSON(data []byte) error {
	type alias ReceiptJSON
	if err := json.Unmarshal(data, (*alias)(r)); err != nil {
		return err
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return err
	}
	for _, key := range []string{
		"usage_tokens_in",
		"usage_tokens_out",
		"usage_duration_ms",
		"usage_external_calls",
		"subject_ref",
		"descriptor_version",
		"schema_hash_hex",
		"impl_hash_hex",
		"runtime_env",
		"authority_proof",
		"input_hash_hex",
		"output_hash_hex",
		"parent_receipts",
		"authority_binding",
		"callee_signature_hex",
		"callee_signature_alg",
	} {
		if _, ok := present[key]; !ok {
			return fmt.Errorf("receipt_json_missing_required_field:%s", key)
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "schema_hash_hex", value: r.SchemaHashHex},
		{name: "impl_hash_hex", value: r.ImplHashHex},
	} {
		hash, err := hexTo32(field.value)
		if err != nil {
			return err
		}
		if isZero32(hash) {
			return ErrInvalidArgument(fmt.Sprintf("%s_unbound", field.name))
		}
	}
	return nil
}

// ToUnverifiedReceipt decodes a complete wire receipt without granting it
// trusted status. Call Verify to obtain SignedInvocationReceipt.
func (projection InvocationReceiptJSON) ToUnverifiedReceipt() (*UnverifiedReceipt, error) {
	r := projection.Receipt
	if strings.TrimSpace(r.InvocationID) == "" {
		return nil, ErrInvalidArgument("invocation_id_required")
	}
	prevHash, err := hexTo32(r.PrevReceiptHashHex)
	if err != nil {
		return nil, err
	}
	payloadDigest, err := hexTo32(r.PayloadSha256Hex)
	if err != nil {
		return nil, err
	}
	nonce, err := hexTo16(r.InvocationNonceHex)
	if err != nil {
		return nil, err
	}
	caller, err := r.CallerBinding.ToAgent()
	if err != nil {
		return nil, err
	}
	callee, err := r.CalleeBinding.ToAgent()
	if err != nil {
		return nil, err
	}
	subject, err := r.SubjectBinding.ToSubject()
	if err != nil {
		return nil, err
	}
	causal, err := r.CausalBinding.ToCtx()
	if err != nil {
		return nil, err
	}
	authority, err := r.AuthorityBinding.ToBinding()
	if err != nil {
		return nil, err
	}
	proof, err := r.AuthorityProof.ToProof()
	if err != nil {
		return nil, err
	}
	schemaHash, err := hexTo32(r.SchemaHashHex)
	if err != nil {
		return nil, err
	}
	if isZero32(schemaHash) {
		return nil, ErrInvalidArgument("schema_hash_hex_unbound")
	}
	implHash, err := hexTo32(r.ImplHashHex)
	if err != nil {
		return nil, err
	}
	if isZero32(implHash) {
		return nil, ErrInvalidArgument("impl_hash_hex_unbound")
	}
	inputHash, err := hexTo32(r.InputHashHex)
	if err != nil {
		return nil, err
	}
	if isZero32(inputHash) {
		return nil, ErrInvalidArgument("input_hash_hex_unbound")
	}
	outputHash, err := hexTo32(r.OutputHashHex)
	if err != nil {
		return nil, err
	}
	if isZero32(outputHash) {
		return nil, ErrInvalidArgument("output_hash_hex_unbound")
	}
	if strings.TrimSpace(r.DescriptorVersion) == "" {
		return nil, ErrInvalidArgument("descriptor_version_required")
	}
	if strings.TrimSpace(r.RuntimeEnv) == "" {
		return nil, ErrInvalidArgument("runtime_env_required")
	}
	subjectRef, err := r.SubjectRef.ToEntityRef()
	if err != nil {
		return nil, err
	}
	parents := make([]ReceiptRef, len(r.ParentReceipts))
	for index, parent := range r.ParentReceipts {
		hash, err := hexTo32(parent.ReceiptHashHex)
		if err != nil {
			return nil, err
		}
		if isZero32(hash) || strings.TrimSpace(parent.ReceiptURA) == "" {
			return nil, ErrInvalidArgument("receipt_parent_reference_semantically_invalid")
		}
		parents[index] = ReceiptRef{ReceiptHash: hash, ReceiptURA: parent.ReceiptURA}
	}
	signatureBytes, err := hex.DecodeString(r.CalleeSignatureHex)
	if err != nil {
		return nil, ErrInvalidArgument(fmt.Sprintf("bad_callee_signature_hex:%s", err))
	}
	var hosted *HostedAttestation
	if projection.SignerBinding != nil {
		signer, err := projection.SignerBinding.ToAgent()
		if err != nil {
			return nil, err
		}
		attestation, err := hex.DecodeString(projection.HostAttestationHex)
		if err != nil {
			return nil, ErrInvalidArgument(fmt.Sprintf("bad_host_attestation_hex:%s", err))
		}
		hosted = &HostedAttestation{SignerBinding: signer, HostAttestation: attestation}
	} else if strings.TrimSpace(projection.HostAttestationHex) != "" {
		return nil, ErrInvalidArgument("host_attestation_requires_signer_binding")
	}
	payload := []byte(nil)
	if projection.PayloadHex != nil {
		payload, err = hex.DecodeString(*projection.PayloadHex)
		if err != nil {
			return nil, ErrInvalidArgument(fmt.Sprintf("bad_payload_hex:%s", err))
		}
		if Sha256(payload) != payloadDigest {
			return nil, ErrInvalidArgument("payload_digest_mismatch")
		}
		if Sha256(payload) != outputHash {
			return nil, ErrInvalidArgument("receipt_output_hash_semantically_invalid")
		}
	}
	binding := AxiomBinding{
		Caller:           caller,
		Callee:           callee,
		Subject:          subject,
		InvocationNonce:  nonce,
		Causal:           causal,
		PayloadDigest:    payloadDigest,
		AbilityBinding:   r.AbilityBinding,
		AuthorityBinding: authority,
		Hosted:           hosted,
		ProofFacts: NewReceiptProofFacts(
			&subjectRef,
			r.DescriptorVersion,
			schemaHash,
			implHash,
			r.RuntimeEnv,
			proof,
			inputHash,
			outputHash,
			parents,
		),
	}
	if err := validateAuthorityBinding(caller, subject, r.AbilityBinding, authority); err != nil {
		return nil, err
	}
	if err := validateAuthorityProof(proof, authority, callee); err != nil {
		return nil, err
	}
	descriptor, err := ParseAbilityDescriptorRef(r.AbilityBinding)
	if err != nil {
		return nil, err
	}
	if descriptor.Version != r.DescriptorVersion {
		return nil, ErrInvalidArgument("receipt_descriptor_version_semantically_invalid")
	}
	expectedSubject, err := EntityRefForSubject(subject)
	if err != nil {
		return nil, err
	}
	if subjectRef != expectedSubject {
		return nil, ErrInvalidArgument("receipt_subject_ref_semantically_invalid")
	}
	if !receiptRefsEqual(parents, enumerableParentReceipts(causal)) {
		return nil, ErrInvalidArgument("receipt_parent_receipts_semantically_invalid")
	}
	if err := validateCalleeSignatureStructure(CalleeSignature{
		Algorithm: r.CalleeSignatureAlg,
		Signature: signatureBytes,
		KeyIDHint: projection.CalleeSignatureKeyIDHint,
	}); err != nil {
		return nil, err
	}
	record := newReceiptRecord(receiptRecordInput{
		Index:              r.Index,
		InvocationID:       r.InvocationID,
		ReceiptType:        r.ReceiptType,
		State:              r.State,
		TimestampUnixMs:    r.TimestampUnixMs,
		PrevReceiptHash:    prevHash,
		Payload:            payload,
		PayloadContentType: projection.PayloadContentType,
		CleanupComplete:    r.CleanupComplete,
		Reason:             r.Reason,
		ChildInvocationID:  projection.ChildInvocationID,
		AxiomBinding:       binding,
		Usage: InvocationUsage{
			TokensIn:      uint64(r.UsageTokensIn),
			TokensOut:     uint64(r.UsageTokensOut),
			DurationMs:    uint64(r.UsageDurationMs),
			ExternalCalls: uint32(r.UsageExternalCalls),
		},
	})
	record.CalleeSignature = CalleeSignature{
		Algorithm: r.CalleeSignatureAlg,
		Signature: append([]byte(nil), signatureBytes...),
		KeyIDHint: projection.CalleeSignatureKeyIDHint,
	}
	if projection.SelfHashHex != nil {
		selfHash, err := hexTo32(*projection.SelfHashHex)
		if err != nil {
			return nil, err
		}
		record.SelfHash = selfHash
	}
	return &UnverifiedReceipt{receipt: record}, nil
}

// ParseInvocationReceiptJSON decodes the portable bundle shape into an
// unverified receipt.
func ParseInvocationReceiptJSON(data []byte) (*UnverifiedReceipt, error) {
	var projection InvocationReceiptJSON
	if err := json.Unmarshal(data, &projection); err != nil {
		return nil, ErrInvalidArgument(fmt.Sprintf("bad_receipt_json:%s", err))
	}
	return projection.ToUnverifiedReceipt()
}

// ProjectSignedInvocationReceipt serializes an already provider-accepted
// receipt without exposing a receipt construction or signing surface.
func ProjectSignedInvocationReceipt(
	receipt SignedInvocationReceipt,
) (InvocationReceiptJSON, error) {
	record := cloneReceiptRecord(receipt.receipt)
	binding := record.AxiomBinding
	if binding.ProofFacts.SubjectRef == nil {
		return InvocationReceiptJSON{}, ErrInvalidArgument("receipt_subject_ref_missing")
	}
	authority, err := AuthorityOrBootstrapJSONFromBinding(binding.AuthorityBinding)
	if err != nil {
		return InvocationReceiptJSON{}, err
	}
	proof, err := AuthorityProofFromProof(binding.ProofFacts.AuthorityProof)
	if err != nil {
		return InvocationReceiptJSON{}, err
	}
	parents := make([]ReceiptRefJSON, len(binding.ProofFacts.ParentReceipts))
	for index, parent := range binding.ProofFacts.ParentReceipts {
		parents[index] = ReceiptRefJSON{
			ReceiptHashHex: hex.EncodeToString(parent.ReceiptHash[:]),
			ReceiptURA:     parent.ReceiptURA,
		}
	}
	payloadHex := hex.EncodeToString(record.Payload)
	selfHashHex := hex.EncodeToString(record.SelfHash[:])
	projection := InvocationReceiptJSON{
		Receipt: ReceiptJSON{
			Index:              record.Index,
			InvocationID:       record.InvocationID,
			ReceiptType:        record.ReceiptType,
			State:              record.State,
			TimestampUnixMs:    record.TimestampUnixMs,
			PrevReceiptHashHex: hex.EncodeToString(record.PrevReceiptHash[:]),
			PayloadSha256Hex:   hex.EncodeToString(binding.PayloadDigest[:]),
			Reason:             record.Reason,
			CleanupComplete:    record.CleanupComplete,
			CallerBinding:      IdentityFromAgent(binding.Caller),
			CalleeBinding:      IdentityFromAgent(binding.Callee),
			SubjectBinding:     IdentityFromSubject(binding.Subject),
			InvocationNonceHex: hex.EncodeToString(binding.InvocationNonce[:]),
			CausalBinding:      CausalFromCtx(binding.Causal),
			AbilityBinding:     binding.AbilityBinding,
			AuthorityBinding:   authority,
			UsageTokensIn:      JSONU64(record.Usage.TokensIn),
			UsageTokensOut:     JSONU64(record.Usage.TokensOut),
			UsageDurationMs:    JSONU64(record.Usage.DurationMs),
			UsageExternalCalls: JSONU32(record.Usage.ExternalCalls),
			SubjectRef:         EntityRefToJSON(*binding.ProofFacts.SubjectRef),
			DescriptorVersion:  binding.ProofFacts.DescriptorVersion,
			SchemaHashHex:      hex.EncodeToString(binding.ProofFacts.SchemaHash[:]),
			ImplHashHex:        hex.EncodeToString(binding.ProofFacts.ImplHash[:]),
			RuntimeEnv:         binding.ProofFacts.RuntimeEnv,
			AuthorityProof:     proof,
			InputHashHex:       hex.EncodeToString(binding.ProofFacts.InputHash[:]),
			OutputHashHex:      hex.EncodeToString(binding.ProofFacts.OutputHash[:]),
			ParentReceipts:     parents,
			CalleeSignatureHex: hex.EncodeToString(record.CalleeSignature.Signature),
			CalleeSignatureAlg: record.CalleeSignature.Algorithm,
		},
		SelfHashHex:              &selfHashHex,
		PayloadHex:               &payloadHex,
		PayloadContentType:       record.PayloadContentType,
		ChildInvocationID:        record.ChildInvocationID,
		CalleeSignatureKeyIDHint: record.CalleeSignature.KeyIDHint,
	}
	if binding.Hosted != nil {
		signer := IdentityFromAgent(binding.Hosted.SignerBinding)
		projection.SignerBinding = &signer
		projection.HostAttestationHex = hex.EncodeToString(binding.Hosted.HostAttestation)
	}
	return projection, nil
}

// ── Pubkey alias map ─────────────────────────────────────────────────

// PubkeyMap — parsed --against-pubkeys alias=path,... spec.
type PubkeyMap struct {
	ByAlias map[string]string
}

// ParsePubkeyMap parses a comma-separated alias=path spec.
func ParsePubkeyMap(spec string) (*PubkeyMap, error) {
	m := &PubkeyMap{ByAlias: map[string]string{}}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := strings.Index(part, "=")
		if idx < 0 {
			return nil, ErrInvalidArgument(fmt.Sprintf("bad_pubkey_spec:%s; expected alias=path", part))
		}
		alias := strings.TrimSpace(part[:idx])
		path := strings.TrimSpace(part[idx+1:])
		m.ByAlias[alias] = filepath.Clean(path)
	}
	return m, nil
}

// ── Helpers ──────────────────────────────────────────────────────────

func hexTo32(s string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, ErrInvalidArgument(fmt.Sprintf("bad_hex:%s", err))
	}
	if len(b) != 32 {
		return out, ErrInvalidArgument(fmt.Sprintf("expected_32_bytes_got_%d", len(b)))
	}
	copy(out[:], b)
	return out, nil
}

func isZero32(value [32]byte) bool {
	for _, b := range value {
		if b != 0 {
			return false
		}
	}
	return true
}

func hexTo16(s string) ([16]byte, error) {
	var out [16]byte
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, ErrInvalidArgument(fmt.Sprintf("bad_hex:%s", err))
	}
	if len(b) != 16 {
		return out, ErrInvalidArgument(fmt.Sprintf("expected_16_bytes_got_%d", len(b)))
	}
	copy(out[:], b)
	return out, nil
}

func parseJSONU64(data []byte) (uint64, error) {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		return 0, ErrInvalidArgument("invalid_u64_decimal")
	}
	if !strings.HasPrefix(raw, "\"") {
		return 0, ErrInvalidArgument("invalid_u64_decimal_string")
	}
	value, err := strconv.Unquote(raw)
	if err != nil {
		return 0, ErrInvalidArgument(fmt.Sprintf("invalid_u64_json_string:%s", err))
	}
	return parseDecimalU64(value)
}

func parseDecimalU64(value string) (uint64, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, ErrInvalidArgument(fmt.Sprintf("invalid_u64_decimal:%s", value))
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, ErrInvalidArgument(fmt.Sprintf("invalid_u64_decimal:%s", value))
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, ErrInvalidArgument(fmt.Sprintf("u64_out_of_range:%s", value))
	}
	return parsed, nil
}

// Kept so encoding/json is always reachable from dependents even if the
// bundle file evolves. Zero runtime cost.
var _ = json.Marshal
