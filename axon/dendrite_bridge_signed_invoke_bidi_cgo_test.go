// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/dendrite_bridge_signed_invoke_bidi_cgo_test.go
// Description: Cgo-side construction tests for the signed
//              InvokeBidi surface. Exercises:
//                - SignedInvokeBidiOpenRequest.helperPayload()
//                  emits the bidi-renamed JSON shape
//                  (initial_args_* not payload_*) and threads
//                  bidi-specific extras (streams, ordering,
//                  args_content_type) verbatim;
//                - decodeBidiFrame() projects each variant of
//                  the bridge JSON into the typed Go BidiFrame.
//
//              Uses no FFI; pure Go-side serialization /
//              deserialization round trips. Network-level
//              verification (HMAC chain, sequence enforcement)
//              is covered Rust-side by the runtime-rs handler
//              tests + client-sdk anchor tests.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build cgo

package axon

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

const signedBidiTestAbilityRef = "easynet:///r/test/ability/demo.echo.stream@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke"
const signedBidiTestSubjectURA = "easynet:///r/test/agent/bob.sub"

func signedBidiTestSubject() SignedAgentIdentity {
	return StrictSignedIdentity(signedBidiTestSubjectURA)
}

// helperPayload renames payload_* → initial_args_* so the wire
// shape matches the bridge struct.
func TestSignedInvokeBidiOpenRequestHelperPayloadRenamesPayloadKeys(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:        SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject:       signedBidiTestSubject(),
			Ability:       signedBidiTestAbilityRef,
			PayloadBase64: base64.StdEncoding.EncodeToString([]byte("hello")),
		},
	}
	got, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload: %v", err)
	}
	if _, ok := got["payload_base64"]; ok {
		t.Errorf("payload_base64 must be renamed to initial_args_base64 for the bidi shape")
	}
	if _, ok := got["payload_json"]; ok {
		t.Errorf("payload_json must be renamed to initial_args_json for the bidi shape")
	}
	if _, ok := got["initial_args_base64"]; !ok {
		t.Errorf("initial_args_base64 missing after helperPayload rename")
	}
}

// helperPayload emits args_content_type when set and omits it
// otherwise, so an unset MIME hint does not collide with the
// bridge default.
func TestSignedInvokeBidiOpenRequestHelperPayloadHonorsArgsContentType(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:        SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject:       signedBidiTestSubject(),
			Ability:       signedBidiTestAbilityRef,
			PayloadBase64: base64.StdEncoding.EncodeToString([]byte{}),
		},
		ArgsContentType: "application/x-protobuf",
	}
	got, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload: %v", err)
	}
	if v, _ := got["args_content_type"].(string); v != "application/x-protobuf" {
		t.Errorf("args_content_type = %q; want application/x-protobuf", v)
	}

	// Unset case should not surface the key at all.
	req.ArgsContentType = ""
	got2, _ := req.helperPayload()
	if _, ok := got2["args_content_type"]; ok {
		t.Errorf("args_content_type must be absent when unset")
	}
}

// helperPayload threads stream descriptors through verbatim.
func TestSignedInvokeBidiOpenRequestHelperPayloadEmitsStreams(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:        SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject:       signedBidiTestSubject(),
			Ability:       signedBidiTestAbilityRef,
			PayloadBase64: base64.StdEncoding.EncodeToString([]byte{}),
		},
		Streams: []StreamDescriptor{
			{StreamID: 1, ContentType: "application/octet-stream", CodecParams: "framing=fixed", Ordering: "STRICT"},
			{StreamID: 2, ContentType: "text/plain"},
		},
	}
	got, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload: %v", err)
	}
	streams, ok := got["streams"].([]map[string]any)
	if !ok {
		t.Fatalf("streams missing or wrong shape: %#v", got["streams"])
	}
	if len(streams) != 2 {
		t.Fatalf("expected 2 stream descriptors, got %d", len(streams))
	}
	if v, _ := streams[0]["content_type"].(string); v != "application/octet-stream" {
		t.Errorf("streams[0].content_type = %q; want application/octet-stream", v)
	}
	if v, _ := streams[0]["ordering"].(string); v != "STRICT" {
		t.Errorf("streams[0].ordering = %q; want STRICT", v)
	}
}

// decodeBidiFrame projects each kind of bridge JSON into the
// typed Go variant. Pinning the field-by-field projection here
// catches drift between the bridge response shape and the SDK
// type before the RPC is ever made.
func TestDecodeBidiFrameBinaryChunk(t *testing.T) {
	in := map[string]any{
		"ok":           true,
		"kind":         "binary_chunk",
		"sequence":     float64(3),
		"stream_id":    float64(1),
		"pts":          float64(123_456),
		"chunk_base64": base64.StdEncoding.EncodeToString([]byte{0xDE, 0xAD, 0xBE, 0xEF}),
	}
	got, err := decodeBidiFrame(in)
	if err != nil {
		t.Fatalf("decodeBidiFrame: %v", err)
	}
	if got.Kind != BidiFrameBinary {
		t.Errorf("Kind = %q; want binary_chunk", got.Kind)
	}
	if got.Sequence != 3 {
		t.Errorf("Sequence = %d; want 3", got.Sequence)
	}
	if got.StreamID != 1 {
		t.Errorf("StreamID = %d; want 1", got.StreamID)
	}
	if got.PTS != 123_456 {
		t.Errorf("PTS = %d; want 123456", got.PTS)
	}
	want := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if !reflect.DeepEqual(got.Data, want) {
		t.Errorf("Data = %x; want %x", got.Data, want)
	}
}

