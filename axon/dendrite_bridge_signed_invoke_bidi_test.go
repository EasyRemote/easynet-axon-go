// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/dendrite_bridge_signed_invoke_bidi_test.go
// Description: Unit tests for the Go signed InvokeBidi session state machine.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build cgo

package axon

import (
	"strings"
	"testing"
)

func TestBidiStreamRejectsSendAfterEOF(t *testing.T) {
	stream := &BidiStream{bridge: &DendriteBridge{}}
	stream.eofSent.Store(true)

	err := stream.Send(0, []byte("late"), 0)
	if err == nil {
		t.Fatal("expected Send after EOF to fail")
	}
	if !strings.Contains(err.Error(), "up direction is closed") {
		t.Fatalf("expected closed-up-direction error, got %v", err)
	}
}
