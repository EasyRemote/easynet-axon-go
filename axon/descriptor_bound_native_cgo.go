//go:build cgo

package axon

// InvokeDescriptorBound sends a complete caller-signed unary request.
// No key material or nonce generation belongs to this transport method.
func (b *DendriteBridge) InvokeDescriptorBound(handle uint64, request DescriptorBoundNativeRequest, options DescriptorBoundNativeOptions) (map[string]any, error) {
	payload, err := descriptorBoundNativePayload(request, options)
	if err != nil {
		return nil, err
	}
	response, err := b.callDescriptorBoundInvoke(handle, payload)
	if err != nil {
		return nil, err
	}
	return response.Payload, nil
}

// StreamDescriptorBound carries signed opening facts unchanged. Use StreamNext
// for raw protobuf chunks and StreamClose to dispose, including on early exit.
func (b *DendriteBridge) StreamDescriptorBound(handle uint64, request DescriptorBoundNativeRequest, options DescriptorBoundNativeStreamOptions) (uint64, *ServerStreamOpenResult, error) {
	payload, err := descriptorBoundNativeStreamPayload(request, options)
	if err != nil {
		return 0, nil, err
	}
	response, err := b.callDescriptorBoundStream(handle, payload)
	if err != nil {
		return 0, nil, err
	}
	streamHandle, err := descriptorBoundStreamHandle(response.Payload)
	if err != nil {
		return 0, nil, err
	}
	return streamHandle, &ServerStreamOpenResult{StreamHandle: streamHandle, RawPayload: stripHoistedKeys(response.Payload)}, nil
}

func descriptorBoundStreamHandle(payload map[string]any) (uint64, error) {
	value, ok := payload["stream_handle"].(float64)
	if !ok || value < 1 || value > 9007199254740991 || value != float64(uint64(value)) {
		return 0, DendriteError{Code: ErrCodeBridge, Message: "invalid descriptor-bound stream handle"}
	}
	return uint64(value), nil
}

// BidiDescriptorBound opens a complete caller-signed duplex request. Defer Dispose.
func (b *DendriteBridge) BidiDescriptorBound(handle uint64, request DescriptorBoundNativeRequest, options DescriptorBoundNativeBidiOptions) (*BidiStream, *BidiOpenResult, error) {
	payload, err := descriptorBoundNativeBidiPayload(request, options)
	if err != nil {
		return nil, nil, err
	}
	response, err := b.callDescriptorBoundBidi(handle, payload)
	if err != nil {
		return nil, nil, err
	}
	streamHandle, err := descriptorBoundStreamHandle(response.Payload)
	if err != nil {
		return nil, nil, err
	}
	requestID, _ := response.Payload["request_id"].(string)
	return &BidiStream{bridge: b, streamHandle: streamHandle}, &BidiOpenResult{StreamHandle: streamHandle, RequestID: requestID, RawPayload: stripHoistedKeys(response.Payload)}, nil
}