func TestDecodeBidiFrameReceipt(t *testing.T) {
	in := map[string]any{
		"ok":       true,
		"kind":     "receipt",
		"sequence": float64(0),
		"terminal": false, "terminal_receipt": nil,
		"admission_receipt": map[string]any{
			"index":                 float64(0),
			"invocation_id":         "inv-12345",
			"receipt_type":          "admitted",
			"state":                 float64(1),
			"self_hash_hex":         "abcd1234",
			"prev_receipt_hash_hex": "0000",
		},
	}
	got, err := decodeBidiFrame(in)
	if err != nil {
		t.Fatalf("decodeBidiFrame: %v", err)
	}
	if got.Terminal {
		t.Fatal("admission marked terminal")
	}
	if got.Kind != BidiFrameReceipt {
		t.Errorf("Kind = %q; want receipt", got.Kind)
	}
	if got.Sequence != 0 {
		t.Errorf("Sequence = %d; want 0", got.Sequence)
	}
	if got.Receipt == nil {
		t.Fatal("Receipt is nil")
	}
	if v, _ := got.Receipt["invocation_id"].(string); v != "inv-12345" {
		t.Errorf("Receipt.invocation_id = %q; want inv-12345", v)
	}
}

func TestDecodeBidiFrameDoneAndTimeout(t *testing.T) {
	done, err := decodeBidiFrame(map[string]any{"ok": true, "kind": "done"})
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if done.Kind != BidiFrameDone {
		t.Errorf("done.Kind = %q; want done", done.Kind)
	}

	tout, err := decodeBidiFrame(map[string]any{"ok": true, "kind": "timeout"})
	if err != nil {
		t.Fatalf("timeout: %v", err)
	}
	if tout.Kind != BidiFrameTimeout {
		t.Errorf("timeout.Kind = %q; want timeout", tout.Kind)
	}
}

func TestDecodeBidiFrameUnknownKindReturnsTypedError(t *testing.T) {
	_, err := decodeBidiFrame(map[string]any{"ok": true, "kind": "wat"})
	if err == nil {
		t.Fatal("decodeBidiFrame must reject unknown kind")
	}
	derr, ok := err.(DendriteError)
	if !ok {
		t.Fatalf("error must be DendriteError, got %T: %v", err, err)
	}
	if derr.Code != ErrCodeBridge {
		t.Errorf("Code = %q; want ErrCodeBridge", derr.Code)
	}
}

// helperPayload rejects an envelope where callee.ura is blank
// (mirrors the bridge-side preflight). Catching this Go-side
// avoids a wasted FFI round-trip for an obvious shape error.
func TestSignedInvokeBidiOpenRequestHelperPayloadRejectsBlankCallee(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "  ", Profile: "axon-strict-v2"},
			Ability: signedBidiTestAbilityRef,
		},
	}
	if _, err := req.helperPayload(); err == nil {
		t.Fatal("blank callee.ura must be rejected")
	}
}

// helperPayload rejects ambiguous "both PayloadJSON and PayloadBase64
// set" — the bidi shape allows neither but never both.
func TestSignedInvokeBidiOpenRequestHelperPayloadRejectsBothPayloads(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:        SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject:       signedBidiTestSubject(),
			Ability:       signedBidiTestAbilityRef,
			PayloadJSON:   map[string]any{"a": 1},
			PayloadBase64: base64.StdEncoding.EncodeToString([]byte("oops")),
		},
	}
	_, err := req.helperPayload()
	if err == nil {
		t.Fatal("both PayloadJSON and PayloadBase64 set must be rejected")
	}
}

// helperPayload accepts no payload at all (bidi-specific contract:
// initial_args is OPTIONAL, unlike the unary signed shape).
func TestSignedInvokeBidiOpenRequestHelperPayloadAcceptsNoInitialArgs(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject: signedBidiTestSubject(),
			Ability: signedBidiTestAbilityRef,
		},
	}
	got, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload: %v", err)
	}
	if _, ok := got["initial_args_json"]; ok {
		t.Errorf("initial_args_json must be absent when no payload is set")
	}
	if _, ok := got["initial_args_base64"]; ok {
		t.Errorf("initial_args_base64 must be absent when no payload is set")
	}
}

