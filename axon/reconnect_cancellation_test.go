//go:build cgo

package axon

import (
	"context"
	"errors"
	"testing"
)

func TestReconnectCancellationAfterHookRetiresClient(t *testing.T) {
	for _, failHook := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failHook], func(t *testing.T) {
			library, opens := nativeOpenCapture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prior := errors.New("prior reconnect failure")
			state := &reconnectState{options: ReconnectOptions{MaxAttempts: 1}, lastReconnectErr: prior}
			state.disconnected.Store(true)
			calls := 0
			state.options.OnReconnect = func(context.Context) error {
				calls++
				cancel()
				if failHook {
					return errors.New("late hook failure")
				}
				return nil
			}
			transport := &SidecarTransport{Endpoint: "http://localhost:50051", LibraryPath: library, reconnect: state}
			defer transport.Close()
			transport.driveReconnect(ctx)
			if calls != 1 || len(opens()) != 1 {
				t.Fatal("unexpected callback/open count")
			}
			if transport.ReconnectGeneration() != 0 || !state.disconnected.Load() {
				t.Fatal("cancelled hook published a successful connection")
			}
			if !errors.Is(transport.LastReconnectError(), prior) {
				t.Fatal("late result replaced prior failure")
			}
			if transport.handle != 0 {
				t.Fatal("cancelled reconnect retained native client handle")
			}
		})
	}
}
