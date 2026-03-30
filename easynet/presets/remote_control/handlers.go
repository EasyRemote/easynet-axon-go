// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/handlers.go
// Description: Go MCP tool handlers for remote device discovery, deployment, execution, and lifecycle workflows.
//
// Protocol Responsibility:
// - Publishes MCP-facing handlers for discovery, deployment, execution, and lifecycle control operations.
// - Maps untyped tool arguments into stable tenant-scoped orchestrator requests and MCP result payloads.
//
// Implementation Approach:
// - Performs validation and response shaping at the tool boundary while delegating side effects to orchestrator helpers.
// - Reuses descriptor/config utilities so remote-control behavior stays aligned across language SDKs.
//
// Usage Contract:
// - Intended to be called from MCP dispatch with untyped argument objects and preset runtime configuration.
// - Handler error payloads are part of the remote-control tool contract and should stay explicit and machine-readable.
//
// Architectural Position:
// - Preset execution layer between MCP server facades and bridge-backed orchestration helpers.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

// Package remotecontrol provides MCP tool handlers for remote device control.
//
// Architecture: ability_lifecycle vs presets/remote_control
//
//	ability_lifecycle (SDK user API)   <- typed AbilityDescriptor, DeployTrace
//	        |
//	        v
//	presets/remote_control (MCP layer) <- untyped map[string]any args from MCP
//	        |
//	        v
//	dendrite_bridge (FFI)              <- native C ABI
//
// Handlers receive untyped args from MCP tool dispatch and delegate to the
// orchestrator for deployment pipelines. The ability_lifecycle module in the
// parent easynet package provides the higher-level typed API for SDK consumers.
package remotecontrol

import (
	"errors"
	"fmt"
	"time"

	"easynet.run/axon/sdk/go/easynet"
	"easynet.run/axon/sdk/go/easynet/mcp"
)

func (kit *RemoteControlCaseKit) handleDiscoverNodes(tenant string, args map[string]any) mcp.McpToolResult {
	_ = args
	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		nodes, err := orch.ListNodes("")
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(nodes))
		for _, node := range nodes {
			out = append(out, map[string]any{
				"node_id":      node["node_id"],
				"display_name": node["display_name"],
				"online":       node["online"],
			})
		}
		return map[string]any{
			"ok":    true,
			"count": len(out),
			"nodes": out,
		}, nil
	})
}

func (kit *RemoteControlCaseKit) handleListRemoteTools(tenant string, args map[string]any) mcp.McpToolResult {
	nodeID := asString(args["node_id"]) // empty string → no node filter
	pattern := asString(args["name_pattern"])
	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		tools, err := orch.ListMCPTools(pattern, []string{}, nodeID)
		if err != nil {
			return nil, err
		}
		// Pass through the full tool entry from the runtime — including
		// input_schema, output_schema, hints, instructions, examples,
		// prerequisites, context_bindings, and category.  Stripping
		// fields here would prevent AI agents from understanding tools.
		items := tools
		return map[string]any{
			"ok":           true,
			"count":        len(items),
			"node_id":      nodeID,
			"name_pattern": pattern,
			"tools":        items,
		}, nil
	})
}

func (kit *RemoteControlCaseKit) handleCallRemoteTool(tenant string, args map[string]any) mcp.McpToolResult {
	toolName := asString(args["tool_name"])
	nodeID := asString(args["node_id"])
	callArgs := asMap(args["arguments"])

	if toolName == "" {
		return errorResult(tenant, "tool_name is required", nil)
	}
	if nodeID == "" {
		return errorResult(tenant, "node_id is required", map[string]any{"tool_name": toolName})
	}
	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		call, err := orch.CallMCPTool(toolName, nodeID, callArgs)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok":        !callFailed(call),
			"tool_name": toolName,
			"node_id":   nodeID,
			"call":      call,
		}, nil
	})
}

