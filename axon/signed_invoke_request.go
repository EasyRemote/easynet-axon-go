// Go request shapes for the existing session-signing API.
//
// File: sdk/go/axon/signed_invoke_request.go
// Responsibility: typed request validation and JSON projection only.
// Current native artifacts require complete caller-signed requests; this shape
// lacks caller/signature fields and is not the current native invocation schema.
// Its existence does not establish native signed-call support. In particular,
// the native bridge does not supply caller authority or generate a nonce.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package axon

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// SignedAgentIdentity is the wire shape for the `callee` and
// `subject` axiom slots in `SignedInvokeRequest`. Mirrors
// `dendrite-bridge::invoke_signed_common::AgentIdentityField`.
//
// Both fields are required non-empty when this struct is sent — the
// bridge's `preflight_identity` rejects whitespace-only values with
// AXON_AXIOM_ENVELOPE_INCOMPLETE. The Go validator below enforces the
// same rule client-side so the failure mode is consistent regardless
// of which language constructed the request.
type SignedAgentIdentity struct {
	// URA is the canonical URA of the agent. Examples:
	//
	//   easynet:///r/acme/authority
	//   easynet:///r/acme/agent/01LLM-host-A-claude
	//
	// MUST be non-empty after trimming.
	URA string

	// Profile is the URA encoding profile selector. RFC 001 §3.1
	// pins this to "axon-strict-v2" today; setting any other
	// value will be rejected by the bridge's `SigningConfig` and
	// `preflight_identity` paths.
	Profile string
}

// StrictSignedIdentity constructs a signed identity using the canonical strict
// URA profile.
func StrictSignedIdentity(ura string) SignedAgentIdentity {
	return SignedAgentIdentity{
		URA:     strings.TrimSpace(ura),
		Profile: uraProfileStrictV2,
	}
}

// signedCausalForm is the serde discriminator the Rust side expects.
// Kept as an unexported type so callers go through the four
// constructors (CausalNone / CausalScalar / CausalList / CausalMerkle)
// rather than poking a string and risking a typo.
type signedCausalForm string

const (
	signedCausalFormNone   signedCausalForm = "none"
	signedCausalFormScalar signedCausalForm = "scalar"
	signedCausalFormList   signedCausalForm = "list"
	signedCausalFormMerkle signedCausalForm = "merkle"
)

// SignedCausalRef references a single prior receipt by its canonical
// hash and the URA at which the receipt is reachable. Used both as
// the Scalar form's payload and as a list element of the List form.
//
// `ReceiptHashHex` is the lowercase hex encoding of the 32-byte
// SHA-256 of the canonical receipt bytes. The bridge re-decodes this
// string per call; a malformed length or non-hex character yields a
// bridge-side AXON_AXIOM_ENVELOPE_INCOMPLETE.
type SignedCausalRef struct {
	ReceiptHashHex string
	ReceiptURA     string
}

// SignedCausalContext is the wire-typed Go counterpart to the Rust
// `CausalContextField` discriminated union. Construct via
// `CausalNone`, `CausalScalar`, `CausalList`, or `CausalMerkle`; the
// zero value of this struct corresponds to "no causal context"
// (form=none) and serialises identically to a missing field on the
// Rust side, both hitting the 0x00 canonical-bytes discriminator per
// `dendrite-bridge::invoke_signed_common::parse_causal`.
//
// Why a single struct instead of an interface or sealed-trait
// emulation: the wire format is a tagged union, so a single struct
// with explicit form + per-form payload fields round-trips most
// naturally to JSON without reflection tricks. The four constructors
// guarantee the unused payload fields are always zero.
type SignedCausalContext struct {
	form        signedCausalForm
	scalar      *SignedCausalRef
	list        []SignedCausalRef
	merkleRoot  string
	merkleProof string
}

// CausalNone constructs the genesis / "no causal predecessor" form.
// Equivalent to leaving the field unset on the wire; both encode to
// the 0x00 discriminator on the canonical-bytes side.
func CausalNone() SignedCausalContext {
	return SignedCausalContext{form: signedCausalFormNone}
}

