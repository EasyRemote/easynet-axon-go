package axon

import "testing"

type nativeTestRequest struct {
	payload map[string]any
	err     error
}

func (r nativeTestRequest) DescriptorBoundNativePayload() (map[string]any, error) {
	return r.payload, r.err
}

func TestDescriptorBoundNativeOptions(t *testing.T) {
	metadata := map[string]string{"trace": "original"}
	options := DescriptorBoundNativeOptions{RequestID: "request", ContentType: "application/octet-stream", Metadata: metadata}
	payload, err := descriptorBoundNativePayload(nativeTestRequest{payload: map[string]any{"mode": "unary", "payload_base64": "AP8="}}, options)
	if err != nil {
		t.Fatal(err)
	}
	metadata["trace"] = "changed"
	if payload["request_id"] != "request" || payload["content_type"] != options.ContentType || payload["timeout_ms"] != DefaultTimeoutMs || payload["metadata"].(map[string]string)["trace"] != "original" || payload["payload_base64"] != "AP8=" {
		t.Fatal("transport options changed signed facts or retained caller metadata")
	}
	for _, invalid := range []DescriptorBoundNativeOptions{{ContentType: "x"}, {RequestID: "r"}, {RequestID: "r", ContentType: "x", TimeoutMs: -1}} {
		if _, err := descriptorBoundNativePayload(nativeTestRequest{payload: map[string]any{"mode": "unary"}}, invalid); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	for _, request := range []DescriptorBoundNativeRequest{nil, nativeTestRequest{}, nativeTestRequest{err: DendriteError{Code: ErrCodeBridge, Message: "invalid carrier"}}} {
		if _, err := descriptorBoundNativePayload(request, options); err == nil {
			t.Fatal("invalid carrier accepted")
		}
	}
}

func TestDescriptorBoundStreamOptions(t *testing.T) {
	base := DescriptorBoundNativeOptions{RequestID: "stream", ContentType: "application/octet-stream"}
	payload, err := descriptorBoundNativeStreamPayload(nativeTestRequest{payload: map[string]any{"mode": "server_stream"}}, DescriptorBoundNativeStreamOptions{DescriptorBoundNativeOptions: base})
	if err != nil {
		t.Fatal(err)
	}
	stream := payload["stream"].(map[string]any)
	if stream["chunk_buffer_size"] != 64 || stream["chunk_timeout_ms"] != DefaultStreamChunkTimeoutMs || stream["request_buffer_size"] != nil || len(stream["streams"].([]any)) != 0 {
		t.Fatal("invalid defaults")
	}
	for _, options := range []DescriptorBoundNativeStreamOptions{
		{DescriptorBoundNativeOptions: base, ChunkTimeoutMs: -1},
		{DescriptorBoundNativeOptions: base, ChunkBufferSize: -1}, {},
	} {
		if _, err := descriptorBoundNativeStreamPayload(nativeTestRequest{payload: map[string]any{"mode": "server_stream"}}, options); err == nil {
			t.Fatal("invalid stream options accepted")
		}
	}
	if _, err := descriptorBoundNativeStreamPayload(nil, DescriptorBoundNativeStreamOptions{DescriptorBoundNativeOptions: base}); err == nil {
		t.Fatal("nil request accepted")
	}
}

func TestDescriptorBoundNativeRejectsModeConversion(t *testing.T) {
	options := DescriptorBoundNativeOptions{RequestID: "mode", ContentType: "application/octet-stream"}
	for _, mode := range []string{"unary", "bidi", ""} {
		if _, err := descriptorBoundNativeStreamPayload(nativeTestRequest{payload: map[string]any{"mode": mode}}, DescriptorBoundNativeStreamOptions{DescriptorBoundNativeOptions: options}); err == nil {
			t.Fatal("stream converted mode", mode)
		}
	}
	for _, mode := range []string{"server_stream", "bidi", ""} {
		if _, err := descriptorBoundNativePayload(nativeTestRequest{payload: map[string]any{"mode": mode}}, options); err == nil {
			t.Fatal("unary accepted mode", mode)
		}
	}
}

func TestDescriptorBoundBidiOptions(t *testing.T) {
	base := DescriptorBoundNativeBidiOptions{DescriptorBoundNativeStreamOptions: DescriptorBoundNativeStreamOptions{DescriptorBoundNativeOptions: DescriptorBoundNativeOptions{RequestID: "bidi", ContentType: "application/octet-stream"}}}
	request := nativeTestRequest{payload: map[string]any{"mode": "bidi"}}
	payload, err := descriptorBoundNativeBidiPayload(request, base)
	if err != nil {
		t.Fatal(err)
	}
	stream := payload["stream"].(map[string]any)
	if stream["request_buffer_size"] != 64 || stream["chunk_buffer_size"] != 64 || stream["chunk_timeout_ms"] != DefaultStreamChunkTimeoutMs {
		t.Fatal("defaults unbounded")
	}
	for _, change := range []string{"request", "chunk", "timeout", "ordering", "content", "mode"} {
		t.Run(change, func(t *testing.T) {
			options := base
			req := nativeTestRequest{payload: map[string]any{"mode": "bidi"}}
			switch change {
			case "request":
				options.RequestBufferSize = -1
			case "chunk":
				options.ChunkBufferSize = -1
			case "timeout":
				options.ChunkTimeoutMs = -1
			case "ordering":
				options.Streams = []StreamDescriptor{{ContentType: "text/plain", Ordering: "loose"}}
			case "content":
				options.Streams = []StreamDescriptor{{}}
			case "mode":
				req.payload["mode"] = "unary"
			}
			if _, err := descriptorBoundNativeBidiPayload(req, options); err == nil {
				t.Fatal("invalid bidi options accepted")
			}
		})
	}
}
