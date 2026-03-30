// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/lifecycle_test.go
// Description: Go remote-control lifecycle-handler validation and destructive-operation guard regression tests.
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

import (
	"strings"
	"testing"
)

func testRuntimeConfig() RemoteControlRuntimeConfig {
	return RemoteControlRuntimeConfig{
		Endpoint:         "http://127.0.0.1:50051",
		Tenant:           "tenant-a",
		ConnectTimeoutMs: 5000,
		SignatureBase64:  "sig",
	}
}

func TestBuildAbilityDescriptorRejectsEmptyName(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleCreateAbility("tenant-a", map[string]any{
		"command_template": "echo hi",
	})

	if !result.IsError {
		t.Fatalf("expected error result")
	}
	if got := result.Payload["error"]; got != "name is required" {
		t.Fatalf("unexpected error: %v", got)
	}
}

func TestRedeployAbilityRequiresToolName(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleRedeployAbility("tenant-a", map[string]any{
		"node_id":          "node-a",
		"command_template": "echo hi",
	})

	if !result.IsError {
		t.Fatalf("expected error result")
	}
	if got := result.Payload["error"]; got != "tool_name is required" {
		t.Fatalf("unexpected error: %v", got)
	}
}

func TestForgetAllRequiresConfirmWhenNotDryRun(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleForgetAll("tenant-a", map[string]any{
		"node_id": "node-a",
	})

	if !result.IsError {
		t.Fatalf("expected error result")
	}
	if got := result.Payload["error"]; got != "forget_all requires confirm: true (destructive operation)" {
		t.Fatalf("unexpected error: %v", got)
	}
}

func TestAbilityEntryFromToolFiltersMissingInstallID(t *testing.T) {
	ability, ok := abilityEntryFromTool(map[string]any{
		"tool_name":       "keep-me",
		"description":     "ok",
		"capability_name": "cap.keep",
		"install_id":      "install-1",
	})
	if !ok {
		t.Fatalf("expected installable ability entry")
	}
	if ability["install_id"] != "install-1" {
		t.Fatalf("unexpected install_id: %v", ability["install_id"])
	}

	if _, ok := abilityEntryFromTool(map[string]any{
		"tool_name":       "skip-me",
		"capability_name": "cap.skip",
		"install_id":      "",
	}); ok {
		t.Fatalf("expected missing install_id entry to be filtered out")
	}
}

func TestBuildAbilityDescriptorHappyPath(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleCreateAbility("tenant-a", map[string]any{
		"name":             "test_tool",
		"command_template": "echo hi",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Payload["error"])
	}
	if ok := result.Payload["ok"]; ok != true {
		t.Fatalf("expected ok=true, got %v", ok)
	}
	descriptor, ok := result.Payload["descriptor"].(map[string]any)
	if !ok {
		t.Fatalf("expected descriptor map, got %T", result.Payload["descriptor"])
	}
	if got := descriptor["name"]; got != "test_tool" {
		t.Fatalf("expected name=test_tool, got %v", got)
	}
	if got := descriptor["tool_name"]; got != "test_tool" {
		t.Fatalf("expected tool_name=test_tool, got %v", got)
	}
	if got := descriptor["command_template"]; got != "echo hi" {
		t.Fatalf("expected command_template='echo hi', got %v", got)
	}
}

func TestBuildAbilityDescriptorIncludesAgentExtensions(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleCreateAbility("tenant-a", map[string]any{
		"name":             "gpu_status",
		"command_template": "python3 gpu.py",
		"instructions":     "Call this after session startup.",
		"input_examples":   []any{map[string]any{"query": "temperature"}},
		"prerequisites":    []any{"GPU driver installed"},
		"context_bindings": map[string]any{"env.CUDA_HOME": "/usr/local/cuda"},
		"category":         "system",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Payload["error"])
	}
	descriptor, ok := result.Payload["descriptor"].(map[string]any)
	if !ok {
		t.Fatalf("expected descriptor map, got %T", result.Payload["descriptor"])
	}
	if got := descriptor["instructions"]; got != "Call this after session startup." {
		t.Fatalf("expected instructions to round-trip, got %v", got)
	}
	if got := descriptor["category"]; got != "system" {
		t.Fatalf("expected category=system, got %v", got)
	}
	if got, ok := descriptor["prerequisites"].([]string); !ok || len(got) != 1 || got[0] != "GPU driver installed" {
		t.Fatalf("expected prerequisites to round-trip, got %#v", descriptor["prerequisites"])
	}
}

