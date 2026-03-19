// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/dendrite_bridge_cgo.go
// Description: Source file for Go SDK facade and Dendrite integration; keeps behavior explicit and interoperable across language/runtime boundaries, including tenant/principal invocation context bridging.
//
// Protocol Responsibility:
// - Implements Go SDK facade and Dendrite integration contracts required by current Axon service and SDK surfaces.
// - Preserves stable request/response semantics and error mapping for dendrite_bridge_cgo.go call paths.
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

//go:build cgo

package easynet

/*
#cgo darwin LDFLAGS: -ldl
#cgo linux LDFLAGS: -ldl

#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>

typedef char* (*fn_open_t)(const char*);
typedef char* (*fn_close_t)(uint64_t);
typedef char* (*fn_unary_t)(uint64_t, const char*);
typedef char* (*fn_stream_t)(uint64_t, const char*);
typedef char* (*fn_cov_t)(void);
typedef void (*fn_free_t)(char*);

static void* axon_dlopen(const char* path) {
    return dlopen(path, RTLD_NOW | RTLD_LOCAL);
}

static int axon_dlclose(void* h) {
    return dlclose(h);
}

static const char* axon_dlerror(void) {
    const char* e = dlerror();
    return e;
}

static void* axon_dlsym(void* h, const char* sym) {
    return dlsym(h, sym);
}

static char* axon_call_open(void* fn, const char* payload) {
    return ((fn_open_t)fn)(payload);
}

static char* axon_call_close(void* fn, uint64_t handle) {
    return ((fn_close_t)fn)(handle);
}

static char* axon_call_unary(void* fn, uint64_t handle, const char* payload) {
    return ((fn_unary_t)fn)(handle, payload);
}

static char* axon_call_stream(void* fn, uint64_t handle, const char* payload) {
    return ((fn_stream_t)fn)(handle, payload);
}

static char* axon_call_client_stream(void* fn, uint64_t handle, const char* payload) {
    return ((fn_stream_t)fn)(handle, payload);
}

static char* axon_call_bidi_stream(void* fn, uint64_t handle, const char* payload) {
    return ((fn_stream_t)fn)(handle, payload);
}

static char* axon_call_invoke_ability(void* fn, uint64_t handle, const char* payload) {
    return ((fn_stream_t)fn)(handle, payload);
}

static char* axon_call_cov(void* fn) {
    return ((fn_cov_t)fn)();
}

static void axon_call_free(void* fn, char* payload) {
    ((fn_free_t)fn)(payload);
}
*/
import "C"

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"
)

const (
	// defaultDeploySignatureSentinelBase64 is base64([0x07, 0x07, 0x07]).
	// The deploy signature resolution order is:
	// 1) explicit parameter
	// 2) AXON_DEPLOY_SIGNATURE_BASE64
	// 3) sentinel (only if AXON_ALLOW_PLACEHOLDER_DEPLOY_SIGNATURE=1|true|yes)
	// Otherwise resolution fails with an error.
	// Uses the canonical EphemeralSignature from ability_lifecycle.go.
	defaultDeploySignatureSentinelBase64 = EphemeralSignature
)

var errDendriteBridgeNotOpened = DendriteError{Message: "dendrite bridge not opened"}
var placeholderDeploySignatureWarning sync.Once

// DendriteError and its Error() method are in dendrite_bridge_types.go.

