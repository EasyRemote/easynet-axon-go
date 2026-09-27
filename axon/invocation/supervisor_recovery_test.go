package axon

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestRecoveryRejectsInvalidGroups(t *testing.T) {
	for _, text := range []string{
		`{}`, `null`, `{"pgids":null}`, `{"pgids":[0]}`, `{"pgids":[-1]}`,
		`{"pgids":[1]}`, `{"pgids":[true]}`, `{"pgids":[2147483648]}`,
		`{"pgids":[4294967296]}`, `{"pgids":[2.5]}`, `{"pgids":["42"]}`,
		`{"pgids":[42,null]}`, `{"pgids":[42,-1]}`,
	} {
		t.Run(text, func(t *testing.T) {
			var rec map[string]any
			if err := json.Unmarshal([]byte(text), &rec); err != nil {
				t.Fatal(err)
			}
			if groups, valid := recoveryPgids(rec); valid {
				t.Fatalf("invalid record accepted: %v", groups)
			}
		})
	}
	for _, number := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, valid := recoveryPgids(map[string]any{"pgids": []any{number}}); valid {
			t.Error("nonfinite number accepted")
		}
	}
}

func TestRecoveryPreservesValidGroups(t *testing.T) {
	for _, text := range []string{`{"pgids":[42,2147483647]}`, `{"pgids":[]}`} {
		var rec map[string]any
		if err := json.Unmarshal([]byte(text), &rec); err != nil {
			t.Fatal(err)
		}
		groups, valid := recoveryPgids(rec)
		if !valid {
			t.Fatal("valid record rejected")
		}
		if len(rec["pgids"].([]any)) > 0 && !reflect.DeepEqual(groups, []int{42, 2147483647}) {
			t.Fatalf("wrong groups: %v", groups)
		}
		if len(rec["pgids"].([]any)) == 0 && len(groups) != 0 {
			t.Fatalf("nonempty groups: %v", groups)
		}
	}
}

func TestRecoveryFailedDeliveryRetainsRecord(t *testing.T) {
	for _, target := range []int{42, 43} {
		for _, stage := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
			t.Run(fmt.Sprintf("%d/%d", target, stage), func(t *testing.T) {
				dir := t.TempDir()
				file := filepath.Join(dir, "record.json")
				original := []byte(`{"pgids":[42,43]}`)
				if err := os.WriteFile(file, original, 0600); err != nil {
					t.Fatal(err)
				}
				var calls [][2]int
				r := orphanReaper{absent: func(int) (bool, error) { return true, nil }, signal: func(pgid int, sig syscall.Signal) error {
					calls = append(calls, [2]int{pgid, int(sig)})
					if pgid == target && sig == stage {
						return os.ErrPermission
					}
					return nil
				}}
				if result := r.reap(dir); len(result) != 0 {
					t.Errorf("failed signal reported reaped: %v", result)
				}
				actual, err := os.ReadFile(file)
				if err != nil || string(actual) != string(original) {
					t.Errorf("record not retained: %q %v", actual, err)
				}
				expected := [][2]int{{42, int(syscall.SIGTERM)}, {43, int(syscall.SIGTERM)}, {42, int(syscall.SIGKILL)}, {43, int(syscall.SIGKILL)}}
				if !reflect.DeepEqual(calls, expected) {
					t.Errorf("incomplete stages: %v", calls)
				}
			})
		}
	}
}