// openCallRemoteToolStream validates args and opens a streaming MCP tool call.
// It intentionally uses the orchestrator's low-level stream API so callers keep
// incremental chunks and explicit close semantics instead of buffered unary MCP output.
func (kit *RemoteControlCaseKit) openCallRemoteToolStream(
	orch *easynet.Orchestrator,
	args map[string]any,
) (*easynet.MCPToolStream, error) {
	toolName := asString(args["tool_name"])
	nodeID := asString(args["node_id"])
	callArgs := asMap(args["arguments"])
	timeoutMs := asInt(args["timeout_ms"])

	if toolName == "" {
		return nil, errors.New("tool_name is required")
	}
	if nodeID == "" {
		return nil, errors.New("node_id is required")
	}
	return orch.CallMCPToolStream(toolName, nodeID, callArgs, timeoutMs)
}

func (kit *RemoteControlCaseKit) handleDisconnectDevice(tenant string, args map[string]any) mcp.McpToolResult {
	nodeID := asString(args["node_id"])
	if nodeID == "" {
		return errorResult(tenant, "node_id is required", nil)
	}
	reason := asStringOrDefault(args["reason"], "disconnect_device: requested by agent")
	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		response, err := orch.DisconnectDevice(nodeID, reason)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok":        true,
			"tenant_id": tenant,
			"node_id":   nodeID,
			"status":    "disconnected",
			"response":  response,
		}, nil
	})
}

func (kit *RemoteControlCaseKit) handleUninstallAbility(tenant string, args map[string]any) mcp.McpToolResult {
	nodeID := asString(args["node_id"])
	installID := asString(args["install_id"])
	if nodeID == "" {
		return errorResult(tenant, "node_id is required", nil)
	}
	if installID == "" {
		return errorResult(tenant, "install_id is required", map[string]any{"node_id": nodeID})
	}
	reason := asStringOrDefault(args["reason"], "uninstall_ability: requested by agent")
	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		response, err := orch.UninstallAbility(nodeID, installID, reason)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok":         true,
			"tenant_id":  tenant,
			"node_id":    nodeID,
			"install_id": installID,
			"status":     "uninstalled",
			"response":   response,
		}, nil
	})
}

func (kit *RemoteControlCaseKit) handlePackageAbility(tenant string, args map[string]any) mcp.McpToolResult {
	_ = tenant
	descriptor, err := buildDescriptor(args, kit.config.SignatureBase64)
	if err != nil {
		return errorResult(tenant, err.Error(), nil)
	}
	return mcp.McpToolResult{
		Payload: map[string]any{
			"ok":        true,
			"tenant_id": tenant,
			"package":   descriptor.toToolPayload(),
		},
	}
}

func (kit *RemoteControlCaseKit) handleDeployAbilityPackage(tenant string, args map[string]any) mcp.McpToolResult {
	nodeID := asString(args["node_id"])
	if nodeID == "" {
		return errorResult(tenant, "node_id is required", nil)
	}
	cleanupOnActivateFailure := asBoolOrDefault(args["cleanup_on_activate_failure"], true)
	descriptor, err := parseOrBuildDescriptor(args, kit.config.SignatureBase64)
	if err != nil {
		return mcp.McpToolResult{
			Payload: map[string]any{
				"ok":        false,
				"tenant_id": tenant,
				"node_id":   nodeID,
				"error":     err.Error(),
			},
			IsError: true,
		}
	}
	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		deploy, err := orch.DeployAbilityPackage(nodeID, descriptor.toDeployDescriptor(), cleanupOnActivateFailure)
		if err != nil {
			return nil, err
		}
		payload := mergeMaps(map[string]any{
			"ok":        true,
			"tenant_id": tenant,
			"node_id":   nodeID,
			"package":   descriptor.toToolPayload(),
		}, deploy)
		return payload, nil
	})
}

// ---------------------------------------------------------------------------
// AI-AGENT PRESET HANDLERS
// handleDeployAbility and handleExecuteCommand are convenience presets
// designed for AI agent workflows. They wrap the generic package_ability /
// deploy_ability_package pipeline into single-call operations.
// ---------------------------------------------------------------------------

