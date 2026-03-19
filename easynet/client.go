// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/client.go
// Description: Source file for Go SDK facade and Dendrite integration; keeps behavior explicit and interoperable across language/runtime boundaries with tenant/principal fluent context.
//
// Protocol Responsibility:
// - Implements Go SDK facade and Dendrite integration contracts required by current Axon service and SDK surfaces.
// - Preserves stable request/response semantics and error mapping for client.go call paths.
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

package easynet

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

type Payload map[string]any

type Transport interface {
	Call(ctx context.Context, tenantID string, resourceURI string, payload Payload) (Payload, error)
}

type CallOptions struct {
	PrincipalID string
}

type PrincipalAwareTransport interface {
	Transport
	CallWithOptions(ctx context.Context, tenantID string, resourceURI string, payload Payload, options CallOptions) (Payload, error)
}

type RawTransport interface {
	CallRaw(ctx context.Context, tenantID string, resourceURI string, payload Payload, options CallOptions) (Payload, error)
}

type SidecarTransport struct {
	Endpoint         string
	ConnectTimeoutMs int
	TimeoutMs        int
	LibraryPath      string

	mu     sync.Mutex
	bridge *DendriteBridge
	handle uint64
}

func defaultAxonEndpoint() string {
	if env := os.Getenv("EASYNET_AXON_ENDPOINT"); env != "" {
		return env
	}
	return "http://127.0.0.1:50051"
}

func subjectVisibilityFromResourceURI(resourceURI string) string {
	normalized := strings.ToLower(strings.TrimSpace(resourceURI))
	if strings.HasPrefix(normalized, "easynet:///r/pub/") {
		return "pub"
	}
	if strings.HasPrefix(normalized, "easynet:///r/prv/") {
		return "prv"
	}
	return "org"
}

func principalToSubjectID(principalID string, resourceURI string) string {
	normalized := strings.TrimSpace(principalID)
	if normalized == "" {
		return ""
	}
	if strings.HasPrefix(normalized, "easynet:") {
		return normalized
	}
	return "easynet:" + subjectVisibilityFromResourceURI(resourceURI) + ":reg:agent." + normalized
}

func (s *SidecarTransport) ensureClient() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Endpoint == "" {
		s.Endpoint = defaultAxonEndpoint()
	}
	if s.ConnectTimeoutMs <= 0 {
		s.ConnectTimeoutMs = DefaultConnectTimeoutMs
	}
	if s.TimeoutMs <= 0 {
		s.TimeoutMs = DefaultTimeoutMs
	}
	if s.bridge != nil && s.handle != 0 {
		return nil
	}

	if s.bridge == nil {
		bridge, err := OpenDendriteBridge(s.LibraryPath)
		if err != nil {
			return err
		}
		s.bridge = bridge
	}

	handle, err := s.bridge.OpenClient(s.Endpoint, s.ConnectTimeoutMs)
	if err != nil {
		_ = s.bridge.CloseLibrary()
		s.bridge = nil
		return err
	}
	s.handle = handle
	return nil
}

func (s *SidecarTransport) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var outErr error
	if s.bridge != nil && s.handle != 0 {
		if err := s.bridge.CloseClient(s.handle); err != nil {
			outErr = err
		}
	}
	if s.bridge != nil {
		if err := s.bridge.CloseLibrary(); err != nil && outErr == nil {
			outErr = err
		}
	}
	s.bridge = nil
	s.handle = 0
	return outErr
}

func (s *SidecarTransport) Call(ctx context.Context, tenantID string, resourceURI string, payload Payload) (Payload, error) {
	return s.CallWithOptions(ctx, tenantID, resourceURI, payload, CallOptions{})
}

type bridgeInvoker func(bridge *DendriteBridge, handle uint64, tenantID, resourceURI string, payload Payload, subjectID string, metadata map[string]string, timeoutMs int) (map[string]any, error)

func (s *SidecarTransport) callInternal(
	ctx context.Context,
	tenantID string,
	resourceURI string,
	payload Payload,
	options CallOptions,
	invoke bridgeInvoker,
) (Payload, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	bridge := s.bridge
	handle := s.handle
	timeoutMs := s.TimeoutMs
	s.mu.Unlock()
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		deadlineMs := int(remaining.Milliseconds())
		if deadlineMs < 1 {
			deadlineMs = 1
		}
		if deadlineMs < timeoutMs {
			timeoutMs = deadlineMs
		}
	}

	subjectID := principalToSubjectID(options.PrincipalID, resourceURI)
	result, err := invoke(bridge, handle, tenantID, resourceURI, payload, subjectID, nil, timeoutMs)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if result == nil {
		return Payload{}, nil
	}
	return Payload(result), nil
}

