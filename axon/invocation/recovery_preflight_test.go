package axon

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoveryPreflightPreservesEarlierLease(t *testing.T) {
	for _, mode := range []string{"finalizing", "unreadable", "malformed"} {
		for _, limit := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s_limit_%d", mode, limit), func(t *testing.T) {
				p := NewPersistentLog(t.TempDir())
				if err := p.InitializeRecovery(RecoverySnapshot{InvocationID: "000_earlier", Phase: "suspended"}); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(p.RecoveryPath("000_earlier"))
				if err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "finalizing":
					err = p.InitializeRecovery(RecoverySnapshot{InvocationID: "zzz_later", Phase: "finalizing"})
				case "unreadable":
					err = os.Mkdir(p.RecoveryPath("zzz_later"), 0700)
				case "malformed":
					err = os.WriteFile(p.RecoveryPath("zzz_later"), []byte("{broken"), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				claimed, failure := p.ClaimRecovery("replacement", time.Second, limit)
				if failure == nil || len(claimed) != 0 {
					t.Fatalf("unresolved batch accepted: %v, %v", claimed, failure)
				}
				if mode == "finalizing" {
					typed, ok := failure.(*AxonError)
					if !ok || typed.Reason != "recovery_finalization_required" || typed.InvocationID != "zzz_later" {
						t.Fatalf("wrong finalization error: %v", failure)
					}
				}
				after, err := os.ReadFile(p.RecoveryPath("000_earlier"))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("failed batch changed earlier lease")
				}
			})
		}
	}
}

func TestRecoveryPreflightRejectsUnreadableDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "log")
	p := NewPersistentLog(directory)
	claimed, err := p.ClaimRecovery("replacement", time.Second, 1)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("empty directory: %v, %v", claimed, err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = p.ClaimRecovery("replacement", time.Second, 1)
	typed, ok := err.(*AxonError)
	if !ok || typed.Reason != "recovery_directory_unavailable" {
		t.Fatalf("directory failure hidden: %v", err)
	}
}
