package axon

import (
	"context"
	"testing"
)

func TestProviderBindingRequiresMatchingDescriptorAndImplementation(t *testing.T) {
	descriptorRef := testDescriptorRef("easynet:///r/test/ability/authority.provider.binding")
	descriptor, err := NewAbilityDescriptor(descriptorRef, Sha256([]byte("schema")))
	if err != nil {
		t.Fatalf("NewAbilityDescriptor: %v", err)
	}
	implementation, err := NewAbilityImpl(
		descriptorRef,
		Sha256([]byte("implementation")),
		"go-test-runtime",
	)
	if err != nil {
		t.Fatalf("NewAbilityImpl: %v", err)
	}
	binding, err := NewProviderBinding(
		descriptor,
		implementation,
		func(context.Context, *AbilityContext) ([]byte, *AxonError) {
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("NewProviderBinding: %v", err)
	}
	if binding.Descriptor().DescriptorRef() != descriptorRef {
		t.Fatalf("descriptor ref = %q", binding.Descriptor().DescriptorRef())
	}
	if binding.Implementation().DescriptorRef() != descriptorRef {
		t.Fatalf("implementation ref = %q", binding.Implementation().DescriptorRef())
	}
}

func TestProviderBindingRejectsDescriptorMismatch(t *testing.T) {
	descriptorRef := testDescriptorRef("easynet:///r/test/ability/authority.provider.binding")
	descriptor, err := NewAbilityDescriptor(descriptorRef, Sha256([]byte("schema")))
	if err != nil {
		t.Fatalf("NewAbilityDescriptor: %v", err)
	}
	otherRef := testDescriptorRef("easynet:///r/test/ability/authority.provider.other")
	implementation, err := NewAbilityImpl(
		otherRef,
		Sha256([]byte("implementation")),
		"go-test-runtime",
	)
	if err != nil {
		t.Fatalf("NewAbilityImpl: %v", err)
	}
	if _, err := NewProviderBinding(
		descriptor,
		implementation,
		func(context.Context, *AbilityContext) ([]byte, *AxonError) {
			return nil, nil
		},
	); err == nil {
		t.Fatal("NewProviderBinding accepted mismatched descriptor evidence")
	}
}