func (kit *RemoteControlCaseKit) handleDeployAbility(tenant string, args map[string]any) mcp.McpToolResult {
	nodeID := asString(args["node_id"])
	if nodeID == "" {
		return mcp.McpToolResult{
			Payload: map[string]any{"ok": false, "tenant_id": tenant, "error": "node_id is required"},
			IsError: true,
		}
	}
	commandTemplate := asString(args["command_template"])
	if commandTemplate == "" {
		return errorResult(tenant, "command_template is required", map[string]any{"node_id": nodeID})
	}
	toolName := asString(args["tool_name"])
	if toolName == "" {
		toolName = fmt.Sprintf("tool_%d_%s", time.Now().UnixMilli(), randomHex(4))
	}
	description := asStringOrDefault(args["description"], fmt.Sprintf("Tool %s", toolName))

	descriptor, err := buildDescriptor(map[string]any{
		"ability_name":     toolName,
		"tool_name":        toolName,
		"description":      description,
		"command_template": commandTemplate,
		"version":          asString(args["version"]),
		"tags":             args["tags"],
		"metadata":         args["metadata"],
		"package_id":       args["package_id"],
		"capability_name":  args["capability_name"],
		"signature_base64": args["signature_base64"],
		"digest":           args["digest"],
	}, kit.config.SignatureBase64)
	if err != nil {
		return errorResult(tenant, err.Error(), map[string]any{
			"node_id":   nodeID,
			"tool_name": toolName,
		})
	}
	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		deploy, err := orch.DeployAbilityPackage(nodeID, descriptor.toDeployDescriptor(), true)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok":          true,
			"node_id":     nodeID,
			"tool_name":   descriptor.ToolName,
			"package_id":  descriptor.PackageID,
			"install_id":  deploy["install_id"],
			"description": description,
			"package":     descriptor.toToolPayload(),
			"deploy":      deploy,
		}, nil
	})
}

func (kit *RemoteControlCaseKit) handleExecuteCommand(tenant string, args map[string]any) mcp.McpToolResult {
	nodeID := asString(args["node_id"])
	command := asString(args["command"])
	if nodeID == "" {
		return mcp.McpToolResult{
			Payload: map[string]any{"ok": false, "tenant_id": tenant, "error": "node_id is required"},
			IsError: true,
		}
	}
	if command == "" {
		return mcp.McpToolResult{
			Payload: map[string]any{"ok": false, "tenant_id": tenant, "node_id": nodeID, "error": "command is required"},
			IsError: true,
		}
	}
	shouldCleanup := asBoolOrDefault(args["cleanup"], true)
	toolName := fmt.Sprintf("cmd_%d_%s", time.Now().UnixMilli(), randomHex(4))

	descriptor, err := buildDescriptor(map[string]any{
		"ability_name":     toolName,
		"tool_name":        toolName,
		"description":      fmt.Sprintf("execute command: %s", command),
		"command_template": defaultCommandTemplate(command),
		"tags":             []any{"mcp", "ability", "execute-command"},
		"signature_base64": kit.config.SignatureBase64,
	}, kit.config.SignatureBase64)
	if err != nil {
		return mcp.McpToolResult{
			Payload: map[string]any{
				"ok":        false,
				"tenant_id": tenant,
				"node_id":   nodeID,
				"tool_name": toolName,
				"command":   command,
				"error":     err.Error(),
			},
			IsError: true,
		}
	}

	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		deploy, err := orch.DeployAbilityPackage(nodeID, descriptor.toDeployDescriptor(), shouldCleanup)
		if err != nil {
			var deployErr *easynet.DeployError
			if errors.As(err, &deployErr) {
				detail := deployErr.Detail
				cleanup := asMapAny(detail["cleanup"])
				if cleanup == nil {
					cleanup = cleanupInstall(orch, nodeID, asString(detail["install_id"]), shouldCleanup)
				}
				return map[string]any{
					"ok":         false,
					"node_id":    nodeID,
					"tool_name":  toolName,
					"command":    command,
					"deploy":     descriptor.toToolPayload(),
					"install_id": asString(detail["install_id"]),
					"cleanup":    cleanup,
					"error":      deployErr.Error(),
				}, nil
			}
			return map[string]any{
				"ok":        false,
				"node_id":   nodeID,
				"tool_name": toolName,
				"command":   command,
				"deploy":    descriptor.toToolPayload(),
				"error":     err.Error(),
			}, nil
		}
		deployID := asString(deploy["install_id"])
		actualTool := asString(deploy["tool_name"])
		if actualTool == "" {
			actualTool = toolName
		}
		call, err := orch.CallMCPTool(actualTool, nodeID, map[string]any{})
		failed := false
		callErr := ""
		if err != nil {
			failed = true
			callErr = err.Error()
		} else {
			failed = callFailed(call)
		}
		cleanup := cleanupInstall(orch, nodeID, deployID, shouldCleanup)
		return map[string]any{
			"ok":        !failed,
			"node_id":   nodeID,
			"tool_name": actualTool,
			"command":   command,
			"deploy":    descriptor.toToolPayload(),
			"call":      call,
			"cleanup":   cleanup,
			"error":     callErr,
		}, nil
	})
}