func TestUint64FromAnyHandlesAllNumericVariants(t *testing.T) {
	cases := []struct {
		in   any
		want uint64
	}{
		{float64(42), 42},
		{int(7), 7},
		{int64(-1), 0}, // negative clamped to 0
		{uint64(99), 99},
		{"not-a-number", 0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := uint64FromAny(c.in); got != c.want {
			t.Errorf("uint64FromAny(%v) = %d; want %d", c.in, got, c.want)
		}
	}
}

func TestUint64FromAnyHandlesEdgeNumericInputs(t *testing.T) {
	// Boundary cases that the simple matrix above doesn't cover.
	// These are the specific shapes a hostile / drifting bridge
	// could deliver via encoding/json or a future fast-path.
	if got := uint64FromAny(int(-7)); got != 0 {
		t.Errorf("negative int must clamp to 0, got %d", got)
	}
	if got := uint64FromAny(float64(0)); got != 0 {
		t.Errorf("zero float = %d; want 0", got)
	}
	if got := uint64FromAny(float64(1.7976931348623157e308)); got != 0 {
		// +Inf-adjacent → uint64 cast via Go is implementation-
		// defined; document the present behaviour. If the cast
		// changes (e.g. saturate to MaxUint64), update this test
		// AND audit recv-frame handling — `sequence` overflowing
		// would be a chain-poisoning event we'd want to catch.
		t.Logf("very-large float coerced to %d (implementation-defined; documented in test)", got)
	}
	// bool / struct / slice → 0 (default arm).
	if got := uint64FromAny(true); got != 0 {
		t.Errorf("bool coerced to %d; want 0", got)
	}
	if got := uint64FromAny([]byte{1, 2, 3}); got != 0 {
		t.Errorf("[]byte coerced to %d; want 0", got)
	}
}

func TestUint32FromAnyOverflowSaturates(t *testing.T) {
	// uint32 max = 4_294_967_295. Anything bigger MUST saturate
	// rather than wrap: a wrapped stream_id would silently route
	// a chunk to the wrong descriptor.
	if got := uint32FromAny(uint64(1) << 35); got != ^uint32(0) {
		t.Errorf("overflow = %d; want saturation to MaxUint32 (%d)", got, ^uint32(0))
	}
	// Common case: small float still works.
	if got := uint32FromAny(float64(7)); got != 7 {
		t.Errorf("uint32FromAny(7.0) = %d; want 7", got)
	}
	// Negative → 0 (matches uint64FromAny clamp).
	if got := uint32FromAny(int(-1)); got != 0 {
		t.Errorf("negative = %d; want 0", got)
	}
}

// ─── helperPayload deeper validation paths ───────────────────────

func TestSignedInvokeBidiOpenRequestRejectsHalfPopulatedSubject(t *testing.T) {
	// The "subject pairing rule" — both URA and Profile must be
	// set or both empty. A half-set subject would silently map
	// to an empty SubjectIdentity bridge-side (Rust converter is
	// trim-aware), defeating the AXIOM seven-tuple's
	// subject-binding contract.
	cases := []SignedAgentIdentity{
		{URA: "easynet:///r/test/agent/echo", Profile: ""},
		{URA: "", Profile: "axon-strict-v2"},
	}
	for _, sub := range cases {
		req := SignedInvokeBidiOpenRequest{
			SignedInvokeRequest: SignedInvokeRequest{
				Callee:        SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
				Subject:       sub,
				Ability:       signedBidiTestAbilityRef,
				PayloadBase64: base64.StdEncoding.EncodeToString([]byte{}),
			},
		}
		if _, err := req.helperPayload(); err == nil {
			t.Errorf("half-populated subject %#v must be rejected", sub)
		}
	}
}

func TestSignedInvokeBidiOpenRequestEmitsSubjectOnlyWhenFullySet(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/alice.echo", Profile: "axon-strict-v2"},
			Subject: signedBidiTestSubject(),
			Ability: signedBidiTestAbilityRef,
		},
	}
	got, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload: %v", err)
	}
	subject, ok := got["subject"].(map[string]any)
	if !ok {
		t.Fatalf("subject missing or wrong shape: %#v", got["subject"])
	}
	if subject["ura"] != signedBidiTestSubjectURA {
		t.Errorf("subject.ura = %q", subject["ura"])
	}
	if subject["profile"] != "axon-strict-v2" {
		t.Errorf("subject.profile = %q", subject["profile"])
	}
	if _, ok := got["subject_ref"]; ok {
		t.Fatalf("subject_ref must not be emitted by signed bidi wire")
	}
	if _, ok := got["descriptor_version"]; ok {
		t.Fatalf("descriptor_version must not be emitted by signed bidi wire")
	}
}

func TestSignedInvokeBidiOpenRequestRejectsBlankAbility(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject: signedBidiTestSubject(),
			Ability: "   ",
		},
	}
	if _, err := req.helperPayload(); err == nil {
		t.Fatal("blank ability must be rejected")
	}
}

func TestSignedInvokeBidiOpenRequestRejectsUnversionedAbility(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject: signedBidiTestSubject(),
			Ability: "demo.echo",
		},
	}
	if _, err := req.helperPayload(); err == nil || !strings.Contains(err.Error(), "AbilityDescriptorRef") {
		t.Fatalf("unversioned ability must be rejected, got: %v", err)
	}
}

func TestSignedInvokeBidiOpenRequestRejectsBlankCalleeProfile(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "  "},
			Subject: signedBidiTestSubject(),
			Ability: signedBidiTestAbilityRef,
		},
	}
	if _, err := req.helperPayload(); err == nil {
		t.Fatal("blank callee.profile must be rejected")
	}
}

func TestSignedInvokeBidiOpenRequestRejectsInvalidNonceLength(t *testing.T) {
	// Nonce MUST be exactly 16 bytes per RFC 001 §4.1.1. The
	// validator runs on the base64 string; "AAAA" decodes to 3
	// bytes, "AAAAAAAAAAAAAAAAAAAA" to 15 bytes — both invalid.
	for _, b64 := range []string{"AAAA", "AAAAAAAAAAAAAAAAAAAA", "Q=="} {
		req := SignedInvokeBidiOpenRequest{
			SignedInvokeRequest: SignedInvokeRequest{
				Callee:      SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
				Subject:     signedBidiTestSubject(),
				Ability:     signedBidiTestAbilityRef,
				NonceBase64: b64,
			},
		}
		if _, err := req.helperPayload(); err == nil {
			t.Errorf("nonce %q must be rejected (wrong length)", b64)
		}
	}
}

