// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/dendrite_bridge_cgo.go
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
// - Callers should provide valid tenant/resource/runtime context before invoking exported APIs; principal context is optional and maps to canonical subject context.
// - Errors should be treated as typed protocol/runtime outcomes rather than silently ignored.
//
// Architectural Position:
// - Part of the Go SDK facade and Dendrite integration layer.
// - Contains only native bridge loading and transport operations.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build cgo

package axon

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

static char* axon_call_descriptor_bound(void* fn, uint64_t handle, const char* payload) {
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
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

var errDendriteBridgeNotOpened = DendriteError{Code: ErrCodeBridge, Message: "dendrite bridge not opened"}

// DendriteError and its Error() method are in dendrite_bridge_types.go.

type dendriteBridgeSymbols struct {
	open                  unsafe.Pointer
	close                 unsafe.Pointer
	unary                 unsafe.Pointer
	stream                unsafe.Pointer
	clientStream          unsafe.Pointer
	bidiStream            unsafe.Pointer
	descriptorBoundInvoke unsafe.Pointer
	protocolCatalog       unsafe.Pointer
	invokeProtocol        unsafe.Pointer
	coverage              unsafe.Pointer
	stringFree            unsafe.Pointer
	serverStreamOpen      unsafe.Pointer
	streamNext            unsafe.Pointer
	streamClose           unsafe.Pointer
	bidiStreamOpen        unsafe.Pointer
	bidiStreamSend        unsafe.Pointer

	descriptorBoundStreamOpen unsafe.Pointer
	descriptorBoundBidiOpen   unsafe.Pointer
	descriptorBoundBidiSend   unsafe.Pointer
	descriptorBoundBidiRecv   unsafe.Pointer
	descriptorBoundBidiClose  unsafe.Pointer
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

func sdkVersionCandidates() []string {
	versions := make([]string, 0, 3)
	if raw := strings.TrimSpace(os.Getenv("SDK_VERSION")); raw != "" {
		versions = append(versions, raw)
	}
	if root, err := projectRoot(); err == nil {
		versionFile := filepath.Join(root, "VERSION")
		if raw, err := os.ReadFile(versionFile); err == nil {
			if parsed := strings.TrimSpace(string(raw)); parsed != "" {
				versions = append(versions, strings.Split(parsed, "\n")[0])
			}
		}
	}
	return dedupeStrings(versions)
}

func libraryFileNames() []string {
	hint := strings.TrimSpace(strings.ToLower(os.Getenv("AXON_DENDRITE_BRIDGE_PLATFORM")))
	switch hint {
	case "ios":
		return []string{"libaxon_dendrite_bridge.dylib", "libaxon_dendrite_bridge.so", "axon_dendrite_bridge.dll"}
	case "android", "linux", "linux-gnu":
		return []string{"libaxon_dendrite_bridge.so", "libaxon_dendrite_bridge.dylib", "axon_dendrite_bridge.dll"}
	case "macos", "darwin", "mac":
		return []string{"libaxon_dendrite_bridge.dylib", "libaxon_dendrite_bridge.so", "axon_dendrite_bridge.dll"}
	case "windows", "win", "win32", "win64":
		return []string{"axon_dendrite_bridge.dll", "libaxon_dendrite_bridge.dylib", "libaxon_dendrite_bridge.so"}
	default:
		primary := "libaxon_dendrite_bridge.so"
		if strings.HasPrefix(strings.ToLower(runtime.GOOS), "darwin") {
			primary = "libaxon_dendrite_bridge.dylib"
		} else if strings.HasPrefix(strings.ToLower(runtime.GOOS), "windows") {
			primary = "axon_dendrite_bridge.dll"
		}
		return dedupeStrings([]string{
			primary,
			"libaxon_dendrite_bridge.dylib",
			"libaxon_dendrite_bridge.so",
			"axon_dendrite_bridge.dll",
		})
	}
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func projectRoot() (string, error) {
	cursor, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cursor = filepath.Clean(cursor)
	for range 12 {
		if _, err := os.Stat(filepath.Join(cursor, "core", "runtime-rs")); err == nil {
			return cursor, nil
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			break
		}
		cursor = parent
	}
	return "", errors.New("project root not found")
}

func addLibraryCandidates(results *[]string, names []string, dirs ...string) {
	for _, dir := range dirs {
		for _, name := range names {
			*results = append(*results, filepath.Join(dir, name))
		}
	}
}

func useLocalSource() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("AXON_DENDRITE_BRIDGE_SOURCE")))
	switch raw {
	case "1", "true", "yes", "on", "local", "source":
		return true
	default:
		return false
	}
}

func homeLibraryCandidates(home string, names []string, versions []string) []string {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	homeRoot := filepath.Clean(home)
	candidates := make([]string, 0, 32)
	dirs := []string{
		homeRoot,
		filepath.Join(homeRoot, "native"),
	}
	for _, version := range versions {
		dirs = append(dirs, filepath.Join(homeRoot, "dist", "sdk-packs", version, "native"))
	}
	addLibraryCandidates(&candidates, names, dirs...)
	return dedupeStrings(candidates)
}

func sourceLibraryCandidates(names []string, versions []string) []string {
	if !useLocalSource() {
		return nil
	}
	root, err := projectRoot()
	if err != nil {
		return nil
	}
	candidates := make([]string, 0, 24)
	for _, version := range versions {
		addLibraryCandidates(&candidates, names, filepath.Join(root, "dist", "sdk-packs", version, "native"))
	}
	target := strings.TrimSpace(os.Getenv("SDK_TARGET"))
	releaseRoot := filepath.Join(root, "core", "runtime-rs", "dendrite-bridge", "target", "release")
	debugRoot := filepath.Join(root, "core", "runtime-rs", "dendrite-bridge", "target", "debug")
	addLibraryCandidates(&candidates, names, releaseRoot, debugRoot)
	if target != "" {
		addLibraryCandidates(
			&candidates,
			names,
			filepath.Join(root, "core", "runtime-rs", "dendrite-bridge", "target", target, "release"),
			filepath.Join(root, "core", "runtime-rs", "dendrite-bridge", "target", target, "debug"),
		)
	}
	return dedupeStrings(candidates)
}

func packageLibraryCandidates(names []string, versions []string) []string {
	here, err := os.Getwd()
	if err != nil {
		here = "."
	}
	here = filepath.Clean(here)
	candidates := make([]string, 0, 32)
	dirs := []string{
		here,
		filepath.Join(here, "native"),
	}
	for _, version := range versions {
		dirs = append(dirs, filepath.Join(here, "dist", "sdk-packs", version, "native"))
	}
	addLibraryCandidates(&candidates, names, dirs...)
	return dedupeStrings(candidates)
}

func bridgeDebugLog(tried []string) {
	if strings.TrimSpace(os.Getenv("AXON_DENDRITE_BRIDGE_DEBUG")) != "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "dendrite bridge: tried candidates:")
	for _, p := range tried {
		fmt.Fprintf(os.Stderr, "  - %s\n", p)
	}
}

func ResolveDendriteLibraryPath(explicitPath string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}
	if env := os.Getenv("AXON_DENDRITE_BRIDGE_LIB"); env != "" {
		return env, nil
	}
	names := libraryFileNames()
	versions := sdkVersionCandidates()
	var tried []string
	for _, c := range homeLibraryCandidates(os.Getenv("AXON_DENDRITE_BRIDGE_HOME"), names, versions) {
		tried = append(tried, c)
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	for _, c := range sourceLibraryCandidates(names, versions) {
		tried = append(tried, c)
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	for _, c := range packageLibraryCandidates(names, versions) {
		tried = append(tried, c)
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	bridgeDebugLog(tried)
	return "", DendriteError{Code: ErrCodeBridge, Message: "dendrite bridge library not found; set AXON_DENDRITE_BRIDGE_LIB or AXON_DENDRITE_BRIDGE_HOME; run with AXON_DENDRITE_BRIDGE_DEBUG=1 for candidate details"}
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
		return nil, DendriteError{Code: ErrCodeBridge, Message: fmt.Sprintf("dlopen failed for %s: %s", resolved, goDLError())}
	}

	sym := dendriteBridgeSymbols{}
	lookup := func(name string) (unsafe.Pointer, error) {
		cName := C.CString(name)
		defer C.free(unsafe.Pointer(cName))
		ptr := C.axon_dlsym(lib, cName)
		if ptr == nil {
			return nil, DendriteError{Code: ErrCodeSymbolNotFound, Message: fmt.Sprintf("dlsym failed for %s: %s", name, goDLError())}
		}
		return ptr, nil
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
	if sym.descriptorBoundInvoke, err = lookup("axon_dendrite_descriptor_bound_invoke_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, DendriteError{
			Code: ErrCodeSymbolNotFound,
			Message: "dendrite native library does not export " +
				"`axon_dendrite_descriptor_bound_invoke_json`; this symbol " +
				"is required by the canonical runtime contract. Rebuild " +
				"core/runtime-rs/dendrite-bridge against the current " +
				"source tree and republish the sdk-pack.",
		}
	}
	if sym.protocolCatalog, err = lookup("axon_dendrite_protocol_catalog_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.invokeProtocol, err = lookup("axon_dendrite_invoke_protocol_json"); err != nil {
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

	if sym.serverStreamOpen, err = lookup("axon_dendrite_server_stream_open_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.streamNext, err = lookup("axon_dendrite_stream_next_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.streamClose, err = lookup("axon_dendrite_stream_close_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.bidiStreamOpen, err = lookup("axon_dendrite_bidi_stream_open_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.bidiStreamSend, err = lookup("axon_dendrite_bidi_stream_send_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}

	if sym.descriptorBoundStreamOpen, err = lookup("axon_dendrite_descriptor_bound_stream_open_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.descriptorBoundBidiOpen, err = lookup("axon_dendrite_descriptor_bound_bidi_open_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.descriptorBoundBidiSend, err = lookup("axon_dendrite_descriptor_bound_bidi_send_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.descriptorBoundBidiRecv, err = lookup("axon_dendrite_descriptor_bound_bidi_recv_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}
	if sym.descriptorBoundBidiClose, err = lookup("axon_dendrite_descriptor_bound_bidi_close_json"); err != nil {
		_ = C.axon_dlclose(lib)
		return nil, err
	}

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
		return DendriteError{Code: ErrCodeBridge, Message: fmt.Sprintf("dlclose failed: %s", goDLError())}
	}
	return nil
}

func (b *DendriteBridge) OpenClient(endpoint string, connectTimeoutMs int) (uint64, error) {
	return b.OpenClientWithOptions(endpoint, DendriteClientOptions{ConnectTimeoutMs: connectTimeoutMs})
}

// OpenClientWithOptions opens a native session with explicit transport trust.
func (b *DendriteBridge) OpenClientWithOptions(endpoint string, options DendriteClientOptions) (uint64, error) {
	return b.openClientPayload(options.openPayload(endpoint))
}

func (b *DendriteBridge) openClientPayload(payload map[string]any) (uint64, error) {
	resp, err := b.callOpen(payload)
	if err != nil {
		return 0, err
	}
	if resp.Handle == 0 {
		return 0, DendriteError{Code: ErrCodeBridge, Message: "dendrite bridge returned empty handle"}
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
		return nil, DendriteError{Code: ErrCodeBridge, Message: "missing unary response payload"}
	}
	out, decodeErr := base64.StdEncoding.DecodeString(resp.ResponseBase64)
	if decodeErr != nil {
		return nil, DendriteError{Code: ErrCodeJSON, Message: fmt.Sprintf("invalid unary response base64: %v", decodeErr)}
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
			return nil, false, DendriteError{Code: ErrCodeStream, Message: fmt.Sprintf("invalid stream chunk base64: %v", decodeErr)}
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
		return nil, DendriteError{Code: ErrCodeBridge, Message: "missing client-stream response payload"}
	}
	out, decodeErr := base64.StdEncoding.DecodeString(resp.ResponseBase64)
	if decodeErr != nil {
		return nil, DendriteError{Code: ErrCodeJSON, Message: fmt.Sprintf("invalid client-stream response base64: %v", decodeErr)}
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
			return nil, false, DendriteError{Code: ErrCodeStream, Message: fmt.Sprintf("invalid bidi-stream chunk base64: %v", decodeErr)}
		}
		out = append(out, decoded)
	}
	return out, resp.Truncated, nil
}

func (b *DendriteBridge) InvokeAbilityWithSubject(
	handle uint64,
	tenantID string,
	resourceURA string,
	payloadJSON any,
	subjectID string,
	metadata map[string]string,
	timeoutMs int,
) (map[string]any, error) {
	return b.InvokeAbilityRawWithSubject(
		handle,
		tenantID,
		resourceURA,
		payloadJSON,
		subjectID,
		metadata,
		timeoutMs,
	)
}

func (b *DendriteBridge) InvokeAbilityRawWithSubject(
	handle uint64,
	tenantID string,
	resourceURA string,
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
	subject, err := ParseSubjectURA(subjectID)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"tenant_id":    tenantID,
		"resource_ura": resourceURA,
		"subject":      map[string]any{"ura": subject.Raw, "profile": uraProfileStrictV2},
		"payload_json": payloadJSON,
		"metadata":     metadata,
		"timeout_ms":   timeoutMs,
		"subject_id":   subject.Raw,
	}
	resp, err := b.callDescriptorBoundInvoke(handle, payload)
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

func (b *DendriteBridge) callDescriptorBoundInvoke(
	handle uint64,
	payload map[string]any,
) (dendriteBridgeJSON, error) {
	return b.callLockedWithPayload(handle, false, payload, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_descriptor_bound(sym.descriptorBoundInvoke, h, cPayload)
	})
}

func (b *DendriteBridge) callDescriptorBoundStream(handle uint64, payload map[string]any) (dendriteBridgeJSON, error) {
	return b.callLockedWithPayload(handle, true, payload, func(sym dendriteBridgeSymbols, h C.uint64_t, cPayload *C.char) *C.char {
		return C.axon_call_stream(sym.descriptorBoundStreamOpen, h, cPayload)
	})
}

func (b *DendriteBridge) callDescriptorBoundBidi(handle uint64, payload map[string]any) (dendriteBridgeJSON, error) {
	return b.callLockedWithPayload(handle, true, payload, func(sym dendriteBridgeSymbols, h C.uint64_t, cPayload *C.char) *C.char {
		return C.axon_call_stream(sym.descriptorBoundBidiOpen, h, cPayload)
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
		return dendriteBridgeJSON{}, DendriteError{Code: ErrCodeBridge, Message: "dendrite bridge returned null pointer"}
	}
	defer C.axon_call_free(b.sym.stringFree, ptr)
	text := C.GoString(ptr)
	if text == "" {
		return dendriteBridgeJSON{}, DendriteError{Code: ErrCodeBridge, Message: "dendrite bridge returned empty payload"}
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return dendriteBridgeJSON{}, DendriteError{Code: ErrCodeJSON, Message: fmt.Sprintf("invalid bridge json: %v", err)}
	}
	ok, _ := raw["ok"].(bool)
	if !ok {
		return dendriteBridgeJSON{}, decodeDendriteFailure([]byte(text))
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
		return 0, DendriteError{Code: ErrCodeStream, Message: "failed to open server stream: missing stream_handle"}
	}
	return uint64(sh), nil
}

// ServerStreamOpenResult is the typed projection of signed server-stream open.
type ServerStreamOpenResult struct {
	StreamHandle uint64
	RawPayload   map[string]any
}

// InvokeAbilityStreamSigned opens a signed InvokeStream session.
func (b *DendriteBridge) InvokeAbilityStreamSigned(
	handle uint64,
	req SignedInvokeStreamRequest,
) (uint64, *ServerStreamOpenResult, error) {
	if b == nil {
		return 0, nil, errDendriteBridgeNotOpened
	}
	payload, err := req.helperPayload()
	if err != nil {
		return 0, nil, DendriteError{Code: ErrCodeBridge, Message: err.Error()}
	}
	resp, err := b.callLockedWithPayload(handle, false, payload, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_call_stream(sym.descriptorBoundStreamOpen, h, cPayload)
	})
	if err != nil {
		return 0, nil, err
	}
	streamHandle := uint64FromAny(resp.Payload["stream_handle"])
	if streamHandle == 0 {
		return 0, nil, DendriteError{
			Code:    ErrCodeBridge,
			Message: "bridge signed stream open response missing or zero stream_handle",
		}
	}
	return streamHandle, &ServerStreamOpenResult{
		StreamHandle: streamHandle,
		RawPayload:   stripHoistedKeys(resp.Payload),
	}, nil
}

// StreamNextResult is in dendrite_bridge_types.go.

// StreamNext pulls the next chunk from an open stream handle.
func (b *DendriteBridge) StreamNext(streamHandle uint64, timeoutMs int) (StreamNextResult, error) {
	if timeoutMs <= 0 {
		timeoutMs = DefaultStreamChunkTimeoutMs
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
		return StreamNextResult{}, DendriteError{Code: ErrCodeStream, Message: fmt.Sprintf("invalid stream chunk base64: %v", decodeErr)}
	}
	return StreamNextResult{Chunk: chunk}, nil
}

// StreamClose closes an open stream handle.
func (b *DendriteBridge) StreamClose(streamHandle uint64) error {
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
		return 0, DendriteError{Code: ErrCodeStream, Message: "failed to open bidi stream: missing stream_handle"}
	}
	return uint64(sh), nil
}

// BidiStreamSend sends a request chunk on an open bidi stream.
func (b *DendriteBridge) BidiStreamSend(streamHandle uint64, chunk []byte) (bool, error) {
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
