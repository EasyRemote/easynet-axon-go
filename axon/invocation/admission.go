package axon

// RFC 001 §5.2 — admission pipeline (Go reference).
//
// Parity with sdk/rust/src/invocation/admission.rs and
// sdk/python/axon_sdk/invocation/admission.py. Four sequential
// checks; the first failure rejects before the envelope reaches
// Admitted:
//
//  1. validateEnvelope           — always on, zero cost.
//  2. validateSignatureStructure — always on, rejects malformed
//                                  signatures *before* the envelope
//                                  can pollute the replay store.
//  3. verifyDescriptorBoundSignature — always on. Admission requires a
//                                      caller-key resolver and never accepts
//                                      self-asserted identity.
//  4. nonceReplayStore.checkAndRecord — always on. Dedup key is
//     (caller.URA, ability, invocation_nonce); ability guards against
//     cross-ability false-positives per review.
//
// All rejections map to KindInvalidArgument with one of three RFC §5.2
// literal reasons. The reason strings are load-bearing — SDKs, CLIs,
// audit tools, and metrics pipelines grep for them. Do not rename.

import (
	"crypto/ed25519"
	"fmt"
	"sync"
	"time"
)

// Canonical rejection reasons (RFC §5.2). These strings are the stable
// wire-visible contract.

const (
	ReasonEnvelopeIncomplete     = "AXON_AXIOM_ENVELOPE_INCOMPLETE"
	ReasonCallerSignatureInvalid = "AXON_CALLER_SIGNATURE_INVALID"
	ReasonNonceReplay            = "AXON_NONCE_REPLAY"
	// DefaultDedupWindowMs is the default nonce-replay window (7 days).
	// This is a correctness/idempotency guard, not a pure security
	// counter. Tunable via nonceReplayStore.withWindowMs.
	DefaultDedupWindowMs int64 = 7 * 86_400_000
)

// ── Envelope completeness ───────────────────────────────────────────

func rejectEnvelope(detail string) *AxonError {
	e := ErrInvalidArgument(ReasonEnvelopeIncomplete)
	e.Message = detail
	return e
}

// validateEnvelope checks the minimum structural invariants of the
// envelope before any cryptographic work. Every failure returns an
// AxonError with Reason=ReasonEnvelopeIncomplete and a granular
// human-readable detail in Message.
//
// Checks applied (RFC 001 §5.2 step 1, strengthened per review):
//   - Ability non-empty
//   - Caller.URA / Callee.URA / Subject.URA non-empty
//   - InvocationNonce is not the all-zero sentinel (a CSPRNG-generated
//     nonce being all-zero has probability ≈ 2⁻¹²⁸; we reject it as a
//     probable uninitialised-field mistake)
//   - ArgsDigest length is 32 bytes (already enforced by the
//     InvocationEnvelope type, rechecked defensively)
func validateEnvelope(env InvocationEnvelope) *AxonError {
	if env.Ability == "" {
		return rejectEnvelope("ability_empty")
	}
	if env.Caller.URA == "" {
		return rejectEnvelope("caller_ura_empty")
	}
	if env.Callee.URA == "" {
		return rejectEnvelope("callee_ura_empty")
	}
	if env.Subject.URA == "" {
		return rejectEnvelope("subject_ura_empty")
	}
	if len(env.ArgsDigest) != 32 {
		return rejectEnvelope("args_digest_wrong_length")
	}
	var zero [16]byte
	if env.InvocationNonce == zero {
		return rejectEnvelope("invocation_nonce_all_zero")
	}
	return nil
}

// ── Signature structure ─────────────────────────────────────────────

func rejectSig(detail string) *AxonError {
	e := ErrInvalidArgument(ReasonCallerSignatureInvalid)
	e.Message = detail
	return e
}

// validateSignatureStructure performs a non-cryptographic check of a
// caller signature. Always on, so malformed signatures are rejected
// before we record the nonce in the replay store.
//
// Cross-language parity: algorithm ids are ASCII lowercase strings
// from URA §7; the only one this SDK accepts today is "ed25519".
func validateSignatureStructure(sig CallerSignature) *AxonError {
	if sig.Algorithm == "" {
		return rejectSig("signature_algorithm_empty")
	}
	switch sig.Algorithm {
	case "ed25519":
		if len(sig.Signature) != ed25519.SignatureSize {
			return rejectSig("ed25519_signature_wrong_length")
		}
		return nil
	default:
		return rejectSig(fmt.Sprintf("unsupported_algorithm:%s", sig.Algorithm))
	}
}

// ── Full cryptographic verification ─────────────────────────────────

func verifyDescriptorBoundSignature(
	env DescriptorBoundEnvelope,
	sig CallerSignature,
	resolver KeyResolver,
) *AxonError {
	draft, err := NewDescriptorBoundInvocationDraft(env)
	if err != nil {
		if axErr, ok := err.(*AxonError); ok {
			return axErr
		}
		e := ErrInvalidArgument(ReasonEnvelopeIncomplete)
		e.Message = err.Error()
		return e
	}
	bytes, err := draft.CanonicalBytes()
	if err != nil {
		if axErr, ok := err.(*AxonError); ok {
			return axErr
		}
		e := ErrInvalidArgument(ReasonEnvelopeIncomplete)
		e.Message = err.Error()
		return e
	}
	if len(bytes) == 0 {
		return ErrInternal("canonical_descriptor_bound_invocation_bytes_empty")
	}
	if err := draft.VerifyCallerSignature(sig, resolver); err != nil {
		e := ErrInvalidArgument(ReasonCallerSignatureInvalid)
		e.Message = err.Error()
		return e
	}
	return nil
}