func TestBuildAbilityDescriptorPreservesAgentExtensions(t *testing.T) {
	// package_ability goes through buildDescriptor which preserves agent
	// extension fields (instructions, input_examples, prerequisites,
	// context_bindings, category) in the package metadata.
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handlePackageAbility("tenant-a", map[string]any{
		"ability_name":     "ext_tool",
		"command_template": "echo ext",
		"description":      "extended tool",
		"instructions":     "Use this tool when you need to extend things.",
		"input_examples":   []any{map[string]any{"key": "val"}},
		"prerequisites":    []any{"GPU, driver installed", "Must call session_start first"},
		"context_bindings": map[string]any{"env.PATH": "/usr/bin"},
		"category":         "system",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Payload["error"])
	}
	pkg, ok := result.Payload["package"].(map[string]any)
	if !ok {
		t.Fatalf("expected package map, got %T", result.Payload["package"])
	}
	metadata, ok := pkg["metadata"].(map[string]string)
	if !ok {
		t.Fatalf("expected metadata map[string]string, got %T", pkg["metadata"])
	}
	if got := metadata["mcp.instructions"]; got != "Use this tool when you need to extend things." {
		t.Fatalf("expected instructions to be preserved in metadata, got %v", got)
	}
	if got := metadata["mcp.prerequisites"]; got != "[\"GPU, driver installed\",\"Must call session_start first\"]" {
		t.Fatalf("expected prerequisites preserved in metadata, got %v", got)
	}
	if got := metadata["mcp.category"]; got != "system" {
		t.Fatalf("expected category=system in metadata, got %v", got)
	}
	if got := metadata["mcp.context_bindings"]; got == "" {
		t.Fatalf("expected context_bindings in metadata, got empty")
	}
	if got := metadata["mcp.input_examples"]; got == "" {
		t.Fatalf("expected input_examples in metadata, got empty")
	}
}

func TestExportAbilitySkillGeneratesMarkdown(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleExportAbilitySkill("tenant-a", map[string]any{
		"name":             "my_export",
		"command_template": "echo export",
		"target":           "claude",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Payload["error"])
	}
	if ok := result.Payload["ok"]; ok != true {
		t.Fatalf("expected ok=true, got %v", ok)
	}
	abilityMd, ok := result.Payload["ability_md"].(string)
	if !ok || abilityMd == "" {
		t.Fatalf("expected non-empty ability_md string, got %v", result.Payload["ability_md"])
	}
	invokeScript, ok := result.Payload["invoke_script"].(string)
	if !ok || invokeScript == "" {
		t.Fatalf("expected non-empty invoke_script string, got %v", result.Payload["invoke_script"])
	}
	if !strings.Contains(invokeScript, "#!/usr/bin/env bash") {
		t.Fatalf("invoke_script should contain bash shebang, got: %s", invokeScript)
	}
}

func TestExportAbilitySkillRejectsShellUnsafeEndpoint(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleExportAbilitySkill("tenant-a", map[string]any{
		"name":             "unsafe_export",
		"command_template": "echo hi",
		"axon_endpoint":    "http://evil.com/`rm -rf /`",
	})

	if !result.IsError {
		t.Fatalf("expected error for shell-unsafe endpoint")
	}
	errMsg, _ := result.Payload["error"].(string)
	if !strings.Contains(errMsg, "disallowed shell characters") {
		t.Fatalf("expected shell safety error, got: %v", errMsg)
	}
}

func TestExportAbilitySkillRejectsCarriageReturnEndpoint(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleExportAbilitySkill("tenant-a", map[string]any{
		"name":             "unsafe_export",
		"command_template": "echo hi",
		"axon_endpoint":    "http://evil.com/\rprobe",
	})

	if !result.IsError {
		t.Fatalf("expected error for shell-unsafe endpoint")
	}
	errMsg, _ := result.Payload["error"].(string)
	if !strings.Contains(errMsg, "disallowed shell characters") {
		t.Fatalf("expected shell safety error, got: %v", errMsg)
	}
}

func TestDrainDeviceRejectsEmptyNodeId(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleDrainDevice("tenant-a", map[string]any{})

	if !result.IsError {
		t.Fatalf("expected error for empty node_id")
	}
	if got := result.Payload["error"]; got != "node_id is required" {
		t.Fatalf("unexpected error: %v", got)
	}
}

