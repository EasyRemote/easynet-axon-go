package axon

// F07 §A14 — receipt-verb tests (Go).
//
// STEP B coverage: the three core verbs Verify/Trace/ProveAuthority.
// STEP C coverage: WRAPPER EQUIVALENCE — the idiomatic sugar returns the
// identical result as the core method it wraps.

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"reflect"
	"testing"
)

// mapResolver — a tiny in-memory KeyResolver for the verb tests.
type mapResolver struct {
	keys map[string]ed25519.PublicKey
}

func (m *mapResolver) Resolve(ura string) (ed25519.PublicKey, error) {
	k, ok := m.keys[ura]
	if !ok {
		return nil, ErrInvalidArgument("unknown_agent_key:" + ura)
	}
	return k, nil
}

const receiptVerbDescriptorRef = "easynet:///r/openai/ability/openai.reviewer.review@1.0.0#" + testDescriptorHashHex + "!invoke"

func appendReceiptForVerbs(
	t *testing.T,
	signingKey ed25519.PrivateKey,
	binding AxiomBinding,
	usage InvocationUsage,
) (SignedInvocationReceipt, *mapResolver) {
	t.Helper()
	envelope, err := NewDescriptorBoundEnvelope(InvocationEnvelope{
		Caller:          binding.Caller,
		Callee:          binding.Callee,
		Subject:         binding.Subject,
		Ability:         binding.AbilityBinding,
		ArgsDigest:      binding.PayloadDigest,
		InvocationNonce: binding.InvocationNonce,
		CausalContext:   binding.Causal,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorityBinding := cloneAuthorityOrBootstrap(binding.AuthorityBinding)
	proofBinding := cloneAuthorityOrBootstrap(authorityBinding)
	issuer := binding.Callee
	policy, err := NewVerifiedAdmissionPolicy(
		envelope,
		authorityBinding,
		InvocationAuthorityProof{
			ProofType:     "receipt-verbs-verified-admission",
			Binding:       &proofBinding,
			ProofHash:     AuthorityOrBootstrapProofHash(authorityBinding),
			Issuer:        &issuer,
			AdmissionHook: "test.go.receipt_verbs.admission.v1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := newVerifiedDescriptorBoundAdmission(envelope, policy)
	if err != nil {
		t.Fatal(err)
	}
	providerBinding := mustProviderBinding(
		t,
		binding.AbilityBinding,
		Sha256([]byte("receipt-verbs-schema")),
		Sha256([]byte("receipt-verbs-impl")),
		"canonical-go-receipt-verbs-runtime-v1",
		func(context.Context, *AbilityContext) ([]byte, *AxonError) { return nil, nil },
	)
	descriptor, err := resolvedDescriptorEvidence(admission, providerBinding)
	if err != nil {
		t.Fatal(err)
	}
	implementation, err := registeredImplementationEvidence(providerBinding)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewDefaultCanonicalReceiptProvider(
		AdmissionPolicyVerifierFunc(func(
			DescriptorBoundEnvelope,
		) (VerifiedAdmissionPolicy, error) {
			return policy, nil
		}),
		ReceiptSigningAuthorityResolverFunc(func(
			callee AgentIdentity,
		) (ReceiptSigningAuthority, error) {
			return NewEd25519ReceiptSigningAuthority(callee, signingKey, "receipt-verbs")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	context, err := provider.Bind(
		"inv-verb-0001",
		admission,
		descriptor,
		implementation,
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := provider.AppendSignedReceipt(context, ReceiptAppendInput{
		ReceiptType:     "admitted",
		State:           StateAdmitted,
		TimestampUnixMs: 1_700_000_000_000,
		Payload:         nil,
		Usage:           usage,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := &mapResolver{keys: map[string]ed25519.PublicKey{
		binding.Callee.URA: signingKey.Public().(ed25519.PublicKey),
	}}
	return receipt, resolver
}

// buildSelfReceipt builds a provider-signed Self_-authority receipt.
func buildSelfReceipt(
	t *testing.T,
	causal CausalContext,
) (SignedInvocationReceipt, *mapResolver) {
	t.Helper()
	var seed [32]byte
	seed[0] = 7
	signingKey, err := SigningKeyFromBytes(seed[:])
	if err != nil {
		t.Fatal(err)
	}

	caller := NewAgentIdentity("easynet:///r/silan/agent/silan.alice", ProfileStrictV2)
	callee := NewAgentIdentity("easynet:///r/openai/agent/openai.reviewer", ProfileStrictV2)
	subject := NewSubjectIdentity("easynet:///r/silan/resource/silan.papers/paper", ProfileStrictV2)
	payloadDigest := Sha256([]byte("{}"))
	authority := AuthorityOrBootstrapFromBinding(SelfAuthority(caller.URA))

	binding := AxiomBinding{
		Caller:           caller,
		Callee:           callee,
		Subject:          subject,
		InvocationNonce:  [16]byte{0x22},
		Causal:           causal,
		PayloadDigest:    payloadDigest,
		AbilityBinding:   receiptVerbDescriptorRef,
		AuthorityBinding: authority,
	}
	return appendReceiptForVerbs(t, signingKey, binding, InvocationUsage{})
}

// ── M1: Verify ───────────────────────────────────────────────────────

func TestVerb_VerifySelfSigned(t *testing.T) {
	r, res := buildSelfReceipt(t, CausalNoneCtx())
	v, err := r.Verify(res)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if v.SigningModel != SigningSelf {
		t.Fatalf("expected self-signed model")
	}
	if v.SignerURA != r.AxiomBinding().Callee.URA {
		t.Fatalf("signer URA mismatch: %s", v.SignerURA)
	}
	if v.SelfHash != r.SelfHash() {
		t.Fatalf("self-hash mismatch")
	}
}

func TestVerb_VerifyDetectsAuthorityTamper(t *testing.T) {
	r, res := buildSelfReceipt(t, CausalNoneCtx())
	// Tamper the authority binding AFTER signing — the tail is in the
	// signed region, so verify must reject.
	r.receipt.AxiomBinding.AuthorityBinding = AuthorityOrBootstrapFromBinding(SelfAuthority("easynet:///r/silan/agent/evil"))
	if _, err := r.Verify(res); err == nil {
		t.Fatalf("tampered authority binding must break Verify")
	}
}

// ── M2: Trace ────────────────────────────────────────────────────────

func TestVerb_TraceForms(t *testing.T) {
	root, _ := buildSelfReceipt(t, CausalNoneCtx())
	if root.Trace().Kind != TraceRoot {
		t.Fatalf("none → Root")
	}

	var h [32]byte
	h[0] = 0x9
	scalar, _ := buildSelfReceipt(t, CausalScalarCtx(ReceiptRef{ReceiptHash: h, ReceiptURA: "p"}))
	tr := scalar.Trace()
	if tr.Kind != TraceParent || tr.Parent.ReceiptURA != "p" {
		t.Fatalf("scalar → Parent")
	}

	list, _ := buildSelfReceipt(t, CausalListCtx([]ReceiptRef{
		{ReceiptHash: h, ReceiptURA: "a"}, {ReceiptHash: h, ReceiptURA: "b"},
	}))
	if tl := list.Trace(); tl.Kind != TraceFanin || tl.Count != 2 {
		t.Fatalf("list → Fanin(2), got %+v", tl)
	}

	merkle, _ := buildSelfReceipt(t, CausalMerkleCtx(h, "proof"))
	if tm := merkle.Trace(); tm.Kind != TraceFanin || tm.Count != 0 {
		t.Fatalf("merkle → Fanin(0), got %+v", tm)
	}
}

// ── M3: ProveAuthority ───────────────────────────────────────────────

func TestVerb_ProveAuthoritySelf(t *testing.T) {
	r, res := buildSelfReceipt(t, CausalNoneCtx())
	p, err := r.ProveAuthority(res)
	if err != nil {
		t.Fatalf("prove self: %v", err)
	}
	if p.Kind != ProofSelf || p.PrincipalURA != r.AxiomBinding().Caller.URA {
		t.Fatalf("expected Self_ proof for caller, got %+v", p)
	}
}

func TestVerb_ProveAuthoritySelfMismatch(t *testing.T) {
	r, res := buildSelfReceipt(t, CausalNoneCtx())
	r.receipt.AxiomBinding.AuthorityBinding = AuthorityOrBootstrapFromBinding(
		SelfAuthority("easynet:///r/silan/agent/not-the-caller"),
	)
	if _, err := r.ProveAuthority(res); err == nil {
		t.Fatalf("principal != caller must be permission_denied")
	} else if ae, ok := err.(*AxonError); !ok || ae.Kind != KindPermissionDenied {
		t.Fatalf("expected permission_denied, got %v", err)
	}
}

// buildDelegatedReceipt signs a real delegation with the issuer key and
// builds a receipt whose authority is that delegation. The authority
// principal is "silan.boss" — the caller (openai.reviewer's counterpart,
// silan.alice) acts under boss's delegation. The delegation is signed by
// "silan.trust_anchor", a DELIBERATELY DIFFERENT identity from the
// authority: v1 no longer requires evidence.issuer == authority (see RFC
// doc "Issuer authenticity vs. issuer authority") — the SDK proves the
// grant is authentically signed by the issuer, not that the issuer was
// entitled to vouch for the authority.
func buildDelegatedReceipt(t *testing.T) (SignedInvocationReceipt, *mapResolver) {
	t.Helper()
	var calleeSeed, issuerSeed [32]byte
	calleeSeed[0] = 3
	issuerSeed[0] = 5
	calleeSk, _ := SigningKeyFromBytes(calleeSeed[:])
	calleeVk := calleeSk.Public().(ed25519.PublicKey)
	issuerSk, _ := SigningKeyFromBytes(issuerSeed[:])
	issuerVk := issuerSk.Public().(ed25519.PublicKey)

	caller := NewAgentIdentity("easynet:///r/silan/agent/silan.alice", ProfileStrictV2)
	callee := NewAgentIdentity("easynet:///r/openai/agent/openai.reviewer", ProfileStrictV2)
	subject := NewSubjectIdentity("easynet:///r/silan/resource/silan.papers/paper", ProfileStrictV2)
	authorityIdentity := NewAgentIdentity("easynet:///r/silan/agent/silan.boss", ProfileStrictV2)
	// The evidence.issuer role (who signs the grant) — genuinely distinct
	// from authorityIdentity above.
	issuerIdentity := NewAgentIdentity("easynet:///r/silan/agent/silan.trust_anchor", ProfileStrictV2)

	// Sign the canonical delegation claim bytes (declaration order),
	// binding both authority and delegatee (envelope.caller).
	payload := delegationPayloadCanonical{
		IssuerURA:    issuerIdentity.URA,
		AuthorityURA: authorityIdentity.URA,
		DelegateeURA: caller.URA,
		Audience:     "easynet:///r/openai/agent/openai.reviewer",
		Scopes:       []string{receiptVerbDescriptorRef},
		IssuedAtMs:   1_600_000_000_000,
		ExpiresAtMs:  4_100_000_000_000, // far future
	}
	payloadBytes, err := json.Marshal(&payload)
	if err != nil {
		t.Fatal(err)
	}
	delegSig := ed25519.Sign(issuerSk, payloadBytes)
	authority := AuthorityOrBootstrapFromBinding(DelegatedAuthority(
		authorityIdentity,
		DelegationEvidence{
			Issuer:      issuerIdentity,
			Audience:    payload.Audience,
			Scopes:      payload.Scopes,
			IssuedAtMs:  payload.IssuedAtMs,
			ExpiresAtMs: payload.ExpiresAtMs,
			Signature:   delegSig,
		},
	))
	payloadDigest := Sha256([]byte("{}"))

	binding := AxiomBinding{
		Caller:           caller,
		Callee:           callee,
		Subject:          subject,
		InvocationNonce:  [16]byte{0x22},
		Causal:           CausalNoneCtx(),
		PayloadDigest:    payloadDigest,
		AbilityBinding:   receiptVerbDescriptorRef,
		AuthorityBinding: authority,
	}
	r, _ := appendReceiptForVerbs(t, calleeSk, binding, InvocationUsage{})
	res := &mapResolver{keys: map[string]ed25519.PublicKey{
		callee.URA:         calleeVk,
		issuerIdentity.URA: issuerVk,
	}}
	return r, res
}

func TestVerb_ProveAuthorityDelegated(t *testing.T) {
	r, res := buildDelegatedReceipt(t)
	// The receipt still verifies for integrity (delegation tail signed).
	if _, err := r.Verify(res); err != nil {
		t.Fatalf("delegated receipt verify: %v", err)
	}
	p, err := r.ProveAuthority(res)
	if err != nil {
		t.Fatalf("prove delegated: %v", err)
	}
	if p.Kind != ProofDelegatedBy {
		t.Fatalf("expected delegated proof, got %+v", p)
	}
	// issuer (who signed) and authority (whose accountability is
	// exercised) are deliberately DIFFERENT identities here — v1 no
	// longer requires them equal (see "Issuer authenticity vs. issuer
	// authority").
	if p.IssuerURA != "easynet:///r/silan/agent/silan.trust_anchor" {
		t.Fatalf("issuer mismatch: %s", p.IssuerURA)
	}
	if p.AuthorityURA != "easynet:///r/silan/agent/silan.boss" {
		t.Fatalf("authority mismatch: %s", p.AuthorityURA)
	}
}

func TestVerb_ProveAuthorityDelegatedBadSignature(t *testing.T) {
	r, res := buildDelegatedReceipt(t)
	// Corrupt the delegation signature.
	r.receipt.AxiomBinding.AuthorityBinding.Binding.Delegation.Signature[0] ^= 0xFF
	if _, err := r.ProveAuthority(res); err == nil {
		t.Fatalf("corrupted delegation signature must be rejected")
	}
}

func TestVerb_ProveAuthorityReservedForm(t *testing.T) {
	r, res := buildSelfReceipt(t, CausalNoneCtx())
	reserved := AuthorityOrBootstrapFromBinding(AuthorityBinding{
		Authority:    NewAgentIdentity("easynet:///r/silan/cap/x", ProfileStrictV2),
		Relation:     AuthorityRelationCredentialOf,
		EvidenceKind: AuthorityEvidenceAttestation,
	})
	r.receipt.AxiomBinding.AuthorityBinding = reserved
	if _, err := r.ProveAuthority(res); err == nil {
		t.Fatalf("credential_of form must be reserved")
	} else if ae, ok := err.(*AxonError); !ok || ae.Kind != KindInvalidArgument {
		t.Fatalf("expected invalid_argument, got %v", err)
	}
}

// buildSessionReceipt signs a real session proof with the issuer key and
// builds a receipt whose authority is SessionOf+Session. Per the
// compatibility matrix: evidence.issuer == envelope.caller (who is
// presenting the session) and authority == envelope.subject (the
// session's accountable owner) — independent of issuer.
func buildSessionReceipt(t *testing.T) (SignedInvocationReceipt, *mapResolver) {
	t.Helper()
	var calleeSeed, issuerSeed [32]byte
	calleeSeed[0] = 11
	issuerSeed[0] = 13
	calleeSk, _ := SigningKeyFromBytes(calleeSeed[:])
	calleeVk := calleeSk.Public().(ed25519.PublicKey)
	// The caller presenting the session IS the session issuer (e.g. a
	// backend presenting a session on behalf of its owning user).
	issuerSk, _ := SigningKeyFromBytes(issuerSeed[:])
	issuerVk := issuerSk.Public().(ed25519.PublicKey)

	caller := NewAgentIdentity("easynet:///r/silan/agent/silan.backend", ProfileStrictV2)
	callee := NewAgentIdentity("easynet:///r/openai/agent/openai.reviewer", ProfileStrictV2)
	subject := NewSubjectIdentity("easynet:///r/silan/resource/silan.papers/paper", ProfileStrictV2)
	// authority == envelope.subject (the session's accountable owner).
	authorityIdentity := AgentIdentity{URA: subject.URA, Profile: subject.Profile}

	payload := sessionAuthorityPayloadCanonical{
		IssuerURA:    caller.URA,
		AuthorityURA: authorityIdentity.URA,
		SessionID:    "sess-0001",
		Scopes:       []string{receiptVerbDescriptorRef},
		Audiences:    []string{"easynet:///r/openai/agent/openai.reviewer"},
		IssuedAtMs:   1_600_000_000_000,
		ExpiresAtMs:  4_100_000_000_000,
	}
	payloadBytes, err := json.Marshal(&payload)
	if err != nil {
		t.Fatal(err)
	}
	sessionSig := ed25519.Sign(issuerSk, payloadBytes)
	authority := AuthorityOrBootstrapFromBinding(SessionAuthority(
		authorityIdentity,
		SessionEvidence{
			Issuer:      caller,
			SessionID:   payload.SessionID,
			Scopes:      payload.Scopes,
			Audiences:   payload.Audiences,
			IssuedAtMs:  payload.IssuedAtMs,
			ExpiresAtMs: payload.ExpiresAtMs,
			Signature:   sessionSig,
		},
	))
	payloadDigest := Sha256([]byte("{}"))

	binding := AxiomBinding{
		Caller:           caller,
		Callee:           callee,
		Subject:          subject,
		InvocationNonce:  [16]byte{0x22},
		Causal:           CausalNoneCtx(),
		PayloadDigest:    payloadDigest,
		AbilityBinding:   receiptVerbDescriptorRef,
		AuthorityBinding: authority,
	}
	r, _ := appendReceiptForVerbs(t, calleeSk, binding, InvocationUsage{})
	res := &mapResolver{keys: map[string]ed25519.PublicKey{
		callee.URA: calleeVk,
		caller.URA: issuerVk,
	}}
	return r, res
}

func TestVerb_ProveAuthoritySession(t *testing.T) {
	r, res := buildSessionReceipt(t)
	if _, err := r.Verify(res); err != nil {
		t.Fatalf("session receipt verify: %v", err)
	}
	p, err := r.ProveAuthority(res)
	if err != nil {
		t.Fatalf("prove session: %v", err)
	}
	if p.Kind != ProofSessionOf {
		t.Fatalf("expected session proof, got %+v", p)
	}
	if p.SessionID != "sess-0001" {
		t.Fatalf("session id mismatch: %s", p.SessionID)
	}
	if p.AuthorityURA != r.AxiomBinding().Subject.URA {
		t.Fatalf("session authority must equal envelope subject: %s != %s", p.AuthorityURA, r.AxiomBinding().Subject.URA)
	}
	if !p.IsSession() {
		t.Fatalf("IsSession accessor disagrees with Kind")
	}
}

func TestVerb_ProveAuthoritySessionIssuerMustMatchCaller(t *testing.T) {
	r, res := buildSessionReceipt(t)
	// SessionOf's real invariant: issuer == envelope.caller. Swap in a
	// binding whose authority equals subject but whose issuer does not
	// match the caller.
	tampered := cloneAuthorityOrBootstrap(r.receipt.AxiomBinding.AuthorityBinding)
	tampered.Binding.Session.Issuer = NewAgentIdentity("easynet:///r/silan/agent/not-the-caller", ProfileStrictV2)
	r.receipt.AxiomBinding.AuthorityBinding = tampered
	if _, err := r.ProveAuthority(res); err == nil {
		t.Fatalf("issuer != caller must be permission_denied")
	} else if ae, ok := err.(*AxonError); !ok || ae.Kind != KindPermissionDenied {
		t.Fatalf("expected permission_denied, got %v", err)
	}
}

func TestVerb_ProveAuthorityBootstrap(t *testing.T) {
	r, res := buildSelfReceipt(t, CausalNoneCtx())
	caller := r.AxiomBinding().Caller
	r.receipt.AxiomBinding.AuthorityBinding = AuthorityOrBootstrapFromBootstrap(
		BootstrapAuthority(caller.URA, "realm-x", receiptVerbDescriptorRef),
	)
	p, err := r.ProveAuthority(res)
	if err != nil {
		t.Fatalf("prove bootstrap: %v", err)
	}
	if p.Kind != ProofBootstrap || !p.IsBootstrap() {
		t.Fatalf("expected bootstrap proof, got %+v", p)
	}
	if p.Realm != "realm-x" || p.Ability != receiptVerbDescriptorRef {
		t.Fatalf("bootstrap fields mismatch: %+v", p)
	}
}

func TestVerb_ProveAuthorityBootstrapPrincipalMismatch(t *testing.T) {
	r, res := buildSelfReceipt(t, CausalNoneCtx())
	r.receipt.AxiomBinding.AuthorityBinding = AuthorityOrBootstrapFromBootstrap(
		BootstrapAuthority("easynet:///r/silan/agent/not-the-caller", "realm-x", receiptVerbDescriptorRef),
	)
	if _, err := r.ProveAuthority(res); err == nil {
		t.Fatalf("bootstrap principal != caller must be permission_denied")
	} else if ae, ok := err.(*AxonError); !ok || ae.Kind != KindPermissionDenied {
		t.Fatalf("expected permission_denied, got %v", err)
	}
}

// ── STEP C: WRAPPER EQUIVALENCE ──────────────────────────────────────

func TestWrapperEquivalence_Verify(t *testing.T) {
	r, res := buildSelfReceipt(t, CausalNoneCtx())
	core, errCore := r.Verify(res)
	sugar, errSugar := r.VerifyWith(WithResolver(res))
	if (errCore == nil) != (errSugar == nil) {
		t.Fatalf("error disagreement: core=%v sugar=%v", errCore, errSugar)
	}
	if !reflect.DeepEqual(core, sugar) {
		t.Fatalf("VerifyWith diverged from Verify\n  core:  %+v\n  sugar: %+v", core, sugar)
	}
}

func TestWrapperEquivalence_Trace(t *testing.T) {
	parentHash := [32]byte{1}
	cases := []CausalContext{
		CausalNoneCtx(),
		CausalScalarCtx(ReceiptRef{ReceiptHash: parentHash, ReceiptURA: "p"}),
		CausalListCtx([]ReceiptRef{
			{ReceiptHash: parentHash, ReceiptURA: "a"},
			{ReceiptHash: parentHash, ReceiptURA: "b"},
			{ReceiptHash: parentHash, ReceiptURA: "c"},
		}),
		CausalMerkleCtx([32]byte{}, "proof"),
	}
	for _, c := range cases {
		r, _ := buildSelfReceipt(t, c)
		core := r.Trace()
		parents := r.TraceParents()
		switch core.Kind {
		case TraceRoot:
			if len(parents) != 0 {
				t.Fatalf("root → empty parents, got %d", len(parents))
			}
		case TraceParent:
			if len(parents) != 1 || parents[0].IsJoin || parents[0].Ref != core.Parent {
				t.Fatalf("parent wrapper diverged: %+v", parents)
			}
		case TraceFanin:
			if len(parents) != core.Count {
				t.Fatalf("fanin wrapper count %d != core %d", len(parents), core.Count)
			}
			for _, p := range parents {
				if !p.IsJoin {
					t.Fatalf("fanin parents must be marked IsJoin")
				}
			}
		}
		// The slice must be range-able (idiomatic requirement).
		n := 0
		for range r.TraceParents() {
			n++
		}
		if n != len(parents) {
			t.Fatalf("range count mismatch")
		}
	}
}

func TestWrapperEquivalence_ProveAuthority(t *testing.T) {
	r, res := buildDelegatedReceipt(t)
	core, errCore := r.ProveAuthority(res)
	sugar, errSugar := r.ProveAuthorityWith(WithResolver(res))
	if (errCore == nil) != (errSugar == nil) {
		t.Fatalf("error disagreement: core=%v sugar=%v", errCore, errSugar)
	}
	if !reflect.DeepEqual(core, sugar) {
		t.Fatalf("ProveAuthorityWith diverged from ProveAuthority\n  core:  %+v\n  sugar: %+v", core, sugar)
	}
	// Typed accessors must agree with the underlying kind.
	if core.IsDelegated() != (core.Kind == ProofDelegatedBy) {
		t.Fatalf("IsDelegated accessor disagrees with Kind")
	}
	if core.Principal() != core.AuthorityURA {
		t.Fatalf("delegated Principal() must be authority URA")
	}
}
