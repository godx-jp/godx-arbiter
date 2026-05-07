//go:build windows

package runner

import (
	"os/exec"
	"syscall"
)

// sysProcAttr is a no-op on Windows; Go's child-process model handles
// teardown via job objects when the parent process exits, and we
// don't drive a richer process group there.
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }

// killGroup on Windows sends a per-process kill via os.Process.Kill —
// Windows doesn't have POSIX process groups. Go's Cmd.Cancel +
// WaitDelay still give us the 5s grace timeout for the tree the child
// itself is responsible for.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
