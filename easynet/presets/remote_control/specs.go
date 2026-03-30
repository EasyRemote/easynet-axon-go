// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/specs.go
// Description: Go definitions for the canonical remote-control MCP tool catalog and JSON schemas.
//
// Protocol Responsibility:
// - Publishes the canonical remote-control MCP tool catalog, descriptions, and input schemas for agents.
// - Keeps tool names and schema-visible behavior aligned across all first-party SDK implementations.
//
// Implementation Approach:
// - Represents tool contracts as static data builders plus small helper combinators for shared schema fragments.
// - Favors explicit schema literals so parity checks can diff outputs deterministically across languages.
//
// Usage Contract:
// - Any rename or schema change here must be mirrored across SDKs and covered by parity tests.
// - Consumers should treat these definitions as externally visible protocol metadata, not UI-only hints.
//
// Architectural Position:
// - Contract catalog layer for the remote-control preset.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package remotecontrol

// agentExtensionProperties returns the 5 agent-facing properties shared across
// deploy_ability, package_ability, and deploy_ability_package tool specs.
func agentExtensionProperties() map[string]any {
	return map[string]any{
		"instructions":     map[string]any{"type": "string", "description": "Full operational instructions for AI agents (SKILL.md equivalent, <5000 tokens). Explain what the tool does, when to use it, step-by-step usage, and error handling."},
		"input_examples":   map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "Example input objects conforming to input_schema. Helps agents understand invocation patterns."},
		"prerequisites":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Requirements before calling this tool (e.g. 'Must call session_start first')."},
		"context_bindings": map[string]any{"type": "object", "description": "Context bindings declaring what this ability accesses (e.g. env.PYTHON_PATH, resource.camera)."},
		"category":         map[string]any{"type": "string", "description": "Tool category for grouping (e.g. 'session', 'filesystem', 'network', 'system')."},
	}
}

// mergeProperties returns a new map containing all entries from base and overlay.
// Neither input map is mutated.
func mergeProperties(base, overlay map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range overlay {
		merged[k] = v
	}
	return merged
}

