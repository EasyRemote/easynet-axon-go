// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/orchestrator.go
// Description: Core orchestrator struct and lifecycle (Open/Close), node selection, and
//              functional options. Coordinates common control-plane flows against Axon.
//
// Protocol Responsibility:
// - Centralizes common control-plane flows so example servers stay thin.
// - Preserves typed request/response semantics (uses existing request models).
//
// Implementation Approach:
// - Builder-style options; explicit Open/Close lifecycle; context-agnostic helpers keep
//   behavior deterministic and testable.
// - Avoids hidden globals; callers pass tenant/endpoint explicitly.
//
// Architectural Position:
// - Part of Go SDK higher-level facade atop Dendrite bridge.
// - Pure helper layer: no transport policy beyond what bridge enforces.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Orchestrator coordinates common control-plane flows against Axon.
type Orchestrator struct {
	Endpoint         string
	Tenant           string
	ConnectTimeoutMs int
	TimeoutMs        int
	LibraryPath      string
	mu               sync.Mutex

	bridge *DendriteBridge
	handle uint64

	// Optional hooks (override-friendly via options)
	signer          func(packageID, capabilityName, digest string) string
	nodeSelector    func(nodes []map[string]any) (map[string]any, error)
	uninstallReason string
}

// OrchestratorOption overrides Orchestrator defaults.
type OrchestratorOption func(*Orchestrator)

// WithEndpoint sets Axon endpoint.
func WithEndpoint(endpoint string) OrchestratorOption {
	return func(o *Orchestrator) { o.Endpoint = endpoint }
}

// WithTenant sets default tenant.
func WithTenant(tenant string) OrchestratorOption {
	return func(o *Orchestrator) { o.Tenant = tenant }
}

// WithConnectTimeoutMs sets bridge connect timeout.
func WithConnectTimeoutMs(ms int) OrchestratorOption {
	return func(o *Orchestrator) { o.ConnectTimeoutMs = ms }
}

// WithTimeoutMs sets per-operation timeout fallback (best-effort; bridge still applies its own defaults).
func WithTimeoutMs(ms int) OrchestratorOption { return func(o *Orchestrator) { o.TimeoutMs = ms } }

// WithLibraryPath sets explicit native bridge path (overrides env/default resolution when non-empty).
func WithLibraryPath(path string) OrchestratorOption {
	return func(o *Orchestrator) { o.LibraryPath = path }
}

// WithSigner overrides how publish requests are signed.
func WithSigner(signer func(packageID, capabilityName, digest string) string) OrchestratorOption {
	return func(o *Orchestrator) { o.signer = signer }
}

// WithNodeSelector sets a custom node selection strategy when nodeID is not provided.
func WithNodeSelector(sel func(nodes []map[string]any) (map[string]any, error)) OrchestratorOption {
	return func(o *Orchestrator) { o.nodeSelector = sel }
}

// WithUninstallReason sets the default deactivate/uninstall reason used in cleanup.
func WithUninstallReason(reason string) OrchestratorOption {
	return func(o *Orchestrator) { o.uninstallReason = strings.TrimSpace(reason) }
}

// NewOrchestrator creates an Orchestrator with sane defaults.
func NewOrchestrator(opts ...OrchestratorOption) *Orchestrator {
	o := &Orchestrator{
		Endpoint:         defaultAxonEndpoint(),
		ConnectTimeoutMs: DefaultConnectTimeoutMs,
		TimeoutMs:        DefaultTimeoutMs,
		uninstallReason:  DefaultUninstallReason,
		signer:           deterministicSignatureTokenBase64,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// Open loads the native bridge and opens a client handle.
func (o *Orchestrator) Open() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.bridge != nil && o.handle != 0 {
		return nil
	}
	b, err := OpenDendriteBridge(o.LibraryPath)
	if err != nil {
		return err
	}
	h, err := b.OpenClient(o.Endpoint, o.ConnectTimeoutMs)
	if err != nil {
		_ = b.CloseLibrary()
		return err
	}
	o.bridge = b
	o.handle = h
	return nil
}

// Close releases client handle and unloads the native bridge.
func (o *Orchestrator) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	var out error
	if o.bridge != nil && o.handle != 0 {
		if err := o.bridge.CloseClient(o.handle); err != nil {
			out = err
		}
	}
	if o.bridge != nil {
		if err := o.bridge.CloseLibrary(); err != nil && out == nil {
			out = err
		}
	}
	o.bridge, o.handle = nil, 0
	return out
}

// ListNodes returns nodes under the given tenant, optionally filtered by owner id ("" for no filter).
func (o *Orchestrator) ListNodes(ownerID string) ([]map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	return o.bridge.ListNodes(o.handle, o.Tenant, ownerID)
}

// SelectNode chooses a node by explicit id, custom selector, or the first online node.
//
// Precedence:
//  1. Explicit nodeID — look up by exact match.
//  2. Custom nodeSelector (if set and nodeID is empty) — delegate to caller-supplied strategy.
//  3. Default — first online node sorted by node_id.
func (o *Orchestrator) SelectNode(nodeID string, ownerID string) (map[string]any, error) {
	nodes, err := o.ListNodes(ownerID)
	if err != nil {
		return nil, err
	}

	// 1. Explicit nodeID takes highest precedence.
	if strings.TrimSpace(nodeID) != "" {
		for _, n := range nodes {
			if strings.TrimSpace(fmt.Sprint(n["node_id"])) == nodeID {
				return n, nil
			}
		}
		return nil, fmt.Errorf("target node not found: %s", nodeID)
	}

	// 2. Custom selector when nodeID is empty.
	if o.nodeSelector != nil {
		picked, selErr := o.nodeSelector(nodes)
		if selErr != nil {
			return nil, selErr
		}
		if picked != nil {
			return picked, nil
		}
		// Selector returned nil without error — fall through to default selection.
	}

	// 3. Default: first online node sorted by node_id.
	online := make([]map[string]any, 0)
	for _, n := range nodes {
		if ok, cast := n["online"].(bool); cast && ok {
			online = append(online, n)
		}
	}
	if len(online) == 0 {
		return nil, errors.New("no online nodes available for dispatch")
	}
	sort.Slice(online, func(i, j int) bool {
		return strings.TrimSpace(fmt.Sprint(online[i]["node_id"])) < strings.TrimSpace(fmt.Sprint(online[j]["node_id"]))
	})
	return online[0], nil
}

// deterministicSignatureTokenBase64 provides a deterministic placeholder signature derived from inputs.
func deterministicSignatureTokenBase64(packageID, capabilityName, digest string) string {
	seed := []byte(fmt.Sprintf("%s:%s:%s", packageID, capabilityName, digest))
	h := sha256.Sum256(seed)
	return base64.StdEncoding.EncodeToString(h[:])
}
