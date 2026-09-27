package axon

import "crypto/ed25519"

// CallMode identifies the invocation interaction shape.
type CallMode string

const (
	CallModeRPC    CallMode = "rpc"
	CallModeStream CallMode = "stream"
	CallModeBidi   CallMode = "bidi"
)

// SignedEnvelope is a caller-signed envelope retained for audit and binding.
type SignedEnvelope struct {
	Envelope  InvocationEnvelope
	Signature CallerSignature
}

// DescriptorBoundInvocationRequest is the sole public LocalRuntime admission
// input. It carries the complete descriptor-bound signed request and launch
// options as one immutable value.
type DescriptorBoundInvocationRequest struct {
	callMode           CallMode
	envelope           DescriptorBoundEnvelope
	signature          CallerSignature
	payload            []byte
	parentInvocationID string
	supervisorSpec     *SupervisorSpec
}

// NewDescriptorBoundInvocationRequest constructs an externally signed request.
// Signature creation belongs to the caller edge, outside LocalRuntime.
func NewDescriptorBoundInvocationRequest(
	callMode CallMode,
	envelope DescriptorBoundEnvelope,
	signature CallerSignature,
	payload []byte,
) DescriptorBoundInvocationRequest {
	return DescriptorBoundInvocationRequest{
		callMode:  callMode,
		envelope:  cloneDescriptorBoundEnvelope(envelope),
		signature: cloneCallerSignature(signature),
		payload:   append([]byte(nil), payload...),
	}
}

// SignDescriptorBoundInvocationRequest is a caller-edge builder. It signs the
// supplied complete envelope with caller-owned key material and returns the
// canonical runtime request; LocalRuntime never receives the private key.
func SignDescriptorBoundInvocationRequest(
	callMode CallMode,
	envelope DescriptorBoundEnvelope,
	signingKey ed25519.PrivateKey,
	payload []byte,
	keyIDHint string,
) (DescriptorBoundInvocationRequest, error) {
	signature, err := signDescriptorBoundInvocation(signingKey, envelope, keyIDHint)
	if err != nil {
		return DescriptorBoundInvocationRequest{}, err
	}
	return NewDescriptorBoundInvocationRequest(callMode, envelope, signature, payload), nil
}

// WithParentInvocationID returns a request linked to a local parent.
func (r DescriptorBoundInvocationRequest) WithParentInvocationID(parentInvocationID string) DescriptorBoundInvocationRequest {
	r.parentInvocationID = parentInvocationID
	return r
}

// WithSupervisor returns a request with root supervisor settings.
func (r DescriptorBoundInvocationRequest) WithSupervisor(spec *SupervisorSpec) DescriptorBoundInvocationRequest {
	r.supervisorSpec = cloneSupervisorSpec(spec)
	return r
}

// CallMode returns the request interaction shape.
func (r DescriptorBoundInvocationRequest) CallMode() CallMode {
	return r.callMode
}

// Envelope returns a defensive copy of the descriptor-bound envelope.
func (r DescriptorBoundInvocationRequest) Envelope() DescriptorBoundEnvelope {
	return cloneDescriptorBoundEnvelope(r.envelope)
}

// Signature returns a defensive copy of the caller signature.
func (r DescriptorBoundInvocationRequest) Signature() CallerSignature {
	return cloneCallerSignature(r.signature)
}

// CanonicalBytes returns the canonical descriptor-bound caller-signature bytes
// for this request's own envelope.
func (r DescriptorBoundInvocationRequest) CanonicalBytes() ([]byte, error) {
	draft, err := NewDescriptorBoundInvocationDraft(r.envelope)
	if err != nil {
		return nil, err
	}
	return draft.CanonicalBytes()
}

// Payload returns a defensive copy of the invocation payload.
func (r DescriptorBoundInvocationRequest) Payload() []byte {
	return append([]byte(nil), r.payload...)
}

// ParentInvocationID returns the optional local parent invocation id.
func (r DescriptorBoundInvocationRequest) ParentInvocationID() string {
	return r.parentInvocationID
}

// SupervisorSpec returns a defensive copy of the optional supervisor settings.
func (r DescriptorBoundInvocationRequest) SupervisorSpec() *SupervisorSpec {
	return cloneSupervisorSpec(r.supervisorSpec)
}

func cloneDescriptorBoundEnvelope(envelope DescriptorBoundEnvelope) DescriptorBoundEnvelope {
	plain := envelope.Envelope()
	plain.CausalContext = cloneCausalContext(plain.CausalContext)
	return DescriptorBoundEnvelope{envelope: plain}
}

func cloneCausalContext(causal CausalContext) CausalContext {
	copy := causal
	if causal.Scalar != nil {
		scalar := *causal.Scalar
		copy.Scalar = &scalar
	}
	copy.List = append([]ReceiptRef(nil), causal.List...)
	return copy
}

func cloneCallerSignature(signature CallerSignature) CallerSignature {
	copy := signature
	copy.Signature = append([]byte(nil), signature.Signature...)
	return copy
}

func cloneSupervisorSpec(spec *SupervisorSpec) *SupervisorSpec {
	if spec == nil {
		return nil
	}
	copy := *spec
	if spec.Tags != nil {
		copy.Tags = make(map[string]string, len(spec.Tags))
		for key, value := range spec.Tags {
			copy.Tags[key] = value
		}
	}
	return &copy
}