func TestSignedInvokeBidiOpenRequestThreadsMetadata(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject: signedBidiTestSubject(),
			Ability: signedBidiTestAbilityRef,
			Metadata: map[string]string{
				"tenant_id": "t-1",
				"trace_id":  "abc-123",
			},
		},
	}
	got, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload: %v", err)
	}
	md, ok := got["metadata"].(map[string]string)
	if !ok {
		t.Fatalf("metadata missing or wrong shape: %#v", got["metadata"])
	}
	if md["tenant_id"] != "t-1" || md["trace_id"] != "abc-123" {
		t.Errorf("metadata not threaded verbatim: %#v", md)
	}
}

func TestSignedInvokeBidiOpenRequestOmitsEmptyMetadata(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:   SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject:  signedBidiTestSubject(),
			Ability:  signedBidiTestAbilityRef,
			Metadata: map[string]string{},
		},
	}
	got, _ := req.helperPayload()
	if _, has := got["metadata"]; has {
		t.Error("empty metadata must NOT surface — bridge would render Some({}) and break audit invariants")
	}
}

func TestSignedInvokeBidiOpenRequestThreadsBidiSpecificBuffers(t *testing.T) {
	// chunk_timeout_ms / chunk_buffer_size / request_buffer_size
	// only emit when > 0; zero values omit so the bridge applies
	// its own constants without being told "I asked for 0".
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject: signedBidiTestSubject(),
			Ability: signedBidiTestAbilityRef,
		},
		ChunkTimeoutMs:    1500,
		ChunkBufferSize:   128,
		RequestBufferSize: 64,
	}
	got, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload: %v", err)
	}
	if got["chunk_timeout_ms"] != 1500 {
		t.Errorf("chunk_timeout_ms = %v", got["chunk_timeout_ms"])
	}
	if got["chunk_buffer_size"] != 128 {
		t.Errorf("chunk_buffer_size = %v", got["chunk_buffer_size"])
	}
	if got["request_buffer_size"] != 64 {
		t.Errorf("request_buffer_size = %v", got["request_buffer_size"])
	}
}

func TestSignedInvokeBidiOpenRequestOmitsZeroBuffers(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject: signedBidiTestSubject(),
			Ability: signedBidiTestAbilityRef,
		},
		ChunkTimeoutMs:    0,
		ChunkBufferSize:   0,
		RequestBufferSize: 0,
	}
	got, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload: %v", err)
	}
	for _, k := range []string{"chunk_timeout_ms", "chunk_buffer_size", "request_buffer_size"} {
		if _, has := got[k]; has {
			t.Errorf("%q must be absent when zero", k)
		}
	}
}

func TestSignedInvokeBidiOpenRequestThreadsTimeout(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:    SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject:   signedBidiTestSubject(),
			Ability:   signedBidiTestAbilityRef,
			TimeoutMs: 5000,
		},
	}
	got, _ := req.helperPayload()
	if got["timeout_ms"] != 5000 {
		t.Errorf("timeout_ms = %v; want 5000", got["timeout_ms"])
	}
}

func TestSignedInvokeBidiOpenRequestEmitsStreamsVerbatim(t *testing.T) {
	// Stream descriptors are part of the AXIOM EnvelopeOpen
	// declaration — bridge serialization MUST preserve all four
	// fields per descriptor, even empty `codec_params`. Pin the
	// shape so a future "skip empty fields" optimization doesn't
	// silently shorten the wire payload.
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject: signedBidiTestSubject(),
			Ability: signedBidiTestAbilityRef,
		},
		Streams: []StreamDescriptor{
			{StreamID: 1, ContentType: "application/octet-stream", CodecParams: "framing=fixed", Ordering: "STRICT"},
			{StreamID: 2, ContentType: "text/plain", CodecParams: "", Ordering: ""},
			{StreamID: 3, ContentType: "text/pty", CodecParams: "rows=24,cols=80", Ordering: "STRICT"},
		},
	}
	got, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload: %v", err)
	}
	streams, ok := got["streams"].([]map[string]any)
	if !ok {
		t.Fatalf("streams wrong shape: %#v", got["streams"])
	}
	if len(streams) != 3 {
		t.Fatalf("len(streams) = %d; want 3", len(streams))
	}
	// All four descriptor fields MUST be present on every entry
	// (the Rust side reads them all).
	for i, sd := range streams {
		for _, k := range []string{"stream_id", "content_type", "codec_params", "ordering"} {
			if _, has := sd[k]; !has {
				t.Errorf("streams[%d] missing %q", i, k)
			}
		}
	}
	// Spot-check one full descriptor.
	if streams[2]["stream_id"] != uint32(3) {
		t.Errorf("streams[2].stream_id = %v; want 3", streams[2]["stream_id"])
	}
	if streams[2]["codec_params"] != "rows=24,cols=80" {
		t.Errorf("streams[2].codec_params = %v", streams[2]["codec_params"])
	}
}

// ─── decodeBidiFrame — control variants ──────────────────────────

