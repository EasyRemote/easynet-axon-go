// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/dendrite_bridge_stub.go
// Description: Source file for Go SDK facade and Dendrite integration; keeps behavior explicit and interoperable across language/runtime boundaries, including tenant/principal invocation context bridging.
//
// Protocol Responsibility:
// - Implements Go SDK facade and Dendrite integration contracts required by current Axon service and SDK surfaces.
// - Preserves stable request/response semantics and error mapping for dendrite_bridge_stub.go call paths.
//
// Implementation Approach:
// - Uses small typed helpers and explicit control flow to avoid hidden side effects.
// - Keeps protocol translation and transport details close to this module boundary.
//
// Usage Contract:
// - Callers should provide valid tenant/resource/runtime context before invoking exported APIs; principal context is optional and, when provided, is mapped to EasyNet subject context.
// - Errors should be treated as typed protocol/runtime outcomes rather than silently ignored.
//
// Architectural Position:
// - Part of the Go SDK facade and Dendrite integration layer.
// - Should not embed unrelated orchestration logic outside this file's responsibility.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build !cgo

package easynet

// Shared types (DendriteError, ProtocolInvokeRequest, StreamNextResult)
// are in dendrite_bridge_types.go.

var errDendriteUnsupported = DendriteError{Message: "dendrite bridge requires cgo; recompile with cgo enabled"}

type DendriteBridge struct{}

func ResolveDendriteLibraryPath(explicitPath string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}
	return "", errDendriteUnsupported
}

func OpenDendriteBridge(_ string) (*DendriteBridge, error) {
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) CloseLibrary() error {
	_ = b
	return nil
}

func (b *DendriteBridge) OpenClient(_ string, _ int) (uint64, error) {
	_ = b
	return 0, errDendriteUnsupported
}

func (b *DendriteBridge) CloseClient(_ uint64) error {
	_ = b
	return errDendriteUnsupported
}

