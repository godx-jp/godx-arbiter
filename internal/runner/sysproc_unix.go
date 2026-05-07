//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// sysProcAttr enables process-group isolation so SIGTERM to the
// group reaches every descendant the child spawned (most notably
// any MCP servers Claude Code launches).
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killGroup sends SIGTERM to the child's process group, then SIGKILL
// after the WaitDelay. Together with cmd.WaitDelay = 5s this gives
// the documented 5s grace window.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		// Fall back to per-process kill if we can't see the pgid.
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	return syscall.Kill(-pgid, syscall.SIGTERM)
}