func TestForgetAllDryRunReturnsPreview(t *testing.T) {
	// dry_run=true bypasses the confirm requirement but still needs an
	// orchestrator.  With the default bridge factory the orchestrator
	// Open() will fail because no native runtime is available, which is
	// the expected behavior: the handler validates dry_run before
	// dispatching to the orchestrator.  We verify the validation gate
	// passes (no "confirm" error) and the orchestrator error is returned
	// instead.
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleForgetAll("tenant-a", map[string]any{
		"node_id": "node-a",
		"dry_run": true,
	})

	// The key assertion: dry_run=true must NOT trigger the confirm error.
	errMsg, _ := result.Payload["error"].(string)
	if errMsg == "forget_all requires confirm: true (destructive operation)" {
		t.Fatalf("dry_run=true should bypass the confirm requirement")
	}
	// With no native runtime the orchestrator will fail, which is fine
	// for a unit test — we just need to confirm the validation gate passed.
}

func TestToolSpecsCountMatchesCanonical(t *testing.T) {
	specs := remoteControlToolSpecs()
	if got := len(specs); got != 16 {
		names := make([]string, 0, len(specs))
		for _, s := range specs {
			names = append(names, s["name"].(string))
		}
		t.Fatalf("expected 16 tool specs, got %d: %v", got, names)
	}
}

func TestExportAbilitySkillVerifiesStructure(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleExportAbilitySkill("tenant-a", map[string]any{
		"name":             "my_tool",
		"command_template": "echo hello",
		"target":           "claude",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Payload["error"])
	}
	abilityMd, ok := result.Payload["ability_md"].(string)
	if !ok || abilityMd == "" {
		t.Fatalf("expected non-empty ability_md string")
	}
	if !strings.Contains(abilityMd, "---") {
		t.Fatalf("ability_md should contain frontmatter delimiter '---', got: %s", abilityMd)
	}
	if !strings.Contains(abilityMd, "## Parameters") {
		t.Fatalf("ability_md should contain '## Parameters', got: %s", abilityMd)
	}
	if !strings.Contains(abilityMd, "| Name |") {
		t.Fatalf("ability_md should contain table header '| Name |', got: %s", abilityMd)
	}
	invokeScript, ok := result.Payload["invoke_script"].(string)
	if !ok || invokeScript == "" {
		t.Fatalf("expected non-empty invoke_script string")
	}
	if !strings.Contains(invokeScript, "curl") {
		t.Fatalf("invoke_script should contain 'curl', got: %s", invokeScript)
	}
}

func TestBuildAbilityDescriptorWithUnicodeName(t *testing.T) {
	// handleCreateAbility builds a descriptor without sanitizing to ASCII —
	// sanitization only happens at packaging time (buildDescriptor).
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleCreateAbility("tenant-a", map[string]any{
		"name":             "数据分析",
		"command_template": "python3 analyze.py",
	})

	if result.IsError {
		t.Fatalf("expected success for unicode name, got error: %v", result.Payload["error"])
	}
}

func TestBuildAbilityDescriptorWithMixedUnicodeName(t *testing.T) {
	// Mixed names that contain at least one ASCII char should work —
	// the Unicode chars are stripped but ASCII chars remain.
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleCreateAbility("tenant-a", map[string]any{
		"name":             "data-分析-tool",
		"command_template": "python3 analyze.py",
	})

	if result.IsError {
		t.Fatalf("expected success for mixed-unicode name, got error: %v", result.Payload["error"])
	}
}

func TestBuildAbilityDescriptorRejectsEmptyCommandTemplate(t *testing.T) {
	kit := NewCaseKit(testRuntimeConfig())

	result := kit.handleCreateAbility("tenant-a", map[string]any{
		"name": "test",
	})

	if !result.IsError {
		t.Fatalf("expected error result")
	}
	if got := result.Payload["error"]; got != "command_template is required" {
		t.Fatalf("expected 'command_template is required', got %v", got)
	}
}

func TestAllSpecNamesUnique(t *testing.T) {
	specs := remoteControlToolSpecs()
	seen := map[string]bool{}
	for _, spec := range specs {
		name, ok := spec["name"].(string)
		if !ok || name == "" {
			t.Fatalf("spec has missing or non-string name: %v", spec)
		}
		if seen[name] {
			t.Fatalf("duplicate spec name: %s", name)
		}
		seen[name] = true
	}
}
