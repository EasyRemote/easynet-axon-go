// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/semantic.go
// Description: Go semantic DSL for generic Dendrite protocol and ability invocation.
//
// Protocol Responsibility:
// - Provides shape-aware protocol invocation builders over cataloged service/rpc entries.
// - Adds an ability invoke builder with Go-native fluent ergonomics.
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

package axon

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

func WithAbilitySubject(subjectURA string) AbilityOption {
	return func(req *AbilityCallBuilder) {
		req.subjectURA = subjectURA
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

func (s *SemanticBridge) Ability(tenantID string, resourceURA string) *AbilityCallBuilder {
	return &AbilityCallBuilder{
		semantic:    s,
		tenantID:    tenantID,
		resourceURA: resourceURA,
		payloadJSON: map[string]any{},
		metadata:    map[string]string{},
		timeoutMs:   DefaultTimeoutMs,
	}
}

type AbilityCallBuilder struct {
	semantic    *SemanticBridge
	tenantID    string
	resourceURA string
	subjectURA  string
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

func (b *AbilityCallBuilder) Subject(subjectURA string) *AbilityCallBuilder {
	b.subjectURA = subjectURA
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
	if strings.TrimSpace(b.resourceURA) == "" {
		return nil, errors.New("resourceURA is required")
	}
	abilityRef, err := requiredAbilityRef(b.resourceURA)
	if err != nil {
		return nil, err
	}
	timeoutMs := b.timeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	metadata := b.metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	subject, err := ParseSubjectURA(b.subjectURA)
	if err != nil {
		return nil, fmt.Errorf("subject is required before ability invoke: %w", err)
	}
	payloadJSON := b.payloadJSON
	if payloadJSON == nil {
		payloadJSON = map[string]any{}
	}
	return b.semantic.bridge.InvokeAbilityWithSubject(
		b.semantic.handle,
		b.tenantID,
		abilityRef,
		payloadJSON,
		subject.Raw,
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
