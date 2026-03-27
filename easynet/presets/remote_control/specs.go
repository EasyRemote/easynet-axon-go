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
			"description": "Discover online nodes registered with Axon Runtime. Use this first to find available devices before calling other tools. Returns a list of nodes with their online status.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{
						"type":        "string",
						"description": "Tenant ID (default AXON_TENANT).",
					},
				},
			},
		},
		{
			"name":        "list_remote_tools",
			"description": "List MCP tools visible for a tenant. Use after discover_nodes to see what tools are available on a specific node or across all nodes.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":    map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":      map[string]any{"type": "string", "description": "Filter tools by a specific node."},
					"name_pattern": map[string]any{"type": "string", "description": "Glob pattern to filter tool names (e.g. 'session_*')."},
				},
			},
		},
		{
			"name":        "call_remote_tool",
			"description": "Call an MCP tool on a selected node and return the full result. Use for quick operations that return a single response. For long-running or streaming tools, prefer call_remote_tool_stream.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"tool_name": map[string]any{"type": "string", "description": "Name of the MCP tool to invoke."},
					"node_id":   map[string]any{"type": "string", "description": "Target node ID (from discover_nodes)."},
					"arguments": map[string]any{"type": "object", "description": "Tool-specific arguments passed to the remote tool."},
				},
				"required": []string{"tool_name", "node_id"},
			},
		},
		{
			"name":        "call_remote_tool_stream",
			"description": "Call an MCP tool on a selected node and stream incremental response chunks. Prefer this for long-running or incremental-output tools instead of call_remote_tool. Falls back to buffered response when streaming is unavailable.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":  map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"tool_name":  map[string]any{"type": "string", "description": "Name of the MCP tool to invoke."},
					"node_id":    map[string]any{"type": "string", "description": "Target node ID (from discover_nodes)."},
					"arguments":  map[string]any{"type": "object", "description": "Tool-specific arguments passed to the remote tool."},
					"timeout_ms": map[string]any{"type": "integer", "description": "Per-chunk timeout in milliseconds for streaming reads (default 60000)."},
					"max_bytes":  map[string]any{"type": "integer", "description": "Maximum total bytes accepted from the stream (default 64 MiB). Set higher for large transfers."},
				},
				"required": []string{"tool_name", "node_id"},
			},
		},
		{
			"name":        "disconnect_device",
			"description": "Deregister a remote device from the Axon Runtime, closing its connection. Use when a device should be removed from the active node list.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":   map[string]any{"type": "string", "description": "Node ID of the device to disconnect."},
					"reason":    map[string]any{"type": "string", "description": "Human-readable reason for disconnection."},
				},
				"required": []string{"node_id"},
			},
		},
		{
			"name":        "uninstall_ability",
			"description": "Uninstall a deployed ability from a device by deactivating and removing it. Use to clean up abilities that are no longer needed.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":  map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":    map[string]any{"type": "string", "description": "Target node ID."},
					"install_id": map[string]any{"type": "string", "description": "Installation ID returned by deploy_ability or deploy_ability_package."},
					"reason":     map[string]any{"type": "string", "description": "Human-readable reason for uninstallation."},
				},
				"required": []string{"node_id", "install_id"},
			},
		},
		// -----------------------------------------------------------
		// AI-AGENT PRESET TOOLS -- convenience wrappers for AI agents
		// -----------------------------------------------------------
		{
			"name":        "deploy_ability",
			"description": "Deploy a simple command-backed MCP ability to a device. This is the easiest way to make a command available as a remote tool. For advanced deployment options (custom schemas, versioning, signatures), use deploy_ability_package instead.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":        map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":          map[string]any{"type": "string", "description": "Target node ID to deploy the ability to."},
					"tool_name":        map[string]any{"type": "string", "description": "Name for the new MCP tool (auto-generated if omitted)."},
					"description":      map[string]any{"type": "string", "description": "Human-readable description of what this tool does."},
					"command_template": map[string]any{"type": "string", "description": "Shell command template to execute on the device."},
					"metadata":         map[string]any{"type": "object", "description": "Additional metadata for the ability."},
				},
				"required": []string{"node_id", "command_template"},
			},
		},
		// -----------------------------------------------------------
		// GENERIC TOOLS (continued) -- ability packaging and deployment
		// -----------------------------------------------------------
		{
			"name":        "package_ability",
			"description": "Build a native ability package descriptor without deploying. Use this to prepare a package for later deployment with deploy_ability_package, or to inspect the descriptor before committing.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":        map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"ability_name":     map[string]any{"type": "string", "description": "Unique name for the ability."},
					"tool_name":        map[string]any{"type": "string", "description": "MCP tool name exposed after deployment."},
					"description":      map[string]any{"type": "string", "description": "Human-readable description."},
					"command_template": map[string]any{"type": "string", "description": "Shell command template to execute."},
					"input_schema":     map[string]any{"type": "object", "description": "JSON Schema for tool input validation."},
					"output_schema":    map[string]any{"type": "object", "description": "JSON Schema for tool output."},
					"version":          map[string]any{"type": "string", "description": "Semantic version (default '1.0.0')."},
					"tags":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tags for categorization."},
					"metadata":         map[string]any{"type": "object", "description": "Additional metadata."},
					"package_id":       map[string]any{"type": "string", "description": "Custom package identifier."},
					"capability_name":  map[string]any{"type": "string", "description": "Override capability name in the registry."},
					"signature_base64": map[string]any{"type": "string", "description": "Base64-encoded deployment signature."},
					"digest":           map[string]any{"type": "string", "description": "Content digest for integrity verification."},
				},
				"required": []string{"ability_name", "command_template"},
			},
		},
		{
			"name":        "deploy_ability_package",
			"description": "Deploy a native ability package through the full publish/install/activate pipeline. Use this for advanced deployments with custom schemas, versioning, or pre-built package descriptors. For simple command deployments, prefer deploy_ability.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":                   map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":                     map[string]any{"type": "string", "description": "Target node ID to deploy to."},
					"package":                     map[string]any{"type": "object", "description": "Pre-built package descriptor (from package_ability). If provided, other descriptor fields are ignored."},
					"ability_name":                map[string]any{"type": "string", "description": "Unique name for the ability."},
					"tool_name":                   map[string]any{"type": "string", "description": "MCP tool name exposed after deployment."},
					"description":                 map[string]any{"type": "string", "description": "Human-readable description."},
					"command_template":            map[string]any{"type": "string", "description": "Shell command template to execute."},
					"input_schema":                map[string]any{"type": "object", "description": "JSON Schema for tool input validation."},
					"output_schema":               map[string]any{"type": "object", "description": "JSON Schema for tool output."},
					"version":                     map[string]any{"type": "string", "description": "Semantic version (default '1.0.0')."},
					"tags":                        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tags for categorization."},
					"metadata":                    map[string]any{"type": "object", "description": "Additional metadata."},
					"package_id":                  map[string]any{"type": "string", "description": "Custom package identifier."},
					"capability_name":             map[string]any{"type": "string", "description": "Override capability name in the registry."},
					"signature_base64":            map[string]any{"type": "string", "description": "Base64-encoded deployment signature."},
					"cleanup_on_activate_failure": map[string]any{"type": "boolean", "description": "Auto-cleanup on activation failure (default true)."},
				},
				"required": []string{"node_id"},
			},
		},
		// AI-AGENT PRESET: one-shot command execution
		{
			"name":        "execute_command",
			"description": "One-shot command execution on a remote device. Deploys a temporary ability, runs the command, returns the output, and cleans up. Use for ad-hoc commands that don't need a persistent tool.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":   map[string]any{"type": "string", "description": "Target node ID to execute on."},
					"command":   map[string]any{"type": "string", "description": "Shell command to execute on the device."},
					"cleanup":   map[string]any{"type": "boolean", "description": "Remove the temporary ability after execution (default true)."},
				},
				"required": []string{"node_id", "command"},
			},
		},
	}
}
