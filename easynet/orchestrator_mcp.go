// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/orchestrator_mcp.go
// Description: MCP-related orchestrator helpers — deploy/update MCP list-dir capabilities,
//              list and call MCP tools.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import "strings"

// DeployMCPListDir deploys an MCP list-dir capability to a node using a typed request.
func (o *Orchestrator) DeployMCPListDir(nodeID string, req DeployMCPListDirRequest) (map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	// Fill required routing fields from orchestrator context when empty.
	if strings.TrimSpace(req.TenantID) == "" {
		req.TenantID = o.Tenant
	}
	if strings.TrimSpace(req.NodeID) == "" {
		req.NodeID = nodeID
	}
	return o.bridge.DeployMCPListDirWithRequest(o.handle, req)
}

// UpdateMCPListDir updates/replaces an existing MCP list-dir install.
func (o *Orchestrator) UpdateMCPListDir(nodeID string, req UpdateMCPListDirRequest) (map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.TenantID) == "" {
		req.TenantID = o.Tenant
	}
	if strings.TrimSpace(req.NodeID) == "" {
		req.NodeID = nodeID
	}
	return o.bridge.UpdateMCPListDirWithRequest(o.handle, req)
}

// ListMCPTools lists MCP tools optionally filtered by name/tags and scoped to a node.
func (o *Orchestrator) ListMCPTools(namePattern string, tags []string, nodeID string) ([]map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	return o.bridge.ListMCPTools(o.handle, o.Tenant, namePattern, tags, nodeID)
}

// CallMCPTool invokes an MCP tool by name on a target node with JSON arguments.
func (o *Orchestrator) CallMCPTool(toolName string, targetNodeID string, argumentsJSON any) (map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	return o.bridge.CallMCPTool(o.handle, o.Tenant, toolName, targetNodeID, argumentsJSON)
}