func (s *SidecarTransport) CallWithOptions(
	ctx context.Context,
	tenantID string,
	resourceURI string,
	payload Payload,
	options CallOptions,
) (Payload, error) {
	return s.callInternal(ctx, tenantID, resourceURI, payload, options, func(b *DendriteBridge, handle uint64, tid, ruri string, p Payload, sid string, md map[string]string, tms int) (map[string]any, error) {
		return b.InvokeAbilityWithSubject(handle, tid, ruri, p, sid, md, tms)
	})
}

func (s *SidecarTransport) CallRaw(
	ctx context.Context,
	tenantID string,
	resourceURI string,
	payload Payload,
	options CallOptions,
) (Payload, error) {
	return s.callInternal(ctx, tenantID, resourceURI, payload, options, func(b *DendriteBridge, handle uint64, tid, ruri string, p Payload, sid string, md map[string]string, tms int) (map[string]any, error) {
		return b.InvokeAbilityRawWithSubject(handle, tid, ruri, p, sid, md, tms)
	})
}

type Client struct {
	transport   Transport
	tenantID    string
	resourceURI string
	principalID string
}

func NewClient(transport Transport) *Client {
	if transport == nil {
		transport = &SidecarTransport{
			Endpoint:         defaultAxonEndpoint(),
			ConnectTimeoutMs: DefaultConnectTimeoutMs,
			TimeoutMs:        DefaultTimeoutMs,
		}
	}
	return &Client{transport: transport}
}

func (c *Client) Tenant(tenantID string) *Client {
	next := *c
	next.tenantID = tenantID
	return &next
}

func (c *Client) Ability(resourceURI string) *Client {
	next := *c
	next.resourceURI = resourceURI
	return &next
}

func (c *Client) Principal(principalID string) *Client {
	next := *c
	next.principalID = principalID
	return &next
}

func (c *Client) Call(ctx context.Context, payload Payload) (Payload, error) {
	if c.tenantID == "" {
		return nil, errors.New("tenant(...) is required before call(...)")
	}
	if c.resourceURI == "" {
		return nil, errors.New("ability(...) is required before call(...)")
	}
	if c.principalID != "" {
		if strings.TrimSpace(c.principalID) == "" {
			return nil, errors.New("principal(...) cannot be blank")
		}
		if aware, ok := c.transport.(PrincipalAwareTransport); ok {
			return aware.CallWithOptions(ctx, c.tenantID, c.resourceURI, payload, CallOptions{
				PrincipalID: c.principalID,
			})
		}
		return nil, errors.New("configured transport does not support principal(...) context")
	}
	return c.transport.Call(ctx, c.tenantID, c.resourceURI, payload)
}

func (c *Client) CallRaw(ctx context.Context, payload Payload) (Payload, error) {
	if c.tenantID == "" {
		return nil, errors.New("tenant(...) is required before callRaw(...)")
	}
	if c.resourceURI == "" {
		return nil, errors.New("ability(...) is required before callRaw(...)")
	}
	rawTransport, ok := c.transport.(RawTransport)
	if !ok {
		return nil, errors.New("configured transport does not support callRaw(...)")
	}
	if c.principalID != "" {
		if strings.TrimSpace(c.principalID) == "" {
			return nil, errors.New("principal(...) cannot be blank")
		}
		return rawTransport.CallRaw(ctx, c.tenantID, c.resourceURI, payload, CallOptions{
			PrincipalID: c.principalID,
		})
	}
	return rawTransport.CallRaw(ctx, c.tenantID, c.resourceURI, payload, CallOptions{})
}

func (c *Client) CallAny(ctx context.Context, payload Payload) (any, error) {
	raw, err := c.CallRaw(ctx, payload)
	if err != nil {
		return nil, err
	}
	result, ok := raw["result_json"]
	if !ok {
		return nil, errors.New("invoke response missing result_json")
	}
	return result, nil
}

type AbilityHandler func(ctx context.Context, in Payload) (Payload, error)

type abilityBuilder struct {
	uri string
	h   AbilityHandler
}

var abilityRegistry = struct {
	sync.Mutex
	m map[string]AbilityHandler
}{m: map[string]AbilityHandler{}}

func Ability(resourceURI string) *abilityBuilder {
	return &abilityBuilder{uri: resourceURI}
}

func (b *abilityBuilder) Handle(h AbilityHandler) *abilityBuilder {
	b.h = h
	return b
}

func (b *abilityBuilder) Expose() {
	if b.h != nil {
		abilityRegistry.Lock()
		abilityRegistry.m[b.uri] = b.h
		abilityRegistry.Unlock()
	}
}
