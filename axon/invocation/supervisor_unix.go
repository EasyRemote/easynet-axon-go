//go:build unix

package axon

import (
	"os/exec"
	"syscall"
)

// applyProcGroupAttrs — POSIX setsid + new process group.
func applyProcGroupAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
}

// killpg — signal every process in the group.
func nativeGroupSignal(pgid int, sig syscall.Signal) (bool, error) {
	if !validProcessGroup(pgid) {
		return false, syscall.EINVAL
	}
	err := syscall.Kill(-pgid, sig)
	if err == syscall.ESRCH {
		return true, nil
	}
	return false, err
}

func signalRecoveryGroup(pgid int, sig syscall.Signal) error {
	_, err := nativeGroupSignal(pgid, sig)
	return err
}

func recoveryGroupAbsent(pgid int) (bool, error) { return nativeGroupSignal(pgid, 0) }
