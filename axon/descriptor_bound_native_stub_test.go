//go:build !cgo

package axon

import "testing"

func TestDescriptorBoundNativeRequiresCGO(t *testing.T) {
	var bridge DendriteBridge
	if _, err := bridge.InvokeDescriptorBound(1, nativeTestRequest{}, DescriptorBoundNativeOptions{}); err != errDendriteUnsupported {
		t.Fatalf("unsupported result: %v", err)
	}
}

func TestDescriptorBoundStreamRequiresCGO(t *testing.T) {
	bridge := &DendriteBridge{}
	if _, _, err := bridge.StreamDescriptorBound(1, nativeTestRequest{}, DescriptorBoundNativeStreamOptions{}); err != errDendriteUnsupported {
		t.Fatalf("stub: %v", err)
	}
}

func TestDescriptorBoundBidiRequiresCGO(t *testing.T) {
	if _, _, err := (&DendriteBridge{}).BidiDescriptorBound(1, nativeTestRequest{}, DescriptorBoundNativeBidiOptions{}); err != errDendriteUnsupported {
		t.Fatal(err)
	}
}
