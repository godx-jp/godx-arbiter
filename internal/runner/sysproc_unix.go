//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// sysProcAttr enables:
//
//   - Setpgid: child runs in its own process group so SIGTERM to
//     the group reaches every descendant the child spawned (most
//     notably any MCP servers Claude Code launches).
//   - Pdeathsig (Linux only, see sysproc_pdeath_*.go): if arbiter
//     itself is SIGKILL'd by the OOM killer, kill -9, or otherwise
//     dies before defer cleanup runs, the kernel delivers SIGTERM
//     to the child automatically. This is the protection against
//     orphan claude sessions that pure userspace can't cover.
func sysProcAttr() *syscall.SysProcAttr {
	attr := &syscall.SysProcAttr{Setpgid: true}
	applyPdeathsig(attr)
	return attr
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
