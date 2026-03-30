// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/kit.go
// Description: Go `RemoteControlCaseKit` MCP provider with orchestrator dispatch and managed stream cleanup.
//
// Protocol Responsibility:
// - Assembles remote-control tool specs, handler dispatch, and orchestrator lifecycle into one MCP provider surface.
// - Owns bridge or orchestrator cleanup for unary and streaming tool calls so callers do not leak resources.
//
// Implementation Approach:
// - Keeps entrypoints thin by delegating validation and business logic to focused handler/orchestrator modules.
// - Caches or scopes transport resources according to the SDK runtime model while preserving per-tenant correctness.
//
// Usage Contract:
// - Use this as the preset-level integration point when exposing EasyNet remote-control tools over MCP.
// - Factory injection points should preserve the same request/response semantics as the default orchestrator.
//
// Architectural Position:
// - Preset composition boundary above handler modules and below case/example entrypoints.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package remotecontrol

import (
	"fmt"
	"os"
	"sync"

	"easynet.run/axon/sdk/go/easynet"
	"easynet.run/axon/sdk/go/easynet/mcp"
)

// RemoteControlCaseKit owns remote-control MCP behavior and delegates transport to the shared MCP server.
type RemoteControlCaseKit struct {
	config              RemoteControlRuntimeConfig
	orchestratorFactory BridgeFactory
}

// managedToolStreamHandle wraps an MCPToolStream with cleanup logic and idempotent Close.
type managedToolStreamHandle struct {
	inner   *easynet.MCPToolStream
	cleanup func()
	mu      sync.Mutex
	closed  bool
}

// maxConsecutiveTimeoutRetries is the maximum number of consecutive per-chunk
// timeouts before the stream is considered dead (matches Python/Java/Swift/Node).
const maxConsecutiveTimeoutRetries = 3

// Recv reads the next chunk from the underlying stream, retrying up to 3
// consecutive timeouts before returning an error.
func (h *managedToolStreamHandle) Recv() ([]byte, bool, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, true, nil
	}
	h.mu.Unlock()

	consecutiveTimeouts := 0
	for {
		result, err := h.inner.Recv()
		if err != nil {
			return nil, false, err
		}
		if result.Done {
			return nil, true, nil
		}
		if result.Timeout {
			consecutiveTimeouts++
			if consecutiveTimeouts >= maxConsecutiveTimeoutRetries {
				return nil, false, easynet.DendriteError{
					Message: fmt.Sprintf("stream timed out after %d consecutive retries", consecutiveTimeouts),
				}
			}
			continue
		}
		return result.Chunk, false, nil
	}
}

// Close closes the stream and invokes cleanup. Idempotent — safe to call multiple times.
func (h *managedToolStreamHandle) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	inner := h.inner
	cleanup := h.cleanup
	h.mu.Unlock()

	err := inner.Close()
	cleanup()
	return err
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
func (kit *RemoteControlCaseKit) HandleToolCall(name string, args map[string]any) mcp.McpToolResult {
	tenant := resolveTenant(args["tenant_id"], kit.config.Tenant)
	switch name {
	case "discover_nodes":
		return kit.handleDiscoverNodes(tenant, args)
	case "list_remote_tools":
		return kit.handleListRemoteTools(tenant, args)
	case "call_remote_tool":
		return kit.handleCallRemoteTool(tenant, args)
	case "call_remote_tool_stream":
		handle, err := kit.HandleToolCallStream(name, args)
		if err != nil {
			return errorResult(tenant, err.Error(), nil)
		}
		if handle == nil {
			return errorResult(tenant, "streaming not available", nil)
		}
		maxBytes := asInt(args["max_bytes"])
		if maxBytes <= 0 {
			maxBytes = mcp.DefaultMaxStreamBytes
		}
		return mcp.ConsumeStream(handle, maxBytes)
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
	case "drain_device":
		return kit.handleDrainDevice(tenant, args)
	case "build_ability_descriptor":
		return kit.handleCreateAbility(tenant, args)
	case "export_ability_skill":
		return kit.handleExportAbilitySkill(tenant, args)
	case "redeploy_ability":
		return kit.handleRedeployAbility(tenant, args)
	case "list_abilities":
		return kit.handleListAbilities(tenant, args)
	case "forget_all":
		return kit.handleForgetAll(tenant, args)
	default:
		return errorResult(tenant, "unknown tool: "+name, nil)
	}
}

// HandleToolCallStream opens a stream-capable tool and returns a managed handle
// when the requested MCP tool supports incremental output.
func (kit *RemoteControlCaseKit) HandleToolCallStream(name string, args map[string]any) (mcp.McpToolStreamHandle, error) {
	if name != "call_remote_tool_stream" {
		return nil, nil
	}
	tenant := resolveTenant(args["tenant_id"], kit.config.Tenant)
	orchestrator := kit.orchestratorFactory(kit.config, tenant)
	stream, err := kit.openCallRemoteToolStream(orchestrator, args)
	if err != nil {
		logCloseError("remotecontrol: failed closing orchestrator after stream open failure", orchestrator.Close())
		return nil, err
	}
	return &managedToolStreamHandle{
		inner: stream,
		cleanup: func() {
			logCloseError("remotecontrol: failed closing orchestrator after stream completion", orchestrator.Close())
		},
	}, nil
}

func (kit *RemoteControlCaseKit) withOrchestrator(
	tenant string,
	fn func(*easynet.Orchestrator) (map[string]any, error),
) mcp.McpToolResult {
	orchestrator := kit.orchestratorFactory(kit.config, tenant)
	if err := orchestrator.Open(); err != nil {
		return errorResult(tenant, err.Error(), nil)
	}
	defer func() {
		logCloseError("remotecontrol: failed closing orchestrator", orchestrator.Close())
	}()

	payload, err := fn(orchestrator)
	if err != nil {
		return errorResult(tenant, err.Error(), nil)
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
	return mcp.McpToolResult{
		Payload: payload,
		IsError: !asBool(payload["ok"]),
	}
}

func logCloseError(context string, err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s: %v\n", context, err)
}

func errorResult(tenant string, message string, fields map[string]any) mcp.McpToolResult {
	payload := map[string]any{
		"ok":        false,
		"tenant_id": tenant,
		"error":     message,
	}
	for key, value := range fields {
		payload[key] = value
	}
	return mcp.McpToolResult{
		Payload: payload,
		IsError: true,
	}
}
