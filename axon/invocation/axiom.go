// Package axon implements the RFC 001 invocation axiom module.
//
// Go reference implementation of the axiom 7-tuple:
// caller, callee, ability, subject, nonce, causal_context, args → receipt.
//
// Cross-language parity constraint: the canonical-bytes encoders in
// this file produce byte-identical output to the Rust and Python
// reference encoders for every conformance vector in
// sdk/conformance/cases/axiom/*.json.
//
// Normative references:
//
//	document/concepts/AXIOM.tex
//	document/rfcs/001-envelope-axiom-alignment.md
//	URA v2 §3 canonicalisation
package axon

import (
	"crypto/ed25519"
	"crypto/rand"
	sha256pkg "crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// ── URA profile ──────────────────────────────────────────────────────

// UraProfile — URA profile selector. Bound structurally to the URA it
// profiles (AXIOM §5.4 Axis B, RFC 001 INV-4).
type UraProfile string

const (
	ProfileStrictV2  UraProfile = "axon-strict-v2"
	ProfileWebSafeV2 UraProfile = "web-safe-v2"
)

// ParseUraProfile parses a profile string or returns an error for
// unknown values.
func ParseUraProfile(s string) (UraProfile, error) {
	switch UraProfile(s) {
	case ProfileStrictV2, ProfileWebSafeV2:
		return UraProfile(s), nil
	default:
		return "", ErrInvalidArgument(fmt.Sprintf("unknown_ura_profile:%s", s))
	}
}

// ── Identity ─────────────────────────────────────────────────────────

// AgentIdentity — URA v2 composite identity for an agent. `Profile` is
// bound structurally to `URA` per RFC 001 §3.1.
type AgentIdentity struct {
	URA     string
	Profile UraProfile
}

// NewAgentIdentity constructs an AgentIdentity.
func NewAgentIdentity(ura string, profile UraProfile) AgentIdentity {
	return AgentIdentity{URA: ura, Profile: profile}
}

// SubjectIdentity — URA v2 composite identity for a subject. Separate
// from AgentIdentity so the compiler catches accidental swaps (INV-7).
type SubjectIdentity struct {
	URA     string
	Profile UraProfile
}

// NewSubjectIdentity constructs a SubjectIdentity.
func NewSubjectIdentity(ura string, profile UraProfile) SubjectIdentity {
	return SubjectIdentity{URA: ura, Profile: profile}
}

// SubjectFromCallee builds the explicit executor-as-target subject identity.
// Descriptor-bound request builders do not call this implicitly.
func SubjectFromCallee(callee AgentIdentity) SubjectIdentity {
	return SubjectIdentity{URA: callee.URA, Profile: callee.Profile}
}

type EntityRefKind uint8

const (
	EntityRefResource     EntityRefKind = 0x01
	EntityRefAgent        EntityRefKind = 0x02
	EntityRefAbility      EntityRefKind = 0x03
	EntityRefSession      EntityRefKind = 0x04
	EntityRefContinuation EntityRefKind = 0x05
	EntityRefStateObject  EntityRefKind = 0x06
	EntityRefDevice       EntityRefKind = 0x07
)

func (k EntityRefKind) String() string {
	switch k {
	case EntityRefResource:
		return "resource"
	case EntityRefAgent:
		return "agent"
	case EntityRefAbility:
		return "ability"
	case EntityRefSession:
		return "session"
	case EntityRefContinuation:
		return "continuation"
	case EntityRefStateObject:
		return "state_object"
	case EntityRefDevice:
		return "device"
	default:
		return "unspecified"
	}
}

func ParseEntityRefKind(s string) (EntityRefKind, error) {
	switch s {
	case "resource":
		return EntityRefResource, nil
	case "agent":
		return EntityRefAgent, nil
	case "ability":
		return EntityRefAbility, nil
	case "session":
		return EntityRefSession, nil
	case "continuation":
		return EntityRefContinuation, nil
	case "state_object":
		return EntityRefStateObject, nil
	case "device":
		return EntityRefDevice, nil
	default:
		return 0, ErrInvalidArgument(fmt.Sprintf("unknown_entity_ref_kind:%s", s))
	}
}

type EntityRef struct {
	Kind    EntityRefKind
	URA     string
	Profile UraProfile
}

type AbilityDescriptorRef struct {
	Raw             string
	AbilityURA      string
	Version         string
	DescriptorHash  [32]byte
	AdmissionAction string
}

const uraScheme = "easynet:///r/"

func entityRefKindForSubjectURA(ura string) (EntityRefKind, error) {
	raw := strings.TrimSpace(ura)
	if !strings.HasPrefix(raw, uraScheme) {
		return 0, ErrInvalidArgument("subject_ref_ura_parse_failed:bad_scheme")
	}
	parts := strings.Split(strings.TrimPrefix(raw, uraScheme), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return 0, ErrInvalidArgument("subject_ref_ura_parse_failed:missing_role")
	}
	switch parts[1] {
	case "agent", "agents", "service", "services":
		return EntityRefAgent, nil
	case "ability", "abilities":
		return EntityRefAbility, nil
	case "device", "devices":
		return EntityRefDevice, nil
	case "resource", "resources":
		return EntityRefResource, nil
	case "session", "sessions":
		return EntityRefSession, nil
	case "continuation", "continuations":
		return EntityRefContinuation, nil
	case "state_object", "state-object", "state_objects", "state-objects", "state", "states":
		return EntityRefStateObject, nil
	default:
		return 0, ErrInvalidArgument(fmt.Sprintf("subject_ref_kind_unsupported:%s", parts[1]))
	}
}

func EntityRefForSubject(subject SubjectIdentity) (EntityRef, error) {
	kind, err := entityRefKindForSubjectURA(subject.URA)
	if err != nil {
		return EntityRef{}, err
	}
	return EntityRef{Kind: kind, URA: subject.URA, Profile: subject.Profile}, nil
}

func ParseAbilityDescriptorRef(abilityRaw string) (AbilityDescriptorRef, error) {
	raw := strings.TrimSpace(abilityRaw)
	if raw == "" {
		return AbilityDescriptorRef{}, ErrInvalidArgument("ability_empty")
	}
	if strings.Count(raw, "@") != 1 {
		return AbilityDescriptorRef{}, ErrInvalidArgument("ability_descriptor_ref_malformed")
	}
	parts := strings.SplitN(raw, "@", 2)
	abilityURA := strings.TrimSpace(parts[0])
	versionAndDigest := strings.TrimSpace(parts[1])
	if abilityURA == "" {
		return AbilityDescriptorRef{}, ErrInvalidArgument("ability_descriptor_ref_missing_ability")
	}
	if err := requireAbilityURA(abilityURA); err != nil {
		return AbilityDescriptorRef{}, err
	}
	if strings.Count(versionAndDigest, "#") != 1 {
		return AbilityDescriptorRef{}, ErrInvalidArgument("ability_descriptor_ref_digest_missing_or_malformed")
	}
	versionParts := strings.SplitN(versionAndDigest, "#", 2)
	version := strings.TrimSpace(versionParts[0])
	digestAndAction := strings.TrimSpace(versionParts[1])
	if version == "" {
		return AbilityDescriptorRef{}, ErrInvalidArgument("ability_descriptor_ref_missing_version")
	}
	if strings.Count(digestAndAction, "!") != 1 {
		return AbilityDescriptorRef{}, ErrInvalidArgument("ability_descriptor_ref_action_missing_or_malformed")
	}
	digestParts := strings.SplitN(digestAndAction, "!", 2)
	digestHex := strings.TrimSpace(digestParts[0])
	action := strings.TrimSpace(digestParts[1])
	if len(digestHex) != 64 || !isASCIIHex(digestHex) {
		return AbilityDescriptorRef{}, ErrInvalidArgument("ability_descriptor_ref_digest_invalid")
	}
	decoded, err := hex.DecodeString(digestHex)
	if err != nil {
		return AbilityDescriptorRef{}, ErrInvalidArgument("ability_descriptor_ref_digest_not_hex")
	}
	var descriptorHash [32]byte
	copy(descriptorHash[:], decoded)
	if !isAdmissionAction(action) {
		return AbilityDescriptorRef{}, ErrInvalidArgument("ability_descriptor_ref_action_invalid")
	}
	canonicalDigest := strings.ToLower(digestHex)
	return AbilityDescriptorRef{
		Raw:             abilityURA + "@" + version + "#" + canonicalDigest + "!" + action,
		AbilityURA:      abilityURA,
		Version:         version,
		DescriptorHash:  descriptorHash,
		AdmissionAction: action,
	}, nil
}

func isASCIIHex(raw string) bool {
	for _, b := range []byte(raw) {
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			return false
		}
	}
	return true
}

