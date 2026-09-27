// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/signed_invoke_stream_request.go
// Description: Typed Go request for signed server-stream invocation.

package axon

// SignedInvokeStreamRequest mirrors the Rust bridge's
// SignedInvokeStreamRequest consumed by
// axon_dendrite_descriptor_bound_stream_open_json.
type SignedInvokeStreamRequest struct {
	SignedInvokeRequest

	// ChunkTimeoutMs controls the native stream-next timeout. A zero value lets
	// the bridge use TimeoutMs.
	ChunkTimeoutMs int

	// ChunkBufferSize controls the native response-channel buffer. A zero value
	// lets the bridge use its default.
	ChunkBufferSize int
}

func (r SignedInvokeStreamRequest) helperPayload() (map[string]any, error) {
	payload, err := r.SignedInvokeRequest.helperPayload()
	if err != nil {
		return nil, err
	}
	if r.ChunkTimeoutMs > 0 {
		payload["chunk_timeout_ms"] = r.ChunkTimeoutMs
	}
	if r.ChunkBufferSize > 0 {
		payload["chunk_buffer_size"] = r.ChunkBufferSize
	}
	return payload, nil
}
