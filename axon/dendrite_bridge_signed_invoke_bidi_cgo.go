// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/dendrite_bridge_signed_invoke_bidi_cgo.go
// Description: Go SDK side of the InvokeBidi RPC (RFC 001 §A16).
//              Wraps the four bridge FFI verbs
//              (`axon_dendrite_descriptor_bound_bidi_{open,send,recv,close}_json`)
//              behind a small `BidiStream` value type and exposes
//              `(*DendriteBridge).InvokeAbilityBidiSigned` as the
//              session-level entry point.
//
// Protocol Responsibility:
// - Realises the Go side of the InvokeBidi caller contract: the
//   open verb runs the unary signed pipeline through Ed25519
//   sign-of-canonical-bytes (in the bridge), encodes
//   `InvokeBidiUp` frame 0, opens the gRPC bidi stream, and
//   registers a per-stream HMAC chain. Subsequent send / recv
//   calls are tagged through the bridge's signed bidi FFI which
//   advances the per-direction chain on every frame.
// - Frame-level integrity (HMAC chain) lives bridge-side; this
//   file's only job is to serialize Go-typed control / chunk
//   intents into the JSON shape the bridge expects, and to
//   surface decoded down-frames back as Go types.
//
// Implementation Approach:
// - Mirrors the unary signed FFI style: open + send + recv + close
//   are required at bridge-open time.
// - The high-level `BidiStream` is a value type (no goroutine,
//   no channel) — the bridge's own background tokio task drives
//   the gRPC stream. Recv blocks the calling Go goroutine for
//   one frame; Send is non-blocking past the bridge mpsc.
// - Wire data is `[]byte` end-to-end; the JSON layer at the FFI
//   boundary base64-encodes binary chunks because the cgo
//   thunk shape is JSON-in / JSON-out. The protocol itself
//   carries pure proto bytes — there is no JSON in the wire.
//   A future fast-path FFI variant with raw byte pointers can
//   be slotted in without touching `BidiStream`'s Go API.
//
// Usage Contract:
// SendEOF and Close half-close the up direction and retain receiving capability.
// Drain Recv through the terminal receipt, then it returns Done without FFI.
// Always defer Dispose for final cleanup, including early exit and errors.
// Terminal receipt delivery means native state was automatically released.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build cgo

package axon

/*
#include <stdint.h>

// The four bidi verbs share two C ABI shapes:
//   1. open  (handle u64, payload *char) -> *char    — same as unary signed
//   2. send / recv  (stream_handle u64, payload *char) -> *char  — same as bidi_stream_send
//   3. close (stream_handle u64) -> *char            — same as stream_close
//
// We declare local thunks here because cgo preambles are
// file-local; the unary thunk in `dendrite_bridge_signed_invoke_cgo.go`
// is not visible to this file.
typedef char* (*axon_bidi_signed_call_fn_t)(uint64_t, const char*);
typedef char* (*axon_bidi_signed_close_fn_t)(uint64_t);

static char* axon_bidi_signed_call(void* fn, uint64_t handle, const char* payload) {
    return ((axon_bidi_signed_call_fn_t)fn)(handle, payload);
}

static char* axon_bidi_signed_close(void* fn, uint64_t handle) {
    return ((axon_bidi_signed_close_fn_t)fn)(handle);
}
*/
import "C"

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// ─── Wire-format constants (cross-language pins) ────────────────
//
// These string values are part of the cross-language SDK contract
// for InvokeBidi. The Rust bridge's
// `SignedInvokeBidiSendRequest` enum tag values use the same
// `snake_case` rendering (serde tag attribute). Renaming any of
// them here without simultaneously updating the bridge breaks
// the protocol.

const (
	bidiSendKindBinaryChunk    = "binary_chunk"
	bidiSendKindPtyResize      = "pty_resize"
	bidiSendKindPtySignal      = "pty_signal"
	bidiSendKindMediaTimestamp = "media_timestamp"
	bidiSendKindEOF            = "eof"

	bidiOrderingStrict = "STRICT"
)

// SignedInvokeBidiOpenRequest mirrors the Rust-side
// `dendrite-bridge::invoke_signed_bidi::SignedInvokeBidiOpenRequest`.
//
// Identity is session-bound (no caller field) per RFC 001 §3.3,
// matching the unary signed shape. The bidi-
// specific extras are `Streams[]` (declared StreamDescriptors)
// and `ArgsContentType` (MIME hint for `InitialArgs`).
type SignedInvokeBidiOpenRequest struct {
	// Inherits the seven-tuple fields by composition so unary signed
	// and bidi signed share one validation surface. The unary
	// `helperPayload` is NOT delegated to here:
	// the bidi shape's `initial_args` is OPTIONAL whereas unary's
	// payload is exactly-one-of, so this struct's `helperPayload`
	// builds the JSON directly.
	SignedInvokeRequest

	// ArgsContentType is the MIME hint for InitialArgs ("application/
	// json", "application/x-protobuf", "application/octet-stream",
	// etc.). The bridge passes this through to the EnvelopeOpen
	// `args_content_type` field; the runtime ability handler may
	// inspect it to pick a decoder. Free-form; the bridge does not
	// validate it.
	ArgsContentType string

	// ChunkTimeoutMs is the per-frame wait timeout for the
	// underlying gRPC bidi stream. Defaults to TimeoutMs when 0,
	// matching the raw streaming transport helper.
	ChunkTimeoutMs int

	// ChunkBufferSize is the response-side mpsc buffer between the
	// bridge's gRPC reader task and the Go-side recv. Defaults to
	// a transport-level constant when 0.
	ChunkBufferSize int

	// RequestBufferSize is the request-side mpsc buffer between
	// the SDK send and the bridge gRPC writer task. Defaults to a
	// transport-level constant when 0.
	RequestBufferSize int

	// Streams declares the per-direction stream descriptors. Empty
	// → implicit single-modal session bound to stream_id 0. Each
	// descriptor's `Ordering` MUST be either empty or "STRICT" in
	// v1; the bridge rejects any other value (LOSS_TOLERANT is a
	// v2 amendment), and the Go-side validator below catches the
	// mismatch before the FFI call.
	Streams []StreamDescriptor
}

// StreamDescriptor declares one logical sub-stream within a bidi session.
// Multiplexed media, terminal, and application channels use separate
// descriptors; BinaryChunk frames select one through stream_id.
type StreamDescriptor struct {
	StreamID    uint32
	ContentType string
	CodecParams string
	Ordering    string // "" or "STRICT" in v1
}

// helperPayload renders the bidi-open JSON the bridge consumes.
//
// The bidi shape diverges from the unary `SignedInvokeRequest.helperPayload`
// in one substantive way: `initial_args` is OPTIONAL (a session
// may open with no immediate args, then exchange BinaryChunk
// frames). The unary path requires exactly-one-of payload_json /
// payload_base64; the bidi path accepts neither and treats it as
// empty bytes. We therefore validate the shared fields manually
// here rather than delegating to the unary `helperPayload`, which
// would reject empty payloads.
//
// The seven-tuple identity validation (callee non-empty, explicit subject,
// descriptor-bound ability, nonce length + non-zero, causal form) is mirrored from the unary path so error messages stay
// consistent across signed verbs. Validation order matches the
// unary `validate()`: callee → subject → ability → payload → nonce → causal.
func (r SignedInvokeBidiOpenRequest) helperPayload() (map[string]any, error) {
	if err := requireNonBlank("callee.ura", r.Callee.URA); err != nil {
		return nil, err
	}
	if err := requireNonBlank("callee.profile", r.Callee.Profile); err != nil {
		return nil, err
	}
	if err := requireNonBlank("subject.ura", r.Subject.URA); err != nil {
		return nil, err
	}
	if err := requireNonBlank("subject.profile", r.Subject.Profile); err != nil {
		return nil, err
	}
	ability, err := requiredAbilityRef(r.Ability)
	if err != nil {
		return nil, err
	}
	// Bidi-specific: initial_args is OPTIONAL but exactly-one-of
	// still applies if BOTH are set (avoids ambiguity).
	hasJSON := r.PayloadJSON != nil
	hasB64 := strings.TrimSpace(r.PayloadBase64) != ""
	if hasJSON && hasB64 {
		return nil, errors.New(
			"exactly one of PayloadJSON or PayloadBase64 may be set, not both",
		)
	}
	if r.NonceBase64 != "" {
		if err := validateNonceBase64Length("nonce_base64", r.NonceBase64); err != nil {
			return nil, err
		}
	}
	if err := r.CausalContext.validate(); err != nil {
		return nil, err
	}
	// Stream descriptor v1 ordering: empty or "STRICT", case-
	// sensitive. Mirrors `bidi_handler::validate_frame_zero` on
	// the runtime side; failing fast Go-side avoids one round
	// trip for an obvious shape error.
	for i, sd := range r.Streams {
		if sd.Ordering != "" && sd.Ordering != bidiOrderingStrict {
			return nil, fmt.Errorf(
				"streams[%d].ordering = %q, only %q (or empty) supported in v1",
				i, sd.Ordering, bidiOrderingStrict,
			)
		}
	}

	payload := map[string]any{
		"callee": map[string]any{
			"ura":     strings.TrimSpace(r.Callee.URA),
			"profile": strings.TrimSpace(r.Callee.Profile),
		},
		"subject": map[string]any{
			"ura":     strings.TrimSpace(r.Subject.URA),
			"profile": strings.TrimSpace(r.Subject.Profile),
		},
		"ability": ability,
	}
	switch {
	case hasJSON:
		payload["initial_args_json"] = r.PayloadJSON
	case hasB64:
		payload["initial_args_base64"] = strings.TrimSpace(r.PayloadBase64)
	}
	// hasJSON == hasB64 == false → no initial_args key emitted.
	// The bridge treats absent as "empty bytes" (initial_args = []).

	if r.NonceBase64 != "" {
		payload["nonce_base64"] = r.NonceBase64
	}
	if causal := r.CausalContext.jsonMap(); causal != nil {
		payload["causal_context"] = causal
	}
	if r.TimeoutMs > 0 {
		payload["timeout_ms"] = r.TimeoutMs
	}
	if len(r.Metadata) > 0 {
		md := make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			md[k] = v
		}
		payload["metadata"] = md
	}
	if strings.TrimSpace(r.ArgsContentType) != "" {
		payload["args_content_type"] = strings.TrimSpace(r.ArgsContentType)
	}
	if r.ChunkTimeoutMs > 0 {
		payload["chunk_timeout_ms"] = r.ChunkTimeoutMs
	}
	if r.ChunkBufferSize > 0 {
		payload["chunk_buffer_size"] = r.ChunkBufferSize
	}
	if r.RequestBufferSize > 0 {
		payload["request_buffer_size"] = r.RequestBufferSize
	}
	if len(r.Streams) > 0 {
		streams := make([]map[string]any, 0, len(r.Streams))
		for _, sd := range r.Streams {
			streams = append(streams, map[string]any{
				"stream_id":    sd.StreamID,
				"content_type": sd.ContentType,
				"codec_params": sd.CodecParams,
				"ordering":     sd.Ordering,
			})
		}
		payload["streams"] = streams
	}
	return payload, nil
}

// BidiOpenResult is the Go-typed projection of the open-verb JSON
// response.
//
// `RawPayload` holds the bridge-specific reserved fields that
// don't have a typed Go counterpart on this struct — currently
// `envelope_signature_base64`, `invocation_nonce_base64`, and
// `canonical_invocation_sha256`. Fields hoisted onto the typed
// struct (`stream_handle`, `request_id`) are stripped from
// `RawPayload` so a caller looking up `result.RawPayload["stream_handle"]`
// gets a clear "absent" rather than a stale duplicate.
type BidiOpenResult struct {
	StreamHandle uint64
	RequestID    string
	RawPayload   map[string]any
}

