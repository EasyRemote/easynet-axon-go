package axon

// The live cleanup owner retains native wait ownership and publishes one outcome.
// Its observation budget does not imply bounded arbitrary callbacks or storage I/O.
import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

type childExitObservation struct {
	done   <-chan error
	exited bool
	err    error
}
type liveCleanupAttempt struct {
	supervisor *Supervisor
	failure    error
}

func (a *liveCleanupAttempt) fail(stage string) {
	if a.failure == nil {
		err := ErrInternal("supervisor_cleanup_failed").WithInvocationID(a.supervisor.InvocationID)
		err.CauseChain = []string{stage}
		a.failure = err
	}
}

func (a *liveCleanupAttempt) publishLocked() {
	s := a.supervisor
	s.cleanupErr = a.failure
	if a.failure != nil {
		s.phase = supervisorFailed
	} else {
		s.phase = supervisorClosed
	}
}

func (a *liveCleanupAttempt) childrenExited(children []*childExitObservation) bool {
	all := true
	for _, child := range children {
		if !child.exited {
			select {
			case child.err = <-child.done:
				child.exited = true
			default:
			}
		}
		if !child.exited {
			all = false
			continue
		}
		var exit *exec.ExitError
		if child.err != nil && !errors.As(child.err, &exit) {
			a.fail("child_wait_failed")
			all = false
		}
	}
	return all
}

func (a *liveCleanupAttempt) run(ctx context.Context, cancelled bool) {
	s := a.supervisor
	s.mu.Lock()
	processes := append([]*ProcessHandle(nil), s.processes...)
	callbacks := s.cancelCbs
	s.cancelCbs = nil
	s.mu.Unlock()
	if cancelled {
		for _, cb := range callbacks {
			if !runCleanupCallback(cb) {
				a.fail("cancel_callback_failed")
			}
		}
	}
	children := make([]*childExitObservation, 0, len(processes))
	for _, h := range processes {
		if h.Cmd != nil {
			done := make(chan error, 1)
			go func(cmd *exec.Cmd) { done <- cmd.Wait() }(h.Cmd)
			children = append(children, &childExitObservation{done: done})
		}
		if _, err := s.signalGroup(h.Pgid, syscall.SIGTERM); err != nil {
			a.fail("term_delivery_failed")
		}
	}
	grace := time.Duration(s.Spec.CancelGraceSeconds * float64(time.Second))
	if grace > 250*time.Millisecond {
		grace = 250 * time.Millisecond
	}
	deadline := time.Now().Add(grace)
graceLoop:
	for time.Now().Before(deadline) && !a.childrenExited(children) {
		select {
		case <-ctx.Done():
			break graceLoop
		case <-time.After(10 * time.Millisecond):
		}
	}
	for _, h := range processes {
		absent, err := s.signalGroup(h.Pgid, 0)
		if err == nil && absent {
			continue
		}
		if _, err := s.signalGroup(h.Pgid, syscall.SIGKILL); err != nil {
			a.fail("kill_delivery_failed")
		}
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		absent := a.childrenExited(children)
		for _, h := range processes {
			gone, err := s.signalGroup(h.Pgid, 0)
			if err != nil {
				a.fail("group_observation_failed")
			}
			if err != nil || !gone {
				absent = false
			}
		}
		if absent {
			break
		}
		if time.Now().After(deadline) {
			a.fail("exit_observation_timeout")
			s.mu.Lock()
			a.publishLocked()
			s.mu.Unlock()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		s.mu.Lock()
		if len(s.cleanupCbs) == 0 {
			if a.failure == nil && s.Spec.PersistForRecovery {
				if err := forget(s.InvocationID); err != nil {
					a.fail("recovery_removal_failed")
				}
			}
			a.publishLocked()
			s.mu.Unlock()
			return
		}
		callbacks := s.cleanupCbs
		s.cleanupCbs = nil
		s.mu.Unlock()
		for _, cb := range callbacks {
			if !runCleanupCallback(cb.cb) {
				a.fail("cleanup_callback_failed")
			}
		}
	}
}
