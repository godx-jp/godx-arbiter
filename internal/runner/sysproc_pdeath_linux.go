//go:build linux

package runner

import "syscall"

// applyPdeathsig sets PR_SET_PDEATHSIG so the kernel SIGTERMs the
// child if arbiter itself dies (OOM kill, kill -9, panic before
// defer fires). Linux-only; macOS / BSD don't expose an equivalent.
func applyPdeathsig(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGTERM
}
