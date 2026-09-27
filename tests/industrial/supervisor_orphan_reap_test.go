// I-03: recover an orphan after its independent host exits.
//go:build unix

package industrial

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_supervisor_orphan_reap(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AXON_SUPERVISOR_STATE_DIR", dir)
	t.Setenv("AXON_GO_ORPHAN_HOST", dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host := exec.CommandContext(ctx, exe, "-test.run=^Test_supervisor_orphan_host$")
	output, err := host.CombinedOutput()
	if err != nil {
		t.Fatalf("host failed: %v %s", err, output)
	}
	file := filepath.Join(dir, "inv_orphan.json")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	pgid := int(record["pgids"].([]any)[0].(float64))
	if err := syscall.Kill(-pgid, 0); err != nil {
		t.Fatalf("orphan not alive: %v", err)
	}
	if result := inv.ReapOrphans(); !reflect.DeepEqual(result, []map[string]any{record}) {
		t.Fatalf("wrong recovery result: %v", result)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("record remains: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(-pgid, 0)
		if err == syscall.ESRCH {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("group remains observable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func Test_supervisor_orphan_host(t *testing.T) {
	dir := os.Getenv("AXON_GO_ORPHAN_HOST")
	if dir == "" {
		t.Skip("child-only host fixture, exercised by orphan recovery test")
	}
	marker := filepath.Join(dir, "ready")
	supervisor := inv.NewSupervisor("inv_orphan", inv.SupervisorSpec{PersistForRecovery: true})
	// Target self-expires if the test aborts; no cleanup signals target stale PIDs.
	if _, err := supervisor.SpawnProcessGroup([]string{"sh", "-c", `printf ready > "$1"; exec sleep 10`, "orphan", marker}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("target did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Exit the actual parent without running supervisor terminal cleanup.
	os.Exit(0)
}
