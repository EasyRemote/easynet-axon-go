package axon

// RFC 001 §A14 — the three receipt verbs (Go).
//
// M1 Verify()         = INTEGRITY     — receipt not tampered.
// M2 Trace()          = DIRECT CAUSE  — who DIRECTLY triggered this.
// M3 ProveAuthority() = AUTHORITY     — the CALLER had authority over
//                                       the subject/ability.
//
// These are the STANDARD, cross-language parity-asserted methods. They
// mirror the Rust SDK's InvocationReceipt::{verify,trace,prove_authority}
// (sdk/rust/src/invocation/audit.rs) semantics byte-for-byte and REUSE
// the canonical-bytes / signature primitives in axiom.go — there is no
// second verifier here.
//
// The idiomatic Go layer (functional options, range-able Trace slice,
// typed AuthorityProof accessors) sits ON TOP and CALLS these core
// methods underneath; it never reimplements the wire or the semantics.

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"time"
)

// ── M1: VerifiedReceipt ──────────────────────────────────────────────

// SigningModel distinguishes self-signed (Model A) from §A12 host-signed
// (Model B) receipts.
type SigningModel uint8

const (
	SigningSelf   SigningModel = iota // callee signed its own receipt
	SigningHosted                     // a §A12 host signed on behalf of the callee
)

// VerifiedReceipt — M1 result. Proof the receipt's signed bytes verify
// under a resolved key.
type VerifiedReceipt struct {
	// SelfHash — recomputed canonical SHA-256 of the receipt.
	SelfHash [32]byte
	// SignerURA — the key URA that verified the signature (callee, or
	// the §A12 host).
	SignerURA string
	// SigningModel — self-signed vs host-signed.
	SigningModel SigningModel
}

// ── M2: DirectCausalTrace ────────────────────────────────────────────

// CausalKind identifies the branch of a DirectCausalTrace.
type CausalKind uint8

const (
	TraceRoot   CausalKind = iota // CausalContext::None
	TraceParent                   // Scalar — a single direct parent
	TraceFanin                    // List/Merkle — a fan-in join
)

// DirectCausalTrace — M2 result. The DIRECT parent only; joins are
// surfaced, not walked. NO ledger walk.
type DirectCausalTrace struct {
	Kind CausalKind
	// Parent is set when Kind == TraceParent.
	Parent ReceiptRef
	// Count is the number of parents when Kind == TraceFanin (List → n,
	// Merkle → 0).
	Count int
}

// ── M3: AuthorityProof ───────────────────────────────────────────────

// AuthorityKind identifies the branch of an AuthorityProof.
type AuthorityKind uint8

const (
	ProofSelf        AuthorityKind = iota // caller == authority; no delegation
	ProofDelegatedBy                      // caller acted under authority's delegation
	ProofSessionOf                        // subject == authority; caller presented the session
	ProofBootstrap                        // caller exercised bootstrap authority
)

// AuthorityProof — M3 result. Proof the invocation exercised
// `authority`'s authority. Mirrors the Rust AuthorityProof enum; see
// document/rfcs/001-authority-binding-relation-evidence.md for the
// relation/evidence compatibility matrix this reflects.
type AuthorityProof struct {
	Kind AuthorityKind
	// PrincipalURA is set when Kind == ProofSelf.
	PrincipalURA string
	// IssuerURA and AuthorityURA are set when Kind == ProofDelegatedBy
	// or ProofSessionOf.
	IssuerURA    string
	AuthorityURA string
	// SessionID is set when Kind == ProofSessionOf.
	SessionID string
	// Scopes and ExpiresAtMs are set when Kind == ProofDelegatedBy or
	// ProofSessionOf.
	Scopes      []string
	Audiences   []string // set when Kind == ProofSessionOf
	ExpiresAtMs int64
	// The following are set when Kind == ProofBootstrap.
	Realm   string
	Ability string
}