// ── Replay store ────────────────────────────────────────────────────

// replayKey is (caller.URA, ability, nonce). The ability component
// guards against cross-ability false-positives per review feedback.
type replayKey struct {
	caller  string
	ability string
	nonce   [16]byte
}

// nonceReplayStore is an in-memory sliding-window nonce replay store.
// Shared across all descriptor-bound requests on a given runtime.
//
// Week-3 semantics: per-runtime lifetime, no persistence. Week-5+ will
// swap this for a WAL-backed store when the production runtime's
// durability contract is wired up.
type nonceReplayStore struct {
	mu       sync.Mutex
	seen     map[replayKey]int64
	windowMs int64
}

// newNonceReplayStore constructs an empty store with the default window.
func newNonceReplayStore() *nonceReplayStore {
	return &nonceReplayStore{
		seen:     make(map[replayKey]int64),
		windowMs: DefaultDedupWindowMs,
	}
}

// withWindowMs constructs a store with a custom dedup window.
func (s *nonceReplayStore) withWindowMs(windowMs int64) *nonceReplayStore {
	return &nonceReplayStore{
		seen:     make(map[replayKey]int64),
		windowMs: windowMs,
	}
}

// checkAndRecord registers a (caller.URA, ability, nonce) observation
// as of nowMs. Returns ReasonNonceReplay on collision inside the
// current window. Out-of-window entries are pruned before the check.
//
// Admission calls this *after* structure/verify, so a malformed or
// unverified caller's nonce never enters the store.
func (s *nonceReplayStore) checkAndRecord(callerURA, ability string, nonce [16]byte, nowMs int64) *AxonError {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := nowMs - s.windowMs
	// Prune expired entries.
	for k, ts := range s.seen {
		if ts < cutoff {
			delete(s.seen, k)
		}
	}

	key := replayKey{caller: callerURA, ability: ability, nonce: nonce}
	if prev, ok := s.seen[key]; ok && prev >= cutoff {
		e := ErrInvalidArgument(ReasonNonceReplay)
		e.Message = fmt.Sprintf("dedup_window_hit:prev_ts=%d:window_ms=%d", prev, s.windowMs)
		return e
	}
	s.seen[key] = nowMs
	return nil
}

// len returns the current number of tracked entries for in-package tests.
func (s *nonceReplayStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

// clear drains all entries for in-package tests.
func (s *nonceReplayStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = make(map[replayKey]int64)
}

// verifyDescriptorBoundAdmission performs the complete canonical admission
// transition. Provider policy is verified before replay state is consumed.
func verifyDescriptorBoundAdmission(
	env DescriptorBoundEnvelope,
	sig CallerSignature,
	resolver KeyResolver,
	replay *nonceReplayStore,
	provider CanonicalReceiptProvider,
	nowMs int64,
) (VerifiedDescriptorBoundAdmission, *AxonError) {
	plain := env.Envelope()
	if err := validateEnvelope(plain); err != nil {
		return VerifiedDescriptorBoundAdmission{}, err
	}
	if _, err := NewDescriptorBoundInvocationDraft(env); err != nil {
		if axErr, ok := err.(*AxonError); ok {
			return VerifiedDescriptorBoundAdmission{}, axErr
		}
		e := ErrInvalidArgument(ReasonEnvelopeIncomplete)
		e.Message = err.Error()
		return VerifiedDescriptorBoundAdmission{}, e
	}
	if err := validateSignatureStructure(sig); err != nil {
		return VerifiedDescriptorBoundAdmission{}, err
	}
	if resolver == nil {
		return VerifiedDescriptorBoundAdmission{}, rejectSig("key_resolver_required")
	}
	if err := verifyDescriptorBoundSignature(env, sig, resolver); err != nil {
		return VerifiedDescriptorBoundAdmission{}, err
	}
	if replay == nil {
		return VerifiedDescriptorBoundAdmission{}, ErrInternal("nonce_replay_store_required")
	}
	if provider == nil {
		return VerifiedDescriptorBoundAdmission{}, ErrPermissionDenied(
			"canonical_receipt_provider_not_configured",
		)
	}
	policy, err := provider.VerifyAdmissionPolicy(env)
	if err != nil {
		if axonErr, ok := err.(*AxonError); ok {
			return VerifiedDescriptorBoundAdmission{}, axonErr
		}
		return VerifiedDescriptorBoundAdmission{}, ErrPermissionDenied(
			"receipt_admission_policy_failed:" + err.Error(),
		)
	}
	if err := replay.checkAndRecord(plain.Caller.URA, plain.Ability, plain.InvocationNonce, nowMs); err != nil {
		return VerifiedDescriptorBoundAdmission{}, err
	}
	admission, err := newVerifiedDescriptorBoundAdmission(env, policy)
	if err != nil {
		if axonErr, ok := err.(*AxonError); ok {
			return VerifiedDescriptorBoundAdmission{}, axonErr
		}
		return VerifiedDescriptorBoundAdmission{}, ErrInvalidArgument(err.Error())
	}
	return admission, nil
}

// NowMs returns the wall-clock time in milliseconds. Centralised so
// callers and tests can substitute it cleanly.
func NowMs() int64 {
	return time.Now().UnixMilli()
}
