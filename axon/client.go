// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/client.go
// Description: Source file for Go SDK facade and Dendrite integration; keeps behavior explicit and interoperable across language/runtime boundaries with ability-bound invocation context.
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
// - Callers should provide valid ability/resource/runtime context before invoking exported APIs; product authority metadata belongs to downstream provider code.
// - Errors should be treated as typed protocol/runtime outcomes rather than silently ignored.
//
// Architectural Position:
// - Part of the Go SDK facade and Dendrite integration layer.
// - Contains only generic client and transport behavior.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package axon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

type Payload map[string]any

type Transport interface {
	Call(ctx context.Context, resourceURA string, payload Payload, options CallOptions) (Payload, error)
}

type CallOptions struct {
	PrincipalID string
}

type RawTransport interface {
	CallRaw(ctx context.Context, resourceURA string, payload Payload, options CallOptions) (Payload, error)
}

type SidecarTransport struct {
	Endpoint         string
	ConnectTimeoutMs int
	// TLSCAPEM is the explicit public CA bundle; configure before using the transport.
	TLSCAPEM    *string
	TimeoutMs   int
	LibraryPath string
	Signing     *SigningConfig

	// AutoReconnect turns on the background reconnect worker. Applications
	// can use ReconnectOptions.OnReconnect to restore provider state after a
	// transport interruption.
	AutoReconnect    bool
	ReconnectOptions ReconnectOptions

	mu        sync.Mutex
	bridge    *DendriteBridge
	handle    uint64
	reconnect *reconnectState
}

func defaultAxonEndpoint() string {
	if env := os.Getenv("AXON_ENDPOINT"); env != "" {
		return env
	}
	return "http://127.0.0.1:50051"
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
	// Kick off the background reconnect worker the first time the transport is
	// used. No-op when AutoReconnect is false; idempotent on repeated calls.
	s.enableAutoReconnect()
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

	var (
		handle uint64
		err    error
	)
	options := DendriteClientOptions{ConnectTimeoutMs: s.ConnectTimeoutMs, TLSCAPEM: s.TLSCAPEM}
	if s.Signing != nil {
		handle, err = s.bridge.OpenSignedClientWithOptions(s.Endpoint, *s.Signing, options)
	} else {
		handle, err = s.bridge.OpenClientWithOptions(s.Endpoint, options)
	}
	if err != nil {
		_ = s.bridge.CloseLibrary()
		s.bridge = nil
		return err
	}
	s.handle = handle
	return nil
}

// Close releases the underlying bridge handle, unloads the native library,
// and joins the auto-reconnect worker goroutine if one was spawned.
//
// Applications **must** call Close on every transport they construct —
// otherwise the reconnect worker keeps the process alive and leaks one
// goroutine per discarded transport. Close is safe to call multiple times
// and is safe to call on a transport that never opened a bridge.
func (s *SidecarTransport) Close() error {
	// Shut down the background reconnect worker before we tear down the
	// bridge so a racing worker can't try to reopen a closed handle. The
	// worker only touches `bridge`/`handle` under `s.mu`, but stopping it
	// first guarantees no resurrect after Close returns.
	s.shutdownReconnect()

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

func (s *SidecarTransport) callInternal(
	ctx context.Context,
	resourceURA string,
	payload Payload,
	options CallOptions,
) (Payload, error) {
	if s.Signing == nil {
		return nil, errors.New("SidecarTransport requires signing for descriptor-bound invocation")
	}
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
	timeoutMs, err := invocationTimeout(ctx, timeoutMs)
	if err != nil {
		return nil, err
	}

	ability, err := ParseAbilityDescriptorRef(resourceURA)
	if err != nil {
		return nil, err
	}
	subject, err := SubjectURAForInvocation(options.PrincipalID, ability.Raw, s.Signing.CallerURA)
	if err != nil {
		return nil, err
	}
	result, err := bridge.InvokeAbilitySigned(handle, SignedInvokeRequest{
		Callee: SignedAgentIdentity{
			URA:     ability.AbilityURA,
			Profile: uraProfileStrictV2,
		},
		Subject: SignedAgentIdentity{
			URA:     subject.Raw,
			Profile: uraProfileStrictV2,
		},
		Ability:     ability.Raw,
		PayloadJSON: payload,
		TimeoutMs:   timeoutMs,
		ContentType: "application/json",
		Metadata:    nil,
	})
	if err != nil {
		// Flag the transport as disconnected when the error smells like a
		// dropped connection so the background worker can drive a reconnect
		// without waiting for the next user call. Harmless if AutoReconnect
		// is off (markDisconnected is a no-op on zero state).
		if isTransportError(err) {
			s.markDisconnected()
		}
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

func extractResultPayload(raw Payload) (Payload, error) {
	result, ok := raw["result_json"]
	if !ok {
		return nil, errors.New("invoke response missing result_json")
	}
	switch typed := result.(type) {
	case Payload:
		return typed, nil
	case map[string]any:
		return Payload(typed), nil
	default:
		return nil, fmt.Errorf("invoke response result_json must be an object for Call; use CallAny or CallRaw for %T", result)
	}
}

func (s *SidecarTransport) Call(
	ctx context.Context,
	resourceURA string,
	payload Payload,
	options CallOptions,
) (Payload, error) {
	raw, err := s.callInternal(ctx, resourceURA, payload, options)
	if err != nil {
		return nil, err
	}
	return extractResultPayload(raw)
}

func (s *SidecarTransport) CallRaw(
	ctx context.Context,
	resourceURA string,
	payload Payload,
	options CallOptions,
) (Payload, error) {
	return s.callInternal(ctx, resourceURA, payload, options)
}

type Client struct {
	transport   Transport
	resourceURA string
	principalID string
}

func NewClient(transport Transport) *Client {
	return &Client{transport: transport}
}

func (c *Client) Ability(resourceURA string) *Client {
	next := *c
	next.resourceURA = strings.TrimSpace(resourceURA)
	return &next
}

func (c *Client) Principal(principalID string) *Client {
	next := *c
	next.principalID = principalID
	return &next
}

func (c *Client) Call(ctx context.Context, payload Payload) (Payload, error) {
	if c.resourceURA == "" {
		return nil, errors.New("ability(...) is required before call(...)")
	}
	if c.transport == nil {
		return nil, errors.New("client requires an explicit signed transport")
	}
	if _, err := requiredAbilityRef(c.resourceURA); err != nil {
		return nil, err
	}
	if c.principalID != "" && strings.TrimSpace(c.principalID) == "" {
		return nil, errors.New("principal(...) cannot be blank")
	}
	return c.transport.Call(ctx, c.resourceURA, payload, c.callOptions())
}

func (c *Client) CallRaw(ctx context.Context, payload Payload) (Payload, error) {
	if c.resourceURA == "" {
		return nil, errors.New("ability(...) is required before callRaw(...)")
	}
	if c.transport == nil {
		return nil, errors.New("client requires an explicit signed transport")
	}
	rawTransport, ok := c.transport.(RawTransport)
	if !ok {
		return nil, errors.New("configured transport does not support callRaw(...)")
	}
	if _, err := requiredAbilityRef(c.resourceURA); err != nil {
		return nil, err
	}
	if c.principalID != "" && strings.TrimSpace(c.principalID) == "" {
		return nil, errors.New("principal(...) cannot be blank")
	}
	return rawTransport.CallRaw(ctx, c.resourceURA, payload, c.callOptions())
}

func (c *Client) callOptions() CallOptions {
	return CallOptions{
		PrincipalID: c.principalID,
	}
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
