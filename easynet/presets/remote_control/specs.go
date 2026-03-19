// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/specs.go
// Description: MCP tool specification definitions for the remote-control preset.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package remotecontrol

func remoteControlToolSpecs() []map[string]any {
	return []map[string]any{
		// -----------------------------------------------------------
		// GENERIC TOOLS -- core remote-control primitives
		// -----------------------------------------------------------
		{
			"name":        "discover_nodes",
			"description": "Discover online nodes registered with Axon Runtime.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{
						"type":        "string",
						"description": "Tenant ID (default AXON_TENANT)",
					},
				},
			},
		},
		{
			"name":        "list_remote_tools",
			"description": "List MCP tools visible for a tenant.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":    map[string]any{"type": "string"},
					"node_id":      map[string]any{"type": "string"},
					"name_pattern": map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "call_remote_tool",
			"description": "Call an MCP tool on a selected node.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{"type": "string"},
					"tool_name": map[string]any{"type": "string"},
					"node_id":   map[string]any{"type": "string"},
					"arguments": map[string]any{"type": "object"},
				},
				"required": []string{"tool_name", "node_id"},
			},
		},
		{
			"name":        "disconnect_device",
			"description": "Deregister a remote device from the Axon Runtime, closing its connection.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{"type": "string"},
					"node_id":   map[string]any{"type": "string"},
					"reason":    map[string]any{"type": "string"},
				},
				"required": []string{"node_id"},
			},
		},
		{
			"name":        "uninstall_ability",
			"description": "Uninstall a deployed ability from a device by deactivating and removing it.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":  map[string]any{"type": "string"},
					"node_id":    map[string]any{"type": "string"},
					"install_id": map[string]any{"type": "string"},
					"reason":     map[string]any{"type": "string"},
				},
				"required": []string{"node_id", "install_id"},
			},
		},
		// -----------------------------------------------------------
		// AI-AGENT PRESET TOOLS -- convenience wrappers for AI agents
		// -----------------------------------------------------------
		{
			"name":        "deploy_ability",
			"description": "Deploy a command-backed MCP ability.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":        map[string]any{"type": "string"},
					"node_id":          map[string]any{"type": "string"},
					"tool_name":        map[string]any{"type": "string"},
					"description":      map[string]any{"type": "string"},
					"command_template": map[string]any{"type": "string"},
				},
				"required": []string{"node_id", "command_template"},
			},
		},
		// -----------------------------------------------------------
		// GENERIC TOOLS (continued) -- ability packaging and deployment
		// -----------------------------------------------------------
		{
			"name":        "package_ability",
			"description": "Build a native ability package descriptor.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":        map[string]any{"type": "string"},
					"ability_name":     map[string]any{"type": "string"},
					"tool_name":        map[string]any{"type": "string"},
					"description":      map[string]any{"type": "string"},
					"command_template": map[string]any{"type": "string"},
					"input_schema":     map[string]any{"type": "object"},
					"output_schema":    map[string]any{"type": "object"},
					"version":          map[string]any{"type": "string"},
					"tags":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"package_id":       map[string]any{"type": "string"},
					"capability_name":  map[string]any{"type": "string"},
					"signature_base64": map[string]any{"type": "string"},
					"digest":           map[string]any{"type": "string"},
				},
				"required": []string{"ability_name", "command_template"},
			},
		},
		{
			"name":        "deploy_ability_package",
			"description": "Deploy a native ability package by publish/install/activate.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":                   map[string]any{"type": "string"},
					"node_id":                     map[string]any{"type": "string"},
					"package":                     map[string]any{"type": "object"},
					"ability_name":                map[string]any{"type": "string"},
					"tool_name":                   map[string]any{"type": "string"},
					"description":                 map[string]any{"type": "string"},
					"command_template":            map[string]any{"type": "string"},
					"input_schema":                map[string]any{"type": "object"},
					"output_schema":               map[string]any{"type": "object"},
					"version":                     map[string]any{"type": "string"},
					"tags":                        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"package_id":                  map[string]any{"type": "string"},
					"capability_name":             map[string]any{"type": "string"},
					"signature_base64":            map[string]any{"type": "string"},
					"cleanup_on_activate_failure": map[string]any{"type": "boolean"},
				},
				"required": []string{"node_id"},
			},
		},
		// AI-AGENT PRESET: one-shot command execution
		{
			"name":        "execute_command",
			"description": "One-shot command execution via temporary MCP ability.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{"type": "string"},
					"node_id":   map[string]any{"type": "string"},
					"command":   map[string]any{"type": "string"},
					"cleanup":   map[string]any{"type": "boolean"},
				},
				"required": []string{"node_id", "command"},
			},
		},
	}
}