// ---------------------------------------------------------------------------
// DEVICE MANAGEMENT & ABILITY LIFECYCLE HANDLERS
// ---------------------------------------------------------------------------

func (kit *RemoteControlCaseKit) handleDrainDevice(tenant string, args map[string]any) mcp.McpToolResult {
	nodeID := asString(args["node_id"])
	if nodeID == "" {
		return errorResult(tenant, "node_id is required", nil)
	}
	reason := asStringOrDefault(args["reason"], "drain_device: requested by agent")
	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		response, err := orch.DrainDevice(nodeID, reason)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok":        true,
			"tenant_id": tenant,
			"node_id":   nodeID,
			"status":    "draining",
			"response":  response,
		}, nil
	})
}

func (kit *RemoteControlCaseKit) handleCreateAbility(_ string, args map[string]any) mcp.McpToolResult {
	name := asString(args["name"])
	if name == "" {
		return mcp.McpToolResult{
			Payload: map[string]any{"ok": false, "error": "name is required"},
			IsError: true,
		}
	}
	description := asString(args["description"])
	commandTemplate := asString(args["command_template"])
	if commandTemplate == "" {
		return mcp.McpToolResult{
			Payload: map[string]any{"ok": false, "error": "command_template is required"},
			IsError: true,
		}
	}
	version := asString(args["version"])
	resourceURI := asString(args["resource_uri"])
	instructions := asString(args["instructions"])
	inputExamples, _ := args["input_examples"].([]any)
	prerequisites := []string{}
	if rawPrerequisites, ok := args["prerequisites"].([]any); ok {
		for _, prerequisite := range rawPrerequisites {
			if value := asString(prerequisite); value != "" {
				prerequisites = append(prerequisites, value)
			}
		}
	}
	contextBindings := map[string]string{}
	if rawBindings, ok := args["context_bindings"].(map[string]any); ok {
		for key, value := range rawBindings {
			if resolved := asString(value); resolved != "" {
				contextBindings[key] = resolved
			}
		}
	}
	category := asString(args["category"])

	inputSchema := asMapOrNil(args["input_schema"])
	outputSchema := asMapOrNil(args["output_schema"])

	var tags []string
	if rawTags, ok := args["tags"].([]any); ok {
		for _, t := range rawTags {
			if s := asString(t); s != "" {
				tags = append(tags, s)
			}
		}
	}

	descriptor, err := easynet.BuildAbilityDescriptor(
		name, description, commandTemplate,
		inputSchema, outputSchema,
		version, tags, resourceURI,
		instructions, inputExamples, prerequisites, contextBindings, category,
	)
	if err != nil {
		return mcp.McpToolResult{
			Payload: map[string]any{"ok": false, "error": err.Error()},
			IsError: true,
		}
	}
	descriptorValue := descriptorToMap(descriptor)
	return mcp.McpToolResult{
		Payload: map[string]any{
			"ok":         true,
			"descriptor": descriptorValue,
		},
	}
}