// CausalScalar constructs the single-predecessor (linear-successor)
// form per AXIOM §"Audit closure under the four causal_context forms"
// `scalar receipt reference` bullet. Both ReceiptHashHex and
// ReceiptURA MUST be non-empty; ReceiptHashHex MUST decode as 32
// bytes of valid hex (validated bridge-side, mirrored by `Validate`).
func CausalScalar(ref SignedCausalRef) SignedCausalContext {
	return SignedCausalContext{form: signedCausalFormScalar, scalar: &ref}
}

// CausalList constructs the bounded-fan-in form. Order is
// significant: the canonical bytes encode entries in the slice's
// declared order, so callers MUST emit them in the same order on
// every signing of the same logical join, otherwise the receipt's
// `prior_hashes` will not byte-match downstream.
//
// Empty slices are rejected by `Validate`: a List-form context with
// zero predecessors is semantically meaningless (it's structurally
// just `None` with extra ceremony) and the bridge rejects it.
func CausalList(refs []SignedCausalRef) SignedCausalContext {
	cp := make([]SignedCausalRef, len(refs))
	copy(cp, refs)
	return SignedCausalContext{form: signedCausalFormList, list: cp}
}

// CausalMerkle constructs the unbounded-fan-in form per AXIOM
// §"Audit closure under the four causal_context forms" `Merkle root
// with URA proof pointer` bullet. `rootHex` is the lowercase hex of
// the 32-byte Merkle root (32 bytes after decode); `proofURA` is the
// URA at which the auditor can fetch the proof artefact (itself a
// receipt or sequence of receipts containing the leaf set + inclusion
// proofs).
func CausalMerkle(rootHex string, proofURA string) SignedCausalContext {
	return SignedCausalContext{
		form:        signedCausalFormMerkle,
		merkleRoot:  rootHex,
		merkleProof: proofURA,
	}
}

// Form returns the active discriminator. Useful for tests and
// observability that want to inspect a context without reaching for
// JSON.
func (c SignedCausalContext) Form() string { return string(c.form) }

// IsNone reports whether the context is the empty / genesis form.
// The zero-value SignedCausalContext is also IsNone — that's
// deliberate so the field can be omitted.
func (c SignedCausalContext) IsNone() bool {
	return c.form == "" || c.form == signedCausalFormNone
}

// validate runs the per-form structural checks that the bridge would
// otherwise apply at the FFI boundary. Mirrors `parse_causal` and
// `decode_hex_32` in `dendrite-bridge::invoke_signed_common`. We
// duplicate the rules client-side so the typed Go surface fails
// before crossing the cgo boundary, with a Go-native error rather
// than a JSON error round-trip.
func (c SignedCausalContext) validate() error {
	switch c.form {
	case "", signedCausalFormNone:
		return nil
	case signedCausalFormScalar:
		if c.scalar == nil {
			return errors.New("causal_context.scalar payload missing")
		}
		if err := validateReceiptHashHex("causal_context.scalar.receipt_hash_hex", c.scalar.ReceiptHashHex); err != nil {
			return err
		}
		if strings.TrimSpace(c.scalar.ReceiptURA) == "" {
			return errors.New("causal_context.scalar.receipt_ura must be non-empty")
		}
		return nil
	case signedCausalFormList:
		if len(c.list) == 0 {
			return errors.New("causal_context.list.prior must contain at least one entry")
		}
		for i, r := range c.list {
			if err := validateReceiptHashHex(
				fmt.Sprintf("causal_context.list.prior[%d].receipt_hash_hex", i), r.ReceiptHashHex,
			); err != nil {
				return err
			}
			if strings.TrimSpace(r.ReceiptURA) == "" {
				return fmt.Errorf("causal_context.list.prior[%d].receipt_ura must be non-empty", i)
			}
		}
		return nil
	case signedCausalFormMerkle:
		if err := validateReceiptHashHex("causal_context.merkle.root_hex", c.merkleRoot); err != nil {
			return err
		}
		if strings.TrimSpace(c.merkleProof) == "" {
			return errors.New("causal_context.merkle.proof_ura must be non-empty")
		}
		return nil
	default:
		return fmt.Errorf("causal_context.form must be one of {none,scalar,list,merkle}, got %q", string(c.form))
	}
}

