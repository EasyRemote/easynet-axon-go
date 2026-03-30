// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/specs_test.go
// Description: Go remote-control tool-spec parity and metadata regression tests.
//
// Protocol Responsibility:
// - Exercises public runtime behavior for the corresponding service surface under success and failure scenarios.
// - Guards regressions in tenant isolation, terminal states, and typed error shaping.
//
// Implementation Approach:
// - Builds in-memory runtimes and drives tonic service methods directly for deterministic assertions.
// - Uses focused fixtures instead of full external environments so protocol invariants stay easy to localize.
//
// Usage Contract:
// - Add new assertions here before changing runtime behavior for the covered service area.
// - Prefer explicit value checks over timing-sensitive or order-fragile expectations.
//
// Architectural Position:
// - Runtime verification boundary protecting public contract stability.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package remotecontrol

import "testing"

func TestRemoteControlToolSpecsIncludeLifecycleParityTools(t *testing.T) {
	specs := remoteControlToolSpecs()
	names := map[string]bool{}
	for _, spec := range specs {
		name, _ := spec["name"].(string)
		names[name] = true
	}

	for _, required := range []string{
		"disconnect_device",
		"uninstall_ability",
		"build_ability_descriptor",
		"redeploy_ability",
		"forget_all",
	} {
		if !names[required] {
			t.Fatalf("expected tool spec %q to be present", required)
		}
	}
}

func TestRemoteControlDefaultInstallTimeoutMatchesCanonicalValue(t *testing.T) {
	if defaultInstallTimeoutSeconds != 45 {
		t.Fatalf("expected default install timeout to be 45 seconds, got %d", defaultInstallTimeoutSeconds)
	}
}

func TestRemoteControlToolSpecsExposeMetadataParity(t *testing.T) {
	specs := remoteControlToolSpecs()
	for _, toolName := range []string{"deploy_ability", "package_ability", "deploy_ability_package"} {
		var found map[string]any
		for _, spec := range specs {
			if spec["name"] == toolName {
				found = spec
				break
			}
		}
		if found == nil {
			t.Fatalf("expected tool spec %q to be present", toolName)
		}
		inputSchema, _ := found["inputSchema"].(map[string]any)
		properties, _ := inputSchema["properties"].(map[string]any)
		metadata, ok := properties["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("expected %q to expose metadata property", toolName)
		}
		if metadata["type"] != "object" {
			t.Fatalf("expected %q metadata property type to be object, got %v", toolName, metadata["type"])
		}
	}
}

func TestRemoteControlLifecycleToolSpecsExposeAgentExtensions(t *testing.T) {
	specs := remoteControlToolSpecs()
	for _, toolName := range []string{"build_ability_descriptor", "export_ability_skill"} {
		var found map[string]any
		for _, spec := range specs {
			if spec["name"] == toolName {
				found = spec
				break
			}
		}
		if found == nil {
			t.Fatalf("expected tool spec %q to be present", toolName)
		}
		inputSchema, _ := found["inputSchema"].(map[string]any)
		properties, _ := inputSchema["properties"].(map[string]any)
		if _, ok := properties["instructions"].(map[string]any); !ok {
			t.Fatalf("expected %q to expose instructions property", toolName)
		}
		if _, ok := properties["input_examples"].(map[string]any); !ok {
			t.Fatalf("expected %q to expose input_examples property", toolName)
		}
		if _, ok := properties["prerequisites"].(map[string]any); !ok {
			t.Fatalf("expected %q to expose prerequisites property", toolName)
		}
		if _, ok := properties["context_bindings"].(map[string]any); !ok {
			t.Fatalf("expected %q to expose context_bindings property", toolName)
		}
		if _, ok := properties["category"].(map[string]any); !ok {
			t.Fatalf("expected %q to expose category property", toolName)
		}
	}
}