func isAdmissionAction(action string) bool {
	switch action {
	case "invoke", "read", "manage", "grant", "stream":
		return true
	default:
		return false
	}
}

func requireAbilityURA(abilityURA string) error {
	raw := strings.TrimSpace(abilityURA)
	if !strings.HasPrefix(raw, "easynet:///r/") {
		return ErrInvalidArgument("ability_descriptor_ref_ura_parse_failed:bad_scheme")
	}
	parts := strings.Split(strings.TrimPrefix(raw, "easynet:///r/"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "ability" || parts[2] == "" {
		return ErrInvalidArgument("ability_descriptor_ref_not_ability_ura")
	}
	if !isAbilityTail(parts[2]) {
		return ErrInvalidArgument("ability_descriptor_ref_ura_parse_failed:ability_tail")
	}
	return nil
}

func isAbilityTail(tail string) bool {
	if tail == "" || strings.Contains(tail, "/") {
		return false
	}
	if rest, ok := strings.CutPrefix(tail, "authority."); ok {
		namespace, local, ok := strings.Cut(rest, ".")
		return ok && namespace != "" && local != ""
	}
	if rest, ok := strings.CutPrefix(tail, "device."); ok {
		deviceID, abilityName, ok := strings.Cut(rest, ".")
		return ok && deviceID != "" && abilityName != ""
	}
	userID, afterUser, ok := strings.Cut(tail, ".")
	if !ok || userID == "" || userID == "authority" || userID == "device" {
		return false
	}
	agentID, abilityName, ok := strings.Cut(afterUser, ".")
	return ok && agentID != "" && abilityName != ""
}

func CanonicalAbilityDescriptorRef(abilityRaw string) (string, error) {
	ref, err := ParseAbilityDescriptorRef(abilityRaw)
	if err != nil {
		return "", err
	}
	return ref.Raw, nil
}

func AbilityURAFromDescriptorRef(abilityRaw string) (string, error) {
	ref, err := ParseAbilityDescriptorRef(abilityRaw)
	if err != nil {
		return "", err
	}
	return ref.AbilityURA, nil
}

// ── Causal context ───────────────────────────────────────────────────

// ReceiptRef — reference to a prior receipt in the causal chain.
type ReceiptRef struct {
	ReceiptHash [32]byte
	ReceiptURA  string
}

// CausalForm identifies the branch of CausalContext.
type CausalForm uint8

const (
	CausalNone   CausalForm = 0x00
	CausalScalar CausalForm = 0x01
	CausalList   CausalForm = 0x02
	CausalMerkle CausalForm = 0x03
)

// CausalContext — tagged union: None / Scalar / List / Merkle.
//
// Go has no sum types; we use a discriminator + pointer fields. The
// canonical encoder reads Form first and then the matching field.
type CausalContext struct {
	Form           CausalForm
	Scalar         *ReceiptRef
	List           []ReceiptRef
	MerkleRoot     [32]byte
	MerkleProofURA string
}

// CausalNoneCtx returns the empty (root) causal context.
func CausalNoneCtx() CausalContext {
	return CausalContext{Form: CausalNone}
}

// CausalScalarCtx returns a scalar (linear-successor) causal context.
func CausalScalarCtx(r ReceiptRef) CausalContext {
	cp := r
	return CausalContext{Form: CausalScalar, Scalar: &cp}
}

// CausalListCtx returns a list (fan-in join) causal context. Order is
// preserved and binds into the canonical bytes.
func CausalListCtx(refs []ReceiptRef) CausalContext {
	cp := make([]ReceiptRef, len(refs))
	copy(cp, refs)
	return CausalContext{Form: CausalList, List: cp}
}

// CausalMerkleCtx returns a merkle-root causal context. Wire-reserved.
func CausalMerkleCtx(root [32]byte, proofURA string) CausalContext {
	return CausalContext{Form: CausalMerkle, MerkleRoot: root, MerkleProofURA: proofURA}
}

// ── Authority binding (RFC 001 §A14) ─────────────────────────────────
//
// RFC 001 amendment "AuthorityBinding: relation/evidence decomposition"
// (document/rfcs/001-authority-binding-relation-evidence.md). Mirrors
// the Rust AuthorityBinding/AuthorityRelation/AuthorityEvidence shape
// byte-for-byte and field-for-field. `authority` is NOT structurally
// pinned to a fixed envelope slot — which envelope identity/context
// participates is defined by `relation`, not by AuthorityBinding itself.

// AuthorityRelation — WHAT relationship binds `Authority` to this
// invocation. Orthogonal to AuthorityEvidenceKind (HOW the daemon
// believes the relation holds). Tag values are the canonical-tail
// discriminator bytes.
type AuthorityRelation uint8

const (
	// AuthorityRelationSelf — envelope.caller == authority. No
	// delegation, no session.
	AuthorityRelationSelf AuthorityRelation = 0x01
	// AuthorityRelationDelegatedBy — envelope.caller acts under a
	// signed grant from authority.
	AuthorityRelationDelegatedBy AuthorityRelation = 0x02
	// AuthorityRelationSessionOf — envelope.subject == authority: the
	// subject is admitted under a session whose accountable owner is
	// that same principal.
	AuthorityRelationSessionOf AuthorityRelation = 0x03
	// AuthorityRelationCredentialOf — RESERVED, inadmissible in v1. A
	// credential (e.g. API key) acts on behalf of authority, attested
	// by a trusted backend rather than signed. Not constructible via
	// any public API until the attestation-scope addendum lands.
	AuthorityRelationCredentialOf AuthorityRelation = 0x04
)

func (r AuthorityRelation) tag() byte { return byte(r) }

func (r AuthorityRelation) String() string {
	switch r {
	case AuthorityRelationSelf:
		return "self"
	case AuthorityRelationDelegatedBy:
		return "delegated_by"
	case AuthorityRelationSessionOf:
		return "session_of"
	case AuthorityRelationCredentialOf:
		return "credential_of"
	default:
		return "unspecified"
	}
}

// AuthorityEvidenceKind — HOW the daemon believes the `Relation` edge
// holds. Orthogonal to AuthorityRelation.
type AuthorityEvidenceKind uint8

const (
	// AuthorityEvidenceIdentity — zero-payload marker: the relation
	// itself (Self_) IS the proof, verified by equality against
	// envelope fields — nothing more to carry.
	AuthorityEvidenceIdentity   AuthorityEvidenceKind = 0x01
	AuthorityEvidenceDelegation AuthorityEvidenceKind = 0x02
	AuthorityEvidenceSession    AuthorityEvidenceKind = 0x03
	// AuthorityEvidenceAttestation — RESERVED, inadmissible in v1. See
	// AuthorityRelationCredentialOf.
	AuthorityEvidenceAttestation AuthorityEvidenceKind = 0x04
)

func (e AuthorityEvidenceKind) tag() byte { return byte(e) }

func (e AuthorityEvidenceKind) String() string {
	switch e {
	case AuthorityEvidenceIdentity:
		return "identity"
	case AuthorityEvidenceDelegation:
		return "delegation"
	case AuthorityEvidenceSession:
		return "session"
	case AuthorityEvidenceAttestation:
		return "attestation"
	default:
		return "unspecified"
	}
}

// DelegationEvidence — signed delegation grant. `Issuer` is who signed
// this grant — a DISTINCT role from AuthorityBinding.Authority (the
// accountable principal). `Issuer` MAY differ from `Authority`: the SDK
// proves the grant is authentically signed by `Issuer`, not that
// `Issuer` was entitled to vouch for `Authority` — that is realm-specific
// policy left to the caller/daemon (see RFC doc "Issuer authenticity vs.
// issuer authority"). `Audience` is singular (NOT the plural `Audiences`
// on SessionEvidence).
type DelegationEvidence struct {
	Issuer      AgentIdentity
	Scopes      []string
	Audience    string
	IssuedAtMs  int64
	ExpiresAtMs int64
	Signature   []byte
}

// SessionEvidence — issuer-signed interactive session authority.
// Deliberately separate from DelegationEvidence: the issuer signs it
// with the issuer key, so it proves runtime session authority rather
// than user-private-key delegation. `Issuer` here is checked against
// envelope.caller (who is presenting this session), NOT against
// Authority (the session's owner) — those are expected to differ in
// the normal case (a backend presents a session on behalf of its
// owning user).
type SessionEvidence struct {
	Issuer      AgentIdentity
	SessionID   string
	Scopes      []string
	Audiences   []string
	IssuedAtMs  int64
	ExpiresAtMs int64
	Signature   []byte
}

// AuthorityBinding — RFC 001 §A14 relation/evidence pair. WHO had
// authority over the invocation and HOW that relation is evidenced. Go
// has no sum types; we use a discriminator (EvidenceKind) + per-arm
// fields, mirroring the CausalContext/CausalForm convention already
// used in this file. The canonical encoder reads EvidenceKind first,
// then the matching field.
//
// There is no field for Identity (zero-payload) or Attestation
// (reserved, unconstructible — no public constructor exists for
// AuthorityRelationCredentialOf).
type AuthorityBinding struct {
	Authority    AgentIdentity
	Relation     AuthorityRelation
	EvidenceKind AuthorityEvidenceKind
	Delegation   *DelegationEvidence // set when EvidenceKind == AuthorityEvidenceDelegation
	Session      *SessionEvidence    // set when EvidenceKind == AuthorityEvidenceSession
}

// Form returns the stable query projection "<relation>+<evidence>",
// e.g. "self+identity", "delegated_by+delegation".
func (a AuthorityBinding) Form() string {
	return a.Relation.String() + "+" + a.EvidenceKind.String()
}

// BootstrapBinding — admission-plane fact: an unknown/untrusted
// identity is being admitted on the strength of its own presented key,
// not an invocation-time authority relationship. Deliberately OUTSIDE
// AuthorityBinding — see document/rfcs/001-authority-binding-relation-
// evidence.md: Bootstrap answers "how did this identity get admitted
// at all", a different plane than "on whose authority does an
// already-admitted caller act".
type BootstrapBinding struct {
	PrincipalURA string
	Realm        string
	Ability      string
}

// AuthorityOrBootstrap — Go equivalent of the Rust AuthorityOrBootstrap
// wrapper enum. The proof envelope's binding slot carries either an
// invocation-time authority relationship or an admission-plane
// bootstrap fact — never both, never neither, when a binding is
// present at all. Discriminated with a bool flag, mirroring the
// discriminator+pointer-fields convention this file already uses for
// CausalContext.
type AuthorityOrBootstrap struct {
	IsBootstrap bool
	Binding     *AuthorityBinding
	Bootstrap   *BootstrapBinding
}

// AuthorityOrBootstrapFromBinding wraps an AuthorityBinding.
func AuthorityOrBootstrapFromBinding(binding AuthorityBinding) AuthorityOrBootstrap {
	cp := binding
	return AuthorityOrBootstrap{Binding: &cp}
}

// AuthorityOrBootstrapFromBootstrap wraps a BootstrapBinding.
func AuthorityOrBootstrapFromBootstrap(bootstrap BootstrapBinding) AuthorityOrBootstrap {
	cp := bootstrap
	return AuthorityOrBootstrap{IsBootstrap: true, Bootstrap: &cp}
}

// Form returns the stable query projection: the wrapped binding's
// Form(), or "bootstrap".
func (a AuthorityOrBootstrap) Form() string {
	if a.IsBootstrap {
		return "bootstrap"
	}
	if a.Binding == nil {
		return "unspecified"
	}
	return a.Binding.Form()
}

// SelfAuthority constructs the Self_+Identity binding (caller is the
// authority principal).
func SelfAuthority(principalURA string) AuthorityBinding {
	return AuthorityBinding{
		Authority:    AgentIdentity{URA: principalURA, Profile: ProfileStrictV2},
		Relation:     AuthorityRelationSelf,
		EvidenceKind: AuthorityEvidenceIdentity,
	}
}

// DelegatedAuthority constructs the DelegatedBy+Delegation binding.
// `authority` is the principal whose authority the caller exercises;
// `evidence` carries the issuer's signed grant.
func DelegatedAuthority(authority AgentIdentity, evidence DelegationEvidence) AuthorityBinding {
	cp := evidence
	cp.Scopes = append([]string(nil), evidence.Scopes...)
	cp.Signature = append([]byte(nil), evidence.Signature...)
	return AuthorityBinding{
		Authority:    authority,
		Relation:     AuthorityRelationDelegatedBy,
		EvidenceKind: AuthorityEvidenceDelegation,
		Delegation:   &cp,
	}
}

// SessionAuthority constructs the SessionOf+Session binding. `authority`
// is the session's accountable owner (checked against envelope.subject
// at verification time); `evidence` carries the issuer's signed session
// proof (issuer is checked against envelope.caller).
func SessionAuthority(authority AgentIdentity, evidence SessionEvidence) AuthorityBinding {
	cp := evidence
	cp.Scopes = append([]string(nil), evidence.Scopes...)
	cp.Audiences = append([]string(nil), evidence.Audiences...)
	cp.Signature = append([]byte(nil), evidence.Signature...)
	return AuthorityBinding{
		Authority:    authority,
		Relation:     AuthorityRelationSessionOf,
		EvidenceKind: AuthorityEvidenceSession,
		Session:      &cp,
	}
}

// BootstrapAuthority constructs the admission-plane BootstrapBinding
// fact (outside AuthorityBinding).
func BootstrapAuthority(principalURA, realm, ability string) BootstrapBinding {
	return BootstrapBinding{PrincipalURA: principalURA, Realm: realm, Ability: ability}
}

// putEvidencePayload appends the evidence-specific payload, optionally
// including the signature. Shared by CanonicalAuthorityBytes
// (includeSignature = true) and the claim-bytes encoders used for
// signature verification (includeSignature = false — a signature
// cannot cover itself). Mirrors the Rust put_evidence_payload.
func putEvidencePayload(out *[]byte, a AuthorityBinding, includeSignature bool) {
	switch a.EvidenceKind {
	case AuthorityEvidenceIdentity:
		// zero bytes
	case AuthorityEvidenceDelegation:
		d := a.Delegation
		if d == nil {
			d = &DelegationEvidence{}
		}
		putIdentity(out, d.Issuer.URA, d.Issuer.Profile)
		putU32(out, uint32(len(d.Scopes)))
		for _, s := range d.Scopes {
			putStr(out, s)
		}
		putStr(out, d.Audience)
		var ia [8]byte
		binary.BigEndian.PutUint64(ia[:], uint64(d.IssuedAtMs))
		*out = append(*out, ia[:]...)
		var ea [8]byte
		binary.BigEndian.PutUint64(ea[:], uint64(d.ExpiresAtMs))
		*out = append(*out, ea[:]...)
		if includeSignature {
			putBytes(out, d.Signature)
		}
	case AuthorityEvidenceSession:
		s := a.Session
		if s == nil {
			s = &SessionEvidence{}
		}
		putIdentity(out, s.Issuer.URA, s.Issuer.Profile)
		putStr(out, s.SessionID)
		putU32(out, uint32(len(s.Scopes)))
		for _, scope := range s.Scopes {
			putStr(out, scope)
		}
		putU32(out, uint32(len(s.Audiences)))
		for _, audience := range s.Audiences {
			putStr(out, audience)
		}
		var ia [8]byte
		binary.BigEndian.PutUint64(ia[:], uint64(s.IssuedAtMs))
		*out = append(*out, ia[:]...)
		var ea [8]byte
		binary.BigEndian.PutUint64(ea[:], uint64(s.ExpiresAtMs))
		*out = append(*out, ea[:]...)
		if includeSignature {
			putBytes(out, s.Signature)
		}
	case AuthorityEvidenceAttestation:
		// Unreachable in v1: CredentialOf+Attestation is not
		// constructible via any public API. If this is ever hit it is
		// a protocol bug, not a valid encoding to silently produce.
		panic("authority_evidence_attestation_reserved_and_not_encodable_in_v1")
	default:
		panic("receipt_authority_binding_required")
	}
}

// CanonicalAuthorityBytes returns the §A14 authority-tail bytes for an
// AuthorityBinding. Mirrors the Rust canonical_authority_bytes
// byte-for-byte: put_identity_ura_profile(authority), relation tag (1
// byte), evidence tag (1 byte), then the evidence-specific payload
// (including the signature, where the evidence carries one). NOT the
// same bytes a signer signs — see CanonicalDelegationClaimBytes /
// CanonicalSessionClaimBytes for the unsigned claim payload.
func CanonicalAuthorityBytes(a AuthorityBinding) []byte {
	out := make([]byte, 0, 64)
	putIdentity(&out, a.Authority.URA, a.Authority.Profile)
	out = append(out, a.Relation.tag())
	out = append(out, a.EvidenceKind.tag())
	putEvidencePayload(&out, a, true)
	return out
}

// CanonicalBootstrapBytes returns the canonical bytes for the
// admission-plane BootstrapBinding fact. Deliberately a SEPARATE
// encoder from CanonicalAuthorityBytes — Bootstrap is not an
// AuthorityBinding arm. Tag 0x06 is preserved from the pre-redesign
// wire form so downstream admission code that already special-cases
// Bootstrap does not need to renumber.
func CanonicalBootstrapBytes(b BootstrapBinding) []byte {
	out := make([]byte, 0, 32)
	out = append(out, 0x06)
	putStr(&out, b.PrincipalURA)
	putStr(&out, b.Realm)
	putStr(&out, b.Ability)
	return out
}

// CanonicalDelegationClaimBytes returns the unsigned claim bytes a
// delegation issuer signs. Identical to the evidence payload inside
// CanonicalAuthorityBytes MINUS the signature, and WITH `delegatee`
// (envelope.caller) bound into the signed material — the claim has no
// stored delegatee field, so the caller must still be part of what was
// signed or a proof issued for one caller could be replayed by another.
func CanonicalDelegationClaimBytes(authority, delegatee AgentIdentity, evidence DelegationEvidence) []byte {
	out := make([]byte, 0, 64)
	putIdentity(&out, authority.URA, authority.Profile)
	putIdentity(&out, delegatee.URA, delegatee.Profile)
	binding := AuthorityBinding{EvidenceKind: AuthorityEvidenceDelegation, Delegation: &evidence}
	putEvidencePayload(&out, binding, false)
	return out
}

// CanonicalSessionClaimBytes returns the unsigned claim bytes a session
// issuer signs. Identical to the evidence payload inside
// CanonicalAuthorityBytes MINUS the signature. `authority` is bound
// into the signed material (it equals envelope.subject at verification
// time — see the compatibility matrix — but the claim still signs over
// it explicitly rather than relying on an unsigned cross-check).
func CanonicalSessionClaimBytes(authority AgentIdentity, evidence SessionEvidence) []byte {
	out := make([]byte, 0, 64)
	putIdentity(&out, authority.URA, authority.Profile)
	binding := AuthorityBinding{EvidenceKind: AuthorityEvidenceSession, Session: &evidence}
	putEvidencePayload(&out, binding, false)
	return out
}

// CanonicalAuthorityOrBootstrapBytes returns the canonical bytes for
// the proof envelope's binding slot, whichever plane it carries.
// Delegates to CanonicalAuthorityBytes / CanonicalBootstrapBytes — no
// separate encoding logic.
func CanonicalAuthorityOrBootstrapBytes(binding AuthorityOrBootstrap) []byte {
	if binding.IsBootstrap {
		b := binding.Bootstrap
		if b == nil {
			b = &BootstrapBinding{}
		}
		return CanonicalBootstrapBytes(*b)
	}
	a := binding.Binding
	if a == nil {
		a = &AuthorityBinding{}
	}
	return CanonicalAuthorityBytes(*a)
}

func AuthorityBindingProofHash(binding AuthorityBinding) [32]byte {
	return Sha256(CanonicalAuthorityBytes(binding))
}

func BootstrapBindingProofHash(binding BootstrapBinding) [32]byte {
	return Sha256(CanonicalBootstrapBytes(binding))
}

func AuthorityOrBootstrapProofHash(binding AuthorityOrBootstrap) [32]byte {
	return Sha256(CanonicalAuthorityOrBootstrapBytes(binding))
}

func AuthorityProofExpectedHash(proof InvocationAuthorityProof) [32]byte {
	if len(proof.ProofPayload) > 0 {
		return Sha256(proof.ProofPayload)
	}
	if proof.Binding != nil {
		return AuthorityOrBootstrapProofHash(*proof.Binding)
	}
	return [32]byte{}
}

// ValidateAuthorityProofHash verifies provider-supplied proof hash evidence.
// Missing hashes are invalid and are never filled by the runtime.
func ValidateAuthorityProofHash(proof InvocationAuthorityProof) error {
	expected := AuthorityProofExpectedHash(proof)
	if expected != zeroHash32 && proof.ProofHash != zeroHash32 && proof.ProofHash == expected {
		return nil
	}
	return ErrInvalidArgument("authority_proof_hash_invalid")
}

// ── Signatures ───────────────────────────────────────────────────────

// CallerSignature — caller-signed blob over canonical invocation bytes.
type CallerSignature struct {
	Algorithm string
	Signature []byte
	KeyIDHint string
}

// CalleeSignature — callee-signed blob over canonical receipt bytes.
type CalleeSignature struct {
	Algorithm string
	Signature []byte
	KeyIDHint string
}

// ── Envelope / Receipt bodies ────────────────────────────────────────

// InvocationEnvelope — the caller-supplied 7-tuple ready for canonical
// encoding and signing.
type InvocationEnvelope struct {
	Caller          AgentIdentity
	Callee          AgentIdentity
	Subject         SubjectIdentity
	Ability         string
	ArgsDigest      [32]byte
	InvocationNonce [16]byte
	CausalContext   CausalContext
}

type DescriptorBoundEnvelope struct {
	envelope InvocationEnvelope
}

func NewDescriptorBoundEnvelope(env InvocationEnvelope) (DescriptorBoundEnvelope, error) {
	if _, err := CanonicalAbilityDescriptorRef(env.Ability); err != nil {
		return DescriptorBoundEnvelope{}, err
	}
	if _, err := EntityRefForSubject(env.Subject); err != nil {
		return DescriptorBoundEnvelope{}, err
	}
	return DescriptorBoundEnvelope{envelope: env}, nil
}

func (e DescriptorBoundEnvelope) Envelope() InvocationEnvelope {
	return e.envelope
}

// InvocationUsage is per-invocation consumption signed into receipt
// canonical bytes. The zero value is explicit protocol data, not absence.
type InvocationUsage struct {
	TokensIn      uint64
	TokensOut     uint64
	DurationMs    uint64
	ExternalCalls uint32
}

// ReceiptBody — a receipt ready for canonical encoding and signing by
// the callee.
type ReceiptBody struct {
	Index           uint64
	InvocationID    string
	ReceiptType     string
	State           string
	TimestampUnixMs int64
	PrevReceiptHash [32]byte
	PayloadDigest   [32]byte
	Reason          string
	CleanupComplete bool
	CallerBinding   AgentIdentity
	CalleeBinding   AgentIdentity
	SubjectBinding  SubjectIdentity
	InvocationNonce [16]byte
	CausalBinding   CausalContext
	// AbilityBinding — RFC 001 §A14: exact runtime-admitted ability.
	// Delegation scopes are matched against this signed value.
	AbilityBinding string
	// AuthorityBinding — RFC 001 §A14: WHO had authority over the
	// invocation, or the admission-plane Bootstrap fact that admitted
	// this caller. Hashed into the signed region AFTER the §A12 hosted
	// tail; always present.
	AuthorityBinding AuthorityOrBootstrap
	// Usage is the seven-axes signed usage tail, always encoded after
	// §A14 authority. The zero value means the invocation consumed nothing.
	Usage InvocationUsage
	// ProofFacts binds descriptor/runtime/authority facts after usage.
	ProofFacts ReceiptProofFacts
}

type InvocationAuthorityProof struct {
	ProofType     string
	Binding       *AuthorityOrBootstrap
	ProofPayload  []byte
	ProofHash     [32]byte
	Issuer        *AgentIdentity
	Signature     *CalleeSignature
	AdmissionHook string
}

type ReceiptProofFacts struct {
	SubjectRef        *EntityRef
	DescriptorVersion string
	SchemaHash        [32]byte
	ImplHash          [32]byte
	RuntimeEnv        string
	AuthorityProof    InvocationAuthorityProof
	InputHash         [32]byte
	OutputHash        [32]byte
	ParentReceipts    []ReceiptRef
	complete          bool
}

func NewReceiptProofFacts(subjectRef *EntityRef, descriptorVersion string, schemaHash [32]byte, implHash [32]byte, runtimeEnv string, authorityProof InvocationAuthorityProof, inputHash [32]byte, outputHash [32]byte, parentReceipts []ReceiptRef) ReceiptProofFacts {
	facts, err := TryNewReceiptProofFacts(
		subjectRef,
		descriptorVersion,
		schemaHash,
		implHash,
		runtimeEnv,
		authorityProof,
		inputHash,
		outputHash,
		parentReceipts,
	)
	if err != nil {
		panic(err)
	}
	return facts
}

func TryNewReceiptProofFacts(subjectRef *EntityRef, descriptorVersion string, schemaHash [32]byte, implHash [32]byte, runtimeEnv string, authorityProof InvocationAuthorityProof, inputHash [32]byte, outputHash [32]byte, parentReceipts []ReceiptRef) (ReceiptProofFacts, error) {
	parents := append([]ReceiptRef(nil), parentReceipts...)
	facts := ReceiptProofFacts{
		SubjectRef:        subjectRef,
		DescriptorVersion: descriptorVersion,
		SchemaHash:        schemaHash,
		ImplHash:          implHash,
		RuntimeEnv:        runtimeEnv,
		AuthorityProof:    authorityProof,
		InputHash:         inputHash,
		OutputHash:        outputHash,
		ParentReceipts:    parents,
	}
	if err := validateReceiptProofFactsSemantics(facts); err != nil {
		return ReceiptProofFacts{}, err
	}
	facts.complete = true
	return facts, nil
}

func validateReceiptProofFactsSemantics(facts ReceiptProofFacts) error {
	if facts.SubjectRef == nil || strings.TrimSpace(facts.SubjectRef.URA) == "" {
		return ErrInvalidArgument("subject_ref_required")
	}
	if strings.TrimSpace(facts.DescriptorVersion) == "" {
		return ErrInvalidArgument("descriptor_version_required")
	}
	if strings.TrimSpace(facts.RuntimeEnv) == "" {
		return ErrInvalidArgument("runtime_env_required")
	}
	for _, field := range []struct {
		name string
		hash [32]byte
	}{
		{name: "schema_hash", hash: facts.SchemaHash},
		{name: "impl_hash", hash: facts.ImplHash},
		{name: "input_hash", hash: facts.InputHash},
		{name: "output_hash", hash: facts.OutputHash},
	} {
		if field.hash == ([32]byte{}) {
			return ErrInvalidArgument(field.name + "_semantic_zero")
		}
	}
	if err := validateCompleteAuthorityProof(facts.AuthorityProof); err != nil {
		return err
	}
	for _, parent := range facts.ParentReceipts {
		if parent.ReceiptHash == ([32]byte{}) || strings.TrimSpace(parent.ReceiptURA) == "" {
			return ErrInvalidArgument("parent_receipt_semantically_invalid")
		}
	}
	return nil
}

func validateCompleteAuthorityProof(proof InvocationAuthorityProof) error {
	if strings.TrimSpace(proof.ProofType) == "" {
		return ErrInvalidArgument("authority_proof_type_required")
	}
	if proof.Binding == nil {
		return ErrInvalidArgument("authority_proof_binding_required")
	}
	if err := validateCompleteAuthorityOrBootstrap(*proof.Binding); err != nil {
		return err
	}
	if proof.Issuer == nil || strings.TrimSpace(proof.Issuer.URA) == "" {
		return ErrInvalidArgument("authority_proof_issuer_required")
	}
	if strings.TrimSpace(proof.AdmissionHook) == "" {
		return ErrInvalidArgument("authority_proof_admission_hook_required")
	}
	if proof.ProofHash == ([32]byte{}) {
		return ErrInvalidArgument("authority_proof_hash_semantic_zero")
	}
	if proof.ProofHash != AuthorityProofExpectedHash(proof) {
		return ErrInvalidArgument("authority_proof_hash_mismatch")
	}
	return nil
}

// nonemptyTrimmedStrings reports whether every string in values is
// non-empty after trimming, and the slice itself is non-empty.
func nonemptyTrimmedStrings(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

// validateCompleteAuthorityOrBootstrap dispatches structural
// completeness validation to whichever plane the binding carries.
// Mirrors the Rust AuthorityOrBootstrap::validate_complete.
func validateCompleteAuthorityOrBootstrap(binding AuthorityOrBootstrap) error {
	if binding.IsBootstrap {
		if binding.Bootstrap == nil {
			return ErrInvalidArgument("authority_bootstrap_incomplete")
		}
		return validateCompleteBootstrapBinding(*binding.Bootstrap)
	}
	if binding.Binding == nil {
		return ErrInvalidArgument("authority_proof_binding_required")
	}
	return validateCompleteAuthorityBinding(*binding.Binding)
}

func validateCompleteBootstrapBinding(bootstrap BootstrapBinding) error {
	if strings.TrimSpace(bootstrap.PrincipalURA) == "" ||
		strings.TrimSpace(bootstrap.Realm) == "" ||
		strings.TrimSpace(bootstrap.Ability) == "" {
		return ErrInvalidArgument("authority_bootstrap_incomplete")
	}
	return nil
}

// validateCompleteAuthorityBinding is the shallow structural
// completeness gate for AuthorityBinding — mirrors the Rust
// AuthorityBinding::validate_complete's compatibility-matrix
// structural checks (non-empty fields), NOT the deep semantic checks
// (signature verification, expiry, scope/audience matching), which
// live in ProveAuthority.
func validateCompleteAuthorityBinding(binding AuthorityBinding) error {
	if strings.TrimSpace(binding.Authority.URA) == "" {
		return ErrInvalidArgument("authority_principal_required")
	}
	switch {
	case binding.Relation == AuthorityRelationSelf && binding.EvidenceKind == AuthorityEvidenceIdentity:
		return nil
	case binding.Relation == AuthorityRelationDelegatedBy && binding.EvidenceKind == AuthorityEvidenceDelegation:
		evidence := binding.Delegation
		if evidence == nil ||
			strings.TrimSpace(evidence.Issuer.URA) == "" ||
			strings.TrimSpace(evidence.Audience) == "" ||
			!nonemptyTrimmedStrings(evidence.Scopes) ||
			len(evidence.Signature) == 0 {
			return ErrInvalidArgument("authority_delegation_incomplete")
		}
		return nil
	case binding.Relation == AuthorityRelationSessionOf && binding.EvidenceKind == AuthorityEvidenceSession:
		evidence := binding.Session
		if evidence == nil ||
			strings.TrimSpace(evidence.Issuer.URA) == "" ||
			strings.TrimSpace(evidence.SessionID) == "" ||
			!nonemptyTrimmedStrings(evidence.Scopes) ||
			!nonemptyTrimmedStrings(evidence.Audiences) ||
			len(evidence.Signature) == 0 {
			return ErrInvalidArgument("authority_session_incomplete")
		}
		return nil
	case binding.Relation == AuthorityRelationCredentialOf && binding.EvidenceKind == AuthorityEvidenceAttestation:
		return ErrInvalidArgument("authority_credential_of_reserved")
	default:
		return ErrInvalidArgument("authority_relation_evidence_mismatch")
	}
}

// HostedAttestation — RFC 001 §A12 hosted-Agent attestation payload.
// Present only when the receipt was signed by a host on behalf of a
// hosted callee. SignerBinding is the host's identity (whose key
// verifies the signature); HostAttestation is that key's signature over
// the canonical host-attestation bytes.
type HostedAttestation struct {
	SignerBinding   AgentIdentity
	HostAttestation []byte
}

// ── Canonical bytes encoders (RFC 001 §4) ────────────────────────────
//
// Every variable-length block is prefixed by a 4-byte big-endian length.
// Fixed-length blocks (hashes, nonce) are emitted as-is. This MUST match
// the Rust and Python descriptor-bound invocation and receipt encoders.

func putU32(out *[]byte, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	*out = append(*out, b[:]...)
}

func putStr(out *[]byte, s string) {
	b := []byte(s)
	putU32(out, uint32(len(b)))
	*out = append(*out, b...)
}

func putBytes(out *[]byte, b []byte) {
	putU32(out, uint32(len(b)))
	*out = append(*out, b...)
}

func putIdentity(out *[]byte, ura string, profile UraProfile) {
	putStr(out, ura)
	putStr(out, string(profile))
}

func CanonicalEntityRef(entity EntityRef) []byte {
	out := make([]byte, 0, 64+len(entity.URA))
	out = append(out, byte(entity.Kind))
	putStr(&out, entity.URA)
	putStr(&out, string(entity.Profile))
	return out
}

// CanonicalCausalContext returns the byte encoding per RFC 001 §4.2.
//
// One-byte discriminator followed by form-specific bytes. Every
// receipt in this pre-release protocol carries an axiom binding, so
// there is one canonical encoding — produced here — and one only.
func CanonicalCausalContext(c CausalContext) []byte {
	out := make([]byte, 0, 64)
	switch c.Form {
	case CausalNone:
		out = append(out, 0x00)
	case CausalScalar:
		if c.Scalar == nil {
			return out
		}
		out = append(out, 0x01)
		out = append(out, c.Scalar.ReceiptHash[:]...)
		putStr(&out, c.Scalar.ReceiptURA)
	case CausalList:
		out = append(out, 0x02)
		putU32(&out, uint32(len(c.List)))
		for _, r := range c.List {
			out = append(out, r.ReceiptHash[:]...)
			putStr(&out, r.ReceiptURA)
		}
	case CausalMerkle:
		out = append(out, 0x03)
		out = append(out, c.MerkleRoot[:]...)
		putStr(&out, c.MerkleProofURA)
	}
	return out
}

func CanonicalAuthorityProofBytes(proof InvocationAuthorityProof) []byte {
	out := make([]byte, 0, 64)
	putStr(&out, proof.ProofType)
	if proof.Binding == nil {
		out = append(out, 0x00)
	} else {
		out = append(out, 0x01)
		putBytes(&out, CanonicalAuthorityOrBootstrapBytes(*proof.Binding))
	}
	putBytes(&out, proof.ProofPayload)
	out = append(out, proof.ProofHash[:]...)
	if proof.Issuer == nil {
		out = append(out, 0x00)
	} else {
		out = append(out, 0x01)
		putIdentity(&out, proof.Issuer.URA, proof.Issuer.Profile)
	}
	if proof.Signature == nil {
		out = append(out, 0x00)
	} else {
		out = append(out, 0x01)
		putStr(&out, proof.Signature.Algorithm)
		putBytes(&out, proof.Signature.Signature)
		putStr(&out, proof.Signature.KeyIDHint)
	}
	putStr(&out, proof.AdmissionHook)
	return out
}

func TryCanonicalReceiptProofFacts(facts ReceiptProofFacts) ([]byte, error) {
	if !facts.complete {
		return nil, ErrInvalidArgument("receipt_proof_facts_required")
	}
	if err := validateReceiptProofFactsSemantics(facts); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 192)
	if facts.SubjectRef == nil {
		out = append(out, 0x00)
	} else {
		out = append(out, 0x01)
		putBytes(&out, CanonicalEntityRef(*facts.SubjectRef))
	}
	putStr(&out, facts.DescriptorVersion)
	out = append(out, facts.SchemaHash[:]...)
	out = append(out, facts.ImplHash[:]...)
	putStr(&out, facts.RuntimeEnv)
	putBytes(&out, CanonicalAuthorityProofBytes(facts.AuthorityProof))
	out = append(out, facts.InputHash[:]...)
	out = append(out, facts.OutputHash[:]...)
	putU32(&out, uint32(len(facts.ParentReceipts)))
	for _, parent := range facts.ParentReceipts {
		out = append(out, parent.ReceiptHash[:]...)
		putStr(&out, parent.ReceiptURA)
	}
	return out, nil
}

func CanonicalReceiptProofFacts(facts ReceiptProofFacts) []byte {
	out, err := TryCanonicalReceiptProofFacts(facts)
	if err != nil {
		panic(err)
	}
	return out
}

func canonicalDescriptorBoundInvocationBytes(env DescriptorBoundEnvelope) ([]byte, error) {
	out := make([]byte, 0, 576)
	raw := env.Envelope()
	putIdentity(&out, raw.Caller.URA, raw.Caller.Profile)
	putIdentity(&out, raw.Callee.URA, raw.Callee.Profile)
	subjectRef, err := EntityRefForSubject(raw.Subject)
	if err != nil {
		return nil, err
	}
	putBytes(&out, CanonicalEntityRef(subjectRef))
	abilityRef, err := CanonicalAbilityDescriptorRef(raw.Ability)
	if err != nil {
		return nil, err
	}
	putStr(&out, abilityRef)
	out = append(out, raw.ArgsDigest[:]...)
	out = append(out, raw.InvocationNonce[:]...)
	causal := CanonicalCausalContext(raw.CausalContext)
	putBytes(&out, causal)
	return out, nil
}

// CanonicalHostAttestationBytes — bytes covering the host → callee
// attestation (RFC 001 §A12). Layout: fixed version tag, then
// length-prefixed callee URA + signer URA. Mirrors the Rust encoder.
func CanonicalHostAttestationBytes(calleeURA, signerURA string) []byte {
	out := make([]byte, 0, 64+len(calleeURA)+len(signerURA))
	out = append(out, []byte("host-attest:v1\n")...)
	putStr(&out, calleeURA)
	putStr(&out, signerURA)
	return out
}

type hostedReceiptURA struct {
	realm string
	kind  string
}

func parseHostedReceiptURA(raw, label string) (hostedReceiptURA, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "easynet:///r/") {
		return hostedReceiptURA{}, ErrInvalidArgument(label + "_ura_invalid:bad_scheme")
	}
	body := strings.TrimPrefix(trimmed, "easynet:///r/")
	realm, afterRealm, ok := strings.Cut(body, "/")
	if !ok || realm == "" {
		return hostedReceiptURA{}, ErrInvalidArgument(label + "_ura_invalid:missing_realm")
	}
	role, _, _ := strings.Cut(afterRealm, "/")
	if role == "" {
		return hostedReceiptURA{}, ErrInvalidArgument(label + "_ura_invalid:missing_role")
	}
	return hostedReceiptURA{realm: realm, kind: role}, nil
}

func ValidateHostedAttestationAuthority(calleeURA, signerURA string) error {
	callee, err := parseHostedReceiptURA(calleeURA, "hosted_callee")
	if err != nil {
		return err
	}
	signer, err := parseHostedReceiptURA(signerURA, "hosted_signer")
	if err != nil {
		return err
	}
	if callee.kind != "agent" && callee.kind != "service" {
		return ErrInvalidArgument("hosted_callee_must_be_agent_or_service")
	}
	if signer.kind != "device" {
		return ErrInvalidArgument("hosted_signer_must_be_device")
	}
	if callee.realm != signer.realm {
		return ErrInvalidArgument("hosted_signer_callee_realm_mismatch")
	}
	return nil
}

func VerifyHostAttestation(calleeURA, signerURA string, hostAttestation []byte, signerKey ed25519.PublicKey) error {
	if err := ValidateHostedAttestationAuthority(calleeURA, signerURA); err != nil {
		return err
	}
	if len(hostAttestation) != ed25519.SignatureSize {
		return ErrInvalidArgument(fmt.Sprintf("host_attestation_wrong_length:expected_%d_got_%d", ed25519.SignatureSize, len(hostAttestation)))
	}
	if !ed25519.Verify(signerKey, CanonicalHostAttestationBytes(calleeURA, signerURA), hostAttestation) {
		return ErrInvalidArgument("host_attestation_invalid")
	}
	return nil
}

// CanonicalReceiptBytes — input to the callee's signature (RFC 001 §4.3).
// Excludes callee_signature and self_hash. Self-signed form: the §A12
// hosted tail is a single 0x00 marker.
func CanonicalReceiptBytes(r ReceiptBody) []byte {
	return CanonicalReceiptBytesWithHosted(r, nil)
}

// CanonicalReceiptBytesWithHosted — canonical receipt bytes with the
// §A12 hosted-Agent tail. Self-signed receipts (hosted == nil) emit a
// single 0x00 discriminator; hosted receipts emit 0x01, the signer
// identity, and the host-attestation bytes. The §A14 authority tail is
// ALWAYS appended at the very end, AFTER the hosted tail. Mirrors the
// Rust canonical_receipt_bytes_with_hosted byte-for-byte.
func CanonicalReceiptBytesWithHosted(r ReceiptBody, hosted *HostedAttestation) []byte {
	out := make([]byte, 0, 512)
	var idx [8]byte
	binary.BigEndian.PutUint64(idx[:], r.Index)
	out = append(out, idx[:]...)
	putStr(&out, r.InvocationID)
	putStr(&out, r.ReceiptType)
	putStr(&out, r.State)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(r.TimestampUnixMs))
	out = append(out, ts[:]...)
	out = append(out, r.PrevReceiptHash[:]...)
	putIdentity(&out, r.CallerBinding.URA, r.CallerBinding.Profile)
	putIdentity(&out, r.CalleeBinding.URA, r.CalleeBinding.Profile)
	putIdentity(&out, r.SubjectBinding.URA, r.SubjectBinding.Profile)
	out = append(out, r.InvocationNonce[:]...)
	causal := CanonicalCausalContext(r.CausalBinding)
	putBytes(&out, causal)
	out = append(out, r.PayloadDigest[:]...)
	putStr(&out, r.Reason)
	if r.CleanupComplete {
		out = append(out, 1)
	} else {
		out = append(out, 0)
	}
	// §A12 hosted-Agent attestation tail.
	if hosted == nil {
		out = append(out, 0x00)
	} else {
		out = append(out, 0x01)
		putIdentity(&out, hosted.SignerBinding.URA, hosted.SignerBinding.Profile)
		putBytes(&out, hosted.HostAttestation)
	}
	putStr(&out, r.AbilityBinding)
	// §A14 authority tail — ALWAYS present, AFTER the §A12 hosted+ability tail.
	if err := validateAuthorityBinding(
		r.CallerBinding,
		r.SubjectBinding,
		r.AbilityBinding,
		r.AuthorityBinding,
	); err != nil {
		panic("receipt_authority_binding_required")
	}
	proofFacts, err := TryCanonicalReceiptProofFacts(r.ProofFacts)
	if err != nil {
		panic(err)
	}
	out = append(out, CanonicalAuthorityOrBootstrapBytes(r.AuthorityBinding)...)
	var usage [28]byte
	binary.BigEndian.PutUint64(usage[0:8], r.Usage.TokensIn)
	binary.BigEndian.PutUint64(usage[8:16], r.Usage.TokensOut)
	binary.BigEndian.PutUint64(usage[16:24], r.Usage.DurationMs)
	binary.BigEndian.PutUint32(usage[24:28], r.Usage.ExternalCalls)
	out = append(out, usage[:]...)
	putBytes(&out, proofFacts)
	return out
}

