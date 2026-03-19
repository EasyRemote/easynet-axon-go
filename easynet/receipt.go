// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/receipt.go
// Description: Lifecycle phase receipts for observability and evaluation.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.
//
// Every phase transition in the Axon ability lifecycle emits a structured
// PhaseReceipt. These receipts serve three purposes:
//
//  1. Audit:      tamper-evident log of what happened, when, and to whom.
//  2. Evaluation: phase-level latency breakdown, failure injection points,
//                 and rollback correctness verification.
//  3. Resume:     after a partial failure, receipts tell the runtime which
//                 phases completed and which need retry.

package easynet

import "time"

// Phase is a phase in the Axon ability lifecycle.
type Phase string

const (
	PhasePublish    Phase = "publish"
	PhaseInstall    Phase = "install"
	PhaseActivate   Phase = "activate"
	PhaseInvoke     Phase = "invoke"
	PhaseDeactivate Phase = "deactivate"
	PhaseUninstall  Phase = "uninstall"
	PhaseDeploy     Phase = "deploy"
)

// PhaseStatus is the outcome of a lifecycle phase transition.
type PhaseStatus string

const (
	PhaseStatusOk      PhaseStatus = "ok"
	PhaseStatusError   PhaseStatus = "error"
	PhaseStatusSkipped PhaseStatus = "skipped"
)

// PhaseReceipt is a structured record of a single lifecycle phase transition.
type PhaseReceipt struct {
	Phase      Phase       `json:"phase"`
	Status     PhaseStatus `json:"status"`
	StartedMs  int64       `json:"started_ms"`
	EndedMs    int64       `json:"ended_ms"`
	DurationMs int64       `json:"duration_ms"`
	TenantID   string      `json:"tenant_id"`
	NodeID     string      `json:"node_id"`
	AbilityID  string      `json:"ability_id"`
	InstallID  string      `json:"install_id,omitempty"`
	Error      string      `json:"error,omitempty"`
	ErrorCode  string      `json:"error_code,omitempty"`
	Metadata   any         `json:"metadata,omitempty"`
}

// PhaseReceiptBuilder accumulates state for an in-progress phase receipt.
type PhaseReceiptBuilder struct {
	phase     Phase
	startedMs int64
	tenantID  string
	nodeID    string
	abilityID string
}

// BeginPhase starts timing a lifecycle phase and returns a builder.
func BeginPhase(phase Phase, tenantID, nodeID, abilityID string) *PhaseReceiptBuilder {
	return &PhaseReceiptBuilder{
		phase:     phase,
		startedMs: time.Now().UnixMilli(),
		tenantID:  tenantID,
		nodeID:    nodeID,
		abilityID: abilityID,
	}
}

// FinishOk completes the phase as successful.
func (b *PhaseReceiptBuilder) FinishOk(installID string, metadata any) PhaseReceipt {
	ended := time.Now().UnixMilli()
	return PhaseReceipt{
		Phase:      b.phase,
		Status:     PhaseStatusOk,
		StartedMs:  b.startedMs,
		EndedMs:    ended,
		DurationMs: ended - b.startedMs,
		TenantID:   b.tenantID,
		NodeID:     b.nodeID,
		AbilityID:  b.abilityID,
		InstallID:  installID,
		Metadata:   metadata,
	}
}

// FinishErr completes the phase as failed. Extracts the canonical error code
// from DendriteError.Code field or any error implementing Code() string.
func (b *PhaseReceiptBuilder) FinishErr(err error) PhaseReceipt {
	ended := time.Now().UnixMilli()
	code := "UNKNOWN"
	if de, ok := err.(DendriteError); ok && de.Code != "" {
		code = de.Code
	} else if coded, ok := err.(interface{ Code() string }); ok {
		code = coded.Code()
	}
	return PhaseReceipt{
		Phase:      b.phase,
		Status:     PhaseStatusError,
		StartedMs:  b.startedMs,
		EndedMs:    ended,
		DurationMs: ended - b.startedMs,
		TenantID:   b.tenantID,
		NodeID:     b.nodeID,
		AbilityID:  b.abilityID,
		Error:      err.Error(),
		ErrorCode:  code,
	}
}

// FinishSkipped marks the phase as intentionally skipped.
func (b *PhaseReceiptBuilder) FinishSkipped(reason string) PhaseReceipt {
	ended := time.Now().UnixMilli()
	return PhaseReceipt{
		Phase:      b.phase,
		Status:     PhaseStatusSkipped,
		StartedMs:  b.startedMs,
		EndedMs:    ended,
		DurationMs: ended - b.startedMs,
		TenantID:   b.tenantID,
		NodeID:     b.nodeID,
		AbilityID:  b.abilityID,
		Metadata:   map[string]string{"skip_reason": reason},
	}
}

// SkippedReceipt creates a zero-duration receipt for a skipped phase.
func SkippedReceipt(phase Phase, tenantID, nodeID, abilityID string) PhaseReceipt {
	now := time.Now().UnixMilli()
	return PhaseReceipt{
		Phase:      phase,
		Status:     PhaseStatusSkipped,
		StartedMs:  now,
		EndedMs:    now,
		DurationMs: 0,
		TenantID:   tenantID,
		NodeID:     nodeID,
		AbilityID:  abilityID,
	}
}

// DeployTrace aggregates receipts for a full deploy operation
// (publish → install → activate).
type DeployTrace struct {
	Receipts []PhaseReceipt `json:"receipts"`
	TotalMs  int64          `json:"total_ms"`
	Ok       bool           `json:"ok"`
}

// BuildDeployTrace creates a DeployTrace from a sequence of receipts.
func BuildDeployTrace(receipts []PhaseReceipt) DeployTrace {
	ok := true
	for _, r := range receipts {
		if r.Status == PhaseStatusError {
			ok = false
			break
		}
	}
	var totalMs int64
	if len(receipts) > 0 {
		totalMs = receipts[len(receipts)-1].EndedMs - receipts[0].StartedMs
	}
	return DeployTrace{
		Receipts: receipts,
		TotalMs:  totalMs,
		Ok:       ok,
	}
}

// Phase returns the receipt for a specific phase, or nil if not found.
func (t *DeployTrace) Phase(phase Phase) *PhaseReceipt {
	for i := range t.Receipts {
		if t.Receipts[i].Phase == phase {
			return &t.Receipts[i]
		}
	}
	return nil
}

// PhaseDurationMs returns the duration of a specific phase in milliseconds,
// or -1 if the phase is not found.
func (t *DeployTrace) PhaseDurationMs(phase Phase) int64 {
	r := t.Phase(phase)
	if r == nil {
		return -1
	}
	return r.DurationMs
}
