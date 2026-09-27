package axon

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	axonsdk "axon.run/sdk/go/axon"
)

const (
	// WireContentEncodingIdentity identifies untransformed invocation payloads.
	WireContentEncodingIdentity = "identity"
	requestIDRandomBytes        = 16
)

// DescriptorBoundWireOptions contains carrier policy that is outside the
// caller-signed seven-tuple.
type DescriptorBoundWireOptions struct {
	ContentType    string
	TimeoutSeconds int32
	Metadata       map[string]string
}

// DescriptorBoundWireProjectionBuilder projects one complete canonical
// request into transport-neutral Axon wire facts. Protobuf adapters must lower
// this projection mechanically and must not reconstruct tuple semantics.
type DescriptorBoundWireProjectionBuilder struct{}

// NewDescriptorBoundWireProjectionBuilder creates the canonical wire
// projection builder.
func NewDescriptorBoundWireProjectionBuilder() DescriptorBoundWireProjectionBuilder {
	return DescriptorBoundWireProjectionBuilder{}
}

// Build validates the signed request, derives its public route from the
// descriptor owner, and assigns one cryptographically random request id.
func (DescriptorBoundWireProjectionBuilder) Build(
	request DescriptorBoundInvocationRequest,
	options DescriptorBoundWireOptions,
) (DescriptorBoundWireProjection, error) {
	if err := validateCallMode(request.callMode); err != nil {
		return DescriptorBoundWireProjection{}, err
	}
	envelope := request.envelope.Envelope()
	if axonErr := validateEnvelope(envelope); axonErr != nil {
		return DescriptorBoundWireProjection{}, axonErr
	}
	if axonErr := validateSignatureStructure(request.signature); axonErr != nil {
		return DescriptorBoundWireProjection{}, axonErr
	}
	if Sha256(request.payload) != envelope.ArgsDigest {
		return DescriptorBoundWireProjection{}, ErrInvalidArgument("payload_digest_mismatch")
	}
	canonicalBytes, err := request.CanonicalBytes()
	if err != nil {
		return DescriptorBoundWireProjection{}, err
	}
	abilityURA, err := AbilityURAFromDescriptorRef(envelope.Ability)
	if err != nil {
		return DescriptorBoundWireProjection{}, err
	}
	routeName, owned := axonsdk.PublicAbilityNameFromAbilityURA(envelope.Callee.URA, abilityURA)
	if !owned || strings.TrimSpace(routeName) == "" {
		return DescriptorBoundWireProjection{}, ErrInvalidArgument("descriptor_ref_callee_owner_mismatch")
	}
	contentType := strings.TrimSpace(options.ContentType)
	if contentType == "" {
		return DescriptorBoundWireProjection{}, ErrInvalidArgument("content_type_missing")
	}
	if options.TimeoutSeconds <= 0 {
		return DescriptorBoundWireProjection{}, ErrInvalidArgument("timeout_seconds_invalid")
	}
	requestID, err := newDescriptorBoundRequestID()
	if err != nil {
		return DescriptorBoundWireProjection{}, err
	}
	return DescriptorBoundWireProjection{
		callMode: request.callMode,
		envelope: DescriptorBoundWireEnvelope{
			requestID: requestID,
			caller:    envelope.Caller,
			callee:    envelope.Callee,
			subject:   envelope.Subject,
			nonce:     envelope.InvocationNonce,
			causal:    cloneCausalContext(envelope.CausalContext),
			signature: cloneCallerSignature(request.signature),
		},
		descriptorRef:   envelope.Ability,
		routeName:       routeName,
		payload:         append([]byte(nil), request.payload...),
		canonical:       append([]byte(nil), canonicalBytes...),
		contentType:     contentType,
		contentEncoding: WireContentEncodingIdentity,
		timeoutSeconds:  options.TimeoutSeconds,
		metadata:        cloneWireMetadata(options.Metadata),
	}, nil
}

