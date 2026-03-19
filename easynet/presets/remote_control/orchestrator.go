// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/orchestrator.go
// Description: Bridge factory and cleanup helpers for the remote-control preset.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package remotecontrol

import (
	"strings"

	"easynet.run/axon/sdk/go/easynet"
)

type BridgeFactory func(config RemoteControlRuntimeConfig, tenant string) *easynet.Orchestrator

func defaultBridgeFactory(config RemoteControlRuntimeConfig, tenant string) *easynet.Orchestrator {
	return easynet.NewOrchestrator(
		easynet.WithEndpoint(config.Endpoint),
		easynet.WithTenant(tenant),
		easynet.WithConnectTimeoutMs(config.ConnectTimeoutMs),
	)
}

func cleanupInstall(orch *easynet.Orchestrator, nodeID, installID string, enabled bool) map[string]any {
	if !enabled {
		return map[string]any{"attempted": false, "ok": false, "reason": "cleanup disabled"}
	}
	if strings.TrimSpace(installID) == "" {
		return map[string]any{"attempted": false, "ok": false, "reason": "install_id empty"}
	}
	summary := orch.CleanupInstalls([]easynet.InstallRef{{
		Mode:      "execute_command",
		NodeID:    nodeID,
		InstallID: installID,
	}})
	failed := asInt(summary["failed"])
	return map[string]any{
		"attempted": summary["attempted"],
		"ok":        failed == 0,
		"results":   summary["results"],
	}
}

func callFailed(call any) bool {
	cast, ok := call.(map[string]any)
	if !ok {
		return true
	}
	state := asInt(cast["state"])
	isError := false
	if raw := asBool(cast["is_error"]); raw {
		isError = true
	}
	return state != defaultInvocationStateCompleted || isError
}
