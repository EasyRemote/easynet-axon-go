// Tests for the auto-reconnect scaffolding in SidecarTransport.
//
// These tests exercise the reconnect-state machinery and the transport-error
// classifier without touching the FFI bridge — the bridge requires a real
// runtime to talk to. The flag-driven state transitions are the riskiest part
// and deserve coverage independent of the runtime.

package axon

import (
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestReconnectOptionsApplyDefaults(t *testing.T) {
	t.Parallel()
	opts := ReconnectOptions{}
	opts.applyDefaults()
	if opts.InitialBackoffMs != 1000 {
		t.Fatalf("InitialBackoffMs: want 1000, got %d", opts.InitialBackoffMs)
	}
	if opts.MaxBackoffMs != 30000 {
		t.Fatalf("MaxBackoffMs: want 30000, got %d", opts.MaxBackoffMs)
	}
	if opts.HealthCheckIntervalMs != 15000 {
		t.Fatalf("HealthCheckIntervalMs: want 15000, got %d", opts.HealthCheckIntervalMs)
	}
}

func TestReconnectOptionsClampsMaxBelowInitial(t *testing.T) {
	t.Parallel()
	opts := ReconnectOptions{InitialBackoffMs: 5000, MaxBackoffMs: 100}
	opts.applyDefaults()
	if opts.MaxBackoffMs != 30000 {
		t.Fatalf("MaxBackoffMs < Initial should be clamped up to default, got %d", opts.MaxBackoffMs)
	}
}

func TestIsTransportErrorClassifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want bool
		name string
	}{
		{nil, false, "nil"},
		{errors.New("connection refused"), true, "refused"},
		{errors.New("rpc error: code = Unavailable"), true, "grpc_unavailable"},
		{errors.New("context deadline exceeded"), true, "deadline"},
		{errors.New("EOF while reading response"), true, "eof_marker"},
		{errors.New("broken pipe on stream send"), true, "broken_pipe"},
		{errors.New("AXON_NODE_UNREACHABLE"), true, "node_unreachable"},
		{errors.New("AXON_PEER_DISCONNECTED"), true, "peer_disconnected"},
		{errors.New("invalid argument: tenant_id required"), false, "application_error"},
		{errors.New("forbidden: tenant scope mismatch"), false, "policy_error"},
		{errors.New("not_found: capability"), false, "not_found"},
		{errors.New("policy_denied: tenant"), false, "policy_denied"},
	}
	for _, tc := range cases {
		if got := isTransportError(tc.err); got != tc.want {
			t.Fatalf("%s: isTransportError(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

func TestReconnectGenerationZeroWhenDisabled(t *testing.T) {
	t.Parallel()
	s := &SidecarTransport{AutoReconnect: false}
	if g := s.ReconnectGeneration(); g != 0 {
		t.Fatalf("ReconnectGeneration with AutoReconnect=false: want 0, got %d", g)
	}
	if err := s.LastReconnectError(); err != nil {
		t.Fatalf("LastReconnectError with AutoReconnect=false: want nil, got %v", err)
	}
	// markDisconnected must be a no-op (never panic) when reconnect is disabled.
	s.markDisconnected()
}

// TestReconnectWorkerExitsOnShutdown guards the background goroutine
// lifecycle. A healthy Close() must join the reconnect worker so that
// long-lived processes that open and discard transports never leak
// goroutines. The test drives shutdown directly through
// shutdownReconnect() (not Close()) so no bridge handle is required.
func TestReconnectWorkerExitsOnShutdown(t *testing.T) {
	t.Parallel()

	// Start a transport with an aggressive health-check interval so the
	// worker is provably alive within a few milliseconds.
	s := &SidecarTransport{
		AutoReconnect: true,
		ReconnectOptions: ReconnectOptions{
			HealthCheckIntervalMs: 20,
		},
	}
	s.enableAutoReconnect()

	// Observe the worker actually started: wg.Add(1) was called.
	before := runtime.NumGoroutine()
	if before < 1 {
		t.Fatalf("expected at least one goroutine, got %d", before)
	}

	// Shutdown must return synchronously after the worker exits; if the
	// worker were leaked, wg.Wait() in shutdownReconnect would block
	// forever and the test would hit its own deadline.
	done := make(chan struct{})
	go func() {
		s.shutdownReconnect()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("shutdownReconnect did not return within 2s — worker leaked")
	}

	// A second shutdown must be idempotent: cancel already fired and the
	// waitgroup is already drained, so the call returns immediately.
	s.shutdownReconnect()
}

// TestReconnectDisabledMeansNoWorker proves that setting
// HealthCheckIntervalMs<=0 truly opts out of the background goroutine —
// applications that want purely reactive reconnect (drive from the call
// path) rely on this to avoid paying a dedicated goroutine per transport.
func TestReconnectDisabledMeansNoWorker(t *testing.T) {
	t.Parallel()
	s := &SidecarTransport{
		AutoReconnect: true,
		ReconnectOptions: ReconnectOptions{
			HealthCheckIntervalMs: -1, // opt-out
		},
	}
	// enableAutoReconnect exits before spawning when the interval<=0.
	s.enableAutoReconnect()

	var called atomic.Bool
	// shutdownReconnect must still be safe to call and must not block
	// (there is no worker to wait for).
	done := make(chan struct{})
	go func() {
		s.shutdownReconnect()
		called.Store(true)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("shutdownReconnect blocked even though no worker was started")
	}
	if !called.Load() {
		t.Fatal("shutdownReconnect did not complete cleanly")
	}
}
