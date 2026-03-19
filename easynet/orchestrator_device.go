// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/orchestrator_device.go
// Description: Device-management orchestrator helpers for disconnect and uninstall flows.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import "strings"

// DisconnectDevice deregisters a node from the runtime using the orchestrator context.
func (o *Orchestrator) DisconnectDevice(nodeID string, reason string) (map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "sdk: disconnect_device"
	}
	return o.bridge.DeregisterNode(o.handle, o.Tenant, nodeID, reason)
}

// UninstallCapability removes an install from a node using the orchestrator context.
func (o *Orchestrator) UninstallCapability(
	nodeID string,
	installID string,
	deactivateReason string,
) (map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(deactivateReason)
	if reason == "" {
		reason = o.uninstallReason
	}
	return o.bridge.UninstallCapability(o.handle, o.Tenant, nodeID, installID, true, reason, false)
}