func (b *DendriteBridge) UnaryCall(_ uint64, _ string, _ []byte, _ map[string]string, _ int) ([]byte, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ServerStreamCall(_ uint64, _ string, _ []byte, _ map[string]string, _ int, _ int) ([][]byte, bool, error) {
	_ = b
	return nil, false, errDendriteUnsupported
}

func (b *DendriteBridge) ClientStreamCall(_ uint64, _ string, _ [][]byte, _ map[string]string, _ int, _ int) ([]byte, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) BidiStreamCall(_ uint64, _ string, _ [][]byte, _ map[string]string, _ int, _ int, _ int) ([][]byte, bool, error) {
	_ = b
	return nil, false, errDendriteUnsupported
}

func (b *DendriteBridge) InvokeAbility(_ uint64, _ string, _ string, _ any, _ map[string]string, _ int) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) InvokeAbilityWithSubject(
	_ uint64,
	_ string,
	_ string,
	_ any,
	_ string,
	_ map[string]string,
	_ int,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) InvokeAbilityRawWithSubject(
	_ uint64,
	_ string,
	_ string,
	_ any,
	_ string,
	_ map[string]string,
	_ int,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ProtocolCoverage() (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ProtocolCatalog() (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) InvokeProtocol(_ uint64, _ ProtocolInvokeRequest) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ListNodes(_ uint64, _ string, _ string) ([]map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) RegisterNode(
	_ uint64,
	_ string,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) Heartbeat(
	_ uint64,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) DeregisterNode(
	_ uint64,
	_ string,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) DrainNode(
	_ uint64,
	_ string,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) PublishCapabilityWithRequest(
	_ uint64,
	_ PublishCapabilityRequest,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) InstallCapabilityWithRequest(
	_ uint64,
	_ InstallCapabilityRequest,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ActivateCapability(
	_ uint64,
	_ string,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ListA2AAgents(
	_ uint64,
	_ string,
	_ []string,
	_ string,
	_ int,
) ([]map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) GetA2AAgentCard(
	_ uint64,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) SendA2ATask(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ any,
	_ string,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) DeployMCPListDir(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) DeployMCPListDirWithRequest(
	_ uint64,
	_ DeployMCPListDirRequest,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ListMCPTools(_ uint64, _ string, _ string, _ []string, _ string) ([]map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) CallMCPTool(_ uint64, _ string, _ string, _ string, _ any) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

// OpenMCPToolStream is the no-cgo stub; returns errDendriteUnsupported.
func (b *DendriteBridge) OpenMCPToolStream(_ uint64, _ string, _ string, _ string, _ any, _ int) (uint64, error) {
	_ = b
	return 0, errDendriteUnsupported
}

// CallMCPToolStreamOpen is kept as a compatibility alias for older callers.
func (b *DendriteBridge) CallMCPToolStreamOpen(
	handle uint64,
	tenantID string,
	toolName string,
	targetNodeID string,
	argumentsJSON any,
	timeoutMs int,
) (uint64, error) {
	return b.OpenMCPToolStream(handle, tenantID, toolName, targetNodeID, argumentsJSON, timeoutMs)
}

func (b *DendriteBridge) UninstallCapability(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ bool,
	_ string,
	_ bool,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) UninstallCapabilityWithRequest(_ uint64, _ UninstallCapabilityRequest) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) UpdateMCPListDir(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ string,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) UpdateMCPListDirWithRequest(
	_ uint64,
	_ UpdateMCPListDirRequest,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) CreateVoiceCall(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ int,
	_ map[string]any,
	_ map[string]string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) GetVoiceCall(
	_ uint64,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) JoinVoiceCall(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ string,
	_ int,
	_ string,
	_ map[string]any,
	_ bool,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) LeaveVoiceCall(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) UpdateVoiceMediaPath(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ int,
	_ string,
	_ bool,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ReportVoiceCallMetrics(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ map[string]any,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) EndVoiceCall(
	_ uint64,
	_ string,
	_ string,
	_ int,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) WatchVoiceCallEvents(
	_ uint64,
	_ string,
	_ string,
	_ uint64,
	_ int,
	_ int,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) CreateVoiceTransportSession(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ string,
	_ int,
	_ map[string]any,
	_ int,
	_ map[string]string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) GetVoiceTransportSession(
	_ uint64,
	_ string,
	_ string,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) SetVoiceTransportDescription(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ int,
	_ map[string]any,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) AddVoiceTransportCandidate(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ int,
	_ map[string]any,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) RefreshVoiceTransportLease(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ int,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) EndVoiceTransportSession(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ bool,
	_ string,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) WatchVoiceTransportEvents(
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ uint64,
	_ int,
	_ int,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

// Incremental streaming stubs (StreamNextResult is in dendrite_bridge_types.go)

func (b *DendriteBridge) ServerStreamOpen(_ uint64, _ string, _ []byte, _ map[string]string, _ int, _ int, _ int) (uint64, error) {
	_ = b
	return 0, errDendriteUnsupported
}

func (b *DendriteBridge) StreamNext(_ uint64, _ int) (StreamNextResult, error) {
	_ = b
	return StreamNextResult{}, errDendriteUnsupported
}

func (b *DendriteBridge) StreamClose(_ uint64) error {
	_ = b
	return errDendriteUnsupported
}

func (b *DendriteBridge) BidiStreamOpen(_ uint64, _ string, _ []byte, _ [][]byte, _ map[string]string, _ int, _ int, _ int, _ int) (uint64, error) {
	_ = b
	return 0, errDendriteUnsupported
}

func (b *DendriteBridge) BidiStreamSend(_ uint64, _ []byte) (bool, error) {
	_ = b
	return false, errDendriteUnsupported
}

func (b *DendriteBridge) BidiStreamFinishSend(_ uint64) (bool, error) {
	_ = b
	return false, errDendriteUnsupported
}