func (kit *RemoteControlCaseKit) handleExportAbilitySkill(_ string, args map[string]any) mcp.McpToolResult {
	name := asString(args["name"])
	if name == "" {
		return mcp.McpToolResult{
			Payload: map[string]any{"ok": false, "error": "name is required"},
			IsError: true,
		}
	}
	description := asString(args["description"])
	commandTemplate := asString(args["command_template"])
	if commandTemplate == "" {
		return mcp.McpToolResult{
			Payload: map[string]any{"ok": false, "error": "command_template is required"},
			IsError: true,
		}
	}
	version := asString(args["version"])
	resourceURI := asString(args["resource_uri"])
	instructions := asString(args["instructions"])
	inputExamples, _ := args["input_examples"].([]any)
	prerequisites := []string{}
	if rawPrerequisites, ok := args["prerequisites"].([]any); ok {
		for _, prerequisite := range rawPrerequisites {
			if value := asString(prerequisite); value != "" {
				prerequisites = append(prerequisites, value)
			}
		}
	}
	contextBindings := map[string]string{}
	if rawBindings, ok := args["context_bindings"].(map[string]any); ok {
		for key, value := range rawBindings {
			if resolved := asString(value); resolved != "" {
				contextBindings[key] = resolved
			}
		}
	}
	category := asString(args["category"])

	inputSchema := asMapOrNil(args["input_schema"])
	outputSchema := asMapOrNil(args["output_schema"])

	var tags []string
	if rawTags, ok := args["tags"].([]any); ok {
		for _, t := range rawTags {
			if s := asString(t); s != "" {
				tags = append(tags, s)
			}
		}
	}

	descriptor, err := easynet.BuildAbilityDescriptor(
		name, description, commandTemplate,
		inputSchema, outputSchema,
		version, tags, resourceURI,
		instructions, inputExamples, prerequisites, contextBindings, category,
	)
	if err != nil {
		return mcp.McpToolResult{
			Payload: map[string]any{"ok": false, "error": err.Error()},
			IsError: true,
		}
	}

	targetStr := asString(args["target"])
	target := easynet.ParseAbilityTarget(targetStr)
	axonEndpoint := asString(args["axon_endpoint"])

	result, err := easynet.ExportAbility(descriptor, target, axonEndpoint)
	if err != nil {
		return mcp.McpToolResult{
			Payload: map[string]any{"ok": false, "error": err.Error()},
			IsError: true,
		}
	}
	return mcp.McpToolResult{
		Payload: map[string]any{
			"ok":            true,
			"ability_name":  result.AbilityName,
			"ability_md":    result.AbilityMd,
			"invoke_script": result.InvokeScript,
		},
	}
}

func abilityEntryFromTool(tool map[string]any) (map[string]any, bool) {
	installID := asString(tool["install_id"])
	if installID == "" {
		return nil, false
	}
	return map[string]any{
		"tool_name":       tool["tool_name"],
		"description":     tool["description"],
		"capability_name": tool["capability_name"],
		"install_id":      installID,
	}, true
}

func (kit *RemoteControlCaseKit) handleRedeployAbility(tenant string, args map[string]any) mcp.McpToolResult {
	nodeID := asString(args["node_id"])
	if nodeID == "" {
		return errorResult(tenant, "node_id is required", nil)
	}
	toolName := asString(args["tool_name"])
	if toolName == "" {
		return errorResult(tenant, "tool_name is required", map[string]any{"node_id": nodeID})
	}
	commandTemplate := asString(args["command_template"])
	if commandTemplate == "" {
		return errorResult(tenant, "command_template is required", map[string]any{
			"node_id":   nodeID,
			"tool_name": toolName,
		})
	}
	description := asStringOrDefault(args["description"], fmt.Sprintf("Redeployed ability: %s", toolName))
	// Preserve all original args (including agent extension fields like
	// instructions, input_examples, prerequisites, context_bindings, category)
	// and override only the required fields.
	merged := make(map[string]any, len(args))
	for k, v := range args {
		merged[k] = v
	}
	merged["ability_name"] = toolName
	merged["tool_name"] = toolName
	merged["description"] = description
	merged["command_template"] = commandTemplate
	descriptor, err := buildDescriptor(merged, kit.config.SignatureBase64)
	if err != nil {
		return errorResult(tenant, err.Error(), map[string]any{
			"node_id":   nodeID,
			"tool_name": toolName,
		})
	}

	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		deploy, err := orch.DeployAbilityPackage(nodeID, descriptor.toDeployDescriptor(), true)
		if err != nil {
			return nil, err
		}
		installID := asString(deploy["install_id"])
		return map[string]any{
			"ok":          true,
			"tenant_id":   tenant,
			"node_id":     nodeID,
			"tool_name":   toolName,
			"description": description,
			"status":      "redeployed",
			"install_id":  installID,
		}, nil
	})
}

