//go:build cgo

package axon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDescriptorBoundNativeCGOResponseAndFailure(t *testing.T) {
	library, _ := nativeOpenCapture(t)
	capture := filepath.Join(t.TempDir(), "invoke.jsonl")
	t.Setenv("AXON_TEST_INVOKE_CAPTURE", capture)
	bridge, err := OpenDendriteBridge(library)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.CloseLibrary()
	request := nativeTestRequest{payload: map[string]any{"mode": "unary", "version": "axon.descriptor-bound-invocation.v1", "payload_base64": "AP8="}}
	options := DescriptorBoundNativeOptions{RequestID: "native-test", ContentType: "application/octet-stream", TimeoutMs: 42}
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":true,"invocation_response":{"state":"COMPLETED"}}`)
	response, err := bridge.InvokeDescriptorBound(7, request, options)
	if err != nil || response["invocation_response"].(map[string]any)["state"] != "COMPLETED" {
		t.Fatalf("success response: %v", err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal(data, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["timeout_ms"] != float64(42) || sent["request_id"] != options.RequestID || sent["payload_base64"] != "AP8=" {
		t.Fatal("CGO changed request")
	}
	evidence := `{"state":"FAILED","result":{"number":9007199254740993}}`
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":false,"error":{"code":"BUSINESS_ERROR","message":"refused","source":"runtime","invocation_response":`+evidence+`}}`)
	_, err = bridge.InvokeDescriptorBound(7, request, options)
	var failure DendriteError
	if !errors.As(err, &failure) || failure.InvocationResponseJSON != evidence || failure.Code != "BUSINESS_ERROR" {
		t.Fatalf("failure evidence: %v", err)
	}
	if err := bridge.CloseLibrary(); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.InvokeDescriptorBound(7, request, options); err == nil {
		t.Fatal("closed library accepted")
	}
}

func TestDescriptorBoundStreamCGO(t *testing.T) {
	library, _ := nativeOpenCapture(t)
	capture := filepath.Join(t.TempDir(), "stream.jsonl")
	t.Setenv("AXON_TEST_INVOKE_CAPTURE", capture)
	bridge, err := OpenDendriteBridge(library)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.CloseLibrary()
	request := nativeTestRequest{payload: map[string]any{"mode": "server_stream", "version": "axon.descriptor-bound-invocation.v1", "payload_base64": "AP8=", "signature": map[string]any{"signature_base64": "caller-owned"}}}
	options := DescriptorBoundNativeStreamOptions{DescriptorBoundNativeOptions: DescriptorBoundNativeOptions{RequestID: "stream", ContentType: "application/octet-stream"}, ChunkBufferSize: 8, ChunkTimeoutMs: 250}
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":true,"stream_handle":9,"admission_receipt_delivery":"first_stream_chunk"}`)
	handle, response, err := bridge.StreamDescriptorBound(7, request, options)
	if err != nil || handle != 9 || response.StreamHandle != 9 {
		t.Fatalf("open: %v", err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal(data, &sent); err != nil {
		t.Fatal(err)
	}
	stream := sent["stream"].(map[string]any)
	if sent["mode"] != "server_stream" || sent["payload_base64"] != "AP8=" || sent["signature"].(map[string]any)["signature_base64"] != "caller-owned" || stream["chunk_buffer_size"] != float64(8) || stream["chunk_timeout_ms"] != float64(250) || stream["request_buffer_size"] != nil {
		t.Fatal("stream projection changed")
	}
	if response.RawPayload["admission_receipt_delivery"] != "first_stream_chunk" {
		t.Fatal("open evidence lost")
	}
	for _, raw := range []string{`0`, `-1`, `1.5`, `9007199254740992`, `null`} {
		t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":true,"stream_handle":`+raw+`}`)
		if _, _, err := bridge.StreamDescriptorBound(7, request, options); err == nil {
			t.Fatal("invalid handle accepted", raw)
		}
	}
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":false,"error":{"code":"BUSINESS_ERROR","message":"refused","source":"runtime","invocation_response":{"state":"FAILED"}}}`)
	_, _, err = bridge.StreamDescriptorBound(7, request, options)
	var failure DendriteError
	if !errors.As(err, &failure) || failure.InvocationResponseJSON != `{"state":"FAILED"}` {
		t.Fatalf("failure evidence lost: %v", err)
	}
	if err := os.Remove(capture); err != nil {
		t.Fatal(err)
	}
	options.ChunkBufferSize = -1
	if _, _, err := bridge.StreamDescriptorBound(7, request, options); err == nil {
		t.Fatal("negative bound accepted")
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("invalid request reached FFI")
	}
	options.ChunkBufferSize = 8
	if _, _, err := bridge.StreamDescriptorBound(0, request, options); err == nil {
		t.Fatal("zero session accepted")
	}
	bridge.CloseLibrary()
	if _, _, err := bridge.StreamDescriptorBound(7, request, options); err == nil {
		t.Fatal("closed library accepted")
	}
}

func TestDescriptorBoundBidiCGO(t *testing.T) {
	library, _ := nativeOpenCapture(t)
	capture := filepath.Join(t.TempDir(), "bidi.jsonl")
	t.Setenv("AXON_TEST_INVOKE_CAPTURE", capture)
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":true,"stream_handle":9,"request_id":"bidi","canonical_invocation_sha256":"original"}`)
	bridge, err := OpenDendriteBridge(library)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.CloseLibrary()
	request := nativeTestRequest{payload: map[string]any{"mode": "bidi", "payload_base64": "AP8=", "signature": map[string]any{"signature_base64": "caller-owned"}}}
	options := DescriptorBoundNativeBidiOptions{DescriptorBoundNativeStreamOptions: DescriptorBoundNativeStreamOptions{DescriptorBoundNativeOptions: DescriptorBoundNativeOptions{RequestID: "bidi", ContentType: "application/octet-stream"}, ChunkBufferSize: 8, ChunkTimeoutMs: 250}, RequestBufferSize: 4, Streams: []StreamDescriptor{{StreamID: 0, ContentType: "application/octet-stream", Ordering: "STRICT"}}}
	stream, opened, err := bridge.BidiDescriptorBound(7, request, options)
	if err != nil || stream.StreamHandle() != 9 || opened.RequestID != "bidi" || opened.RawPayload["canonical_invocation_sha256"] != "original" {
		t.Fatalf("open: %v", err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal(data, &sent); err != nil {
		t.Fatal(err)
	}
	settings := sent["stream"].(map[string]any)
	if sent["mode"] != "bidi" || sent["payload_base64"] != "AP8=" || sent["signature"].(map[string]any)["signature_base64"] != "caller-owned" || settings["request_buffer_size"] != float64(4) || settings["chunk_buffer_size"] != float64(8) || len(settings["streams"].([]any)) != 1 {
		t.Fatal("bidi projection changed")
	}
	for _, value := range []string{"0", "-1", "1.5", "9007199254740992", "null"} {
		t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":true,"stream_handle":`+value+`}`)
		if _, _, err := bridge.BidiDescriptorBound(7, request, options); err == nil {
			t.Fatal("invalid handle accepted")
		}
	}
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":false,"error":{"code":"BUSINESS_ERROR","message":"refused","source":"runtime","invocation_response":{"state":"FAILED"}}}`)
	_, _, err = bridge.BidiDescriptorBound(7, request, options)
	var failure DendriteError
	if !errors.As(err, &failure) || failure.InvocationResponseJSON != `{"state":"FAILED"}` {
		t.Fatalf("failure evidence: %v", err)
	}
	if err := os.Remove(capture); err != nil {
		t.Fatal(err)
	}
	options.RequestBufferSize = -1
	if _, _, err := bridge.BidiDescriptorBound(7, request, options); err == nil {
		t.Fatal("negative option accepted")
	}
	options.RequestBufferSize = 4
	if _, _, err := bridge.BidiDescriptorBound(0, request, options); err == nil {
		t.Fatal("zero session accepted")
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("invalid request reached FFI")
	}
	if err := bridge.CloseLibrary(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bridge.BidiDescriptorBound(7, request, options); err == nil {
		t.Fatal("closed library accepted")
	}
}