// InvokeAbilityBidiSigned opens a signed InvokeBidi session and
// returns a `BidiStream` value that drives the per-frame send /
// recv flow.
//
// The first `Recv` on the returned stream blocks until the
// runtime emits the admission receipt as down-frame 0.
// Subsequent recvs surface BinaryChunk / Receipt / Control
// frames. The terminal receipt (Completed / Failed / Cancelled)
// is the last receipt frame before the stream EOFs (`BidiFrameDone`).
func (b *DendriteBridge) InvokeAbilityBidiSigned(
	handle uint64,
	req SignedInvokeBidiOpenRequest,
) (*BidiStream, *BidiOpenResult, error) {
	if b == nil {
		return nil, nil, errDendriteBridgeNotOpened
	}
	payload, err := req.helperPayload()
	if err != nil {
		return nil, nil, DendriteError{Code: ErrCodeBridge, Message: err.Error()}
	}
	resp, err := b.callLockedWithPayload(handle, false, payload, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_bidi_signed_call(sym.descriptorBoundBidiOpen, h, cPayload)
	})
	if err != nil {
		return nil, nil, err
	}
	streamHandle := uint64FromAny(resp.Payload["stream_handle"])
	if streamHandle == 0 {
		return nil, nil, DendriteError{
			Code:    ErrCodeBridge,
			Message: "bridge open-bidi response missing or zero stream_handle",
		}
	}
	requestID, _ := resp.Payload["request_id"].(string)
	out := stripHoistedKeys(resp.Payload)
	stream := &BidiStream{
		bridge:       b,
		streamHandle: streamHandle,
	}
	return stream, &BidiOpenResult{
		StreamHandle: streamHandle,
		RequestID:    requestID,
		RawPayload:   out,
	}, nil
}

// BidiStream is a Go-side handle on one open InvokeBidi session.
// Methods serialize Go intents into the bridge's signed bidi FFI
// and surface decoded down-frames back. The underlying gRPC
// stream + HMAC chain state live entirely bridge-side; this
// struct holds only the stream_handle.
//
// Concurrency model:
//   - Safe to share across goroutines for the producer / consumer
//     split (one goroutine calling Send/SendPty*, another calling
//     Recv). The bridge's per-stream chain mutex serializes them
//     on the FFI side without blocking opposite directions.
//   - Two concurrent Send calls (or two concurrent Recv calls) on
//     the same stream are NOT recommended: the bridge will
//     serialize them, but the resulting frame ordering is whichever
//     goroutine wins the per-stream mutex first — nondeterministic
//     from the caller's point of view. If you need a producer
//     queue, build one in Go on top of a single-goroutine sender.
//
// Lifecycle:
//   - `eofSent` is set the first time SendEOF or Close emits an
//     eof control frame so subsequent half-close is a no-op.
//   - released marks final disposal or native terminal-receipt release.
//     Independent direction locks serialize sends and receives; disposal can
//     interrupt a pending receive through native cancellation.
type BidiStream struct {
	bridge       *DendriteBridge
	streamHandle uint64
	// eofSent is 1 once an eof control frame has been emitted on
	// the up direction. Atomic so SendEOF + Close racing across
	// goroutines is safe — only one of them actually sends.
	eofSent  atomic.Bool
	released atomic.Bool
	upMu     sync.Mutex
	recvMu   sync.Mutex
}

// StreamHandle exposes the underlying handle for telemetry /
// audit code. Callers MUST NOT pass it to the raw stream FFI
// (`StreamClose`, `BidiStreamSend`) — those bypass the chain
// state and would poison the next signed frame.
func (s *BidiStream) StreamHandle() uint64 {
	if s == nil {
		return 0
	}
	return s.streamHandle
}

func (s *BidiStream) ensureUpOpen() error {
	if s == nil || s.bridge == nil {
		return errDendriteBridgeNotOpened
	}
	if s.released.Load() || s.eofSent.Load() {
		return errors.New("signed bidi up direction is closed")
	}
	return nil
}

// Send transmits one BinaryChunk on the up direction. `streamID`
// MUST match a descriptor declared in `Streams` at open time
// (or 0 for implicit single-modal sessions). `pts` is optional
// (microseconds since session reference clock); pass 0 for
// PTY-style streams that don't need timing metadata.
func (s *BidiStream) Send(streamID uint32, data []byte, pts uint64) error {
	if err := s.ensureUpOpen(); err != nil {
		return err
	}
	return s.callSend(map[string]any{
		"kind":         bidiSendKindBinaryChunk,
		"stream_id":    streamID,
		"chunk_base64": base64.StdEncoding.EncodeToString(data),
		"pts":          pts,
	})
}

// SendPtyResize sends a PtyResize control frame.
func (s *BidiStream) SendPtyResize(cols, rows uint32) error {
	if err := s.ensureUpOpen(); err != nil {
		return err
	}
	return s.callSend(map[string]any{
		"kind": bidiSendKindPtyResize,
		"cols": cols,
		"rows": rows,
	})
}

// SendPtySignal sends a PtySignal control frame. `signal` is a
// POSIX signal number (e.g. syscall.SIGINT == 2). Non-POSIX
// hosts may ignore signals they cannot express.
func (s *BidiStream) SendPtySignal(signal int32) error {
	if err := s.ensureUpOpen(); err != nil {
		return err
	}
	return s.callSend(map[string]any{
		"kind":   bidiSendKindPtySignal,
		"signal": signal,
	})
}

// SendMediaTimestamp sends an out-of-band timing pulse without an associated
// content chunk.
func (s *BidiStream) SendMediaTimestamp(streamID uint32, pts uint64) error {
	if err := s.ensureUpOpen(); err != nil {
		return err
	}
	return s.callSend(map[string]any{
		"kind":      bidiSendKindMediaTimestamp,
		"stream_id": streamID,
		"pts":       pts,
	})
}

// SendEOF sends a graceful close-up signal. After this call the
// caller MUST drain `Recv` until it returns the terminal receipt
// (BidiFrameReceipt with state ∈ {Completed, Failed, Cancelled}),
// then optionally a final BidiFrameDone, before calling Close.
//
// Idempotent: calling SendEOF twice (or SendEOF + Close) only
// emits one eof frame. The second call is a no-op returning nil.
//
// Failure semantics: if the underlying FFI send returns an error
// (e.g. transport hiccup), `eofSent` STAYS set. A retry would
// either send a duplicate eof (bridge state already torn down)
// or fail identically; the caller's recourse is to drop the
// stream and open a new session. Resetting `eofSent` on failure
// would create a TOCTOU window where another goroutine sees
// eofSent=true and short-circuits while we're about to clear it,
// turning idempotence into nondeterminism. The conservative
// "once attempted, always attempted" semantics keeps the chain
// state machine simple and predictable.
func (s *BidiStream) SendEOF() error {
	if s == nil {
		return errDendriteBridgeNotOpened
	}
	if !s.eofSent.CompareAndSwap(false, true) {
		return nil
	}
	return s.callSend(map[string]any{"kind": bidiSendKindEOF})
}

func (s *BidiStream) callSend(payload map[string]any) error {
	if s == nil || s.bridge == nil {
		return errDendriteBridgeNotOpened
	}
	s.upMu.Lock()
	defer s.upMu.Unlock()
	if s.released.Load() || (s.eofSent.Load() && payload["kind"] != bidiSendKindEOF) {
		return errors.New("signed bidi up direction is closed")
	}
	_, err := s.bridge.callLockedWithPayload(s.streamHandle, false, payload, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_bidi_signed_call(sym.descriptorBoundBidiSend, h, cPayload)
	})
	return err
}

// BidiFrameKind labels the variant a `BidiFrame` carries. The
// string values mirror the bridge's recv-verb response shape
// (`kind` field) and are part of the cross-language SDK
// contract.
type BidiFrameKind string

const (
	// BidiFrameBinary — `Data` carries the BinaryChunk bytes,
	// `StreamID` and `PTS` carry the proto fields.
	BidiFrameBinary BidiFrameKind = "binary_chunk"

	// BidiFrameReceipt — `Receipt` carries the decoded
	// InvocationReceipt projection (admission, terminal, etc.).
	BidiFrameReceipt BidiFrameKind = "receipt"

	// BidiFrameControl — `Control` carries the decoded BidiControl
	// payload variant. Today only the runtime emits eof on the
	// down direction; future hosted abilities may surface
	// PTY/media controls.
	BidiFrameControl BidiFrameKind = "control"

	// BidiFrameDone — server EOF. No more frames will arrive on
	// this stream; the bridge has already freed the chain state.
	BidiFrameDone BidiFrameKind = "done"

	// BidiFrameTimeout — recv timed out before any frame arrived.
	// The chain is still alive; caller may retry recv.
	BidiFrameTimeout BidiFrameKind = "timeout"
)

// BidiFrame is one decoded down-direction frame. The `Kind`
// field discriminates which sub-fields are populated.
type BidiFrame struct {
	Kind     BidiFrameKind
	Sequence uint64

	// Binary fields — populated when Kind == BidiFrameBinary.
	StreamID uint32
	Data     []byte
	PTS      uint64

	// Receipt fields — populated when Kind == BidiFrameReceipt.
	// `Receipt` is the raw bridge JSON projection (index,
	// invocation_id, receipt_type, state, self_hash_hex,
	// prev_receipt_hash_hex, callee_signature_base64, ...). The
	// caller decodes whichever fields they care about; the SDK
	// avoids a typed Go struct here so receipt-shape evolution
	// in the proto doesn't churn the SDK ABI.
	Receipt map[string]any
	// Terminal identifies the native terminal receipt; it is not a verification result.
	Terminal bool

	// Control field — populated when Kind == BidiFrameControl.
	Control map[string]any
}

// Recv blocks for one signed down-frame, verifies the chain
// (sequence + HMAC) bridge-side, and returns the decoded
// payload. `timeoutMs` ≤ 0 falls back to the bridge's default
// (currently 30s).
//
// On chain violation (sequence gap, MAC mismatch, length wrong)
// the returned error is a typed `DendriteError` with code
// `ErrCodeBridge` and a stable `AXON_BIDI_*` reason in the
// message — these are the same wire codes the runtime emits in
// its terminal receipt's `reason` field. After such an error
// the chain is poisoned and recv MUST NOT be retried.
func (s *BidiStream) Recv(timeoutMs int) (BidiFrame, error) {
	if s == nil || s.bridge == nil {
		return BidiFrame{}, errDendriteBridgeNotOpened
	}
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	if s.released.Load() {
		return BidiFrame{Kind: BidiFrameDone}, nil
	}
	payload := map[string]any{}
	if timeoutMs > 0 {
		payload["timeout_ms"] = timeoutMs
	}
	resp, err := s.bridge.callLockedWithPayload(s.streamHandle, false, payload, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
		cPayload *C.char,
	) *C.char {
		return C.axon_bidi_signed_call(sym.descriptorBoundBidiRecv, h, cPayload)
	})
	if err != nil {
		return BidiFrame{}, err
	}
	frame, err := decodeBidiFrame(resp.Payload)
	if err == nil && frame.Kind == BidiFrameReceipt && frame.Terminal {
		s.released.Store(true)
		s.eofSent.Store(true)
	}
	return frame, err
}

func decodeBidiFrame(payload map[string]any) (BidiFrame, error) {
	rawKind, _ := payload["kind"].(string)
	kind := BidiFrameKind(rawKind)
	seq := uint64FromAny(payload["sequence"])
	switch kind {
	case BidiFrameBinary:
		var data []byte
		if b64, ok := payload["chunk_base64"].(string); ok && b64 != "" {
			d, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				return BidiFrame{}, DendriteError{
					Code:    ErrCodeBridge,
					Message: fmt.Sprintf("invalid chunk_base64 from bridge: %v", err),
				}
			}
			data = d
		}
		return BidiFrame{
			Kind:     BidiFrameBinary,
			Sequence: seq,
			StreamID: uint32FromAny(payload["stream_id"]),
			Data:     data,
			PTS:      uint64FromAny(payload["pts"]),
		}, nil
	case BidiFrameReceipt:
		terminal, validTerminal := payload["terminal"].(bool)
		admission, hasAdmission := payload["admission_receipt"]
		final, hasFinal := payload["terminal_receipt"]
		_, legacy := payload["receipt"]
		selected, opposite := admission, final
		if terminal {
			selected, opposite = final, admission
		}
		receipt, validReceipt := selected.(map[string]any)
		if !validTerminal || !hasAdmission || !hasFinal || legacy || !validReceipt || receipt == nil || opposite != nil {
			return BidiFrame{}, DendriteError{Code: ErrCodeBridge, Message: "invalid canonical bidi receipt projection"}
		}
		return BidiFrame{Kind: BidiFrameReceipt, Sequence: seq, Receipt: receipt, Terminal: terminal}, nil
	case BidiFrameControl:
		ctrl, _ := payload["control"].(map[string]any)
		return BidiFrame{
			Kind:     BidiFrameControl,
			Sequence: seq,
			Control:  ctrl,
		}, nil
	case BidiFrameDone:
		return BidiFrame{Kind: BidiFrameDone}, nil
	case BidiFrameTimeout:
		return BidiFrame{Kind: BidiFrameTimeout}, nil
	default:
		return BidiFrame{}, DendriteError{
			Code:    ErrCodeBridge,
			Message: fmt.Sprintf("unknown bidi frame kind from bridge: %q", rawKind),
		}
	}
}