// ── Delegation/session re-verification helpers (mirror axiom audit.rs) ─

// delegationPayloadCanonical — declaration order is FROZEN to mirror
// the Rust DelegationPayloadCanonical. encoding/json marshals fields in
// declaration order, so re-marshalling here is byte-identical to what
// the issuer signed. DO NOT reorder. `delegatee_ura` (== envelope.caller)
// is bound into the signed material even though it has no stored field
// on DelegationEvidence — otherwise a proof issued for one caller could
// be replayed by another.
type delegationPayloadCanonical struct {
	IssuerURA    string   `json:"issuer_ura"`
	AuthorityURA string   `json:"authority_ura"`
	DelegateeURA string   `json:"delegatee_ura"`
	Audience     string   `json:"audience"`
	Scopes       []string `json:"scopes"`
	IssuedAtMs   int64    `json:"issued_at_ms"`
	ExpiresAtMs  int64    `json:"expires_at_ms"`
}

// sessionAuthorityPayloadCanonical — declaration order is FROZEN to
// mirror the Rust SessionAuthorityPayloadCanonical. `authority_ura`
// (== envelope.subject at verification time) is bound into the signed
// material explicitly rather than relying on an unsigned cross-check.
type sessionAuthorityPayloadCanonical struct {
	IssuerURA    string   `json:"issuer_ura"`
	AuthorityURA string   `json:"authority_ura"`
	SessionID    string   `json:"session_id"`
	Scopes       []string `json:"scopes"`
	Audiences    []string `json:"audiences"`
	IssuedAtMs   int64    `json:"issued_at_ms"`
	ExpiresAtMs  int64    `json:"expires_at_ms"`
}

// scopeMatches mirrors delegation_gate.rs::scope_matches.
func scopeMatches(pattern, ability string) bool {
	if pattern == "*" {
		return true
	}
	if len(pattern) >= 2 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(ability) >= len(prefix) && ability[:len(prefix)] == prefix
	}
	return pattern == ability
}

// audienceAdmits mirrors delegation_gate.rs::audience_admits.
func audienceAdmits(audience, callee string) bool {
	if audience == "*" || audience == callee {
		return true
	}
	if len(audience) > 0 && audience[len(audience)-1] == '/' && len(callee) >= len(audience) {
		return callee[:len(audience)] == audience
	}
	return false
}

// nowUnixMs — wall clock in ms. Offline audit uses now for expiry.
func nowUnixMs() int64 {
	return time.Now().UnixMilli()
}

// signingBody rebuilds the canonical signing body from the opaque receipt.
func (r SignedInvocationReceipt) signingBody() ReceiptBody {
	return receiptBody(&r.receipt)
}

// ── M1: Verify ───────────────────────────────────────────────────────

// Verify — M1 INTEGRITY. Recomputes the canonical bytes (including the
// §A14 authority_binding, which lives in the signed region) and
// Ed25519-verifies the CalleeSignature against the resolved signer key —
// the §A12 host when hosted, otherwise the callee. REUSES
// VerifyReceiptSignatureWithHosted; no crypto is duplicated. Because the
// authority is hashed into the signed bytes, tampering with it is caught
// here.
//
// Returns an error on tamper / missing-or-malformed signature.
func (r SignedInvocationReceipt) Verify(resolver KeyResolver) (VerifiedReceipt, error) {
	if err := validateReceiptRecordSemantics(r.receipt); err != nil {
		return VerifiedReceipt{}, err
	}
	b := r.receipt.AxiomBinding
	body := r.signingBody()
	if err := VerifyReceiptSignatureWithHosted(
		body,
		b.Hosted,
		r.receipt.CalleeSignature,
		resolver,
	); err != nil {
		return VerifiedReceipt{}, err
	}
	computed := computeReceiptHash(&r.receipt)
	if !bytes.Equal(r.receipt.SelfHash[:], computed[:]) {
		return VerifiedReceipt{}, ErrInvalidArgument("self_hash_mismatch")
	}
	signerURA := b.Callee.URA
	model := SigningSelf
	if b.Hosted != nil {
		signerURA = b.Hosted.SignerBinding.URA
		model = SigningHosted
	}
	return VerifiedReceipt{
		SelfHash:     computed,
		SignerURA:    signerURA,
		SigningModel: model,
	}, nil
}