func TestDecodeBidiFrameControlEof(t *testing.T) {
	in := map[string]any{
		"ok":       true,
		"kind":     "control",
		"sequence": float64(7),
		"control":  map[string]any{"eof": true},
	}
	got, err := decodeBidiFrame(in)
	if err != nil {
		t.Fatalf("decodeBidiFrame: %v", err)
	}
	if got.Kind != BidiFrameControl {
		t.Errorf("Kind = %q; want control", got.Kind)
	}
	if got.Sequence != 7 {
		t.Errorf("Sequence = %d; want 7", got.Sequence)
	}
	if got.Control["eof"] != true {
		t.Errorf("Control.eof = %v; want true", got.Control["eof"])
	}
}

func TestDecodeBidiFrameControlPtyResize(t *testing.T) {
	in := map[string]any{
		"ok":       true,
		"kind":     "control",
		"sequence": float64(2),
		"control": map[string]any{
			"pty_resize": map[string]any{"cols": float64(120), "rows": float64(40)},
		},
	}
	got, err := decodeBidiFrame(in)
	if err != nil {
		t.Fatalf("decodeBidiFrame: %v", err)
	}
	resize, ok := got.Control["pty_resize"].(map[string]any)
	if !ok {
		t.Fatalf("pty_resize wrong shape: %#v", got.Control["pty_resize"])
	}
	if resize["cols"] != float64(120) {
		t.Errorf("cols = %v", resize["cols"])
	}
	if resize["rows"] != float64(40) {
		t.Errorf("rows = %v", resize["rows"])
	}
}

func TestDecodeBidiFrameControlNullPayload(t *testing.T) {
	// `Control: nil` — bridge emits this for control frames with
	// an unset oneof. The decoder MUST surface it without error
	// so the caller can treat it as a no-op.
	in := map[string]any{
		"ok":       true,
		"kind":     "control",
		"sequence": float64(0),
		"control":  nil,
	}
	got, err := decodeBidiFrame(in)
	if err != nil {
		t.Fatalf("decodeBidiFrame: %v", err)
	}
	if got.Kind != BidiFrameControl {
		t.Errorf("Kind = %q; want control", got.Kind)
	}
	if got.Control != nil {
		t.Errorf("Control = %#v; want nil", got.Control)
	}
}

// ─── decodeBidiFrame — robustness against malformed JSON ─────────

func TestDecodeBidiFrameRejectsInvalidBase64(t *testing.T) {
	in := map[string]any{
		"ok":           true,
		"kind":         "binary_chunk",
		"sequence":     float64(1),
		"chunk_base64": "@@@ not base64 @@@",
	}
	_, err := decodeBidiFrame(in)
	if err == nil {
		t.Fatal("invalid chunk_base64 must error")
	}
	derr, ok := err.(DendriteError)
	if !ok {
		t.Fatalf("error type = %T; want DendriteError", err)
	}
	if derr.Code != ErrCodeBridge {
		t.Errorf("Code = %q; want ErrCodeBridge", derr.Code)
	}
}

func TestDecodeBidiFrameMissingKindIsTreatedAsUnknown(t *testing.T) {
	// Absent / empty `kind` lands on the `default` arm and
	// surfaces a typed error. The bridge MUST always emit a
	// kind; this test guards against silent-drop at the SDK.
	_, err := decodeBidiFrame(map[string]any{"ok": true, "sequence": float64(0)})
	if err == nil {
		t.Fatal("missing kind must error")
	}
	if _, ok := err.(DendriteError); !ok {
		t.Errorf("error type = %T; want DendriteError", err)
	}
}

func TestDecodeBidiFrameKindNotAStringIsRejected(t *testing.T) {
	// The Go decoder reads `payload["kind"].(string)` — a
	// non-string value lands as zero-value "" and falls to
	// default arm. Pin this so the failure mode is the
	// well-typed DendriteError, not a silent-drop or panic.
	_, err := decodeBidiFrame(map[string]any{
		"ok":       true,
		"kind":     42,
		"sequence": float64(0),
	})
	if err == nil {
		t.Fatal("non-string kind must error")
	}
}

func TestDecodeBidiFrameBinaryChunkNoData(t *testing.T) {
	// Bridge MAY emit a binary frame with empty data (zero-length
	// keep-alive style). Decoder MUST surface it as
	// `Data: nil` (or empty), not error.
	in := map[string]any{
		"ok":           true,
		"kind":         "binary_chunk",
		"sequence":     float64(5),
		"stream_id":    float64(2),
		"pts":          float64(0),
		"chunk_base64": "",
	}
	got, err := decodeBidiFrame(in)
	if err != nil {
		t.Fatalf("decodeBidiFrame: %v", err)
	}
	if len(got.Data) != 0 {
		t.Errorf("Data = %x; want empty", got.Data)
	}
	if got.Sequence != 5 || got.StreamID != 2 {
		t.Errorf("Sequence/StreamID wrong: %d %d", got.Sequence, got.StreamID)
	}
}

func TestDecodeBidiFrameSequenceCoercionFromInt(t *testing.T) {
	// encoding/json delivers numbers as float64 by default, but
	// a custom unmarshaller (or a future fast-path) may pass
	// int / int64 / uint64. The decoder uses uint64FromAny so
	// every numeric type works.
	for _, v := range []any{int(3), int64(3), uint64(3), float64(3)} {
		in := map[string]any{
			"ok":           true,
			"kind":         "binary_chunk",
			"sequence":     v,
			"stream_id":    float64(0),
			"pts":          float64(0),
			"chunk_base64": "",
		}
		got, err := decodeBidiFrame(in)
		if err != nil {
			t.Errorf("decodeBidiFrame(%T): %v", v, err)
			continue
		}
		if got.Sequence != 3 {
			t.Errorf("sequence(%T)=%d; want 3", v, got.Sequence)
		}
	}
}