// stripHoistedKeys returns a defensive copy of `src` with the
// keys that BidiOpenResult lifts to typed fields removed
// (`stream_handle`, `request_id`) plus the framing `ok` flag.
//
// Centralizing this avoids two failure modes:
//  1. Forgetting to strip a key when a new typed field is added
//     to BidiOpenResult — the test fixture pins the strip set.
//  2. Mutating the bridge's response map in place, which would
//     surprise callers re-using `resp.Payload` (we don't today,
//     but the contract is cheap to honor).
func stripHoistedKeys(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for k, v := range src {
		switch k {
		case "ok", "stream_handle", "request_id":
			continue
		}
		out[k] = v
	}
	return out
}

// uint64FromAny coerces any JSON-decoded numeric value to uint64.
//
// `encoding/json` always materializes JSON numbers as `float64`,
// so the float64 branch is the realistic path. The int / int64 /
// uint64 branches are kept for callers (notably tests) that
// inject Go-native integer literals into the same decoder helper
// without going through json.
//
// Negative values clamp to 0; a non-numeric type returns 0.
func uint64FromAny(v any) uint64 {
	switch t := v.(type) {
	case float64:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case int:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case int64:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case uint64:
		return t
	default:
		return 0
	}
}

// uint32FromAny is `uint64FromAny` with a saturating cast to
// uint32 — values exceeding `^uint32(0)` clamp to max u32 rather
// than wrapping. The clamp is conservative; in practice the only
// caller is BidiFrame.StreamID, where values >2^32 are protocol
// errors the bridge would have already rejected.
func uint32FromAny(v any) uint32 {
	x := uint64FromAny(v)
	if x > uint64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(x)
}

// Close half-closes the up direction once, like SendEOF. Receiving remains valid.
// It does not dispose the stream. Always use Dispose for final cleanup.
func (s *BidiStream) Close() error {
	if s == nil {
		return errDendriteBridgeNotOpened
	}
	s.upMu.Lock()
	defer s.upMu.Unlock()
	if s.released.Load() {
		return nil
	}
	// CAS on eofSent is the single fast-path AND the dispatcher
	// election. The loser of a concurrent Close race or a
	// post-SendEOF Close observes the swap failure and returns
	// nil — same idempotent contract.
	if !s.eofSent.CompareAndSwap(false, true) {
		return nil
	}
	if s.bridge == nil {
		return errDendriteBridgeNotOpened
	}
	_, err := s.bridge.callLockedHandleNoPayload(s.streamHandle, false, func(
		sym dendriteBridgeSymbols,
		h C.uint64_t,
	) *C.char {
		return C.axon_bidi_signed_close(sym.descriptorBoundBidiClose, h)
	})
	return err
}

// Dispose releases the native stream, including after half-close or read errors.
// It attempts final cleanup once. Terminal receipts already release native state.
// Dispose may run while Recv is waiting, allowing native cancellation to unblock it.
func (s *BidiStream) Dispose() error {
	if s == nil {
		return errDendriteBridgeNotOpened
	}
	s.upMu.Lock()
	defer s.upMu.Unlock()
	if s.released.Swap(true) {
		return nil
	}
	s.eofSent.Store(true)
	if s.bridge == nil {
		return errDendriteBridgeNotOpened
	}
	return s.bridge.StreamClose(s.streamHandle)
}