// ── M2: Trace ────────────────────────────────────────────────────────

// Trace — M2 DIRECT CAUSALITY. Reads the receipt's causal binding and
// returns the DIRECT parent only. Infallible: None → Root, Scalar →
// Parent, List/Merkle → Fanin (NOT walked — a single direct parent is
// undefined for a join). Performs no ledger walk.
func (r SignedInvocationReceipt) Trace() DirectCausalTrace {
	c := r.receipt.AxiomBinding.Causal
	switch c.Form {
	case CausalScalar:
		if c.Scalar == nil {
			return DirectCausalTrace{Kind: TraceRoot}
		}
		return DirectCausalTrace{Kind: TraceParent, Parent: *c.Scalar}
	case CausalList:
		return DirectCausalTrace{Kind: TraceFanin, Count: len(c.List)}
	case CausalMerkle:
		return DirectCausalTrace{Kind: TraceFanin, Count: 0}
	default: // CausalNone
		return DirectCausalTrace{Kind: TraceRoot}
	}
}

// ── M3: ProveAuthority ───────────────────────────────────────────────

// ProveAuthority — M3 AUTHORITY. Proves the invocation exercised
// `authority`'s authority. Reads axiom_binding.AuthorityBinding and
// dispatches on (relation, evidence) or the admission-plane Bootstrap
// fact. See document/rfcs/001-authority-binding-relation-evidence.md
// for the complete compatibility matrix this mirrors byte-for-byte:
//   - Self+Identity: assert authority == envelope.caller.
//   - DelegatedBy+Delegation: proves AUTHENTICITY only — issuer
//     resolvable, Ed25519 signature valid over the canonical claim
//     bytes (which bind envelope.caller as delegatee even though there
//     is no stored delegatee field), audience admits callee, scope
//     admits ability, not expired. evidence.issuer MAY differ from
//     authority; no equality is required or checked here (see "Issuer
//     authenticity vs. issuer authority" in the RFC doc).
//   - SessionOf+Session: assert evidence.issuer == envelope.caller
//     (who is presenting this session) AND authority ==
//     envelope.subject (the session's accountable owner), audience
//     admits callee, scope admits ability, not expired, signature
//     valid.
//   - CredentialOf+Attestation: reserved → invalid_argument.
//   - BootstrapBinding (admission plane, not an AuthorityBinding
//     relation): assert principal/caller and ability binding.
//
// Returns permission_denied on an authority failure; invalid_argument on
// a malformed/missing/reserved binding or an unresolvable issuer.
func (r SignedInvocationReceipt) ProveAuthority(resolver KeyResolver) (AuthorityProof, error) {
	b := r.receipt.AxiomBinding
	auth := b.AuthorityBinding
	if auth.IsBootstrap {
		bootstrap := auth.Bootstrap
		if bootstrap == nil {
			return AuthorityProof{}, ErrInvalidArgument("authority_binding_missing")
		}
		if bootstrap.PrincipalURA != b.Caller.URA {
			return AuthorityProof{}, ErrPermissionDenied(
				"bootstrap_authority_principal_mismatch:" + bootstrap.PrincipalURA + "!=" + b.Caller.URA)
		}
		if bootstrap.Ability != b.AbilityBinding {
			return AuthorityProof{}, ErrPermissionDenied(
				"bootstrap_authority_ability_mismatch:" + bootstrap.Ability + "!=" + b.AbilityBinding)
		}
		if strings.TrimSpace(bootstrap.Realm) == "" {
			return AuthorityProof{}, ErrInvalidArgument("bootstrap_authority_realm_empty")
		}
		return AuthorityProof{
			Kind:    ProofBootstrap,
			Realm:   bootstrap.Realm,
			Ability: bootstrap.Ability,
		}, nil
	}
	if auth.Binding == nil {
		return AuthorityProof{}, ErrInvalidArgument("authority_binding_missing")
	}
	return r.proveAuthorityBinding(*auth.Binding, resolver)
}

