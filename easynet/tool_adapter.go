// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/tool_adapter.go
// Description: Tool adapter that bridges LLM tool-call interfaces (OpenAI, Anthropic, etc.) with Axon ability invocations; enables abilities to be used as native LLM tools without protocol knowledge.
//
// Protocol Responsibility:
// - Converts between LLM tool-call schemas (OpenAI function-calling, Anthropic tool-use) and Axon ability request/response envelopes.
// - Preserves ability identity via resource URI mapping and invocation semantics.
//
// Implementation Approach:
// - Uses functional options pattern consistent with existing Go SDK style.
// - Provides format-specific output (OpenAI, Anthropic, generic) from a unified ToolSpec model.
// - Delegates execution to the existing Client/Transport layer.
//
// Usage Contract:
// - Callers register abilities with schema metadata, then export tool definitions for their LLM of choice.
// - Execution dispatch routes tool calls back through the Axon ability invoke path.
//
// Architectural Position:
// - Sits above the Client/Transport facade; does not touch DendriteBridge directly.
// - Should not embed LLM-specific conversation logic beyond schema translation.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// ---------------------------------------------------------------------------
// ToolSpec: language-neutral tool definition
// ---------------------------------------------------------------------------

// ToolSpec holds a single tool definition that can be exported to any LLM format.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	ResourceURI string         `json:"resource_uri"`
	Parameters  map[string]any `json:"parameters"`
}

// ToOpenAI exports the spec as an OpenAI Responses API / Chat Completions tool definition.
func (s *ToolSpec) ToOpenAI() map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        s.Name,
		"description": s.Description,
		"parameters":  s.Parameters,
	}
}

// ToOpenAIChat exports the spec as an OpenAI Chat Completions API tool definition (nested format).
func (s *ToolSpec) ToOpenAIChat() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        s.Name,
			"description": s.Description,
			"parameters":  s.Parameters,
		},
	}
}

// ToAnthropic exports the spec as an Anthropic Messages API tool definition.
func (s *ToolSpec) ToAnthropic() map[string]any {
	return map[string]any{
		"name":         s.Name,
		"description":  s.Description,
		"input_schema": s.Parameters,
	}
}

// ToDict exports the spec as a generic map (superset of fields).
func (s *ToolSpec) ToDict() map[string]any {
	return map[string]any{
		"name":         s.Name,
		"description":  s.Description,
		"resource_uri": s.ResourceURI,
		"parameters":   s.Parameters,
	}
}

// ---------------------------------------------------------------------------
// LocalHandler type
// ---------------------------------------------------------------------------

// ToolHandler is a function that handles a local tool call.
type ToolHandler func(ctx context.Context, args Payload) (Payload, error)

// ---------------------------------------------------------------------------
// RegisterOption: functional options for registration
// ---------------------------------------------------------------------------

// RegisterOption configures a tool registration.
type RegisterOption func(*registerConfig)

type registerConfig struct {
	description string
	resourceURI string
	parameters  map[string]any
}

// WithToolDescription sets the tool description.
func WithToolDescription(desc string) RegisterOption {
	return func(c *registerConfig) { c.description = desc }
}

// WithToolResourceURI overrides the default resource URI.
func WithToolResourceURI(uri string) RegisterOption {
	return func(c *registerConfig) { c.resourceURI = uri }
}

// WithToolParameters sets explicit JSON Schema parameters.
func WithToolParameters(params map[string]any) RegisterOption {
	return func(c *registerConfig) { c.parameters = params }
}

// ---------------------------------------------------------------------------
// AbilityToolAdapter
// ---------------------------------------------------------------------------

// AbilityToolAdapter bridges LLM tool-call interfaces with Axon ability invocations.
type AbilityToolAdapter struct {
	TenantID    string
	Transport   Transport
	PrincipalID string

	mu            sync.RWMutex
	specs         map[string]*ToolSpec
	localHandlers map[string]ToolHandler
	order         []string
}

// NewToolAdapter creates a new AbilityToolAdapter.
func NewToolAdapter(tenantID string, transport Transport) *AbilityToolAdapter {
	return &AbilityToolAdapter{
		TenantID:      tenantID,
		Transport:     transport,
		specs:         map[string]*ToolSpec{},
		localHandlers: map[string]ToolHandler{},
	}
}

// WithPrincipalID sets the principal context used for remote tool execution.
func (a *AbilityToolAdapter) WithPrincipalID(principalID string) *AbilityToolAdapter {
	a.PrincipalID = principalID
	return a
}