// jsonMap encodes the context as the wire-tagged JSON object the
// bridge consumes. Returns nil for the None form so the encoded
// envelope can omit the field entirely (the bridge treats absent and
// `{"form":"none"}` identically per `parse_causal`).
func (c SignedCausalContext) jsonMap() map[string]any {
	switch c.form {
	case "", signedCausalFormNone:
		return nil
	case signedCausalFormScalar:
		return map[string]any{
			"form":             string(signedCausalFormScalar),
			"receipt_hash_hex": c.scalar.ReceiptHashHex,
			"receipt_ura":      c.scalar.ReceiptURA,
		}
	case signedCausalFormList:
		entries := make([]map[string]any, 0, len(c.list))
		for _, r := range c.list {
			entries = append(entries, map[string]any{
				"receipt_hash_hex": r.ReceiptHashHex,
				"receipt_ura":      r.ReceiptURA,
			})
		}
		return map[string]any{
			"form":  string(signedCausalFormList),
			"prior": entries,
		}
	case signedCausalFormMerkle:
		return map[string]any{
			"form":      string(signedCausalFormMerkle),
			"root_hex":  c.merkleRoot,
			"proof_ura": c.merkleProof,
		}
	default:
		// Defensive: validate() would have caught this; jsonMap is
		// called only after validate() succeeds, so reaching here is
		// a programming error in the caller.
		return nil
	}
}

// SignedInvokeRequest retains the existing Go request shape, which omits
// caller and signature. It cannot carry a complete externally signed request
// to the current native bridge; see the Go SDK native capability boundary.
type SignedInvokeRequest struct {
	// Callee is the agent the invocation targets. URA + Profile both
	// MUST be non-empty.
	Callee SignedAgentIdentity

	// Subject is the explicitly signed subject identity. URA + Profile
	// both MUST be non-empty. It is never derived from Callee.
	Subject SignedAgentIdentity

	// Ability is an AbilityDescriptorRef (`ability_ura@version`), e.g.
	// "easynet:///r/acme/ability/authority.runtime.forward@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke".
	// Bare ability URAs are rejected at the SDK boundary instead of being
	// upgraded or defaulted.
	Ability string

	// Exactly one of PayloadJSON or PayloadBase64 MUST be set.
	//
	// PayloadJSON is the structured arguments object. The bridge
	// re-marshals this with `serde_json::to_vec` before computing
	// the args_digest (SHA-256 of the args bytes), so any Go-side
	// representation that round-trips to the same canonical JSON
	// matches.
	PayloadJSON any

	// PayloadBase64 is the alternative for callers that already have
	// a canonical bytes representation. Decoded as-is and SHA-256'd
	// without re-encoding.
	PayloadBase64 string

	// ContentType labels the InvokeRequest arguments after payload decoding.
	// Leave empty to let the bridge infer application/json for PayloadJSON
	// and application/octet-stream for PayloadBase64.
	ContentType string

	// NonceBase64 is the caller-supplied nonce in this request shape.
	// An omitted value is not generated by the current native bridge.
	NonceBase64 string

	// CausalContext per AXIOM §"causal_context". Default zero-value
	// is the None / genesis form.
	CausalContext SignedCausalContext

	// TimeoutMs ≤ 0 lets the bridge apply its default (currently
	// matches DefaultTimeoutMs = 30s). Negative values are normalised
	// to 0 by the bridge's parse_timeout_ms.
	TimeoutMs int

	// Metadata is non-axiom gRPC metadata. RFC 001 §4.1.2 forbids
	// this from entering the signed canonical bytes; the bridge
	// upholds that boundary. This is where the
	// `x-easynet-delegation` header rides for DelegationProof
	// (§A14).
	Metadata map[string]string
}