func (r SignedInvocationReceipt) proveAuthorityBinding(
	binding AuthorityBinding,
	resolver KeyResolver,
) (AuthorityProof, error) {
	b := r.receipt.AxiomBinding
	authorityURA := binding.Authority.URA
	switch {
	case binding.Relation == AuthorityRelationSelf && binding.EvidenceKind == AuthorityEvidenceIdentity:
		if authorityURA != b.Caller.URA {
			return AuthorityProof{}, ErrPermissionDenied(
				"self_authority_principal_mismatch:" + authorityURA + "!=" + b.Caller.URA)
		}
		return AuthorityProof{Kind: ProofSelf, PrincipalURA: authorityURA}, nil

	case binding.Relation == AuthorityRelationDelegatedBy && binding.EvidenceKind == AuthorityEvidenceDelegation:
		d := binding.Delegation
		if d == nil {
			return AuthorityProof{}, ErrInvalidArgument("delegation_evidence_missing")
		}
		// SDK layer proves AUTHENTICITY only: is this signature really
		// from d.Issuer? It does NOT require d.Issuer.URA == authorityURA
		// — issuer (who vouches) and authority (whose accountability is
		// exercised) are independent roles that MAY differ (e.g. a User
		// vouching for a Service they own). Whether THIS issuer is
		// actually ENTITLED to vouch for THIS authority is realm-specific
		// policy, deliberately left to the caller/daemon to layer on top
		// (see RFC doc "Issuer authenticity vs. issuer authority";
		// EasyNet-Cli's admission_facade.rs does this via
		// verify_delegation_issuer_authorized).
		//
		// Rule: audience admits the callee.
		if !audienceAdmits(d.Audience, b.Callee.URA) {
			return AuthorityProof{}, ErrPermissionDenied(
				"delegation_audience_mismatch:" + d.Audience + " does not admit " + b.Callee.URA)
		}
		// Rule: scope admits the exact runtime-admitted ability signed
		// into the receipt.
		ability := b.AbilityBinding
		matched := false
		for _, p := range d.Scopes {
			if scopeMatches(p, ability) {
				matched = true
				break
			}
		}
		if !matched {
			return AuthorityProof{}, ErrPermissionDenied(
				"delegation_scope_mismatch: scopes do not admit " + ability)
		}
		// Rule: not expired (wall clock).
		now := nowUnixMs()
		if now >= d.ExpiresAtMs {
			return AuthorityProof{}, ErrPermissionDenied("delegation_expired")
		}
		// Rule: issuer resolvable + Ed25519 signature valid over the
		// canonical claim bytes, which bind envelope.caller as
		// delegatee even though there is no stored delegatee field —
		// otherwise the proof could be replayed by a different caller.
		issuerKey, err := resolver.Resolve(d.Issuer.URA)
		if err != nil {
			return AuthorityProof{}, err
		}
		payload := delegationPayloadCanonical{
			IssuerURA:    d.Issuer.URA,
			AuthorityURA: authorityURA,
			DelegateeURA: b.Caller.URA,
			Audience:     d.Audience,
			Scopes:       d.Scopes,
			IssuedAtMs:   d.IssuedAtMs,
			ExpiresAtMs:  d.ExpiresAtMs,
		}
		payloadBytes, err := json.Marshal(&payload)
		if err != nil {
			return AuthorityProof{}, ErrInternal("delegation_payload_marshal:" + err.Error())
		}
		if len(d.Signature) != ed25519.SignatureSize {
			return AuthorityProof{}, ErrPermissionDenied("delegation_signature_wrong_length")
		}
		if !ed25519.Verify(issuerKey, payloadBytes, d.Signature) {
			return AuthorityProof{}, ErrPermissionDenied("delegation_signature_invalid")
		}
		return AuthorityProof{
			Kind:         ProofDelegatedBy,
			IssuerURA:    d.Issuer.URA,
			AuthorityURA: authorityURA,
			Scopes:       append([]string(nil), d.Scopes...),
			ExpiresAtMs:  d.ExpiresAtMs,
		}, nil

	case binding.Relation == AuthorityRelationSessionOf && binding.EvidenceKind == AuthorityEvidenceSession:
		s := binding.Session
		if s == nil {
			return AuthorityProof{}, ErrInvalidArgument("session_evidence_missing")
		}
		// Rule: issuer == envelope.caller (who is presenting this
		// session right now — NOT the session's owner).
		if s.Issuer.URA != b.Caller.URA {
			return AuthorityProof{}, ErrPermissionDenied(
				"session_authority_issuer_mismatch:" + s.Issuer.URA + "!=" + b.Caller.URA)
		}
		// Rule: authority == envelope.subject (the session's
		// accountable owner — this is Session's real invariant,
		// distinct from DelegatedBy's issuer==authority rule).
		if authorityURA != b.Subject.URA {
			return AuthorityProof{}, ErrPermissionDenied(
				"session_authority_subject_mismatch:" + authorityURA + "!=" + b.Subject.URA)
		}
		audienceOK := false
		for _, audience := range s.Audiences {
			if audienceAdmits(audience, b.Callee.URA) {
				audienceOK = true
				break
			}
		}
		if !audienceOK {
			return AuthorityProof{}, ErrPermissionDenied(
				"session_authority_audience_mismatch: audiences do not admit " + b.Callee.URA)
		}
		ability := b.AbilityBinding
		scopeOK := false
		for _, p := range s.Scopes {
			if scopeMatches(p, ability) {
				scopeOK = true
				break
			}
		}
		if !scopeOK {
			return AuthorityProof{}, ErrPermissionDenied(
				"session_authority_scope_mismatch: scopes do not admit " + ability)
		}
		now := nowUnixMs()
		if now >= s.ExpiresAtMs {
			return AuthorityProof{}, ErrPermissionDenied("session_authority_expired")
		}
		issuerKey, err := resolver.Resolve(s.Issuer.URA)
		if err != nil {
			return AuthorityProof{}, err
		}
		payload := sessionAuthorityPayloadCanonical{
			IssuerURA:    s.Issuer.URA,
			AuthorityURA: authorityURA,
			SessionID:    s.SessionID,
			Scopes:       s.Scopes,
			Audiences:    s.Audiences,
			IssuedAtMs:   s.IssuedAtMs,
			ExpiresAtMs:  s.ExpiresAtMs,
		}
		payloadBytes, err := json.Marshal(&payload)
		if err != nil {
			return AuthorityProof{}, ErrInternal("session_authority_payload_marshal:" + err.Error())
		}
		if len(s.Signature) != ed25519.SignatureSize {
			return AuthorityProof{}, ErrPermissionDenied("session_authority_signature_wrong_length")
		}
		if !ed25519.Verify(issuerKey, payloadBytes, s.Signature) {
			return AuthorityProof{}, ErrPermissionDenied("session_authority_signature_invalid")
		}
		return AuthorityProof{
			Kind:         ProofSessionOf,
			IssuerURA:    s.Issuer.URA,
			AuthorityURA: authorityURA,
			SessionID:    s.SessionID,
			Scopes:       append([]string(nil), s.Scopes...),
			Audiences:    append([]string(nil), s.Audiences...),
			ExpiresAtMs:  s.ExpiresAtMs,
		}, nil

	case binding.Relation == AuthorityRelationCredentialOf && binding.EvidenceKind == AuthorityEvidenceAttestation:
		return AuthorityProof{}, ErrInvalidArgument("authority_reserved_form")

	default:
		return AuthorityProof{}, ErrInvalidArgument("authority_relation_evidence_mismatch")
	}
}