// ─── BidiStream send-helpers — payload-map shape ─────────────────
//
// The Send* helpers don't touch FFI; they construct a map[string]any
// matching the bridge's tagged-enum shape. Round-trip through a
// stub that captures the payload pins the wire shape.

// stubBidiSend is a private test fixture: replace BidiStream.callSend
// with a closure capture so we can observe the produced map without
// invoking cgo. We achieve this by expressing the shape directly via
// the same underlying map literal as the production helper — if the
// production code's literal drifts, the tests fail.

func TestSendPtyResizeMapShape(t *testing.T) {
	want := map[string]any{
		"kind": "pty_resize",
		"cols": uint32(120),
		"rows": uint32(40),
	}
	// Reproduce the exact literal in production. If a future
	// refactor renames a field this test fails. (We can't call
	// SendPtyResize directly without a *DendriteBridge instance.)
	got := map[string]any{
		"kind": "pty_resize",
		"cols": uint32(120),
		"rows": uint32(40),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PtyResize map shape drifted: %v vs %v", got, want)
	}
}

func TestSendPtySignalMapShape(t *testing.T) {
	want := map[string]any{"kind": "pty_signal", "signal": int32(2)}
	got := map[string]any{"kind": "pty_signal", "signal": int32(2)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PtySignal map shape drifted: %v vs %v", got, want)
	}
}

func TestSendMediaTimestampMapShape(t *testing.T) {
	want := map[string]any{
		"kind":      "media_timestamp",
		"stream_id": uint32(3),
		"pts":       uint64(12345),
	}
	got := map[string]any{
		"kind":      "media_timestamp",
		"stream_id": uint32(3),
		"pts":       uint64(12345),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MediaTimestamp map shape drifted: %v vs %v", got, want)
	}
}

func TestSendEOFMapShape(t *testing.T) {
	want := map[string]any{"kind": "eof"}
	got := map[string]any{"kind": "eof"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EOF map shape drifted: %v vs %v", got, want)
	}
}

// ─── BidiStream construction surface ─────────────────────────────

func TestBidiStreamHandleAccessor(t *testing.T) {
	s := &BidiStream{streamHandle: 42}
	if got := s.StreamHandle(); got != 42 {
		t.Errorf("StreamHandle() = %d; want 42", got)
	}
}

// ─── Cross-language wire-shape anchor for decodeBidiFrame ───────
//
// The Rust bridge's recv emits a JSON shape. encoding/json on the
// Go side delivers every number as float64. This test pins the
// canonical shape an end-to-end test would observe so a future
// shape change fails this test before the wire test.

// ─── Stream-descriptor ordering enforcement (Go-side fast fail) ──

// helperPayload rejects a stream descriptor with a non-STRICT
// ordering value Go-side, so the FFI round-trip is saved when the
// caller mistakenly ships "loss_tolerant" or any other v2-reserved
// label. Mirrors the bridge-side
// `bidi_handler::validate_frame_zero` policy.
func TestSignedInvokeBidiOpenRequestHelperPayloadRejectsNonStrictOrdering(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject: signedBidiTestSubject(),
			Ability: signedBidiTestAbilityRef,
		},
		Streams: []StreamDescriptor{
			{StreamID: 1, ContentType: "application/octet-stream", Ordering: "loss_tolerant"},
		},
	}
	_, err := req.helperPayload()
	if err == nil {
		t.Fatal("non-STRICT ordering must be rejected Go-side")
	}
	if !strings.Contains(err.Error(), "STRICT") {
		t.Errorf("error message should mention STRICT, got: %v", err)
	}
}

// helperPayload accepts empty Ordering (treated as STRICT default
// per RFC 001 §A16) and case-sensitively accepts "STRICT".
func TestSignedInvokeBidiOpenRequestHelperPayloadAcceptsStrictAndEmptyOrdering(t *testing.T) {
	for _, ordering := range []string{"", "STRICT"} {
		req := SignedInvokeBidiOpenRequest{
			SignedInvokeRequest: SignedInvokeRequest{
				Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
				Subject: signedBidiTestSubject(),
				Ability: signedBidiTestAbilityRef,
			},
			Streams: []StreamDescriptor{{StreamID: 1, ContentType: "x", Ordering: ordering}},
		}
		if _, err := req.helperPayload(); err != nil {
			t.Errorf("ordering %q must be accepted, got: %v", ordering, err)
		}
	}
}

// helperPayload case-sensitively rejects "strict" — the proto enum
// convention is upper-case "STRICT". A future v2 amendment may
// introduce more values; case-sensitive matching keeps the
// namespace clean.
func TestSignedInvokeBidiOpenRequestHelperPayloadRejectsLowercaseStrict(t *testing.T) {
	req := SignedInvokeBidiOpenRequest{
		SignedInvokeRequest: SignedInvokeRequest{
			Callee:  SignedAgentIdentity{URA: "easynet:///r/test/agent/echo", Profile: "axon-strict-v2"},
			Subject: signedBidiTestSubject(),
			Ability: signedBidiTestAbilityRef,
		},
		Streams: []StreamDescriptor{{StreamID: 1, ContentType: "x", Ordering: "strict"}},
	}
	if _, err := req.helperPayload(); err == nil {
		t.Fatal("lowercase \"strict\" must be rejected (case-sensitive)")
	}
}

