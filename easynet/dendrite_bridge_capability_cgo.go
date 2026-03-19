// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/dendrite_bridge_capability_cgo.go
// Description: Source file for Go SDK bridge and capability helper implementation; keeps behavior explicit and interoperable across language/runtime boundaries.
//
// Protocol Responsibility:
// - Implements Go SDK bridge and capability helper implementation contracts required by current Axon service and SDK surfaces.
// - Preserves stable request/response semantics and error mapping for dendrite_bridge_capability_cgo.go call paths.
//
// Implementation Approach:
// - Uses small typed helpers and explicit control flow to avoid hidden side effects.
// - Keeps protocol translation and transport details close to this module boundary.
//
// Usage Contract:
// - Callers should provide valid tenant/resource/runtime context before invoking exported APIs.
// - Errors should be treated as typed protocol/runtime outcomes rather than silently ignored.
//
// Architectural Position:
// - Part of the Go SDK bridge and capability helper implementation layer.
// - Should not embed unrelated orchestration logic outside this file's responsibility.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build cgo

package easynet

// PublishCapabilityWithRequest publishes a capability using a typed request object.
func (b *DendriteBridge) PublishCapabilityWithRequest(
	handle uint64,
	req PublishCapabilityRequest,
) (map[string]any, error) {

	if err := req.validate(); err != nil {
		return nil, err
	}
	payload, err := req.helperPayload()
	if err != nil {
		return nil, err
	}
	return b.callHelperPayload(handle, payload, b.sym.publishAbility)
}

// InstallCapabilityWithRequest installs a capability using a typed request object.
func (b *DendriteBridge) InstallCapabilityWithRequest(
	handle uint64,
	req InstallCapabilityRequest,
) (map[string]any, error) {

	if err := req.validate(); err != nil {
		return nil, err
	}
	payload, err := req.helperPayload()
	if err != nil {
		return nil, err
	}
	return b.callHelperPayload(handle, payload, b.sym.installAbility)
}

func (b *DendriteBridge) ActivateCapability(
	handle uint64,
	tenantID string,
	nodeID string,
	installID string,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id":  tenantID,
		"node_id":    nodeID,
		"install_id": installID,
	}
	return b.callHelperPayload(handle, payload, b.sym.activateAbility)
}

func (b *DendriteBridge) ListA2AAgents(
	handle uint64,
	tenantID string,
	tags []string,
	ownerID string,
	limit int,
) ([]map[string]any, error) {

	if tags == nil {
		tags = []string{}
	}
	payload := map[string]any{
		"tenant_id": tenantID,
		"tags":      tags,
		"owner_id":  ownerID,
		"limit":     limit,
	}
	return b.callHelperPayloadSlice(handle, payload, b.sym.listA2aAgents, "agents")
}

func (b *DendriteBridge) GetA2AAgentCard(
	handle uint64,
	tenantID string,
	nodeID string,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id": tenantID,
		"node_id":   nodeID,
	}
	return b.callHelperPayload(handle, payload, b.sym.getA2aAgentCard)
}

func (b *DendriteBridge) SendA2ATask(
	handle uint64,
	tenantID string,
	targetAgentID string,
	skillID string,
	inputJSON any,
	inputBase64 string,
	taskID string,
	idempotencyKey string,
) (map[string]any, error) {

	if inputJSON == nil {
		inputJSON = map[string]any{}
	}
	payload := map[string]any{
		"tenant_id":       tenantID,
		"target_agent_id": targetAgentID,
		"skill_id":        skillID,
		"input_json":      inputJSON,
		"input_base64":    inputBase64,
		"task_id":         taskID,
		"idempotency_key": idempotencyKey,
	}
	return b.callHelperPayload(handle, payload, b.sym.sendA2aTask)
}

func (b *DendriteBridge) DeployMCPListDir(
	handle uint64,
	tenantID string,
	nodeID string,
	targetPath string,
	commandTemplate string,
) (map[string]any, error) {

	return b.DeployMCPListDirWithRequest(handle, DeployMCPListDirRequest{
		TenantID:        tenantID,
		NodeID:          nodeID,
		TargetPath:      targetPath,
		CommandTemplate: commandTemplate,
	})
}

// DeployMCPListDirWithRequest deploys an MCP list-dir capability on a node with a typed request object.
func (b *DendriteBridge) DeployMCPListDirWithRequest(
	handle uint64,
	req DeployMCPListDirRequest,
) (map[string]any, error) {

	req.applyDefaults()
	if err := req.validate(); err != nil {
		return nil, err
	}
	resolvedSignature, err := resolveDeploySignatureBase64(req.SignatureBase64)
	if err != nil {
		return nil, err
	}
	req.SignatureBase64 = resolvedSignature
	payload, err := req.helperPayload()
	if err != nil {
		return nil, err
	}
	return b.callHelperPayload(handle, payload, b.sym.deployListDir)
}

