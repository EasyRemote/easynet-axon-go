// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/semantic.go
// Description: Go semantic DSL (builder + functional options) for Dendrite protocol/ability/control operations.
//
// Protocol Responsibility:
// - Provides shape-aware protocol invocation builders over cataloged service/rpc entries.
// - Adds ability invoke builder and tenant/node-scoped control helpers with Go-native fluent ergonomics.
//
// Implementation Approach:
// - Caches protocol catalog index and resolves calls by service/rpc or path.
// - Delegates execution to existing `DendriteBridge` methods to preserve wire-level behavior.
//
// Usage Contract:
// - Requires an opened `DendriteBridge` and valid session handle.
// - Builder calls should set request payload bytes/chunks before invoking stream/unary operations.
//
// Architectural Position:
// - Optional ergonomics layer above low-level Go bridge bindings.
// - Must not alter runtime protocol semantics or helper output envelopes.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

type ProtocolCatalogEntry struct {
	Service string
	RPC     string
	Path    string
	Shape   string
	Proto   string
}

type ProtocolOption func(*ProtocolInvokeRequest)

func WithMetadata(metadata map[string]string) ProtocolOption {
	return func(req *ProtocolInvokeRequest) {
		req.Metadata = metadata
	}
}

// WithProtocolTimeoutMs sets per-call timeout for protocol invocations.
func WithProtocolTimeoutMs(timeoutMs int) ProtocolOption {
	return func(req *ProtocolInvokeRequest) {
		req.TimeoutMs = timeoutMs
	}
}

func WithMaxChunks(maxChunks int) ProtocolOption {
	return func(req *ProtocolInvokeRequest) {
		req.MaxChunks = maxChunks
	}
}

func WithMaxRequestChunks(maxRequestChunks int) ProtocolOption {
	return func(req *ProtocolInvokeRequest) {
		req.MaxRequestChunks = maxRequestChunks
	}
}

func WithMaxResponseChunks(maxResponseChunks int) ProtocolOption {
	return func(req *ProtocolInvokeRequest) {
		req.MaxResponseChunks = maxResponseChunks
	}
}

type AbilityOption func(*AbilityCallBuilder)

func WithAbilityMetadata(metadata map[string]string) AbilityOption {
	return func(req *AbilityCallBuilder) {
		req.metadata = metadata
	}
}

func WithAbilityTimeoutMs(timeoutMs int) AbilityOption {
	return func(req *AbilityCallBuilder) {
		req.timeoutMs = timeoutMs
	}
}

type SemanticBridge struct {
	bridge       *DendriteBridge
	handle       uint64
	byServiceRPC map[string]ProtocolCatalogEntry
	byPath       map[string]ProtocolCatalogEntry
}

func NewSemanticBridge(bridge *DendriteBridge, handle uint64) (*SemanticBridge, error) {
	if bridge == nil {
		return nil, errors.New("bridge is nil")
	}
	if handle == 0 {
		return nil, errors.New("handle must be > 0")
	}

	catalogPayload, err := bridge.ProtocolCatalog()
	if err != nil {
		return nil, err
	}

	rowsAny, ok := catalogPayload["rpcs"].([]any)
	if !ok {
		return nil, errors.New("protocol catalog missing rpcs list")
	}

	byServiceRPC := map[string]ProtocolCatalogEntry{}
	byPath := map[string]ProtocolCatalogEntry{}
	for _, rowAny := range rowsAny {
		row, ok := rowAny.(map[string]any)
		if !ok {
			continue
		}
		entry := ProtocolCatalogEntry{
			Service: fmt.Sprint(row["service"]),
			RPC:     fmt.Sprint(row["rpc"]),
			Path:    fmt.Sprint(row["path"]),
			Shape:   fmt.Sprint(row["shape"]),
			Proto:   fmt.Sprint(row["proto"]),
		}
		if entry.Service != "" && entry.RPC != "" {
			byServiceRPC[entry.Service+"."+entry.RPC] = entry
		}
		if entry.Path != "" {
			byPath[entry.Path] = entry
		}
	}

	return &SemanticBridge{
		bridge:       bridge,
		handle:       handle,
		byServiceRPC: byServiceRPC,
		byPath:       byPath,
	}, nil
}

