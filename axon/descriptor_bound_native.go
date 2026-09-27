package axon

// DescriptorBoundNativeRequest projects caller-owned authority into a fresh
// native JSON document. Canonical request implementations live above transport.
// The native runtime still verifies admission; transport never creates authority.
type DescriptorBoundNativeRequest interface {
	DescriptorBoundNativePayload() (map[string]any, error)
}

// DescriptorBoundNativeOptions are outside the caller-signed seven-tuple.
type DescriptorBoundNativeOptions struct {
	RequestID   string
	ContentType string
	TimeoutMs   int
	Metadata    map[string]string
}

func descriptorBoundNativePayload(request DescriptorBoundNativeRequest, options DescriptorBoundNativeOptions) (map[string]any, error) {
	return descriptorBoundNativePayloadForMode(request, options, "unary")
}

func descriptorBoundNativePayloadForMode(request DescriptorBoundNativeRequest, options DescriptorBoundNativeOptions, mode string) (map[string]any, error) {
	if request == nil {
		return nil, DendriteError{Code: ErrCodeBridge, Message: "descriptor-bound request is required"}
	}
	if err := requireNonBlank("request_id", options.RequestID); err != nil {
		return nil, err
	}
	if err := requireNonBlank("content_type", options.ContentType); err != nil {
		return nil, err
	}
	if options.TimeoutMs < 0 {
		return nil, DendriteError{Code: ErrCodeBridge, Message: "timeout_ms must not be negative"}
	}
	payload, err := request.DescriptorBoundNativePayload()
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, DendriteError{Code: ErrCodeBridge, Message: "descriptor-bound payload is required"}
	}
	if payload["mode"] != mode {
		return nil, DendriteError{Code: ErrCodeBridge, Message: "descriptor-bound request mode does not match transport"}
	}
	timeout := options.TimeoutMs
	if timeout == 0 {
		timeout = DefaultTimeoutMs
	}
	metadata := make(map[string]string, len(options.Metadata))
	for key, value := range options.Metadata {
		metadata[key] = value
	}
	payload["request_id"] = options.RequestID
	payload["content_type"] = options.ContentType
	payload["timeout_ms"] = timeout
	payload["metadata"] = metadata
	payload["stream"] = nil
	return payload, nil
}

// DescriptorBoundNativeStreamOptions configure a complete-request server stream.
type DescriptorBoundNativeStreamOptions struct {
	DescriptorBoundNativeOptions
	ChunkTimeoutMs  int
	ChunkBufferSize int
}

func descriptorBoundNativeStreamPayload(request DescriptorBoundNativeRequest, options DescriptorBoundNativeStreamOptions) (map[string]any, error) {
	return descriptorBoundNativeStreamingPayload(request, options, "server_stream", nil, nil)
}

// DescriptorBoundNativeBidiOptions configure a complete-request duplex session.
type DescriptorBoundNativeBidiOptions struct {
	DescriptorBoundNativeStreamOptions
	RequestBufferSize int
	Streams           []StreamDescriptor
}

func descriptorBoundNativeBidiPayload(request DescriptorBoundNativeRequest, options DescriptorBoundNativeBidiOptions) (map[string]any, error) {
	capacity := options.RequestBufferSize
	if capacity < 0 {
		return nil, DendriteError{Code: ErrCodeBridge, Message: "request buffer must not be negative"}
	}
	if capacity == 0 {
		capacity = 64
	}
	return descriptorBoundNativeStreamingPayload(request, options.DescriptorBoundNativeStreamOptions, "bidi", capacity, options.Streams)
}

func descriptorBoundNativeStreamingPayload(request DescriptorBoundNativeRequest, options DescriptorBoundNativeStreamOptions, mode string, requestCapacity any, descriptors []StreamDescriptor) (map[string]any, error) {
	if options.ChunkTimeoutMs < 0 || options.ChunkBufferSize < 0 {
		return nil, DendriteError{Code: ErrCodeBridge, Message: "stream timeout and buffer must not be negative"}
	}
	streams := make([]any, 0, len(descriptors))
	for _, sd := range descriptors {
		if err := requireNonBlank("stream content_type", sd.ContentType); err != nil {
			return nil, err
		}
		if sd.Ordering != "" && sd.Ordering != "STRICT" {
			return nil, DendriteError{Code: ErrCodeBridge, Message: "stream ordering must be STRICT or empty"}
		}
		streams = append(streams, map[string]any{"stream_id": sd.StreamID, "content_type": sd.ContentType, "codec_params": sd.CodecParams, "ordering": sd.Ordering})
	}
	payload, err := descriptorBoundNativePayloadForMode(request, options.DescriptorBoundNativeOptions, mode)
	if err != nil {
		return nil, err
	}
	timeout, capacity := options.ChunkTimeoutMs, options.ChunkBufferSize
	if timeout == 0 {
		timeout = DefaultStreamChunkTimeoutMs
	}
	if capacity == 0 {
		capacity = 64
	}
	payload["stream"] = map[string]any{"chunk_timeout_ms": timeout, "chunk_buffer_size": capacity, "request_buffer_size": requestCapacity, "streams": streams}
	return payload, nil
}