// DescriptorBoundWireProjection is the immutable transport-neutral source for
// unary, server-stream, and bidi protobuf lowering.
type DescriptorBoundWireProjection struct {
	callMode        CallMode
	envelope        DescriptorBoundWireEnvelope
	descriptorRef   string
	routeName       string
	payload         []byte
	canonical       []byte
	contentType     string
	contentEncoding string
	timeoutSeconds  int32
	metadata        map[string]string
}

// CallMode returns the invocation interaction shape.
func (p DescriptorBoundWireProjection) CallMode() CallMode {
	return p.callMode
}

// Envelope returns a defensive copy of the wire envelope.
func (p DescriptorBoundWireProjection) Envelope() DescriptorBoundWireEnvelope {
	return cloneDescriptorBoundWireEnvelope(p.envelope)
}

// DescriptorRef returns the exact caller-signed ability descriptor reference.
func (p DescriptorBoundWireProjection) DescriptorRef() string {
	return p.descriptorRef
}

// RouteName returns the owner-relative public ability name used by every call
// mode. Signature presence never changes this routing decision.
func (p DescriptorBoundWireProjection) RouteName() string {
	return p.routeName
}

// Payload returns a defensive copy of invocation arguments.
func (p DescriptorBoundWireProjection) Payload() []byte {
	return append([]byte(nil), p.payload...)
}

// CanonicalBytes returns the descriptor-bound caller-signature bytes.
func (p DescriptorBoundWireProjection) CanonicalBytes() []byte {
	return append([]byte(nil), p.canonical...)
}

// ContentType returns the invocation payload media type.
func (p DescriptorBoundWireProjection) ContentType() string {
	return p.contentType
}

// ContentEncoding returns the explicit payload encoding.
func (p DescriptorBoundWireProjection) ContentEncoding() string {
	return p.contentEncoding
}

// TimeoutSeconds returns the carrier timeout.
func (p DescriptorBoundWireProjection) TimeoutSeconds() int32 {
	return p.timeoutSeconds
}

// Metadata returns a defensive copy of carrier metadata.
func (p DescriptorBoundWireProjection) Metadata() map[string]string {
	return cloneWireMetadata(p.metadata)
}

// DescriptorBoundWireEnvelope is the immutable canonical envelope projection
// consumed by protobuf adapters.
type DescriptorBoundWireEnvelope struct {
	requestID string
	caller    AgentIdentity
	callee    AgentIdentity
	subject   SubjectIdentity
	nonce     [16]byte
	causal    CausalContext
	signature CallerSignature
}

// RequestID returns the Axon-assigned request id.
func (e DescriptorBoundWireEnvelope) RequestID() string {
	return e.requestID
}

// Caller returns the caller identity.
func (e DescriptorBoundWireEnvelope) Caller() AgentIdentity {
	return e.caller
}

// Callee returns the callee identity.
func (e DescriptorBoundWireEnvelope) Callee() AgentIdentity {
	return e.callee
}

// Subject returns the explicit subject identity.
func (e DescriptorBoundWireEnvelope) Subject() SubjectIdentity {
	return e.subject
}

// Nonce returns the invocation nonce by value.
func (e DescriptorBoundWireEnvelope) Nonce() [16]byte {
	return e.nonce
}

// CausalContext returns a defensive copy of the causal context.
func (e DescriptorBoundWireEnvelope) CausalContext() CausalContext {
	return cloneCausalContext(e.causal)
}

// CallerSignature returns a defensive copy of the caller signature.
func (e DescriptorBoundWireEnvelope) CallerSignature() CallerSignature {
	return cloneCallerSignature(e.signature)
}

func newDescriptorBoundRequestID() (string, error) {
	var random [requestIDRandomBytes]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", ErrInternal("request_id_generation_failed")
	}
	return "req-" + hex.EncodeToString(random[:]), nil
}

func cloneWireMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	copy := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copy[key] = value
	}
	return copy
}

func cloneDescriptorBoundWireEnvelope(envelope DescriptorBoundWireEnvelope) DescriptorBoundWireEnvelope {
	envelope.causal = cloneCausalContext(envelope.causal)
	envelope.signature = cloneCallerSignature(envelope.signature)
	return envelope
}