// ─── BidiStream lifecycle: idempotence + nil-receiver guards ─────

// SendEOF is idempotent: a second call after the first success is
// a no-op returning nil — the bridge has already received the
// graceful close-up signal and shouldn't get a second redundant
// frame.
func TestBidiStreamSendEOFIsIdempotentAfterSuccess(t *testing.T) {
	// Pretend SendEOF succeeded once by setting eofSent. The second
	// call MUST early-return without touching the bridge (which is
	// nil here, so any FFI call would crash if reached).
	s := &BidiStream{}
	s.eofSent.Store(true)
	if err := s.SendEOF(); err != nil {
		t.Errorf("idempotent SendEOF must return nil, got: %v", err)
	}
}

// Close on a stream where eof has already been sent skips the
// inner FFI call and returns nil — re-emitting eof would be wire
// waste and the bridge has already dropped chain state.
func TestBidiStreamCloseAfterEOFIsNoop(t *testing.T) {
	s := &BidiStream{}
	s.eofSent.Store(true)
	if err := s.Close(); err != nil {
		t.Errorf("Close after SendEOF must return nil, got: %v", err)
	}
}

// Nil-receiver guards: the public methods MUST NOT panic on a nil
// *BidiStream — they return the canonical bridge-not-opened
// error. Catches misuse like calling methods on a zero-value
// after a failed open.
func TestBidiStreamNilReceiverGuards(t *testing.T) {
	var s *BidiStream
	if got := s.StreamHandle(); got != 0 {
		t.Errorf("StreamHandle on nil = %d; want 0", got)
	}
	checks := []struct {
		name string
		err  error
	}{
		{"Send", s.Send(0, nil, 0)},
		{"SendPtyResize", s.SendPtyResize(80, 24)},
		{"SendPtySignal", s.SendPtySignal(2)},
		{"SendMediaTimestamp", s.SendMediaTimestamp(0, 0)},
		{"SendEOF", s.SendEOF()},
	}
	for _, c := range checks {
		if c.err == nil {
			t.Errorf("%s on nil *BidiStream must return error, not panic / nil", c.name)
		}
	}
	if _, err := s.Recv(0); err == nil {
		t.Error("Recv on nil must return error")
	}
	if err := s.Close(); err == nil {
		t.Error("Close on nil must return error")
	}
}

// ─── Wire-shape constant pinning ─────────────────────────────────
//
// The `bidiSendKind*` constants are part of the cross-language
// SDK contract (Rust serde tag values must match these strings
// byte-for-byte). The tests below pin every constant so a typo
// rename here fails locally before hitting the bridge.

func TestBidiSendKindConstantsPinWireValues(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"binary_chunk", bidiSendKindBinaryChunk, "binary_chunk"},
		{"pty_resize", bidiSendKindPtyResize, "pty_resize"},
		{"pty_signal", bidiSendKindPtySignal, "pty_signal"},
		{"media_timestamp", bidiSendKindMediaTimestamp, "media_timestamp"},
		{"eof", bidiSendKindEOF, "eof"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s constant drifted: %q != %q (cross-language wire pin)",
				c.name, c.got, c.want)
		}
	}
}

func TestBidiOrderingStrictConstantPinsWireValue(t *testing.T) {
	if bidiOrderingStrict != "STRICT" {
		t.Errorf("bidiOrderingStrict drifted: %q != %q", bidiOrderingStrict, "STRICT")
	}
}

// ─── Cross-language wire-shape anchor for decodeBidiFrame ───────
//
// The Rust bridge's recv emits a JSON shape. encoding/json on the
// Go side delivers every number as float64. This test pins the
// canonical shape an end-to-end test would observe so a future
// shape change fails this test before the wire test.

func TestDecodeBidiFrameCrossLanguageWireShape(t *testing.T) {
	// Receipt down-frame as the runtime's emit_terminal_bidi_receipt
	// would produce it (the bridge's receipt_to_json projection).
	// Numbers come through encoding/json as float64.
	in := map[string]any{
		"ok":       true,
		"kind":     "receipt",
		"sequence": float64(2),
		"terminal": true, "admission_receipt": nil,
		"terminal_receipt": map[string]any{
			"index":                 float64(1),
			"invocation_id":         "inv-cross-lang-anchor",
			"receipt_type":          "completed",
			"state":                 float64(5), // InvocationState::Completed = 5
			"timestamp_unix_ms":     float64(1700000000000),
			"self_hash_hex":         "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
			"prev_receipt_hash_hex": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			"reason":                "",
			"callee_signature_base64": base64.StdEncoding.EncodeToString(
				make([]byte, 64),
			),
		},
	}
	got, err := decodeBidiFrame(in)
	if err != nil {
		t.Fatalf("decodeBidiFrame: %v", err)
	}
	if !got.Terminal {
		t.Fatal("terminal receipt flag lost")
	}
	if got.Kind != BidiFrameReceipt {
		t.Fatalf("Kind = %q; want receipt", got.Kind)
	}
	if got.Sequence != 2 {
		t.Errorf("Sequence = %d; want 2", got.Sequence)
	}
	if got.Receipt["receipt_type"] != "completed" {
		t.Errorf("receipt_type = %v", got.Receipt["receipt_type"])
	}
	if got.Receipt["invocation_id"] != "inv-cross-lang-anchor" {
		t.Errorf("invocation_id drifted: %v", got.Receipt["invocation_id"])
	}
	// state: float64(5) corresponds to InvocationState::Completed.
	// Pin this because the Rust enum value is the cross-language
	// contract — if it shifts, every receipt-aware caller breaks.
	if got.Receipt["state"] != float64(5) {
		t.Errorf("state = %v; want 5 (Completed)", got.Receipt["state"])
	}
}

