package axon

import (
	"context"
	"strings"
)

// AbilityDescriptor is immutable catalog evidence for one governed descriptor.
type AbilityDescriptor struct {
	descriptorRef AbilityDescriptorRef
	schemaHash    [32]byte
}

// NewAbilityDescriptor validates descriptor identity and schema evidence.
func NewAbilityDescriptor(
	descriptorRef string,
	schemaHash [32]byte,
) (AbilityDescriptor, error) {
	parsed, err := ParseAbilityDescriptorRef(descriptorRef)
	if err != nil {
		return AbilityDescriptor{}, err
	}
	if parsed.DescriptorHash == zeroHash32 {
		return AbilityDescriptor{}, ErrInvalidArgument("descriptor_hash_missing")
	}
	if schemaHash == zeroHash32 {
		return AbilityDescriptor{}, ErrInvalidArgument("schema_hash_missing")
	}
	return AbilityDescriptor{
		descriptorRef: parsed,
		schemaHash:    schemaHash,
	}, nil
}

// DescriptorRef returns the exact governed descriptor reference.
func (d AbilityDescriptor) DescriptorRef() string {
	return d.descriptorRef.Raw
}

// AbilityURA returns the routable ability identity without descriptor evidence.
func (d AbilityDescriptor) AbilityURA() string {
	return d.descriptorRef.AbilityURA
}

// Version returns the descriptor version.
func (d AbilityDescriptor) Version() string {
	return d.descriptorRef.Version
}

// SchemaHash returns the catalog-owned schema hash.
func (d AbilityDescriptor) SchemaHash() [32]byte {
	return d.schemaHash
}

// AbilityImpl is immutable evidence for one executable implementation.
type AbilityImpl struct {
	descriptorRef AbilityDescriptorRef
	implHash      [32]byte
	runtimeEnv    string
}

// NewAbilityImpl validates executable evidence before provider binding.
func NewAbilityImpl(
	descriptorRef string,
	implHash [32]byte,
	runtimeEnv string,
) (AbilityImpl, error) {
	parsed, err := ParseAbilityDescriptorRef(descriptorRef)
	if err != nil {
		return AbilityImpl{}, err
	}
	if implHash == zeroHash32 {
		return AbilityImpl{}, ErrInvalidArgument("impl_hash_missing")
	}
	runtimeEnv = strings.TrimSpace(runtimeEnv)
	if runtimeEnv == "" {
		return AbilityImpl{}, ErrInvalidArgument("runtime_env_missing")
	}
	return AbilityImpl{
		descriptorRef: parsed,
		implHash:      implHash,
		runtimeEnv:    runtimeEnv,
	}, nil
}

// DescriptorRef returns the descriptor implemented by this executable.
func (i AbilityImpl) DescriptorRef() string {
	return i.descriptorRef.Raw
}

// ImplHash returns the implementation content hash.
func (i AbilityImpl) ImplHash() [32]byte {
	return i.implHash
}

// RuntimeEnv returns the implementation runtime environment identifier.
func (i AbilityImpl) RuntimeEnv() string {
	return i.runtimeEnv
}

// ProviderBinding atomically binds one governed descriptor to one executable
// implementation and handler.
type ProviderBinding struct {
	descriptor     AbilityDescriptor
	implementation AbilityImpl
	handler        AbilityFn
	recovery       *RecoveryContinuation
}

// NewProviderBinding rejects mismatched or incomplete provider evidence.
func NewProviderBinding(
	descriptor AbilityDescriptor,
	implementation AbilityImpl,
	handler AbilityFn,
) (ProviderBinding, error) {
	if descriptor.descriptorRef.Raw == "" {
		return ProviderBinding{}, ErrInvalidArgument("ability_descriptor_required")
	}
	if implementation.descriptorRef.Raw == "" {
		return ProviderBinding{}, ErrInvalidArgument("ability_impl_required")
	}
	if descriptor.descriptorRef.Raw != implementation.descriptorRef.Raw {
		return ProviderBinding{}, ErrInvalidArgument("provider_binding_descriptor_ref_mismatch")
	}
	if handler == nil {
		return ProviderBinding{}, ErrInvalidArgument("provider_binding_handler_required")
	}
	return ProviderBinding{
		descriptor:     descriptor,
		implementation: implementation,
		handler:        handler,
	}, nil
}

// Descriptor returns the immutable descriptor evidence.
func (b ProviderBinding) Descriptor() AbilityDescriptor {
	return b.descriptor
}

// Implementation returns the immutable executable evidence.
func (b ProviderBinding) Implementation() AbilityImpl {
	return b.implementation
}

// RecoveryFn resumes a persisted invocation from its durable checkpoint.
type RecoveryFn func(
	ctx context.Context,
	ability *AbilityContext,
	checkpoint []byte,
) ([]byte, *AxonError)

// RecoveryContinuation is provider-owned continuation behavior for one
// generic ability implementation.
type RecoveryContinuation struct {
	resume            RecoveryFn
	initialCheckpoint []byte
}

// NewRecoveryContinuation constructs validated recovery behavior.
func NewRecoveryContinuation(
	resume RecoveryFn,
	initialCheckpoint []byte,
) (RecoveryContinuation, error) {
	if resume == nil {
		return RecoveryContinuation{}, ErrInvalidArgument("recovery_resume_required")
	}
	return RecoveryContinuation{
		resume:            resume,
		initialCheckpoint: append([]byte(nil), initialCheckpoint...),
	}, nil
}

// WithRecovery binds recovery behavior to the same provider implementation.
func (b ProviderBinding) WithRecovery(
	continuation RecoveryContinuation,
) (ProviderBinding, error) {
	if continuation.resume == nil {
		return ProviderBinding{}, ErrInvalidArgument("recovery_resume_required")
	}
	copy := continuation
	copy.initialCheckpoint = append([]byte(nil), continuation.initialCheckpoint...)
	b.recovery = &copy
	return b, nil
}