// validate enforces every client-side rule we can check before
// dispatching. The list here mirrors the bridge-side preflight at
// `dendrite-bridge::invoke_signed::*` so the failure mode is the
// same regardless of where the rule trips first.
func (r SignedInvokeRequest) validate() error {
	if err := requireNonBlank("callee.ura", r.Callee.URA); err != nil {
		return err
	}
	if err := requireNonBlank("callee.profile", r.Callee.Profile); err != nil {
		return err
	}
	if err := requireNonBlank("subject.ura", r.Subject.URA); err != nil {
		return err
	}
	if err := requireNonBlank("subject.profile", r.Subject.Profile); err != nil {
		return err
	}
	if _, err := requiredAbilityRef(r.Ability); err != nil {
		return err
	}

	hasJSON := r.PayloadJSON != nil
	hasB64 := strings.TrimSpace(r.PayloadBase64) != ""
	switch {
	case hasJSON && hasB64:
		return errors.New("exactly one of PayloadJSON or PayloadBase64 must be set, not both")
	case !hasJSON && !hasB64:
		return errors.New("exactly one of PayloadJSON or PayloadBase64 must be set")
	}

	if r.NonceBase64 != "" {
		if err := validateNonceBase64Length("nonce_base64", r.NonceBase64); err != nil {
			return err
		}
	}

	if err := r.CausalContext.validate(); err != nil {
		return err
	}

	return nil
}

// helperPayload renders the JSON shape the bridge expects, mirroring
// the field names of the Rust `SignedInvokeRequest` exactly. Optional
// fields are omitted when empty so the wire payload stays minimal
// and the bridge's `Option<…>` deserialisations resolve to `None`
// rather than `Some(empty)` — a distinction the Rust side preserves
// for nonce ("absent → CSPRNG", "present → use this exact value").
func (r SignedInvokeRequest) helperPayload() (map[string]any, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	ability, err := requiredAbilityRef(r.Ability)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"callee": map[string]any{
			"ura":     strings.TrimSpace(r.Callee.URA),
			"profile": strings.TrimSpace(r.Callee.Profile),
		},
		"subject": map[string]any{
			"ura":     strings.TrimSpace(r.Subject.URA),
			"profile": strings.TrimSpace(r.Subject.Profile),
		},
		"ability": ability,
	}
	if r.PayloadJSON != nil {
		payload["payload_json"] = r.PayloadJSON
	} else {
		payload["payload_base64"] = strings.TrimSpace(r.PayloadBase64)
	}
	if strings.TrimSpace(r.ContentType) != "" {
		payload["content_type"] = strings.TrimSpace(r.ContentType)
	}
	if r.NonceBase64 != "" {
		payload["nonce_base64"] = r.NonceBase64
	}
	if causal := r.CausalContext.jsonMap(); causal != nil {
		payload["causal_context"] = causal
	}
	if r.TimeoutMs > 0 {
		payload["timeout_ms"] = r.TimeoutMs
	}
	if len(r.Metadata) > 0 {
		// Defensive copy so the caller cannot mutate the map after
		// dispatch and influence what landed on the wire.
		md := make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			md[k] = v
		}
		payload["metadata"] = md
	}
	return payload, nil
}

// validateReceiptHashHex applies the lowercase 32-byte hex rule the
// bridge's `decode_hex_32` enforces. Hex with mixed case decodes
// fine for `hex.DecodeString`, so we additionally require lowercase
// to keep the wire deterministic and to match the canonical bytes
// the receipt-side encoder emits.
func validateReceiptHashHex(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must be non-empty", field)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return fmt.Errorf("%s must be hex (got %q): %w", field, value, err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("%s must decode to 32 bytes, got %d", field, len(decoded))
	}
	if value != strings.ToLower(value) {
		return fmt.Errorf("%s must be lowercase hex", field)
	}
	return nil
}

// validateNonceBase64Length checks the 16-byte / not-all-zero
// invariant that the bridge's resolve_nonce enforces. We do NOT
// generate a nonce client-side: leaving NonceBase64 empty is the
// idiomatic path because letting the bridge generate ensures the
// nonce comes from the canonical CSPRNG path on the platform that
// owns the seed (rand::rngs::OsRng on the Rust side).
func validateNonceBase64Length(field string, value string) error {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("%s is not valid base64: %w", field, err)
	}
	if len(decoded) != 16 {
		return fmt.Errorf("%s must decode to exactly 16 bytes, got %d", field, len(decoded))
	}
	allZero := true
	for _, b := range decoded {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return fmt.Errorf("%s must not be all-zero", field)
	}
	return nil
}
