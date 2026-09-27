// Canonical request projection into the native transport's carrier contract.
// Invocation owns signed facts; the parent transport package owns FFI only.
package axon

import (
	transport "axon.run/sdk/go/axon"
	"encoding/base64"
)

var _ transport.DescriptorBoundForwardingRequest = DescriptorBoundInvocationRequest{}

// DescriptorBoundNativePayload returns a fresh complete native document.
// It preserves caller-owned authority and rejects local-only launch controls.
func (r DescriptorBoundInvocationRequest) DescriptorBoundNativePayload() (map[string]any, error) {
	mode := "unary"
	switch r.callMode {
	case CallModeRPC:
	case CallModeStream:
		mode = "server_stream"
	case CallModeBidi:
		mode = "bidi"
	default:
		return nil, ErrInvalidArgument("native_call_mode_unsupported")
	}
	if r.parentInvocationID != "" || r.supervisorSpec != nil {
		return nil, ErrInvalidArgument("local_parent_or_supervisor_not_transportable")
	}
	envelope := r.envelope.Envelope()
	if err := validateEnvelope(envelope); err != nil {
		return nil, err
	}
	if err := validateSignatureStructure(r.signature); err != nil {
		return nil, err
	}
	if Sha256(r.payload) != envelope.ArgsDigest {
		return nil, ErrInvalidArgument("payload_digest_mismatch")
	}
	if _, err := r.CanonicalBytes(); err != nil {
		return nil, err
	}
	return map[string]any{
		"version": "axon.descriptor-bound-invocation.v1", "mode": mode,
		"envelope": map[string]any{
			"caller": IdentityFromAgent(envelope.Caller), "callee": IdentityFromAgent(envelope.Callee),
			"subject": IdentityFromSubject(envelope.Subject), "ability": envelope.Ability,
			"nonce_base64":   base64.StdEncoding.EncodeToString(envelope.InvocationNonce[:]),
			"causal_context": CausalFromCtx(envelope.CausalContext),
		},
		"signature": map[string]any{"algorithm": r.signature.Algorithm,
			"signature_base64": base64.StdEncoding.EncodeToString(r.signature.Signature), "key_id_hint": r.signature.KeyIDHint},
		"payload_base64": base64.StdEncoding.EncodeToString(r.payload),
	}, nil
}

// DescriptorBoundAbility returns the caller-signed execution contract.
func (r DescriptorBoundInvocationRequest) DescriptorBoundAbility() string {
	return r.envelope.Envelope().Ability
}
