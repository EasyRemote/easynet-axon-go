// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/orchestrator_device.go
// Description: Go device-management orchestrator helpers that wrap bridge-level capability and node operations in public ability terminology.
//
// Protocol Responsibility:
// - Wraps low-level bridge operations in remote-control terminology such as ability, device, and tool workflows.
// - Centralizes publish, install, invoke, cleanup, and list flows shared by remote-control handlers.
//
// Implementation Approach:
// - Keeps bridge semantics explicit while translating internal capability and node naming into public preset APIs.
// - Holds connection and timeout policy close to the transport boundary so handler code stays declarative.
//
// Usage Contract:
// - Callers should construct or reuse orchestrators with valid endpoint, tenant, and native-library context.
// - Close or release orchestrator resources when the preset is no longer serving requests.
//
// Architectural Position:
// - Mid-layer adapter between remote-control handlers and bridge or client transport implementations.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import "strings"

// DisconnectDevice deregisters a device (node) from the runtime.
//
// The node is removed from the topology immediately; any in-flight
// invocations targeting it will fail with NOT_FOUND.  The reason string
// is recorded in the audit log and defaults to "sdk: disconnect_device"
// when empty.
//
// Bridge mapping: bridge.DeregisterNode (internal "node" terminology).
func (o *Orchestrator) DisconnectDevice(nodeID string, reason string) (map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "sdk: disconnect_device"
	}
	return o.bridge.DeregisterNode(o.handle, o.Tenant, nodeID, reason)
}

// UninstallAbility removes an installed ability from a node.
//
// This is the public API method aligned with the Ability/Skill/Tool
// terminology freeze.  Internally it delegates to the bridge's
// UninstallCapability FFI call — "Capability" is the internal RPC/FFI
// layer name and must not be changed without a coordinated native
// library release.
func (o *Orchestrator) UninstallAbility(
	nodeID string,
	installID string,
	deactivateReason string,
) (map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(deactivateReason)
	if reason == "" {
		reason = o.uninstallReason
	}
	return o.bridge.UninstallCapability(o.handle, o.Tenant, nodeID, installID, true, reason, false)
}

// UninstallCapability is a deprecated alias for [UninstallAbility].
// Kept for backwards compatibility — new code should use UninstallAbility.
//
// Deprecated: Use UninstallAbility instead.
func (o *Orchestrator) UninstallCapability(
	nodeID string,
	installID string,
	deactivateReason string,
) (map[string]any, error) {
	return o.UninstallAbility(nodeID, installID, deactivateReason)
}

// DrainDevice gracefully drains a device — the runtime stops routing new
// invocations to it while allowing in-flight ones to complete.  Use this
// before disconnecting to avoid dropped requests.
//
// Bridge mapping: bridge.DrainNode (internal "node" terminology).
func (o *Orchestrator) DrainDevice(nodeID string, reason string) (map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "sdk: drain_device"
	}
	return o.bridge.DrainNode(o.handle, o.Tenant, nodeID, reason)
}

// ListAbilities queries all deployed abilities (MCP tools) on a remote device.
//
// Returns the full tool entry from the runtime including input_schema,
// output_schema, hints, instructions, examples, prerequisites,
// context_bindings, and category — so that AI agents have complete
// metadata for reasoning about available abilities.
//
// Bridge mapping: bridge.ListMCPTools with empty name pattern (all tools).
func (o *Orchestrator) ListAbilities(nodeID string) ([]map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	return o.bridge.ListMCPTools(o.handle, o.Tenant, "", nil, nodeID)
}

// ForgetAll removes all deployed abilities from a device.
//
// This is a destructive operation — it uninstalls every ability on the
// target node.  Requires confirm=true as a safety gate; without it the
// call is rejected.  When dryRun is true, returns the list of abilities
// that *would* be removed (and those that *cannot* be removed due to
// missing install_id) without performing any actual uninstalls.
//
// The returned ForgetAllResult includes separate Removed and Failed lists
// so callers can audit partial-success scenarios.
func (o *Orchestrator) ForgetAll(nodeID string, confirm bool, dryRun bool) (*ForgetAllResult, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	var opts []ForgetAllOptions
	if dryRun {
		opts = append(opts, ForgetAllOptions{DryRun: true})
	}
	return ForgetAll(o.bridge, o.handle, o.Tenant, nodeID, confirm, opts...)
}

// DeployAbilityDescriptor deploys an AbilityDescriptor to a device via the
// full MCP deploy pipeline (publish → install → activate).
//
// The descriptor carries all metadata needed to materialise the ability on
// the target node, including agent extension properties (instructions,
// input_examples, prerequisites, context_bindings, category) which are
// encoded into the metadata map under the "mcp." prefix.
//
// The signature is verified by the runtime to ensure the package has not
// been tampered with during transit.
func (o *Orchestrator) DeployAbilityDescriptor(nodeID string, descriptor *AbilityDescriptor, signature string) (*DeployResult, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	return DeployToNode(o.bridge, o.handle, o.Tenant, nodeID, descriptor, signature)
}