func (s *SemanticBridge) Service(service string) *ServiceScope {
	return &ServiceScope{semantic: s, service: service}
}

func (s *SemanticBridge) Path(path string) *ProtocolCallBuilder {
	entry, hasEntry := s.byPath[path]
	builder := &ProtocolCallBuilder{
		semantic: s,
		req: ProtocolInvokeRequest{
			Path:              path,
			TimeoutMs:         DefaultTimeoutMs,
			MaxChunks:         DefaultMaxChunks,
			MaxRequestChunks:  DefaultMaxChunks,
			MaxResponseChunks: DefaultMaxChunks,
		},
	}
	if hasEntry {
		entryCopy := entry
		builder.entry = &entryCopy
		builder.req.Service = entry.Service
		builder.req.RPC = entry.RPC
	}
	return builder
}

func (s *SemanticBridge) Tenant(tenantID string) *TenantScope {
	return &TenantScope{semantic: s, tenantID: tenantID}
}

func (s *SemanticBridge) Ability(tenantID string, resourceURI string) *AbilityCallBuilder {
	return &AbilityCallBuilder{
		semantic:    s,
		tenantID:    tenantID,
		resourceURI: resourceURI,
		payloadJSON: map[string]any{},
		metadata:    map[string]string{},
		timeoutMs:   DefaultTimeoutMs,
	}
}

type AbilityCallBuilder struct {
	semantic    *SemanticBridge
	tenantID    string
	resourceURI string
	payloadJSON any
	metadata    map[string]string
	timeoutMs   int
}

func (b *AbilityCallBuilder) Payload(payloadJSON any) *AbilityCallBuilder {
	b.payloadJSON = payloadJSON
	return b
}

func (b *AbilityCallBuilder) Metadata(metadata map[string]string) *AbilityCallBuilder {
	if metadata == nil {
		b.metadata = map[string]string{}
		return b
	}
	b.metadata = metadata
	return b
}

func (b *AbilityCallBuilder) TimeoutMs(timeoutMs int) *AbilityCallBuilder {
	b.timeoutMs = timeoutMs
	return b
}

func (b *AbilityCallBuilder) Apply(opts ...AbilityOption) *AbilityCallBuilder {
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	return b
}

func (b *AbilityCallBuilder) Invoke() (map[string]any, error) {
	if strings.TrimSpace(b.tenantID) == "" {
		return nil, errors.New("tenantID is required")
	}
	if strings.TrimSpace(b.resourceURI) == "" {
		return nil, errors.New("resourceURI is required")
	}
	timeoutMs := b.timeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	metadata := b.metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	payloadJSON := b.payloadJSON
	if payloadJSON == nil {
		payloadJSON = map[string]any{}
	}
	return b.semantic.bridge.InvokeAbility(
		b.semantic.handle,
		b.tenantID,
		b.resourceURI,
		payloadJSON,
		metadata,
		timeoutMs,
	)
}

type ServiceScope struct {
	semantic *SemanticBridge
	service  string
}

func (s *ServiceScope) RPC(rpc string) *ProtocolCallBuilder {
	key := s.service + "." + rpc
	entry, hasEntry := s.semantic.byServiceRPC[key]
	builder := &ProtocolCallBuilder{
		semantic: s.semantic,
		req: ProtocolInvokeRequest{
			Service:           s.service,
			RPC:               rpc,
			TimeoutMs:         DefaultTimeoutMs,
			MaxChunks:         DefaultMaxChunks,
			MaxRequestChunks:  DefaultMaxChunks,
			MaxResponseChunks: DefaultMaxChunks,
		},
	}
	if hasEntry {
		entryCopy := entry
		builder.entry = &entryCopy
		builder.req.Path = entry.Path
	}
	return builder
}