type dendriteBridgeSymbols struct {
	open            unsafe.Pointer
	close           unsafe.Pointer
	unary           unsafe.Pointer
	stream          unsafe.Pointer
	clientStream    unsafe.Pointer
	bidiStream      unsafe.Pointer
	invokeAbility   unsafe.Pointer
	protocolCatalog unsafe.Pointer
	invokeProtocol  unsafe.Pointer
	listNodes       unsafe.Pointer
	registerNode    unsafe.Pointer
	heartbeat       unsafe.Pointer
	publishAbility  unsafe.Pointer
	installAbility  unsafe.Pointer
	activateAbility unsafe.Pointer
	listA2aAgents   unsafe.Pointer
	getA2aAgentCard unsafe.Pointer
	sendA2aTask     unsafe.Pointer
	deployListDir   unsafe.Pointer
	listMcpTools    unsafe.Pointer
	callMcpTool     unsafe.Pointer
	uninstall       unsafe.Pointer
	deregisterNode  unsafe.Pointer
	drainNode       unsafe.Pointer
	updateListDir   unsafe.Pointer
	voiceCreateCall unsafe.Pointer
	voiceGetCall    unsafe.Pointer
	voiceJoinCall   unsafe.Pointer
	voiceLeaveCall  unsafe.Pointer
	voiceUpdatePath unsafe.Pointer
	voiceReport     unsafe.Pointer
	voiceEndCall    unsafe.Pointer
	voiceWatchCall  unsafe.Pointer
	voiceCreateSess unsafe.Pointer
	voiceGetSess    unsafe.Pointer
	voiceSetDesc    unsafe.Pointer
	voiceAddCand    unsafe.Pointer
	voiceRefresh    unsafe.Pointer
	voiceEndSess    unsafe.Pointer
	voiceWatchSess  unsafe.Pointer
	coverage        unsafe.Pointer
	stringFree      unsafe.Pointer
	// Incremental streaming (optional — may be nil for older native libraries)
	serverStreamOpen unsafe.Pointer
	streamNext       unsafe.Pointer
	streamClose      unsafe.Pointer
	bidiStreamOpen   unsafe.Pointer
	bidiStreamSend   unsafe.Pointer
}

type DendriteBridge struct {
	mu      sync.RWMutex
	libPath string
	lib     unsafe.Pointer
	sym     dendriteBridgeSymbols
}

type dendriteBridgeJSON struct {
	OK             bool           `json:"ok"`
	Error          string         `json:"error,omitempty"`
	Handle         uint64         `json:"handle,omitempty"`
	ResponseBase64 string         `json:"response_base64,omitempty"`
	ChunksBase64   []string       `json:"chunks_base64,omitempty"`
	Truncated      bool           `json:"truncated,omitempty"`
	Payload        map[string]any `json:"-"`
}

// ProtocolInvokeRequest is in dendrite_bridge_types.go.

func allowPlaceholderDeploySignature() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AXON_ALLOW_PLACEHOLDER_DEPLOY_SIGNATURE"))) {
	case "1", "true", "yes":
		placeholderDeploySignatureWarning.Do(func() {
			fmt.Fprintln(os.Stderr, "warning: AXON_ALLOW_PLACEHOLDER_DEPLOY_SIGNATURE is enabled; the placeholder deploy signature is for local development only and must not be used in production")
		})
		return true
	default:
		return false
	}
}

func resolveDeploySignatureBase64(signatureBase64 string) (string, error) {
	if trimmed := strings.TrimSpace(signatureBase64); trimmed != "" {
		return trimmed, nil
	}
	if fromEnv := strings.TrimSpace(os.Getenv("AXON_DEPLOY_SIGNATURE_BASE64")); fromEnv != "" {
		return fromEnv, nil
	}
	if allowPlaceholderDeploySignature() {
		return defaultDeploySignatureSentinelBase64, nil
	}
	return "", errors.New(
		"deploy signature required: set signatureBase64, AXON_DEPLOY_SIGNATURE_BASE64, or explicitly enable AXON_ALLOW_PLACEHOLDER_DEPLOY_SIGNATURE",
	)
}

func ResolveDendriteLibraryPath(explicitPath string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}
	if env := os.Getenv("EASYNET_DENDRITE_BRIDGE_LIB"); env != "" {
		return env, nil
	}
	candidates := []string{
		"./native/libaxon_dendrite_bridge.dylib",
		"./native/libaxon_dendrite_bridge.so",
		"./native/axon_dendrite_bridge.dll",
		"libaxon_dendrite_bridge.dylib",
		"libaxon_dendrite_bridge.so",
		"axon_dendrite_bridge.dll",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	exe, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exe)
		for _, c := range []string{
			filepath.Join(exeDir, "native", "libaxon_dendrite_bridge.dylib"),
			filepath.Join(exeDir, "native", "libaxon_dendrite_bridge.so"),
			filepath.Join(exeDir, "native", "axon_dendrite_bridge.dll"),
		} {
			if _, statErr := os.Stat(c); statErr == nil {
				return c, nil
			}
		}
	}

	return "", DendriteError{Message: "dendrite bridge library not found; set EASYNET_DENDRITE_BRIDGE_LIB or pass explicit path"}
}