// Sha256 returns the SHA-256 digest — the hash pinned throughout RFC 001.
func Sha256(b []byte) [32]byte {
	return sha256pkg.Sum256(b)
}

// ── Key resolver (§5.4) ──────────────────────────────────────────────

// KeyResolver — pluggable agent_ura → Ed25519 public key lookup.
//
// INTERFACE STABILITY: this is the boundary between RFC 001 (file
// resolver) and RFC 002 (Registry-backed resolver). Signature-verify
// paths MUST depend on this interface, not on a concrete resolver.
type KeyResolver interface {
	Resolve(agentURA string) (ed25519.PublicKey, error)
}

// FileKeyResolver — RFC 001's shipped resolver. Maps agent_ura → path
// containing the raw 32-byte public key.
type FileKeyResolver struct {
	mapping map[string]string
}

// NewFileKeyResolver constructs an empty FileKeyResolver.
func NewFileKeyResolver() *FileKeyResolver {
	return &FileKeyResolver{mapping: map[string]string{}}
}

// Insert registers a URA → pubkey-file mapping.
func (r *FileKeyResolver) Insert(agentURA, path string) {
	r.mapping[agentURA] = path
}

// Resolve implements KeyResolver.
func (r *FileKeyResolver) Resolve(agentURA string) (ed25519.PublicKey, error) {
	path, ok := r.mapping[agentURA]
	if !ok {
		return nil, ErrInvalidArgument(fmt.Sprintf("unknown_agent_key:%s", agentURA))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrInternal(fmt.Sprintf("key_file_read_failed:%s:%s", path, err))
	}
	if len(b) != 32 {
		return nil, ErrInvalidArgument(fmt.Sprintf("key_file_wrong_length:expected_32_got_%d", len(b)))
	}
	return ed25519.PublicKey(b), nil
}