func (kit *RemoteControlCaseKit) handleListAbilities(tenant string, args map[string]any) mcp.McpToolResult {
	nodeID := asString(args["node_id"])
	if nodeID == "" {
		return errorResult(tenant, "node_id is required", nil)
	}
	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		tools, err := orch.ListAbilities(nodeID)
		if err != nil {
			return nil, err
		}
		abilities := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			if ability, ok := abilityEntryFromTool(tool); ok {
				abilities = append(abilities, ability)
			}
		}
		return map[string]any{
			"ok":        true,
			"tenant_id": tenant,
			"node_id":   nodeID,
			"count":     len(abilities),
			"abilities": abilities,
		}, nil
	})
}

func (kit *RemoteControlCaseKit) handleForgetAll(tenant string, args map[string]any) mcp.McpToolResult {
	nodeID := asString(args["node_id"])
	if nodeID == "" {
		return errorResult(tenant, "node_id is required", nil)
	}
	confirmed := asBool(args["confirm"])
	dryRun := asBool(args["dry_run"])
	if !confirmed && !dryRun {
		return errorResult(tenant, "forget_all requires confirm: true (destructive operation)", map[string]any{
			"node_id": nodeID,
		})
	}
	return kit.withOrchestrator(tenant, func(orch *easynet.Orchestrator) (map[string]any, error) {
		result, err := orch.ForgetAll(nodeID, confirmed, dryRun)
		if err != nil {
			// Partial success — still return what we have.
			if result != nil {
				return map[string]any{
					"ok":            false,
					"tenant_id":     tenant,
					"node_id":       nodeID,
					"removed":       result.Removed,
					"removed_count": result.RemovedCount,
					"failed":        result.Failed,
					"failed_count":  result.FailedCount,
					"error":         err.Error(),
				}, nil
			}
			return nil, err
		}
		return map[string]any{
			"ok":            true,
			"tenant_id":     tenant,
			"node_id":       nodeID,
			"dry_run":       dryRun,
			"removed":       result.Removed,
			"removed_count": result.RemovedCount,
			"failed":        result.Failed,
			"failed_count":  result.FailedCount,
		}, nil
	})
}

// descriptorToMap converts an AbilityDescriptor to a map[string]any for MCP payload.
func descriptorToMap(d *easynet.AbilityDescriptor) map[string]any {
	out := map[string]any{
		"name":             d.Name,
		"tool_name":        d.ToolName,
		"description":      d.Description,
		"command_template": d.CommandTemplate,
		"version":          d.Version,
		"tags":             d.Tags,
		"resource_uri":     d.ResourceURI,
	}
	if d.InputSchema != nil {
		out["input_schema"] = d.InputSchema
	}
	if d.OutputSchema != nil {
		out["output_schema"] = d.OutputSchema
	}
	if d.Instructions != "" {
		out["instructions"] = d.Instructions
	}
	if len(d.InputExamples) > 0 {
		out["input_examples"] = d.InputExamples
	}
	if len(d.Prerequisites) > 0 {
		out["prerequisites"] = d.Prerequisites
	}
	if len(d.ContextBindings) > 0 {
		out["context_bindings"] = d.ContextBindings
	}
	if d.Category != "" {
		out["category"] = d.Category
	}
	return out
}

func asMapAny(raw any) map[string]any {
	cast, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return cast
}