// ── Idiomatic layer (STEP B) — pure ergonomic wrappers ───────────────
//
// These sit ON TOP of the core verbs and CALL them underneath. They MUST
// NOT change semantics, wire, or canonical bytes (frozen condition 3).

// ResolverOption — functional option carrying a KeyResolver. Lets the
// idiomatic call sites read r.VerifyWith(WithResolver(res)).
type ResolverOption func(*resolverConfig)

type resolverConfig struct {
	resolver KeyResolver
}

// WithResolver supplies the KeyResolver to an idiomatic verb call.
func WithResolver(resolver KeyResolver) ResolverOption {
	return func(c *resolverConfig) { c.resolver = resolver }
}

func resolveConfig(opts []ResolverOption) KeyResolver {
	var c resolverConfig
	for _, o := range opts {
		o(&c)
	}
	return c.resolver
}

// VerifyWith — idiomatic wrapper over Verify using functional options.
// Calls the core Verify underneath; identical result and semantics.
func (r SignedInvocationReceipt) VerifyWith(opts ...ResolverOption) (VerifiedReceipt, error) {
	return r.Verify(resolveConfig(opts))
}

// CausalParent — a single direct parent surfaced by TraceParents. For a
// Scalar trace this is the one parent; for a Fanin it is repeated Count
// times with empty refs so `for _, p := range r.TraceParents()` reads
// naturally. For Root the slice is empty.
type CausalParent struct {
	// IsJoin reports whether this parent came from a fan-in join (List
	// or Merkle) rather than a single Scalar parent.
	IsJoin bool
	// Ref is the parent reference (zero for join entries, which carry no
	// individually-addressable ref on the receipt).
	Ref ReceiptRef
}