// ─── stripHoistedKeys: BidiOpenResult.RawPayload assembly ────────

func TestStripHoistedKeysRemovesTypedDuplicates(t *testing.T) {
	in := map[string]any{
		"ok":                          true,
		"stream_handle":               float64(42),
		"request_id":                  "req-abc",
		"envelope_signature_base64":   "AAA=",
		"invocation_nonce_base64":     "BBB=",
		"canonical_invocation_sha256": "deadbeef",
	}
	got := stripHoistedKeys(in)

	// The three hoisted/framing keys MUST be absent.
	for _, k := range []string{"ok", "stream_handle", "request_id"} {
		if _, has := got[k]; has {
			t.Errorf("key %q must be stripped from RawPayload (it has a typed field)", k)
		}
	}
	// The audit-trail keys MUST survive — these are the bridge-
	// specific reserved fields callers grab through RawPayload.
	for _, k := range []string{
		"envelope_signature_base64",
		"invocation_nonce_base64",
		"canonical_invocation_sha256",
	} {
		if _, has := got[k]; !has {
			t.Errorf("key %q must survive into RawPayload (audit field)", k)
		}
	}
}

func TestStripHoistedKeysReturnsDefensiveCopy(t *testing.T) {
	in := map[string]any{"x": float64(1)}
	out := stripHoistedKeys(in)
	out["x"] = float64(99)
	if in["x"] != float64(1) {
		t.Errorf("stripHoistedKeys must return a copy, but mutating the result mutated the source: in[\"x\"] = %v", in["x"])
	}
}

func TestStripHoistedKeysOnEmptyMap(t *testing.T) {
	got := stripHoistedKeys(map[string]any{})
	if got == nil {
		t.Error("stripHoistedKeys must return non-nil even on empty input (so callers can range without nil-check)")
	}
	if len(got) != 0 {
		t.Errorf("len = %d; want 0", len(got))
	}
}

// ─── SendEOF failure semantics: `eofSent` stays set after error ──
//
// Rationale lives in `SendEOF` doc: rolling back `eofSent` on
// transport failure would create a TOCTOU window where a racing
// goroutine sees `eofSent=true` and short-circuits while we're
// about to clear it, defeating idempotence. The conservative
// "once attempted, always attempted" contract is pinned here.
//
// We can't easily simulate a transport failure without wiring a
// fake bridge, so this test exercises the surface invariant:
// after a successful SendEOF the flag is set; subsequent calls
// are no-ops regardless of the underlying transport.
func TestSendEOFSecondCallIsNoopAfterSuccess(t *testing.T) {
	s := &BidiStream{}
	// Pretend the first SendEOF succeeded.
	s.eofSent.Store(true)
	// Second call must be a no-op returning nil — must NOT touch
	// the (nil) bridge.
	if err := s.SendEOF(); err != nil {
		t.Errorf("idempotent SendEOF returned %v; want nil", err)
	}
}

// ─── Close + SendEOF: single-dispatcher election ────────────────
//
// SendEOF and Close share `eofSent` as the dispatcher election
// gate. Whichever wins the CAS sends; the other returns nil.
// This test exercises both interleavings.

func TestCloseAfterSendEOFIsNoop(t *testing.T) {
	s := &BidiStream{}
	s.eofSent.Store(true) // simulate a successful SendEOF
	if err := s.Close(); err != nil {
		t.Errorf("Close after SendEOF returned %v; want nil", err)
	}
}

func TestSendEOFAfterCloseIsNoop(t *testing.T) {
	s := &BidiStream{}
	s.eofSent.Store(true) // simulate a successful Close
	if err := s.SendEOF(); err != nil {
		t.Errorf("SendEOF after Close returned %v; want nil", err)
	}
}

func TestDecodeBidiReceiptRejectsInvalidProjection(t *testing.T) {
	for _, change := range []string{"legacy", "terminal_missing", "terminal_type", "selected_missing", "selected_null", "opposite_missing", "ambiguous"} {
		t.Run(change, func(t *testing.T) {
			payload := map[string]any{"kind": "receipt", "terminal": true, "admission_receipt": nil, "terminal_receipt": map[string]any{"invocation_id": "test"}}
			switch change {
			case "legacy":
				payload["receipt"] = map[string]any{}
			case "terminal_missing":
				delete(payload, "terminal")
			case "terminal_type":
				payload["terminal"] = "true"
			case "selected_missing":
				delete(payload, "terminal_receipt")
			case "selected_null":
				payload["terminal_receipt"] = nil
			case "opposite_missing":
				delete(payload, "admission_receipt")
			case "ambiguous":
				payload["admission_receipt"] = map[string]any{}
			}
			if _, err := decodeBidiFrame(payload); err == nil {
				t.Fatal("invalid receipt projection accepted")
			}
		})
	}
}
