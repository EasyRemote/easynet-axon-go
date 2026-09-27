// File: supervisor_recovery.go
// Description: Persisted orphan recovery outcome ownership.
// Protocol responsibility: Retain failed recovery attempts instead of reporting success.
// Implementation: Validate records, attempt both stages, confirm bounded absence, then remove.
// Usage contract: Injected delivery is private; public scanning uses native signals.
// Architectural position: SDK process recovery; deployment owns record authenticity.
package axon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ReapOrphans returns removed records; delivery is not proof of group death.
func ReapOrphans() []map[string]any {
	return (orphanReaper{signal: signalRecoveryGroup, absent: recoveryGroupAbsent, grace: 200 * time.Millisecond, observationTimeout: 2 * time.Second}).reap(stateDir())
}

type orphanReaper struct {
	signal             func(int, syscall.Signal) error
	absent             func(int) (bool, error)
	observationTimeout time.Duration
	grace              time.Duration
}

func (r orphanReaper) reap(dir string) []map[string]any {
	var reaped []map[string]any
	entries, err := os.ReadDir(dir)
	if err != nil {
		return reaped
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		pgids, valid := recoveryPgids(rec)
		if !valid {
			continue
		}
		terminated := r.signalAll(pgids, syscall.SIGTERM)
		if len(pgids) > 0 {
			time.Sleep(r.grace)
		}
		killed := r.signalAll(pgids, syscall.SIGKILL)
		if terminated && killed && r.confirmAbsence(pgids) {
			if err := os.Remove(p); err == nil {
				reaped = append(reaped, rec)
			}
		}
	}
	return reaped
}

func (r orphanReaper) signalAll(pgids []int, sig syscall.Signal) bool {
	successful := true
	for _, pgid := range pgids {
		if err := r.signal(pgid, sig); err != nil {
			successful = false
		}
	}
	return successful
}

func (r orphanReaper) confirmAbsence(pgids []int) bool {
	started := time.Now()
	for {
		allAbsent := true
		observationFailed := false
		for _, pgid := range pgids {
			absent, err := r.absent(pgid)
			if err != nil {
				observationFailed = true
			} else if !absent {
				allAbsent = false
			}
		}
		if observationFailed {
			return false
		}
		if allAbsent {
			return true
		}
		remaining := r.observationTimeout - time.Since(started)
		if remaining <= 0 {
			return false
		}
		if remaining > 10*time.Millisecond {
			remaining = 10 * time.Millisecond
		}
		time.Sleep(remaining)
	}
}
