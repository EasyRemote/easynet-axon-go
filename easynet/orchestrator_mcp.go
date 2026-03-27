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

// MCPToolStream wraps a streaming MCP tool call, providing Next and Close operations.
type MCPToolStream struct {
	bridge       *DendriteBridge
	streamHandle uint64
}

// Next pulls the next chunk from the stream.
// If timeoutMs <= 0 the bridge layer applies its own default.
func (s *MCPToolStream) Next(timeoutMs int) (StreamNextResult, error) {
	return s.bridge.StreamNext(s.streamHandle, timeoutMs)
}

// Recv pulls the next chunk using the stream's configured default timeout.
func (s *MCPToolStream) Recv() (StreamNextResult, error) {
	return s.Next(0)
}

// Close closes the stream and releases its resources.
func (s *MCPToolStream) Close() error {
	return s.bridge.StreamClose(s.streamHandle)
}

// Handle returns the underlying stream handle for advanced use.
func (s *MCPToolStream) Handle() uint64 {
	return s.streamHandle
}

// CallMCPToolStream opens a streaming MCP tool call and returns an MCPToolStream for
// incremental consumption. The caller must call Close on the returned stream when done.
func (o *Orchestrator) CallMCPToolStream(
	toolName string,
	targetNodeID string,
	argumentsJSON any,
	timeoutMs int,
) (*MCPToolStream, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	if timeoutMs <= 0 {
		timeoutMs = DefaultMCPToolStreamTimeoutMs
	}
	sh, err := o.bridge.OpenMCPToolStream(o.handle, o.Tenant, toolName, targetNodeID, argumentsJSON, timeoutMs)
	if err != nil {
		return nil, err
	}
	return &MCPToolStream{
		bridge:       o.bridge,
		streamHandle: sh,
	}, nil
}