// Register adds a local handler as both a tool definition and executor.
func (a *AbilityToolAdapter) Register(name string, handler ToolHandler, opts ...RegisterOption) *AbilityToolAdapter {
	cfg := &registerConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	uri := cfg.resourceURI
	if uri == "" {
		uri = "easynet:///r/org/" + name
	}
	params := cfg.parameters
	if params == nil {
		params = defaultToolParameters()
	}
	spec := &ToolSpec{
		Name:        name,
		Description: cfg.description,
		ResourceURI: uri,
		Parameters:  params,
	}
	a.mu.Lock()
	if _, exists := a.specs[name]; !exists {
		a.order = append(a.order, name)
	}
	a.specs[name] = spec
	a.localHandlers[name] = handler
	a.mu.Unlock()
	return a
}

// RegisterAbility adds a remote ability as a tool (no local handler).
func (a *AbilityToolAdapter) RegisterAbility(name string, resourceURI string, opts ...RegisterOption) *AbilityToolAdapter {
	cfg := &registerConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	params := cfg.parameters
	if params == nil {
		params = defaultToolParameters()
	}
	spec := &ToolSpec{
		Name:        name,
		Description: cfg.description,
		ResourceURI: resourceURI,
		Parameters:  params,
	}
	a.mu.Lock()
	if _, exists := a.specs[name]; !exists {
		a.order = append(a.order, name)
	}
	a.specs[name] = spec
	delete(a.localHandlers, name)
	a.mu.Unlock()
	return a
}

// -- Export ----------------------------------------------------------------

// Specs returns all registered tool specs in insertion order.
func (a *AbilityToolAdapter) Specs() []*ToolSpec {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make([]*ToolSpec, 0, len(a.order))
	for _, name := range a.order {
		if s, ok := a.specs[name]; ok {
			result = append(result, s)
		}
	}
	return result
}

// AsOpenAITools exports tool definitions in OpenAI format.
func (a *AbilityToolAdapter) AsOpenAITools(names ...string) []map[string]any {
	return mapSpecs(a.iterSpecs(names), (*ToolSpec).ToOpenAI)
}

// AsOpenAIChatTools exports tool definitions in OpenAI Chat Completions format (nested).
func (a *AbilityToolAdapter) AsOpenAIChatTools(names ...string) []map[string]any {
	return mapSpecs(a.iterSpecs(names), (*ToolSpec).ToOpenAIChat)
}

// AsAnthropicTools exports tool definitions in Anthropic format.
func (a *AbilityToolAdapter) AsAnthropicTools(names ...string) []map[string]any {
	return mapSpecs(a.iterSpecs(names), (*ToolSpec).ToAnthropic)
}

// AsDicts exports tool definitions as generic maps.
func (a *AbilityToolAdapter) AsDicts(names ...string) []map[string]any {
	return mapSpecs(a.iterSpecs(names), (*ToolSpec).ToDict)
}

func (a *AbilityToolAdapter) iterSpecs(names []string) []*ToolSpec {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(names) == 0 {
		result := make([]*ToolSpec, 0, len(a.order))
		for _, name := range a.order {
			if s, ok := a.specs[name]; ok {
				result = append(result, s)
			}
		}
		return result
	}
	result := make([]*ToolSpec, 0, len(names))
	for _, name := range names {
		if s, ok := a.specs[name]; ok {
			result = append(result, s)
		}
	}
	return result
}

func mapSpecs(specs []*ToolSpec, fn func(*ToolSpec) map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		result = append(result, fn(s))
	}
	return result
}

// -- Execution -------------------------------------------------------------

// Execute runs a tool call by name.
// If a local handler was registered, it is called directly.
// Otherwise the call is dispatched to the remote ability via the transport.
func (a *AbilityToolAdapter) Execute(ctx context.Context, name string, arguments Payload) (Payload, error) {
	a.mu.RLock()
	handler, hasHandler := a.localHandlers[name]
	spec, hasSpec := a.specs[name]
	a.mu.RUnlock()

	// Local handler path
	if hasHandler {
		return handler(ctx, arguments)
	}

	// Remote ability path
	if !hasSpec {
		return nil, errors.New("unknown tool: " + name)
	}
	if a.Transport == nil {
		return nil, errors.New("no transport configured for remote ability calls")
	}
	if a.PrincipalID != "" {
		principalTransport, ok := a.Transport.(PrincipalAwareTransport)
		if !ok {
			return nil, errors.New("configured transport does not support principal(...) context")
		}
		return principalTransport.CallWithOptions(ctx, a.TenantID, spec.ResourceURI, arguments, CallOptions{
			PrincipalID: a.PrincipalID,
		})
	}
	return a.Transport.Call(ctx, a.TenantID, spec.ResourceURI, arguments)
}

// ExecuteJSON runs a tool call with a JSON string argument and returns JSON string result.
func (a *AbilityToolAdapter) ExecuteJSON(ctx context.Context, name string, argsJSON string) (string, error) {
	var args Payload
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	result, err := a.Execute(ctx, name, args)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func defaultToolParameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
