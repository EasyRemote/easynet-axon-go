//go:build !cgo

package axon

// InvokeDescriptorBound requires the native CGO transport.
func (b *DendriteBridge) InvokeDescriptorBound(handle uint64, request DescriptorBoundNativeRequest, options DescriptorBoundNativeOptions) (map[string]any, error) {
	return nil, errDendriteUnsupported
}

// StreamDescriptorBound requires the native CGO transport.
func (b *DendriteBridge) StreamDescriptorBound(handle uint64, request DescriptorBoundNativeRequest, options DescriptorBoundNativeStreamOptions) (uint64, *ServerStreamOpenResult, error) {
	return 0, nil, errDendriteUnsupported
}

// BidiDescriptorBound requires the native CGO transport.
func (b *DendriteBridge) BidiDescriptorBound(handle uint64, request DescriptorBoundNativeRequest, options DescriptorBoundNativeBidiOptions) (*BidiStream, *BidiOpenResult, error) {
	return nil, nil, errDendriteUnsupported
}