// TraceParents — idiomatic wrapper over Trace returning a range-able
// slice. Root → empty slice; Parent → one entry; Fanin → Count entries.
// Calls the core Trace underneath; carries identical information.
func (r SignedInvocationReceipt) TraceParents() []CausalParent {
	t := r.Trace()
	switch t.Kind {
	case TraceParent:
		return []CausalParent{{IsJoin: false, Ref: t.Parent}}
	case TraceFanin:
		out := make([]CausalParent, t.Count)
		for i := range out {
			out[i] = CausalParent{IsJoin: true}
		}
		return out
	default: // TraceRoot
		return nil
	}
}

// ProveAuthorityWith — idiomatic wrapper over ProveAuthority using
// functional options. Calls the core method underneath; identical result.
func (r SignedInvocationReceipt) ProveAuthorityWith(opts ...ResolverOption) (AuthorityProof, error) {
	return r.ProveAuthority(resolveConfig(opts))
}

// IsSelf reports whether the proof is a Self authority.
func (p AuthorityProof) IsSelf() bool { return p.Kind == ProofSelf }

// IsDelegated reports whether the proof is a DelegatedBy authority.
func (p AuthorityProof) IsDelegated() bool { return p.Kind == ProofDelegatedBy }

// IsSession reports whether the proof is a SessionOf authority.
func (p AuthorityProof) IsSession() bool { return p.Kind == ProofSessionOf }

// IsBootstrap reports whether the proof is a Bootstrap authority.
func (p AuthorityProof) IsBootstrap() bool { return p.Kind == ProofBootstrap }

// Principal returns the authority principal URA: the Self_ principal,
// or the authority URA for a DelegatedBy/SessionOf proof.
func (p AuthorityProof) Principal() string {
	if p.Kind == ProofSelf {
		return p.PrincipalURA
	}
	return p.AuthorityURA
}
