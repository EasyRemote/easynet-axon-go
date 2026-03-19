// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/tool_adapter_test.go
// Description: Unit tests for AbilityToolAdapter covering local handler execution, registration order preservation, unknown-tool and nil-transport error paths, re-registration semantics, and principal context propagation to remote transports.
//
// Protocol Responsibility:
// - Validates that AbilityToolAdapter faithfully routes tool calls through local handlers or remote transports with correct tenant/principal/resource context.
// - Ensures registration order, re-registration replacement, and error semantics match the cross-language SDK contract.
//
// Implementation Approach:
// - Uses mock transports and table-driven assertions for deterministic, side-effect-free verification.
// - Each test isolates a single behavioral facet to keep failure diagnostics precise.
//
// Usage Contract:
// - Tests are runnable via `go test ./...` without external dependencies.
// - No network, FFI, or file-system access is required.
//
// Architectural Position:
// - Part of the Go SDK test suite; exercises the AbilityToolAdapter public API surface only.
// - Should not depend on internal adapter implementation details.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import (
	"context"
	"testing"
)

type principalCaptureTransport struct {
	tenantID    string
	resourceURI string
	options     CallOptions
}

func (t *principalCaptureTransport) Call(_ context.Context, tenantID string, resourceURI string, payload Payload) (Payload, error) {
	t.tenantID = tenantID
	t.resourceURI = resourceURI
	return payload, nil
}

func (t *principalCaptureTransport) CallWithOptions(
	_ context.Context,
	tenantID string,
	resourceURI string,
	payload Payload,
	options CallOptions,
) (Payload, error) {
	t.tenantID = tenantID
	t.resourceURI = resourceURI
	t.options = options
	return payload, nil
}

func TestToolAdapterPrincipalContextFlowsToRemoteTransport(t *testing.T) {
	transport := &principalCaptureTransport{}
	adapter := NewToolAdapter("tenant-test", transport).WithPrincipalID("agent.alice")
	adapter.RegisterAbility("camera", "easynet:///r/prv/camera")

	result, err := adapter.Execute(context.Background(), "camera", Payload{"mode": "photo"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := transport.options.PrincipalID; got != "agent.alice" {
		t.Fatalf("principal not propagated: got %q", got)
	}
	if got := transport.resourceURI; got != "easynet:///r/prv/camera" {
		t.Fatalf("resource uri mismatch: got %q", got)
	}
	if got := result["mode"]; got != "photo" {
		t.Fatalf("payload mismatch: got %#v", got)
	}
}

func TestToolAdapterLocalHandlerExecutesDirect(t *testing.T) {
	adapter := NewToolAdapter("tenant-test", nil)
	adapter.Register("greet", func(_ context.Context, args Payload) (Payload, error) {
		name, _ := args["name"].(string)
		return Payload{"greeting": "hello " + name}, nil
	}, WithToolDescription("Greet someone"))

	result, err := adapter.Execute(context.Background(), "greet", Payload{"name": "Bob"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := result["greeting"]; got != "hello Bob" {
		t.Fatalf("unexpected result: got %#v", got)
	}
}

func TestToolAdapterSpecsPreserveInsertionOrder(t *testing.T) {
	adapter := NewToolAdapter("t", nil)
	adapter.Register("beta", func(_ context.Context, args Payload) (Payload, error) { return args, nil })
	adapter.Register("alpha", func(_ context.Context, args Payload) (Payload, error) { return args, nil })
	adapter.Register("gamma", func(_ context.Context, args Payload) (Payload, error) { return args, nil })

	specs := adapter.Specs()
	if len(specs) != 3 {
		t.Fatalf("expected 3 specs, got %d", len(specs))
	}
	expected := []string{"beta", "alpha", "gamma"}
	for i, name := range expected {
		if specs[i].Name != name {
			t.Fatalf("specs[%d].Name = %q, want %q", i, specs[i].Name, name)
		}
	}
}

func TestToolAdapterUnknownToolReturnsError(t *testing.T) {
	adapter := NewToolAdapter("t", nil)
	_, err := adapter.Execute(context.Background(), "nonexistent", Payload{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if err.Error() != "unknown tool: nonexistent" {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

func TestToolAdapterNilTransportReturnsError(t *testing.T) {
	adapter := NewToolAdapter("t", nil)
	adapter.RegisterAbility("remote", "easynet:///r/org/remote")

	_, err := adapter.Execute(context.Background(), "remote", Payload{})
	if err == nil {
		t.Fatal("expected error for nil transport")
	}
	if err.Error() != "no transport configured for remote ability calls" {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

func TestToolAdapterReRegistrationUpdatesSpec(t *testing.T) {
	adapter := NewToolAdapter("t", nil)
	handler := func(_ context.Context, args Payload) (Payload, error) { return args, nil }

	adapter.Register("tool", handler, WithToolDescription("first"))
	adapter.Register("tool", handler, WithToolDescription("second"))

	specs := adapter.Specs()
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec after re-registration, got %d", len(specs))
	}
	if specs[0].Description != "second" {
		t.Fatalf("description not updated: got %q", specs[0].Description)
	}
}