// ── Signing primitives ───────────────────────────────────────────────

// SigningKeyFromBytes constructs an Ed25519 private key from a 32-byte
// seed. Parity with Rust SigningKey::from_bytes — same 32-byte input
// produces the same public key across SDKs.
func SigningKeyFromBytes(secret []byte) (ed25519.PrivateKey, error) {
	if len(secret) != 32 {
		return nil, ErrInvalidArgument(fmt.Sprintf("secret_wrong_length:expected_32_got_%d", len(secret)))
	}
	return ed25519.NewKeyFromSeed(secret), nil
}

func signDescriptorBoundInvocation(sk ed25519.PrivateKey, env DescriptorBoundEnvelope, keyIDHint string) (CallerSignature, error) {
	b, err := canonicalDescriptorBoundInvocationBytes(env)
	if err != nil {
		return CallerSignature{}, err
	}
	sig := ed25519.Sign(sk, b)
	return CallerSignature{Algorithm: "ed25519", Signature: sig, KeyIDHint: keyIDHint}, nil
}

func verifyDescriptorBoundInvocationSignature(env DescriptorBoundEnvelope, sig CallerSignature, resolver KeyResolver) error {
	if sig.Algorithm != "ed25519" {
		return ErrInvalidArgument(fmt.Sprintf("unsupported_algorithm:%s", sig.Algorithm))
	}
	key, err := resolver.Resolve(env.Envelope().Caller.URA)
	if err != nil {
		return err
	}
	if len(sig.Signature) != ed25519.SignatureSize {
		return ErrInvalidArgument(fmt.Sprintf("signature_wrong_length:expected_%d_got_%d", ed25519.SignatureSize, len(sig.Signature)))
	}
	b, err := canonicalDescriptorBoundInvocationBytes(env)
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, b, sig.Signature) {
		e := ErrInvalidArgument("CALLER_SIGNATURE_INVALID")
		e.Message = "caller_signature_invalid"
		return e
	}
	return nil
}

