//go:build windows

package axon

import (
	"errors"
	"os/exec"
	"syscall"
)

// applyProcGroupAttrs — create a new process group on Windows.
func applyProcGroupAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000200, // CREATE_NEW_PROCESS_GROUP
	}
}

// Native group cleanup requires Job ownership; a leaf PID is not equivalent.
func nativeGroupSignal(_ int, _ syscall.Signal) (bool, error) {
	return false, errors.ErrUnsupported
}

func signalRecoveryGroup(pgid int, sig syscall.Signal) error {
	_, err := nativeGroupSignal(pgid, sig)
	return err
}

func recoveryGroupAbsent(pgid int) (bool, error) { return nativeGroupSignal(pgid, 0) }
