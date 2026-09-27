package axon

import "crypto/ed25519"

// DescriptorBoundInvocationBuilder owns construction of the canonical
// descriptor-bound invocation tuple. Callers supply typed facts; the builder
// computes the payload digest and is the only place that assembles the
// InvocationEnvelope.
type DescriptorBoundInvocationBuilder struct {
	caller        AgentIdentity
	callee        AgentIdentity
	subject       SubjectIdentity
	descriptorRef string
	nonce         [16]byte
	causal        CausalContext
	payload       []byte
}

// NewDescriptorBoundInvocationBuilder creates an empty canonical invocation
// builder.
func NewDescriptorBoundInvocationBuilder() *DescriptorBoundInvocationBuilder {
	return &DescriptorBoundInvocationBuilder{}
}

// WithCaller sets the caller identity.
func (b *DescriptorBoundInvocationBuilder) WithCaller(caller AgentIdentity) *DescriptorBoundInvocationBuilder {
	b.caller = caller
	return b
}

// WithCallee sets the callee identity.
func (b *DescriptorBoundInvocationBuilder) WithCallee(callee AgentIdentity) *DescriptorBoundInvocationBuilder {
	b.callee = callee
	return b
}

// WithSubject sets the explicit invocation subject.
func (b *DescriptorBoundInvocationBuilder) WithSubject(subject SubjectIdentity) *DescriptorBoundInvocationBuilder {
	b.subject = subject
	return b
}

// WithDescriptorRef sets the exact ability descriptor reference bound into
// caller-signature canonical bytes.
func (b *DescriptorBoundInvocationBuilder) WithDescriptorRef(descriptorRef string) *DescriptorBoundInvocationBuilder {
	b.descriptorRef = descriptorRef
	return b
}

// WithNonce sets the caller-generated invocation nonce.
func (b *DescriptorBoundInvocationBuilder) WithNonce(nonce [16]byte) *DescriptorBoundInvocationBuilder {
	b.nonce = nonce
	return b
}

// WithCausalContext sets the complete causal predecessor context.
func (b *DescriptorBoundInvocationBuilder) WithCausalContext(causal CausalContext) *DescriptorBoundInvocationBuilder {
	b.causal = cloneCausalContext(causal)
	return b
}

// WithPayload sets the invocation payload. The builder binds its SHA-256
// digest into the canonical envelope.
func (b *DescriptorBoundInvocationBuilder) WithPayload(payload []byte) *DescriptorBoundInvocationBuilder {
	b.payload = append([]byte(nil), payload...)
	return b
}

// Build validates and freezes one canonical descriptor-bound invocation
// draft. A draft is intentionally unsigned so the same canonical bytes can be
// sent to an external caller-signature provider before dispatch.
func (b *DescriptorBoundInvocationBuilder) Build() (DescriptorBoundInvocationDraft, error) {
	if b == nil {
		return DescriptorBoundInvocationDraft{}, ErrInvalidArgument("descriptor_bound_builder_required")
	}
	envelope, err := NewDescriptorBoundEnvelope(InvocationEnvelope{
		Caller:          b.caller,
		Callee:          b.callee,
		Subject:         b.subject,
		Ability:         b.descriptorRef,
		ArgsDigest:      Sha256(b.payload),
		InvocationNonce: b.nonce,
		CausalContext:   cloneCausalContext(b.causal),
	})
	if err != nil {
		return DescriptorBoundInvocationDraft{}, err
	}
	if axonErr := validateEnvelope(envelope.Envelope()); axonErr != nil {
		return DescriptorBoundInvocationDraft{}, axonErr
	}
	draft, err := NewDescriptorBoundInvocationDraft(envelope)
	if err != nil {
		return DescriptorBoundInvocationDraft{}, err
	}
	draft.payload = append([]byte(nil), b.payload...)
	draft.payloadBound = true
	return draft, nil
}

// DescriptorBoundInvocationDraft is an immutable descriptor-bound envelope
// proof owner before runtime admission. It may be envelope-only for
// canonical/signature vector checks; only payload-bound drafts can bind into a
// DescriptorBoundInvocationRequest.
type DescriptorBoundInvocationDraft struct {
	envelope     DescriptorBoundEnvelope
	payload      []byte
	payloadBound bool
}

// NewDescriptorBoundInvocationDraft constructs an envelope-only proof owner.
func NewDescriptorBoundInvocationDraft(envelope DescriptorBoundEnvelope) (DescriptorBoundInvocationDraft, error) {
	if axonErr := validateEnvelope(envelope.Envelope()); axonErr != nil {
		return DescriptorBoundInvocationDraft{}, axonErr
	}
	draft := DescriptorBoundInvocationDraft{
		envelope: cloneDescriptorBoundEnvelope(envelope),
	}
	if _, err := draft.CanonicalBytes(); err != nil {
		return DescriptorBoundInvocationDraft{}, err
	}
	return draft, nil
}

// Envelope returns a defensive copy of the descriptor-bound envelope.
func (d DescriptorBoundInvocationDraft) Envelope() DescriptorBoundEnvelope {
	return cloneDescriptorBoundEnvelope(d.envelope)
}

// Payload returns a defensive copy of the digest-bound payload.
func (d DescriptorBoundInvocationDraft) Payload() []byte {
	return append([]byte(nil), d.payload...)
}

// CanonicalBytes returns the Axon-owned caller-signature byte sequence.
func (d DescriptorBoundInvocationDraft) CanonicalBytes() ([]byte, error) {
	return canonicalDescriptorBoundInvocationBytes(d.envelope)
}

// SignCallerSignature signs this draft's descriptor-bound envelope with
// caller-owned key material.
func (d DescriptorBoundInvocationDraft) SignCallerSignature(
	signingKey ed25519.PrivateKey,
	keyIDHint string,
) (CallerSignature, error) {
	return signDescriptorBoundInvocation(signingKey, d.envelope, keyIDHint)
}

// VerifyCallerSignature verifies a caller signature against this draft's own
// descriptor-bound envelope.
func (d DescriptorBoundInvocationDraft) VerifyCallerSignature(
	signature CallerSignature,
	resolver KeyResolver,
) error {
	return verifyDescriptorBoundInvocationSignature(d.envelope, signature, resolver)
}

// BindCallerSignature validates caller-signature structure and returns the
// complete request accepted by LocalRuntime and wire projection builders.
func (d DescriptorBoundInvocationDraft) BindCallerSignature(
	callMode CallMode,
	signature CallerSignature,
) (DescriptorBoundInvocationRequest, error) {
	if !d.payloadBound {
		return DescriptorBoundInvocationRequest{}, ErrInvalidArgument("descriptor_bound_payload_required")
	}
	if err := validateCallMode(callMode); err != nil {
		return DescriptorBoundInvocationRequest{}, err
	}
	if axonErr := validateSignatureStructure(signature); axonErr != nil {
		return DescriptorBoundInvocationRequest{}, axonErr
	}
	return NewDescriptorBoundInvocationRequest(
		callMode,
		d.envelope,
		signature,
		d.payload,
	), nil
}

func validateCallMode(callMode CallMode) error {
	switch callMode {
	case CallModeRPC, CallModeStream, CallModeBidi:
		return nil
	default:
		return ErrInvalidArgument("invocation_call_mode_invalid")
	}
}