func (b *DendriteBridge) ListMCPTools(
	handle uint64,
	tenantID string,
	namePattern string,
	tags []string,
	nodeID string,
) ([]map[string]any, error) {

	payload := map[string]any{
		"tenant_id":    tenantID,
		"name_pattern": namePattern,
		"tags":         tags,
		"node_id":      nodeID,
	}
	return b.callHelperPayloadSlice(handle, payload, b.sym.listMcpTools, "tools")
}

func (b *DendriteBridge) CallMCPTool(
	handle uint64,
	tenantID string,
	toolName string,
	targetNodeID string,
	argumentsJSON any,
) (map[string]any, error) {

	if argumentsJSON == nil {
		argumentsJSON = map[string]any{}
	}
	payload := map[string]any{
		"tenant_id":      tenantID,
		"tool_name":      toolName,
		"target_node_id": targetNodeID,
		"arguments_json": argumentsJSON,
	}
	return b.callHelperPayload(handle, payload, b.sym.callMcpTool)
}

// UninstallCapabilityWithRequest uninstalls a capability using a typed request object.
func (b *DendriteBridge) UninstallCapabilityWithRequest(handle uint64, req UninstallCapabilityRequest) (map[string]any, error) {
	payload, err := req.helperPayload()
	if err != nil {
		return nil, err
	}
	return b.callHelperPayload(handle, payload, b.sym.uninstall)
}

func (b *DendriteBridge) UninstallCapability(
	handle uint64,
	tenantID string,
	nodeID string,
	installID string,
	deactivateFirst bool,
	deactivateReason string,
	force bool,
) (map[string]any, error) {
	return b.UninstallCapabilityWithRequest(handle, UninstallCapabilityRequest{
		TenantID:         tenantID,
		NodeID:           nodeID,
		InstallID:        installID,
		DeactivateFirst:  &deactivateFirst,
		DeactivateReason: deactivateReason,
		Force:            &force,
	})
}

func (b *DendriteBridge) UpdateMCPListDir(
	handle uint64,
	tenantID string,
	nodeID string,
	existingInstallID string,
	targetPath string,
	commandTemplate string,
	version string,
) (map[string]any, error) {

	return b.UpdateMCPListDirWithRequest(handle, UpdateMCPListDirRequest{
		TenantID:          tenantID,
		NodeID:            nodeID,
		ExistingInstallID: existingInstallID,
		TargetPath:        targetPath,
		CommandTemplate:   commandTemplate,
		Version:           version,
	})
}

// UpdateMCPListDirWithRequest replaces an existing MCP list-dir install using a typed request object.
func (b *DendriteBridge) UpdateMCPListDirWithRequest(
	handle uint64,
	req UpdateMCPListDirRequest,
) (map[string]any, error) {

	req.applyDefaults()
	if err := req.validate(); err != nil {
		return nil, err
	}
	resolvedSignature, err := resolveDeploySignatureBase64(req.SignatureBase64)
	if err != nil {
		return nil, err
	}
	req.SignatureBase64 = resolvedSignature
	payload, err := req.helperPayload()
	if err != nil {
		return nil, err
	}
	return b.callHelperPayload(handle, payload, b.sym.updateListDir)
}

func (b *DendriteBridge) ListNodes(handle uint64, tenantID string, ownerID string) ([]map[string]any, error) {

	payload := map[string]any{
		"tenant_id": tenantID,
		"owner_id":  ownerID,
	}
	return b.callHelperPayloadSlice(handle, payload, b.sym.listNodes, "nodes")
}

func (b *DendriteBridge) RegisterNode(
	handle uint64,
	tenantID string,
	nodeID string,
	displayName string,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id":    tenantID,
		"node_id":      nodeID,
		"display_name": displayName,
	}
	return b.callHelperPayload(handle, payload, b.sym.registerNode)
}

func (b *DendriteBridge) Heartbeat(
	handle uint64,
	tenantID string,
	nodeID string,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id": tenantID,
		"node_id":   nodeID,
	}
	return b.callHelperPayload(handle, payload, b.sym.heartbeat)
}

func (b *DendriteBridge) DeregisterNode(
	handle uint64,
	tenantID string,
	nodeID string,
	reason string,
) (map[string]any, error) {
	if b.sym.deregisterNode == nil {
		return nil, DendriteError{
			Message: "dendrite bridge symbol not available: axon_dendrite_deregister_node_json",
			Code:    "BRIDGE",
		}
	}

	payload := map[string]any{
		"tenant_id": tenantID,
		"node_id":   nodeID,
		"reason":    reason,
	}
	return b.callHelperPayload(handle, payload, b.sym.deregisterNode)
}

func (b *DendriteBridge) DrainNode(
	handle uint64,
	tenantID string,
	nodeID string,
	reason string,
) (map[string]any, error) {
	if b.sym.drainNode == nil {
		return nil, DendriteError{
			Message: "dendrite bridge symbol not available: axon_dendrite_drain_node_json",
			Code:    "BRIDGE",
		}
	}

	payload := map[string]any{
		"tenant_id": tenantID,
		"node_id":   nodeID,
		"reason":    reason,
	}
	return b.callHelperPayload(handle, payload, b.sym.drainNode)
}