// VerifyReceiptSignature — verify a CalleeSignature via resolver
// (self-signed form: signer == callee).
func VerifyReceiptSignature(r ReceiptBody, sig CalleeSignature, resolver KeyResolver) error {
	return VerifyReceiptSignatureWithHosted(r, nil, sig, resolver)
}

// VerifyReceiptSignatureWithHosted — verify a CalleeSignature over the
// canonical receipt bytes including the §A12 hosted tail. The signer key
// is resolved from the host (hosted.SignerBinding.URA) when hosted, else
// from the callee. Mirrors the Rust verify_receipt_signature_with_hosted.
func VerifyReceiptSignatureWithHosted(r ReceiptBody, hosted *HostedAttestation, sig CalleeSignature, resolver KeyResolver) error {
	if sig.Algorithm != "ed25519" {
		return ErrInvalidArgument(fmt.Sprintf("unsupported_algorithm:%s", sig.Algorithm))
	}
	signerURA := r.CalleeBinding.URA
	if hosted != nil {
		signerURA = hosted.SignerBinding.URA
	}
	key, err := resolver.Resolve(signerURA)
	if err != nil {
		return err
	}
	if hosted != nil {
		if err := VerifyHostAttestation(r.CalleeBinding.URA, hosted.SignerBinding.URA, hosted.HostAttestation, key); err != nil {
			return err
		}
	}
	if len(sig.Signature) != ed25519.SignatureSize {
		return ErrInvalidArgument(fmt.Sprintf("signature_wrong_length:expected_%d_got_%d", ed25519.SignatureSize, len(sig.Signature)))
	}
	b := CanonicalReceiptBytesWithHosted(r, hosted)
	if !ed25519.Verify(key, b, sig.Signature) {
		return ErrInvalidArgument("callee_signature_invalid")
	}
	return nil
}

// FreshNonce generates a 16-byte CSPRNG nonce per RFC 001 §5.1.
func FreshNonce() [16]byte {
	var n [16]byte
	if _, err := rand.Read(n[:]); err != nil {
		// crypto/rand never returns an error on supported platforms;
		// if it ever did we cannot proceed safely with an unsigned nonce.
		panic(fmt.Sprintf("crypto/rand: %s", err))
	}
	return n
}