func OpenDendriteBridge(libPath string) (*DendriteBridge, error) {
	resolved, err := ResolveDendriteLibraryPath(libPath)
	if err != nil {
		return nil, err
	}

	cPath := C.CString(resolved)
	defer C.free(unsafe.Pointer(cPath))

	lib := C.axon_dlopen(cPath)
	if lib == nil {
		return nil, DendriteError{Message: fmt.Sprintf("dlopen failed for %s: %s", resolved, goDLError())}
	}

	sym := dendriteBridgeSymbols{}
	lookup := func(name string) (unsafe.Pointer, error) {
		cName := C.CString(name)
		defer C.free(unsafe.Pointer(cName))
		ptr := C.axon_dlsym(lib, cName)
		if ptr == nil {
			return nil, DendriteError{Message: fmt.Sprintf("dlsym failed for %s: %s", name, goDLError())}
		}
		return ptr, nil
	}
	lookupOptional := func(name string) unsafe.Pointer {
		ptr, err := lookup(name)
		if err != nil {
			return nil
		}
		return ptr
	}

	if sym.open, err = lookup("axon_dendrite_client_open_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.close, err = lookup("axon_dendrite_client_close_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.unary, err = lookup("axon_dendrite_unary_call_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.stream, err = lookup("axon_dendrite_server_stream_call_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.clientStream, err = lookup("axon_dendrite_client_stream_call_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.bidiStream, err = lookup("axon_dendrite_bidi_stream_call_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.invokeAbility, err = lookup("axon_dendrite_invoke_ability_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.protocolCatalog, err = lookup("axon_dendrite_protocol_catalog_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.invokeProtocol, err = lookup("axon_dendrite_invoke_protocol_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.listNodes, err = lookup("axon_dendrite_list_nodes_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.registerNode, err = lookup("axon_dendrite_register_node_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.heartbeat, err = lookup("axon_dendrite_heartbeat_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.publishAbility, err = lookup("axon_dendrite_publish_capability_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.installAbility, err = lookup("axon_dendrite_install_capability_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.activateAbility, err = lookup("axon_dendrite_activate_capability_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.listA2aAgents, err = lookup("axon_dendrite_list_a2a_agents_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.getA2aAgentCard, err = lookup("axon_dendrite_get_a2a_agent_card_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.sendA2aTask, err = lookup("axon_dendrite_send_a2a_task_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.deployListDir, err = lookup("axon_dendrite_deploy_mcp_list_dir_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.listMcpTools, err = lookup("axon_dendrite_list_mcp_tools_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.callMcpTool, err = lookup("axon_dendrite_call_mcp_tool_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.uninstall, err = lookup("axon_dendrite_uninstall_capability_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	sym.deregisterNode = lookupOptional("axon_dendrite_deregister_node_json")
	sym.drainNode = lookupOptional("axon_dendrite_drain_node_json")
	if sym.updateListDir, err = lookup("axon_dendrite_update_mcp_list_dir_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceCreateCall, err = lookup("axon_dendrite_voice_create_call_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceGetCall, err = lookup("axon_dendrite_voice_get_call_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceJoinCall, err = lookup("axon_dendrite_voice_join_call_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceLeaveCall, err = lookup("axon_dendrite_voice_leave_call_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceUpdatePath, err = lookup("axon_dendrite_voice_update_media_path_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceReport, err = lookup("axon_dendrite_voice_report_call_metrics_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceEndCall, err = lookup("axon_dendrite_voice_end_call_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceWatchCall, err = lookup("axon_dendrite_voice_watch_call_events_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceCreateSess, err = lookup("axon_dendrite_voice_create_transport_session_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceGetSess, err = lookup("axon_dendrite_voice_get_transport_session_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceSetDesc, err = lookup("axon_dendrite_voice_set_transport_description_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceAddCand, err = lookup("axon_dendrite_voice_add_transport_candidate_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceRefresh, err = lookup("axon_dendrite_voice_refresh_transport_lease_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceEndSess, err = lookup("axon_dendrite_voice_end_transport_session_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.voiceWatchSess, err = lookup("axon_dendrite_voice_watch_transport_events_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.coverage, err = lookup("axon_dendrite_protocol_coverage_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.stringFree, err = lookup("axon_dendrite_string_free"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}

	// Incremental streaming symbols are optional (graceful degradation for older native libs).
	optionalLookup := func(name string) unsafe.Pointer {
		cName := C.CString(name)
		defer C.free(unsafe.Pointer(cName))
		return C.axon_dlsym(lib, cName)
	}
	sym.serverStreamOpen = optionalLookup("axon_dendrite_server_stream_open_json")
	sym.streamNext = optionalLookup("axon_dendrite_stream_next_json")
	sym.streamClose = optionalLookup("axon_dendrite_stream_close_json")
	sym.bidiStreamOpen = optionalLookup("axon_dendrite_bidi_stream_open_json")
	sym.bidiStreamSend = optionalLookup("axon_dendrite_bidi_stream_send_json")

	return &DendriteBridge{libPath: resolved, lib: lib, sym: sym}, nil
}

func (b *DendriteBridge) CloseLibrary() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lib == nil {
		return nil
	}
	ret := C.axon_dlclose(b.lib)
	b.lib = nil
	if ret != 0 {
		return DendriteError{Message: fmt.Sprintf("dlclose failed: %s", goDLError())}
	}
	return nil
}

func (b *DendriteBridge) OpenClient(endpoint string, connectTimeoutMs int) (uint64, error) {
	if connectTimeoutMs <= 0 {
		connectTimeoutMs = 5000
	}
	payload := map[string]any{
		"endpoint":           endpoint,
		"connect_timeout_ms": connectTimeoutMs,
	}
	resp, err := b.callOpen(payload)
	if err != nil {
		return 0, err
	}
	if resp.Handle == 0 {
		return 0, DendriteError{Message: "dendrite bridge returned empty handle"}
	}
	return resp.Handle, nil
}

func (b *DendriteBridge) CloseClient(handle uint64) error {
	_, err := b.callClose(handle)
	return err
}

func (b *DendriteBridge) UnaryCall(
	handle uint64,
	path string,
	requestBytes []byte,
	metadata map[string]string,
	timeoutMs int,
) ([]byte, error) {
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	payload := map[string]any{
		"path":           path,
		"request_base64": base64.StdEncoding.EncodeToString(requestBytes),
		"metadata":       metadata,
		"timeout_ms":     timeoutMs,
	}
	resp, err := b.callUnary(handle, payload)
	if err != nil {
		return nil, err
	}
	if resp.ResponseBase64 == "" {
		return nil, DendriteError{Message: "missing unary response payload"}
	}
	out, decodeErr := base64.StdEncoding.DecodeString(resp.ResponseBase64)
	if decodeErr != nil {
		return nil, DendriteError{Message: fmt.Sprintf("invalid unary response base64: %v", decodeErr)}
	}
	return out, nil
}

func (b *DendriteBridge) ServerStreamCall(
	handle uint64,
	path string,
	requestBytes []byte,
	metadata map[string]string,
	timeoutMs int,
	maxChunks int,
) ([][]byte, bool, error) {
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if maxChunks <= 0 {
		maxChunks = 4096
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	payload := map[string]any{
		"path":           path,
		"request_base64": base64.StdEncoding.EncodeToString(requestBytes),
		"metadata":       metadata,
		"timeout_ms":     timeoutMs,
		"max_chunks":     maxChunks,
	}
	resp, err := b.callStream(handle, payload)
	if err != nil {
		return nil, false, err
	}

	out := make([][]byte, 0, len(resp.ChunksBase64))
	for _, c := range resp.ChunksBase64 {
		decoded, decodeErr := base64.StdEncoding.DecodeString(c)
		if decodeErr != nil {
			return nil, false, DendriteError{Message: fmt.Sprintf("invalid stream chunk base64: %v", decodeErr)}
		}
		out = append(out, decoded)
	}
	return out, resp.Truncated, nil
}

func (b *DendriteBridge) ClientStreamCall(
	handle uint64,
	path string,
	requestChunks [][]byte,
	metadata map[string]string,
	timeoutMs int,
	maxRequestChunks int,
) ([]byte, error) {
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if maxRequestChunks <= 0 {
		maxRequestChunks = 4096
	}

	if metadata == nil {
		metadata = map[string]string{}
	}

	encoded := make([]string, 0, len(requestChunks))
	for _, chunk := range requestChunks {
		encoded = append(encoded, base64.StdEncoding.EncodeToString(chunk))
	}

	payload := map[string]any{
		"path":                  path,
		"request_chunks_base64": encoded,
		"metadata":              metadata,
		"timeout_ms":            timeoutMs,
		"max_request_chunks":    maxRequestChunks,
	}
	resp, err := b.callClientStream(handle, payload)
	if err != nil {
		return nil, err
	}
	if resp.ResponseBase64 == "" {
		return nil, DendriteError{Message: "missing client-stream response payload"}
	}
	out, decodeErr := base64.StdEncoding.DecodeString(resp.ResponseBase64)
	if decodeErr != nil {
		return nil, DendriteError{Message: fmt.Sprintf("invalid client-stream response base64: %v", decodeErr)}
	}
	return out, nil
}

func (b *DendriteBridge) BidiStreamCall(
	handle uint64,
	path string,
	requestChunks [][]byte,
	metadata map[string]string,
	timeoutMs int,
	maxRequestChunks int,
	maxResponseChunks int,
) ([][]byte, bool, error) {
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if maxRequestChunks <= 0 {
		maxRequestChunks = 4096
	}
	if maxResponseChunks <= 0 {
		maxResponseChunks = 4096
	}
	if metadata == nil {
		metadata = map[string]string{}
	}

	encoded := make([]string, 0, len(requestChunks))
	for _, chunk := range requestChunks {
		encoded = append(encoded, base64.StdEncoding.EncodeToString(chunk))
	}

	payload := map[string]any{
		"path":                  path,
		"request_chunks_base64": encoded,
		"metadata":              metadata,
		"timeout_ms":            timeoutMs,
		"max_request_chunks":    maxRequestChunks,
		"max_response_chunks":   maxResponseChunks,
	}
	resp, err := b.callBidiStream(handle, payload)
	if err != nil {
		return nil, false, err
	}

	out := make([][]byte, 0, len(resp.ChunksBase64))
	for _, c := range resp.ChunksBase64 {
		decoded, decodeErr := base64.StdEncoding.DecodeString(c)
		if decodeErr != nil {
			return nil, false, DendriteError{Message: fmt.Sprintf("invalid bidi-stream chunk base64: %v", decodeErr)}
		}
		out = append(out, decoded)
	}
	return out, resp.Truncated, nil
}

func (b *DendriteBridge) InvokeAbility(
	handle uint64,
	tenantID string,
	resourceURI string,
	payloadJSON any,
	metadata map[string]string,
	timeoutMs int,
) (map[string]any, error) {
	return b.InvokeAbilityRawWithSubject(
		handle,
		tenantID,
		resourceURI,
		payloadJSON,
		"",
		metadata,
		timeoutMs,
	)
}

func (b *DendriteBridge) InvokeAbilityWithSubject(
	handle uint64,
	tenantID string,
	resourceURI string,
	payloadJSON any,
	subjectID string,
	metadata map[string]string,
	timeoutMs int,
) (map[string]any, error) {
	return b.InvokeAbilityRawWithSubject(
		handle,
		tenantID,
		resourceURI,
		payloadJSON,
		subjectID,
		metadata,
		timeoutMs,
	)
}

func (b *DendriteBridge) InvokeAbilityRawWithSubject(
	handle uint64,
	tenantID string,
	resourceURI string,
	payloadJSON any,
	subjectID string,
	metadata map[string]string,
	timeoutMs int,
) (map[string]any, error) {
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	payload := map[string]any{
		"tenant_id":    tenantID,
		"resource_uri": resourceURI,
		"payload_json": payloadJSON,
		"metadata":     metadata,
		"timeout_ms":   timeoutMs,
	}
	if subjectID != "" {
		payload["subject_id"] = subjectID
	}
	resp, err := b.callInvokeAbility(handle, payload)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(resp.Payload))
	for k, v := range resp.Payload {
		if k == "ok" {
			continue
		}
		out[k] = v
	}
	return out, nil
}

func (b *DendriteBridge) ProtocolCoverage() (map[string]any, error) {
	resp, err := b.callCoverage()
	if err != nil {
		return nil, err
	}
	return resp.Payload, nil
}

func (b *DendriteBridge) ProtocolCatalog() (map[string]any, error) {
	resp, err := b.callLockedNoPayload(func(sym dendriteBridgeSymbols) *C.char {
		return C.axon_call_cov(sym.protocolCatalog)
	})
	if err != nil {
		return nil, err
	}
	return resp.Payload, nil
}

func (b *DendriteBridge) InvokeProtocol(handle uint64, req ProtocolInvokeRequest) (map[string]any, error) {
	if req.Metadata == nil {
		req.Metadata = map[string]string{}
	}
	payload := map[string]any{
		"service":               req.Service,
		"rpc":                   req.RPC,
		"path":                  req.Path,
		"request_base64":        req.RequestBase64,
		"request_chunks_base64": req.RequestChunksBase64,
		"metadata":              req.Metadata,
		"timeout_ms":            req.TimeoutMs,
		"max_chunks":            req.MaxChunks,
		"max_request_chunks":    req.MaxRequestChunks,
		"max_response_chunks":   req.MaxResponseChunks,
	}
	resp, err := b.callHelper(handle, payload, b.sym.invokeProtocol)
	if err != nil {
		return nil, err
	}
	return resp.Payload, nil
}

func asMapSlice(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func (b *DendriteBridge) callLockedNoPayload(
	invoke func(sym dendriteBridgeSymbols) *C.char,
) (dendriteBridgeJSON, error) {
	if b == nil {
		return dendriteBridgeJSON{}, errDendriteBridgeNotOpened
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.lib == nil {
		return dendriteBridgeJSON{}, errDendriteBridgeNotOpened
	}
	ptr := invoke(b.sym)
	return b.consumeJSON(ptr)
}

func (b *DendriteBridge) callLockedHandleNoPayload(
	handle uint64,
	requireHandle bool,
	invoke func(sym dendriteBridgeSymbols, handle C.uint64_t) *C.char,
) (dendriteBridgeJSON, error) {
	if b == nil {
		return dendriteBridgeJSON{}, errDendriteBridgeNotOpened
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.lib == nil {
		return dendriteBridgeJSON{}, errDendriteBridgeNotOpened
	}
	if requireHandle && handle == 0 {
		return dendriteBridgeJSON{}, errors.New("invalid handle: 0")
	}
	ptr := invoke(b.sym, C.uint64_t(handle))
	return b.consumeJSON(ptr)
}

func (b *DendriteBridge) callLockedWithPayload(
	handle uint64,
	requireHandle bool,
	payload map[string]any,
	invoke func(sym dendriteBridgeSymbols, handle C.uint64_t, payload *C.char) *C.char,
) (dendriteBridgeJSON, error) {
	if b == nil {
		return dendriteBridgeJSON{}, errDendriteBridgeNotOpened
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.lib == nil {
		return dendriteBridgeJSON{}, errDendriteBridgeNotOpened
	}
	if requireHandle && handle == 0 {
		return dendriteBridgeJSON{}, errors.New("invalid handle: 0")
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return dendriteBridgeJSON{}, err
	}
	cPayload := C.CString(string(jsonBytes))
	defer C.free(unsafe.Pointer(cPayload))
	ptr := invoke(b.sym, C.uint64_t(handle), cPayload)
	return b.consumeJSON(ptr)
}

func (b *DendriteBridge) callOpen(payload map[string]any) (dendriteBridgeJSON, error) {
	return b.callLockedWithPayload(0, false, payload, func(
		sym dendriteBridgeSymbols,
		_ C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_open(sym.open, cPayload)
	})
}

func (b *DendriteBridge) callClose(handle uint64) (dendriteBridgeJSON, error) {
	return b.callLockedHandleNoPayload(handle, false, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
	) *C.char {
		return C.axon_call_close(sym.close, h)
	})
}

func (b *DendriteBridge) callUnary(
	handle uint64,
	payload map[string]any,
) (dendriteBridgeJSON, error) {
	return b.callLockedWithPayload(handle, false, payload, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_unary(sym.unary, h, cPayload)
	})
}

func (b *DendriteBridge) callStream(
	handle uint64,
	payload map[string]any,
) (dendriteBridgeJSON, error) {
	return b.callLockedWithPayload(handle, false, payload, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_stream(sym.stream, h, cPayload)
	})
}

func (b *DendriteBridge) callClientStream(
	handle uint64,
	payload map[string]any,
) (dendriteBridgeJSON, error) {
	return b.callLockedWithPayload(handle, false, payload, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_client_stream(sym.clientStream, h, cPayload)
	})
}

func (b *DendriteBridge) callBidiStream(
	handle uint64,
	payload map[string]any,
) (dendriteBridgeJSON, error) {
	return b.callLockedWithPayload(handle, false, payload, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_bidi_stream(sym.bidiStream, h, cPayload)
	})
}

func (b *DendriteBridge) callInvokeAbility(
	handle uint64,
	payload map[string]any,
) (dendriteBridgeJSON, error) {
	return b.callLockedWithPayload(handle, false, payload, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_invoke_ability(sym.invokeAbility, h, cPayload)
	})
}

func (b *DendriteBridge) callHelper(
	handle uint64,
	payload map[string]any,
	fn unsafe.Pointer,
) (dendriteBridgeJSON, error) {
	return b.callLockedWithPayload(handle, true, payload, func(
		_ dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_stream(fn, h, cPayload)
	})
}

func (b *DendriteBridge) callHelperPayload(
	handle uint64,
	payload map[string]any,
	fn unsafe.Pointer,
) (map[string]any, error) {
	resp, err := b.callHelper(handle, payload, fn)
	if err != nil {
		return nil, err
	}
	return resp.Payload, nil
}

func (b *DendriteBridge) callHelperPayloadSlice(
	handle uint64,
	payload map[string]any,
	fn unsafe.Pointer,
	field string,
) ([]map[string]any, error) {
	result, err := b.callHelperPayload(handle, payload, fn)
	if err != nil {
		return nil, err
	}
	return asMapSlice(result[field]), nil
}

func (b *DendriteBridge) callCoverage() (dendriteBridgeJSON, error) {
	return b.callLockedNoPayload(func(sym dendriteBridgeSymbols) *C.char {
		return C.axon_call_cov(sym.coverage)
	})
}

func (b *DendriteBridge) consumeJSON(ptr *C.char) (dendriteBridgeJSON, error) {
	if ptr == nil {
		return dendriteBridgeJSON{}, DendriteError{Message: "dendrite bridge returned null pointer"}
	}
	defer C.axon_call_free(b.sym.stringFree, ptr)
	text := C.GoString(ptr)
	if text == "" {
		return dendriteBridgeJSON{}, DendriteError{Message: "dendrite bridge returned empty payload"}
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return dendriteBridgeJSON{}, DendriteError{Message: fmt.Sprintf("invalid bridge json: %v", err)}
	}
	ok, _ := raw["ok"].(bool)
	if !ok {
		msg, _ := raw["error"].(string)
		if msg == "" {
			msg = "unknown dendrite bridge error"
		}
		return dendriteBridgeJSON{}, DendriteError{Message: msg}
	}

	out := dendriteBridgeJSON{OK: true, Payload: raw}
	if v, ok := raw["handle"].(float64); ok {
		out.Handle = uint64(v)
	}
	if v, ok := raw["response_base64"].(string); ok {
		out.ResponseBase64 = v
	}
	if v, ok := raw["truncated"].(bool); ok {
		out.Truncated = v
	}
	if chunks, ok := raw["chunks_base64"].([]any); ok {
		for _, c := range chunks {
			if s, ok := c.(string); ok {
				out.ChunksBase64 = append(out.ChunksBase64, s)
			}
		}
	}
	return out, nil
}

// -- Incremental streaming (pull model) ------------------------------------

var errStreamingUnsupported = DendriteError{Message: "incremental streaming not supported by loaded native library"}

// ServerStreamOpen opens a server-streaming RPC and returns a stream handle.
func (b *DendriteBridge) ServerStreamOpen(
	handle uint64,
	path string,
	requestBytes []byte,
	metadata map[string]string,
	timeoutMs int,
	chunkTimeoutMs int,
	chunkBufferSize int,
) (uint64, error) {
	if b.sym.serverStreamOpen == nil {
		return 0, errStreamingUnsupported
	}
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if chunkBufferSize <= 0 {
		chunkBufferSize = 64
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	payload := map[string]any{
		"path":              path,
		"request_base64":    base64.StdEncoding.EncodeToString(requestBytes),
		"metadata":          metadata,
		"timeout_ms":        timeoutMs,
		"chunk_buffer_size": chunkBufferSize,
	}
	if chunkTimeoutMs > 0 {
		payload["chunk_timeout_ms"] = chunkTimeoutMs
	}
	resp, err := b.callHelper(handle, payload, b.sym.serverStreamOpen)
	if err != nil {
		return 0, err
	}
	sh, _ := resp.Payload["stream_handle"].(float64)
	if sh == 0 {
		return 0, DendriteError{Message: "failed to open server stream: missing stream_handle"}
	}
	return uint64(sh), nil
}

// StreamNextResult is in dendrite_bridge_types.go.

// StreamNext pulls the next chunk from an open stream handle.
func (b *DendriteBridge) StreamNext(streamHandle uint64, timeoutMs int) (StreamNextResult, error) {
	if b.sym.streamNext == nil {
		return StreamNextResult{}, errStreamingUnsupported
	}
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	payload := map[string]any{"timeout_ms": timeoutMs}
	resp, err := b.callLockedWithPayload(streamHandle, false, payload, func(
		_ dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_stream(b.sym.streamNext, h, cPayload)
	})
	if err != nil {
		return StreamNextResult{}, err
	}
	if done, _ := resp.Payload["done"].(bool); done {
		return StreamNextResult{Done: true}, nil
	}
	if timeout, _ := resp.Payload["timeout"].(bool); timeout {
		return StreamNextResult{Timeout: true}, nil
	}
	b64, _ := resp.Payload["chunk_base64"].(string)
	chunk, decodeErr := base64.StdEncoding.DecodeString(b64)
	if decodeErr != nil {
		return StreamNextResult{}, DendriteError{Message: fmt.Sprintf("invalid stream chunk base64: %v", decodeErr)}
	}
	return StreamNextResult{Chunk: chunk}, nil
}

// StreamClose closes an open stream handle.
func (b *DendriteBridge) StreamClose(streamHandle uint64) error {
	if b.sym.streamClose == nil {
		return errStreamingUnsupported
	}
	_, err := b.callLockedHandleNoPayload(streamHandle, false, func(
		_ dendriteBridgeSymbols,
		h C.uint64_t,
	) *C.char {
		return C.axon_call_close(b.sym.streamClose, h)
	})
	return err
}

// BidiStreamOpen opens a bidi-streaming RPC and returns a stream handle.
func (b *DendriteBridge) BidiStreamOpen(
	handle uint64,
	path string,
	requestBytes []byte,
	requestChunks [][]byte,
	metadata map[string]string,
	timeoutMs int,
	chunkTimeoutMs int,
	chunkBufferSize int,
	requestBufferSize int,
) (uint64, error) {
	if b.sym.bidiStreamOpen == nil {
		return 0, errStreamingUnsupported
	}
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if chunkBufferSize <= 0 {
		chunkBufferSize = 64
	}
	if requestBufferSize <= 0 {
		requestBufferSize = 64
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	payload := map[string]any{
		"path":                path,
		"metadata":            metadata,
		"timeout_ms":          timeoutMs,
		"chunk_buffer_size":   chunkBufferSize,
		"request_buffer_size": requestBufferSize,
	}
	if chunkTimeoutMs > 0 {
		payload["chunk_timeout_ms"] = chunkTimeoutMs
	}
	if requestBytes != nil {
		payload["request_base64"] = base64.StdEncoding.EncodeToString(requestBytes)
	}
	if len(requestChunks) > 0 {
		encoded := make([]string, 0, len(requestChunks))
		for _, chunk := range requestChunks {
			encoded = append(encoded, base64.StdEncoding.EncodeToString(chunk))
		}
		payload["request_chunks_base64"] = encoded
	}
	resp, err := b.callHelper(handle, payload, b.sym.bidiStreamOpen)
	if err != nil {
		return 0, err
	}
	sh, _ := resp.Payload["stream_handle"].(float64)
	if sh == 0 {
		return 0, DendriteError{Message: "failed to open bidi stream: missing stream_handle"}
	}
	return uint64(sh), nil
}

// BidiStreamSend sends a request chunk on an open bidi stream.
func (b *DendriteBridge) BidiStreamSend(streamHandle uint64, chunk []byte) (bool, error) {
	if b.sym.bidiStreamSend == nil {
		return false, errStreamingUnsupported
	}
	payload := map[string]any{
		"chunk_base64": base64.StdEncoding.EncodeToString(chunk),
	}
	resp, err := b.callLockedWithPayload(streamHandle, false, payload, func(
		_ dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_stream(b.sym.bidiStreamSend, h, cPayload)
	})
	if err != nil {
		return false, err
	}
	sent, _ := resp.Payload["sent"].(bool)
	return sent, nil
}

// BidiStreamFinishSend closes the request side of an open bidi stream.
func (b *DendriteBridge) BidiStreamFinishSend(streamHandle uint64) (bool, error) {
	if b.sym.bidiStreamSend == nil {
		return false, errStreamingUnsupported
	}
	payload := map[string]any{
		"done": true,
	}
	resp, err := b.callLockedWithPayload(streamHandle, false, payload, func(
		_ dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_stream(b.sym.bidiStreamSend, h, cPayload)
	})
	if err != nil {
		return false, err
	}
	closed, _ := resp.Payload["request_stream_closed"].(bool)
	return closed, nil
}

func goDLError() string {
	err := C.axon_dlerror()
	if err == nil {
		return "<no dlerror message>"
	}
	return C.GoString(err)
}