type ProtocolCallBuilder struct {
	semantic *SemanticBridge
	req      ProtocolInvokeRequest
	entry    *ProtocolCatalogEntry
}

func (b *ProtocolCallBuilder) RequestBytes(request []byte) *ProtocolCallBuilder {
	b.req.RequestBase64 = base64.StdEncoding.EncodeToString(request)
	b.req.RequestChunksBase64 = nil
	return b
}

func (b *ProtocolCallBuilder) RequestChunks(chunks [][]byte) *ProtocolCallBuilder {
	encoded := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		encoded = append(encoded, base64.StdEncoding.EncodeToString(chunk))
	}
	b.req.RequestChunksBase64 = encoded
	b.req.RequestBase64 = ""
	return b
}

func (b *ProtocolCallBuilder) Apply(opts ...ProtocolOption) *ProtocolCallBuilder {
	for _, opt := range opts {
		if opt != nil {
			opt(&b.req)
		}
	}
	return b
}

func (b *ProtocolCallBuilder) Invoke() (map[string]any, error) {
	if b.req.TimeoutMs <= 0 {
		b.req.TimeoutMs = DefaultTimeoutMs
	}
	if b.req.MaxChunks <= 0 {
		b.req.MaxChunks = DefaultMaxChunks
	}
	if b.req.MaxRequestChunks <= 0 {
		b.req.MaxRequestChunks = DefaultMaxChunks
	}
	if b.req.MaxResponseChunks <= 0 {
		b.req.MaxResponseChunks = DefaultMaxChunks
	}
	return b.semantic.bridge.InvokeProtocol(b.semantic.handle, b.req)
}

func (b *ProtocolCallBuilder) InvokeUnary() ([]byte, error) {
	if err := b.expectShape("unary"); err != nil {
		return nil, err
	}
	resp, err := b.Invoke()
	if err != nil {
		return nil, err
	}
	raw, ok := resp["response_base64"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("missing unary response_base64: %v", resp)
	}
	return base64.StdEncoding.DecodeString(raw)
}

func (b *ProtocolCallBuilder) InvokeServerStream() ([][]byte, bool, error) {
	if err := b.expectShape("server_stream"); err != nil {
		return nil, false, err
	}
	resp, err := b.Invoke()
	if err != nil {
		return nil, false, err
	}
	chunks, err := decodeChunkList(resp["chunks_base64"])
	if err != nil {
		return nil, false, err
	}
	truncated, _ := resp["truncated"].(bool)
	return chunks, truncated, nil
}

func (b *ProtocolCallBuilder) InvokeClientStream() ([]byte, error) {
	if err := b.expectShape("client_stream"); err != nil {
		return nil, err
	}
	resp, err := b.Invoke()
	if err != nil {
		return nil, err
	}
	raw, ok := resp["response_base64"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("missing client-stream response_base64: %v", resp)
	}
	return base64.StdEncoding.DecodeString(raw)
}

func (b *ProtocolCallBuilder) InvokeBidiStream() ([][]byte, bool, error) {
	if err := b.expectShape("bidi_stream"); err != nil {
		return nil, false, err
	}
	resp, err := b.Invoke()
	if err != nil {
		return nil, false, err
	}
	chunks, err := decodeChunkList(resp["chunks_base64"])
	if err != nil {
		return nil, false, err
	}
	truncated, _ := resp["truncated"].(bool)
	return chunks, truncated, nil
}

func (b *ProtocolCallBuilder) expectShape(expected string) error {
	if b.entry == nil || b.entry.Shape == "" {
		return nil
	}
	if b.entry.Shape != expected {
		return fmt.Errorf(
			"rpc shape mismatch, expected=%s actual=%s rpc=%s.%s",
			expected,
			b.entry.Shape,
			b.entry.Service,
			b.entry.RPC,
		)
	}
	return nil
}

