package axon

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// ResourceLimit mirrors the spec across SDKs.
type ResourceLimit struct {
	CPUSeconds      float64 // 0 = unset
	MemoryBytes     uint64
	OpenFDs         uint32
	SubprocessCount uint32
	WallSeconds     float64
}

// SupervisorSpec — process-guard + resource-limit configuration.
type SupervisorSpec struct {
	ResourceLimit      ResourceLimit
	CancelGraceSeconds float64 // default 5.0 if 0
	PersistForRecovery bool
	Tags               map[string]string
}

// ProcessHandle — a spawned child process in its own process group.
type ProcessHandle struct {
	Cmd          *exec.Cmd
	Pgid         int
	RegisteredAt time.Time
}

// Alive reports whether the underlying process is still running.
func (h *ProcessHandle) Alive() bool {
	if h.Cmd == nil || h.Cmd.Process == nil {
		return false
	}
	// `FindProcess` never errs on Unix; signal 0 is the liveness probe.
	if err := h.Cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

type supervisorPhase uint8

const (
	supervisorActive supervisorPhase = iota
	supervisorClosing
	supervisorClosed
	supervisorFailed
)

// Supervisor — industrial-grade process guard.
type Supervisor struct {
	InvocationID string
	Spec         SupervisorSpec

	mu           sync.Mutex
	processes    []*ProcessHandle
	cleanupCbs   []namedCb
	cancelCbs    []func()
	subprocCount uint32
	phase        supervisorPhase
	terminalOnce sync.Once
	terminalDone chan struct{}
	cleanupErr   error
	signalGroup  func(int, syscall.Signal) (bool, error)
}

type namedCb struct {
	name string
	cb   func()
}

// NewSupervisor constructs a supervisor for an invocation.
func NewSupervisor(invocationID string, spec SupervisorSpec) *Supervisor {
	if spec.CancelGraceSeconds == 0 {
		spec.CancelGraceSeconds = 0.25
	}
	return &Supervisor{
		InvocationID: invocationID,
		Spec:         spec,
		terminalDone: make(chan struct{}),
		signalGroup:  nativeGroupSignal,
	}
}

// RegisterCleanup — runs after process kill, before terminal receipt.
func (s *Supervisor) RegisterCleanup(cb func(), name string) {
	if cb == nil {
		return
	}
	s.mu.Lock()
	if s.phase == supervisorClosed {
		s.mu.Unlock()
		runCleanupCallback(cb)
		return
	}
	s.cleanupCbs = append(s.cleanupCbs, namedCb{name: name, cb: cb})
	s.mu.Unlock()
}

// OnCancel — runs when cancel flow starts.
func (s *Supervisor) OnCancel(cb func()) {
	if cb == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != supervisorActive {
		return
	}
	s.cancelCbs = append(s.cancelCbs, cb)
}

// SpawnProcessGroup starts a command in its own process group (setsid
// on POSIX; CREATE_NEW_PROCESS_GROUP on Windows).
func (s *Supervisor) SpawnProcessGroup(args []string) (*ProcessHandle, *AxonError) {
	if len(args) == 0 {
		return nil, ErrInvalidArgument("empty_command")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != supervisorActive {
		return nil, ErrInvalidArgument("supervisor_cleanup_started")
	}
	if s.Spec.ResourceLimit.SubprocessCount > 0 &&
		s.subprocCount >= s.Spec.ResourceLimit.SubprocessCount {
		return nil, ErrResourceExhausted("subprocess_limit")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = nil
	// Platform-specific attrs set via helper (see below).
	applyProcGroupAttrs(cmd)

	if err := cmd.Start(); err != nil {
		return nil, ErrInternal("spawn_failed:" + err.Error())
	}
	// Setsid makes the POSIX child its own group leader; the Windows
	// process identifier is retained without implying Job ownership.
	pgid := cmd.Process.Pid
	handle := &ProcessHandle{Cmd: cmd, Pgid: pgid, RegisteredAt: time.Now()}

	s.processes = append(s.processes, handle)
	s.subprocCount++
	shouldPersist := s.Spec.PersistForRecovery
	pgids := make([]int, len(s.processes))
	for i, h := range s.processes {
		pgids[i] = h.Pgid
	}
	if shouldPersist {
		persist(s.InvocationID, pgids)
	}
	return handle, nil
}

// SpawnedPIDs returns pids of children (in spawn order).
func (s *Supervisor) SpawnedPIDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int, 0, len(s.processes))
	for _, h := range s.processes {
		if h.Cmd != nil && h.Cmd.Process != nil {
			out = append(out, h.Cmd.Process.Pid)
		}
	}
	return out
}

// SpawnedPGIDs returns process-group ids of children (in spawn order).
func (s *Supervisor) SpawnedPGIDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int, len(s.processes))
	for i, h := range s.processes {
		out[i] = h.Pgid
	}
	return out
}

// CleanupComplete reports whether the terminal cleanup barrier has closed.
func (s *Supervisor) CleanupComplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase == supervisorClosed
}

// RunCancelFlow executes cancel hooks and closes the terminal cleanup barrier.
func (s *Supervisor) RunCancelFlow(ctx context.Context) {
	s.runTerminalFlow(ctx, true)
}

// RunCompletionFlow closes the same cleanup barrier without cancel hooks.
func (s *Supervisor) RunCompletionFlow(ctx context.Context) {
	s.runTerminalFlow(ctx, false)
}

// CleanupError reports the immutable result of the shared cleanup attempt.
func (s *Supervisor) CleanupError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupErr
}

func (s *Supervisor) runTerminalFlow(ctx context.Context, cancelled bool) {
	s.terminalOnce.Do(func() {
		s.mu.Lock()
		s.phase = supervisorClosing
		s.mu.Unlock()
		go func() {
			defer close(s.terminalDone)
			(&liveCleanupAttempt{supervisor: s}).run(ctx, cancelled)
		}()
	})
	<-s.terminalDone
}

func runCleanupCallback(cb func()) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	cb()
	return true
}

// ─── Persistence + orphan reap ───────────────────────────────

func stateDir() string {
	if p := os.Getenv("AXON_SUPERVISOR_STATE_DIR"); p != "" {
		_ = os.MkdirAll(p, 0o700)
		return p
	}
	p := filepath.Join(os.TempDir(), "axon-supervisor")
	_ = os.MkdirAll(p, 0o700)
	return p
}

func statePath(invocationID string) string {
	return filepath.Join(stateDir(), invocationID+".json")
}

func persist(invocationID string, pgids []int) {
	obj := map[string]any{
		"invocation_id": invocationID,
		"pgids":         pgids,
		"ts":            time.Now().UnixMilli(),
	}
	if b, err := json.Marshal(obj); err == nil {
		_ = os.WriteFile(statePath(invocationID), b, 0o600)
	}
}

func forget(invocationID string) error {
	err := os.Remove(statePath(invocationID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

const maxProcessGroup = 1<<31 - 1

func validProcessGroup(pgid int) bool {
	return pgid > 1 && pgid <= maxProcessGroup
}

func recoveryPgids(rec map[string]any) ([]int, bool) {
	arr, ok := rec["pgids"].([]any)
	if !ok {
		return nil, false
	}
	pgids := make([]int, 0, len(arr))
	for _, value := range arr {
		f, ok := value.(float64)
		if !ok || f <= 1 || f > maxProcessGroup || math.Trunc(f) != f {
			return nil, false
		}
		pgids = append(pgids, int(f))
	}
	return pgids, true
}