func TestRecoveryFailedRecordAllowsIndependentSuccess(t *testing.T) {
	dir := t.TempDir()
	failed := filepath.Join(dir, "failed.json")
	success := filepath.Join(dir, "success.json")
	for file, text := range map[string]string{failed: `{"pgids":[42]}`, success: `{"pgids":[43]}`} {
		if err := os.WriteFile(file, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := orphanReaper{absent: func(int) (bool, error) { return true, nil }, signal: func(pgid int, _ syscall.Signal) error {
		if pgid == 42 {
			return os.ErrPermission
		}
		return nil
	}}
	result := r.reap(dir)
	if len(result) != 1 || result[0]["pgids"].([]any)[0] != float64(43) {
		t.Fatalf("wrong recovery results: %v", result)
	}
	if _, err := os.Stat(failed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(success); !os.IsNotExist(err) {
		t.Fatalf("success record remains: %v", err)
	}
}

func TestRecoveryUnlinkFailureIsNotReaped(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "record.json")
	if err := os.WriteFile(file, []byte(`{"pgids":[42]}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := orphanReaper{absent: func(int) (bool, error) { return true, nil }, signal: func(_ int, sig syscall.Signal) error {
		if sig == syscall.SIGKILL {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(file, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(file, "retain"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}}
	if result := r.reap(dir); len(result) != 0 {
		t.Fatalf("unlink failure reported: %v", result)
	}
	if _, err := os.Stat(filepath.Join(file, "retain")); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryUnsupportedDeliveryRetainsRecord(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "record.json")
	if err := os.WriteFile(file, []byte(`{"pgids":[42]}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := orphanReaper{absent: func(int) (bool, error) { return true, nil }, signal: func(int, syscall.Signal) error { return errors.ErrUnsupported }}
	if result := r.reap(dir); len(result) != 0 {
		t.Fatal(result)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryMalformedRecordsNeverSignal(t *testing.T) {
	dir := t.TempDir()
	for i, text := range []string{`{bad`, `{"pgids":[42,0]}`, `{"pgids":[42,"43"]}`} {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", i)), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := orphanReaper{absent: func(int) (bool, error) { return true, nil }, signal: func(int, syscall.Signal) error { t.Fatal("invalid record signalled"); return nil }}
	if result := r.reap(dir); len(result) != 0 {
		t.Fatal(result)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		t.Fatalf("lost malformed records: %v %v", entries, err)
	}
}

func TestRecoveryPresentGroupRetainsRecord(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "record.json")
	text := []byte(`{"pgids":[42,43]}`)
	if err := os.WriteFile(file, text, 0600); err != nil {
		t.Fatal(err)
	}
	var observed []int
	r := orphanReaper{signal: func(int, syscall.Signal) error { return nil }, absent: func(pgid int) (bool, error) { observed = append(observed, pgid); return false, nil }}
	if result := r.reap(dir); len(result) != 0 {
		t.Errorf("present group reported reaped: %v", result)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != string(text) {
		t.Errorf("record lost: %q %v", data, err)
	}
	if !reflect.DeepEqual(observed, []int{42, 43}) {
		t.Errorf("observation incomplete: %v", observed)
	}
}

func TestRecoveryDeniedObservationRetainsRecord(t *testing.T) {
	for _, failure := range []error{os.ErrPermission, errors.ErrUnsupported} {
		dir := t.TempDir()
		file := filepath.Join(dir, "record.json")
		if err := os.WriteFile(file, []byte(`{"pgids":[42]}`), 0600); err != nil {
			t.Fatal(err)
		}
		r := orphanReaper{signal: func(int, syscall.Signal) error { return nil }, absent: func(int) (bool, error) { return false, failure }}
		if result := r.reap(dir); len(result) != 0 {
			t.Errorf("failed observation reported reaped: %v", result)
		}
		if _, err := os.Stat(file); err != nil {
			t.Error(err)
		}
	}
}

func TestRecoveryDelayedAbsencePrecedesRemoval(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "record.json")
	if err := os.WriteFile(file, []byte(`{"pgids":[42,43]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var observed []int
	r := orphanReaper{signal: func(int, syscall.Signal) error { return nil }, observationTimeout: time.Second,
		absent: func(pgid int) (bool, error) {
			if _, err := os.Stat(file); err != nil {
				t.Fatal("record removed before observation", err)
			}
			observed = append(observed, pgid)
			return pgid == 42 || len(observed) >= 6, nil
		},
	}
	if result := r.reap(dir); len(result) != 1 {
		t.Fatal("record not recovered", result)
	}
	if !reflect.DeepEqual(observed, []int{42, 43, 42, 43, 42, 43}) {
		t.Fatal("wrong observations", observed)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("record remains", err)
	}
}

func TestRecoveryObservationTimeoutAllowsIndependentSuccess(t *testing.T) {
	dir := t.TempDir()
	failed := filepath.Join(dir, "failed.json")
	success := filepath.Join(dir, "success.json")
	for file, text := range map[string]string{failed: `{"pgids":[42,43]}`, success: `{"pgids":[44]}`} {
		if err := os.WriteFile(file, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var observed []int
	r := orphanReaper{signal: func(int, syscall.Signal) error { return nil }, absent: func(pgid int) (bool, error) { observed = append(observed, pgid); return pgid != 42, nil }}
	result := r.reap(dir)
	if len(result) != 1 || result[0]["pgids"].([]any)[0] != float64(44) {
		t.Fatal("wrong recovered records", result)
	}
	if !reflect.DeepEqual(observed, []int{42, 43, 44}) {
		t.Fatal("incomplete observations", observed)
	}
	if _, err := os.Stat(failed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(success); !os.IsNotExist(err) {
		t.Fatal("success record remains", err)
	}
}
