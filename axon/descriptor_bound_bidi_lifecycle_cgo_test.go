//go:build cgo

package axon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestBidiTerminalStopsNativeAccess(t *testing.T) {
	library, _ := nativeOpenCapture(t)
	capture := filepath.Join(t.TempDir(), "bidi.jsonl")
	t.Setenv("AXON_TEST_INVOKE_CAPTURE", capture)
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":true,"kind":"receipt","sequence":2,"terminal":true,"admission_receipt":null,"terminal_receipt":{"invocation_id":"test"}}`)
	bridge, err := OpenDendriteBridge(library)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.CloseLibrary()
	s := &BidiStream{bridge: bridge, streamHandle: 9}
	frame, err := s.Recv(100)
	if err != nil || !frame.Terminal {
		t.Fatalf("terminal: %v", err)
	}
	if err := bridge.CloseLibrary(); err != nil {
		t.Fatal(err)
	}
	frame, err = s.Recv(100)
	if err != nil || frame.Kind != BidiFrameDone {
		t.Fatalf("read after terminal: %v", err)
	}
	if err := s.Send(0, []byte("late"), 0); err == nil {
		t.Fatal("send after terminal accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	disposer, ok := any(s).(interface{ Dispose() error })
	if !ok {
		t.Fatal("final disposal absent")
	}
	if err := disposer.Dispose(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") != 1 {
		t.Fatal("extra FFI after terminal")
	}
}

func TestBidiDisposeAfterEOF(t *testing.T) {
	library, _ := nativeOpenCapture(t)
	capture := filepath.Join(t.TempDir(), "close.jsonl")
	t.Setenv("AXON_TEST_INVOKE_CAPTURE", capture)
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":true}`)
	bridge, err := OpenDendriteBridge(library)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.CloseLibrary()
	s := &BidiStream{bridge: bridge, streamHandle: 9}
	s.eofSent.Store(true)
	disposer, ok := any(s).(interface{ Dispose() error })
	if !ok {
		t.Fatal("final disposal absent")
	}
	if err := disposer.Dispose(); err != nil {
		t.Fatal(err)
	}
	if err := disposer.Dispose(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "raw_close\n" {
		t.Fatalf("disposal count: %q", data)
	}
	frame, err := s.Recv(100)
	if err != nil || frame.Kind != BidiFrameDone {
		t.Fatalf("read after disposal: %v", err)
	}
}

func TestBidiConcurrentDisposeAfterReadError(t *testing.T) {
	library, _ := nativeOpenCapture(t)
	capture := filepath.Join(t.TempDir(), "error-close.jsonl")
	t.Setenv("AXON_TEST_INVOKE_CAPTURE", capture)
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":false,"error":{"code":"AXON_BIDI_TEST","message":"read failed","source":"bridge"}}`)
	bridge, err := OpenDendriteBridge(library)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.CloseLibrary()
	s := &BidiStream{bridge: bridge, streamHandle: 9}
	if _, err := s.Recv(100); err == nil {
		t.Fatal("read failure lost")
	}
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":true}`)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Dispose(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "raw_close\n") != 1 {
		t.Fatalf("cleanup count: %q", data)
	}
}