func decodeChunkList(raw any) ([][]byte, error) {
	items, ok := raw.([]any)
	if !ok {
		return [][]byte{}, nil
	}
	out := make([][]byte, 0, len(items))
	for _, item := range items {
		decoded, err := base64.StdEncoding.DecodeString(fmt.Sprint(item))
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}
	return out, nil
}

type TenantScope struct {
	semantic *SemanticBridge
	tenantID string
}

func (t *TenantScope) ListNodes(ownerID string) ([]map[string]any, error) {
	return t.semantic.bridge.ListNodes(t.semantic.handle, t.tenantID, ownerID)
}

func (t *TenantScope) ListMCPTools(namePattern string, tags []string, nodeID string) ([]map[string]any, error) {
	return t.semantic.bridge.ListMCPTools(t.semantic.handle, t.tenantID, namePattern, tags, nodeID)
}

func (t *TenantScope) Node(nodeID string) *NodeScope {
	return &NodeScope{tenant: t, nodeID: nodeID}
}

type NodeScope struct {
	tenant *TenantScope
	nodeID string
}

func (n *NodeScope) DeployMCPListDir(targetPath, commandTemplate string) (map[string]any, error) {
	return n.DeployMCPListDirWithRequest(DeployMCPListDirRequest{
		TargetPath:      targetPath,
		CommandTemplate: commandTemplate,
	})
}

// DeployMCPListDirWithRequest deploys an MCP list-dir capability on this node.
// Note: req.TenantID and req.NodeID are overridden by this scope's tenant and node.
func (n *NodeScope) DeployMCPListDirWithRequest(
	req DeployMCPListDirRequest,
) (map[string]any, error) {
	req.TenantID = n.tenant.tenantID
	req.NodeID = n.nodeID
	return n.tenant.semantic.bridge.DeployMCPListDirWithRequest(
		n.tenant.semantic.handle,
		req,
	)
}

func (n *NodeScope) CallMCPTool(toolName string, argumentsJSON any) (map[string]any, error) {
	return n.tenant.semantic.bridge.CallMCPTool(
		n.tenant.semantic.handle,
		n.tenant.tenantID,
		toolName,
		n.nodeID,
		argumentsJSON,
	)
}

func (n *NodeScope) UninstallCapability(installID string, deactivateReason string) (map[string]any, error) {
	return n.tenant.semantic.bridge.UninstallCapabilityWithRequest(
		n.tenant.semantic.handle,
		UninstallCapabilityRequest{
			TenantID:         n.tenant.tenantID,
			NodeID:           n.nodeID,
			InstallID:        installID,
			DeactivateFirst:  BoolPtr(true),
			DeactivateReason: deactivateReason,
			Force:            BoolPtr(false),
		},
	)
}

// use BoolPtr from coerce.go to avoid duplicate definitions

func (n *NodeScope) UpdateMCPListDir(
	existingInstallID,
	targetPath,
	commandTemplate,
	version string,
) (map[string]any, error) {
	deactivate := true
	uninstall := true
	force := false
	return n.UpdateMCPListDirWithRequest(UpdateMCPListDirRequest{
		ExistingInstallID: existingInstallID,
		DeactivateOld:     &deactivate,
		UninstallOld:      &uninstall,
		ForceUninstall:    &force,
		TargetPath:        targetPath,
		CommandTemplate:   commandTemplate,
		Version:           version,
	})
}

// UpdateMCPListDirWithRequest replaces an existing MCP list-dir install on this node.
// Note: req.TenantID and req.NodeID are overridden by this scope's tenant and node.
func (n *NodeScope) UpdateMCPListDirWithRequest(
	req UpdateMCPListDirRequest,
) (map[string]any, error) {
	req.TenantID = n.tenant.tenantID
	req.NodeID = n.nodeID
	return n.tenant.semantic.bridge.UpdateMCPListDirWithRequest(
		n.tenant.semantic.handle,
		req,
	)
}
