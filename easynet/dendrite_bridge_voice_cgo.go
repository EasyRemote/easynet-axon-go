// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/dendrite_bridge_voice_cgo.go
// Description: Source file for Go SDK bridge and capability helper implementation; keeps behavior explicit and interoperable across language/runtime boundaries.
//
// Protocol Responsibility:
// - Implements Go SDK bridge and capability helper implementation contracts required by current Axon service and SDK surfaces.
// - Preserves stable request/response semantics and error mapping for dendrite_bridge_voice_cgo.go call paths.
//
// Implementation Approach:
// - Uses small typed helpers and explicit control flow to avoid hidden side effects.
// - Keeps protocol translation and transport details close to this module boundary.
//
// Usage Contract:
// - Callers should provide valid tenant/resource/runtime context before invoking exported APIs.
// - Errors should be treated as typed protocol/runtime outcomes rather than silently ignored.
//
// Architectural Position:
// - Part of the Go SDK bridge and capability helper implementation layer.
// - Should not embed unrelated orchestration logic outside this file's responsibility.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build cgo

package easynet

func (b *DendriteBridge) CreateVoiceCall(
	handle uint64,
	tenantID string,
	callID string,
	displayName string,
	participantLimit int,
	preferredCodec map[string]any,
	metadata map[string]string,
) (map[string]any, error) {

	if preferredCodec == nil {
		preferredCodec = map[string]any{}
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	payload := map[string]any{
		"tenant_id":         tenantID,
		"call_id":           callID,
		"display_name":      displayName,
		"participant_limit": participantLimit,
		"preferred_codec":   preferredCodec,
		"metadata":          metadata,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceCreateCall)
}

func (b *DendriteBridge) GetVoiceCall(
	handle uint64,
	tenantID string,
	callID string,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id": tenantID,
		"call_id":   callID,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceGetCall)
}

func (b *DendriteBridge) JoinVoiceCall(
	handle uint64,
	tenantID string,
	callID string,
	participantID string,
	nodeID string,
	transport int,
	streamSessionID string,
	codecProfile map[string]any,
	muted bool,
) (map[string]any, error) {

	if codecProfile == nil {
		codecProfile = map[string]any{}
	}
	payload := map[string]any{
		"tenant_id":         tenantID,
		"call_id":           callID,
		"participant_id":    participantID,
		"node_id":           nodeID,
		"transport":         transport,
		"stream_session_id": streamSessionID,
		"codec_profile":     codecProfile,
		"muted":             muted,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceJoinCall)
}

func (b *DendriteBridge) LeaveVoiceCall(
	handle uint64,
	tenantID string,
	callID string,
	participantID string,
	reason string,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id":      tenantID,
		"call_id":        callID,
		"participant_id": participantID,
		"reason":         reason,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceLeaveCall)
}

func (b *DendriteBridge) UpdateVoiceMediaPath(
	handle uint64,
	tenantID string,
	callID string,
	participantID string,
	transport int,
	streamSessionID string,
	muted bool,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id":         tenantID,
		"call_id":           callID,
		"participant_id":    participantID,
		"transport":         transport,
		"stream_session_id": streamSessionID,
		"muted":             muted,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceUpdatePath)
}

func (b *DendriteBridge) ReportVoiceCallMetrics(
	handle uint64,
	tenantID string,
	callID string,
	participantID string,
	metrics map[string]any,
) (map[string]any, error) {

	if metrics == nil {
		metrics = map[string]any{}
	}
	payload := map[string]any{
		"tenant_id":      tenantID,
		"call_id":        callID,
		"participant_id": participantID,
		"metrics":        metrics,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceReport)
}

func (b *DendriteBridge) EndVoiceCall(
	handle uint64,
	tenantID string,
	callID string,
	endReason int,
	detail string,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id":  tenantID,
		"call_id":    callID,
		"end_reason": endReason,
		"detail":     detail,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceEndCall)
}

func (b *DendriteBridge) WatchVoiceCallEvents(
	handle uint64,
	tenantID string,
	callID string,
	fromSequence uint64,
	maxEvents int,
	timeoutMs int,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id":     tenantID,
		"call_id":       callID,
		"from_sequence": fromSequence,
		"max_events":    maxEvents,
		"timeout_ms":    timeoutMs,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceWatchCall)
}

func (b *DendriteBridge) CreateVoiceTransportSession(
	handle uint64,
	tenantID string,
	callID string,
	participantID string,
	transportSessionID string,
	transport int,
	localDescription map[string]any,
	requestedTTLSeconds int,
	metadata map[string]string,
) (map[string]any, error) {

	if localDescription == nil {
		localDescription = map[string]any{}
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	payload := map[string]any{
		"tenant_id":             tenantID,
		"call_id":               callID,
		"participant_id":        participantID,
		"transport_session_id":  transportSessionID,
		"transport":             transport,
		"local_description":     localDescription,
		"requested_ttl_seconds": requestedTTLSeconds,
		"metadata":              metadata,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceCreateSess)
}

func (b *DendriteBridge) GetVoiceTransportSession(
	handle uint64,
	tenantID string,
	callID string,
	transportSessionID string,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id":            tenantID,
		"call_id":              callID,
		"transport_session_id": transportSessionID,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceGetSess)
}

func (b *DendriteBridge) SetVoiceTransportDescription(
	handle uint64,
	tenantID string,
	callID string,
	transportSessionID string,
	side int,
	description map[string]any,
) (map[string]any, error) {

	if description == nil {
		description = map[string]any{}
	}
	payload := map[string]any{
		"tenant_id":            tenantID,
		"call_id":              callID,
		"transport_session_id": transportSessionID,
		"side":                 side,
		"description":          description,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceSetDesc)
}

func (b *DendriteBridge) AddVoiceTransportCandidate(
	handle uint64,
	tenantID string,
	callID string,
	transportSessionID string,
	side int,
	candidate map[string]any,
) (map[string]any, error) {

	if candidate == nil {
		candidate = map[string]any{}
	}
	payload := map[string]any{
		"tenant_id":            tenantID,
		"call_id":              callID,
		"transport_session_id": transportSessionID,
		"side":                 side,
		"candidate":            candidate,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceAddCand)
}

func (b *DendriteBridge) RefreshVoiceTransportLease(
	handle uint64,
	tenantID string,
	callID string,
	transportSessionID string,
	requestedTTLSeconds int,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id":             tenantID,
		"call_id":               callID,
		"transport_session_id":  transportSessionID,
		"requested_ttl_seconds": requestedTTLSeconds,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceRefresh)
}

func (b *DendriteBridge) EndVoiceTransportSession(
	handle uint64,
	tenantID string,
	callID string,
	transportSessionID string,
	failed bool,
	reason string,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id":            tenantID,
		"call_id":              callID,
		"transport_session_id": transportSessionID,
		"failed":               failed,
		"reason":               reason,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceEndSess)
}

func (b *DendriteBridge) WatchVoiceTransportEvents(
	handle uint64,
	tenantID string,
	callID string,
	transportSessionID string,
	fromSequence uint64,
	maxEvents int,
	timeoutMs int,
) (map[string]any, error) {

	payload := map[string]any{
		"tenant_id":            tenantID,
		"call_id":              callID,
		"transport_session_id": transportSessionID,
		"from_sequence":        fromSequence,
		"max_events":           maxEvents,
		"timeout_ms":           timeoutMs,
	}
	return b.callHelperPayload(handle, payload, b.sym.voiceWatchSess)
}
