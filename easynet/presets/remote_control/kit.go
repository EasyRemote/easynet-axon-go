// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/kit.go
// Description: RemoteControlCaseKit: MCP tool provider with orchestrator dispatch.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package remotecontrol

import (
	"easynet.run/axon/sdk/go/easynet"
	"easynet.run/axon/sdk/go/easynet/mcp"
)

// RemoteControlCaseKit owns remote-control MCP behavior and delegates transport to the shared MCP server.
type RemoteControlCaseKit struct {
	config              RemoteControlRuntimeConfig
	orchestratorFactory BridgeFactory
}

// RemoteControlCaseKitOption customizes kit behavior.
type RemoteControlCaseKitOption func(*RemoteControlCaseKit)

// WithBridgeFactory overrides orchestrator creation for tests or custom transport wiring.
func WithBridgeFactory(factory BridgeFactory) RemoteControlCaseKitOption {
	return func(kit *RemoteControlCaseKit) {
		if factory != nil {
			kit.orchestratorFactory = factory
		}
	}
}

// NewCaseKit creates a case object with overridable behavior.
func NewCaseKit(config RemoteControlRuntimeConfig, opts ...RemoteControlCaseKitOption) *RemoteControlCaseKit {
	kit := &RemoteControlCaseKit{
		config:              config,
		orchestratorFactory: defaultBridgeFactory,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(kit)
		}
	}
	return kit
}

// ToolSpecs returns MCP tool metadata for the remote-control case.
func (kit *RemoteControlCaseKit) ToolSpecs() []map[string]any {
	return remoteControlToolSpecs()
}

// HandleToolCall dispatches MCP calls by tool name.
func (kit *RemoteControlCaseKit) HandleToolCall(name string, args map[string]any) mcp.ToolResult {
	tenant := resolveTenant(args["tenant_id"], kit.config.Tenant)
	switch name {
	case "discover_nodes":
		return kit.handleDiscoverNodes(tenant, args)
	case "list_remote_tools":
		return kit.handleListRemoteTools(tenant, args)
	case "call_remote_tool":
		return kit.handleCallRemoteTool(tenant, args)
	case "disconnect_device":
		return kit.handleDisconnectDevice(tenant, args)
	case "uninstall_ability":
		return kit.handleUninstallAbility(tenant, args)
	case "package_ability":
		return kit.handlePackageAbility(tenant, args)
	case "deploy_ability_package":
		return kit.handleDeployAbilityPackage(tenant, args)
	case "deploy_ability":
		return kit.handleDeployAbility(tenant, args)
	case "execute_command":
		return kit.handleExecuteCommand(tenant, args)
	default:
		return mcp.ToolResult{
			Payload: map[string]any{
				"ok":        false,
				"tenant_id": tenant,
				"error":     "unknown tool: " + name,
			},
			IsError: true,
		}
	}
}

func (kit *RemoteControlCaseKit) withOrchestrator(
	tenant string,
	fn func(*easynet.Orchestrator) (map[string]any, error),
) mcp.ToolResult {
	orchestrator := kit.orchestratorFactory(kit.config, tenant)
	if err := orchestrator.Open(); err != nil {
		return mcp.ToolResult{
			Payload: map[string]any{"ok": false, "tenant_id": tenant, "error": err.Error()},
			IsError: true,
		}
	}
	defer func() { _ = orchestrator.Close() }()

	payload, err := fn(orchestrator)
	if err != nil {
		return mcp.ToolResult{
			Payload: map[string]any{"ok": false, "tenant_id": tenant, "error": err.Error()},
			IsError: true,
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if _, ok := payload["ok"]; !ok {
		payload["ok"] = true
	}
	if _, ok := payload["tenant_id"]; !ok {
		payload["tenant_id"] = tenant
	}
	return mcp.ToolResult{
		Payload: payload,
		IsError: !asBool(payload["ok"]),
	}
}