// remoteControlToolSpecs returns the canonical tool specification list for
// the remote-control preset.  Each entry is a map with "name", "description",
// and "inputSchema" keys that the MCP runtime exposes to AI agents.
//
// The 16 tools are grouped:
//   - Generic (6): discover_nodes, list_remote_tools, call_remote_tool,
//     call_remote_tool_stream, disconnect_device, uninstall_ability
//   - Packaging (4): deploy_ability, package_ability, deploy_ability_package,
//     execute_command
//   - Lifecycle (6): drain_device, build_ability_descriptor,
//     export_ability_skill, redeploy_ability, list_abilities, forget_all
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
				"properties": mergeProperties(map[string]any{
					"tenant_id":        map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":          map[string]any{"type": "string", "description": "Target node ID to deploy the ability to."},
					"tool_name":        map[string]any{"type": "string", "description": "Name for the new MCP tool (auto-generated if omitted)."},
					"description":      map[string]any{"type": "string", "description": "Human-readable description of what this tool does."},
					"command_template": map[string]any{"type": "string", "description": "Shell command template to execute on the device."},
					"metadata":         map[string]any{"type": "object", "description": "Additional metadata for the ability."},
				}, agentExtensionProperties()),
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
				"properties": mergeProperties(map[string]any{
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
				}, agentExtensionProperties()),
				"required": []string{"ability_name", "command_template"},
			},
		},
		{
			"name":        "deploy_ability_package",
			"description": "Deploy a native ability package through the full publish/install/activate pipeline. Use this for advanced deployments with custom schemas, versioning, or pre-built package descriptors. For simple command deployments, prefer deploy_ability.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": mergeProperties(map[string]any{
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
				}, agentExtensionProperties()),
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
		// -----------------------------------------------------------
		// DEVICE MANAGEMENT & ABILITY LIFECYCLE TOOLS
		// -----------------------------------------------------------
		{
			"name":        "drain_device",
			"description": "Drain a remote device — stop accepting new invocations while finishing in-flight ones.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":   map[string]any{"type": "string", "description": "Target device node ID."},
					"reason":    map[string]any{"type": "string", "description": "Reason for draining."},
				},
				"required": []string{"node_id"},
			},
		},
		{
			"name":        "build_ability_descriptor",
			"description": "Build an AbilityDescriptor locally without deploying it.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": mergeProperties(map[string]any{
					"name":             map[string]any{"type": "string", "description": "Ability name."},
					"description":      map[string]any{"type": "string", "description": "Ability description."},
					"command_template": map[string]any{"type": "string", "description": "Shell command template to back the ability."},
					"input_schema":     map[string]any{"type": "object", "description": "Input JSON schema."},
					"output_schema":    map[string]any{"type": "object", "description": "Output JSON schema."},
					"version":          map[string]any{"type": "string", "description": "Ability version."},
					"tags":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ability tags."},
					"resource_uri":     map[string]any{"type": "string", "description": "Resource URI."},
				}, agentExtensionProperties()),
				"required": []string{"name", "command_template"},
			},
		},
		{
			"name":        "export_ability_skill",
			"description": "Export an AbilityDescriptor as an Agent Skills SKILL.md and invoke.sh.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": mergeProperties(map[string]any{
					"name":             map[string]any{"type": "string", "description": "Ability name."},
					"description":      map[string]any{"type": "string", "description": "Ability description."},
					"command_template": map[string]any{"type": "string", "description": "Shell command template to back the ability."},
					"input_schema":     map[string]any{"type": "object", "description": "Input JSON schema."},
					"output_schema":    map[string]any{"type": "object", "description": "Output JSON schema."},
					"version":          map[string]any{"type": "string", "description": "Ability version."},
					"tags":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ability tags."},
					"resource_uri":     map[string]any{"type": "string", "description": "Resource URI."},
					"target":           map[string]any{"type": "string", "description": "Ability target: claude, codex, openclaw, agent_skills."},
					"axon_endpoint":    map[string]any{"type": "string", "description": "Axon endpoint for invoke script."},
				}, agentExtensionProperties()),
				"required": []string{"name", "command_template"},
			},
		},
		{
			"name":        "redeploy_ability",
			"description": "Redeploy an ability to a device by rebuilding and replacing its full package.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id":        map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":          map[string]any{"type": "string", "description": "Target device node ID."},
					"tool_name":        map[string]any{"type": "string", "description": "Existing tool name to update."},
					"description":      map[string]any{"type": "string", "description": "New description."},
					"command_template": map[string]any{"type": "string", "description": "New command template."},
					"input_schema":     map[string]any{"type": "object", "description": "New input schema."},
					"output_schema":    map[string]any{"type": "object", "description": "New output schema."},
				},
				"required": []string{"node_id", "tool_name", "command_template"},
			},
		},
		{
			"name":        "list_abilities",
			"description": "List locally deployed abilities on a device (filters out non-installable MCP entries).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":   map[string]any{"type": "string", "description": "Target device node ID."},
				},
				"required": []string{"node_id"},
			},
		},
		{
			"name":        "forget_all",
			"description": "Remove ALL deployed abilities from a device. Destructive — requires confirm: true unless dry_run: true.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tenant_id": map[string]any{"type": "string", "description": "Tenant ID (default AXON_TENANT)."},
					"node_id":   map[string]any{"type": "string", "description": "Target device node ID."},
					"confirm":   map[string]any{"type": "boolean", "description": "Must be true to confirm this destructive operation. Can be omitted when dry_run is true."},
					"dry_run":   map[string]any{"type": "boolean", "description": "When true, list abilities that would be removed without uninstalling."},
				},
				"required": []string{"node_id"},
			},
		},
	}
}
