//go:build windows

package runner

import "syscall"

// sysProcAttr is a no-op on Windows; Go's child-process model handles
// teardown via job objects when the parent process exits, and we
// don't drive a richer process group there.
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }
