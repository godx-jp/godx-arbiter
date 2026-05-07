//go:build !windows

package runner

import "syscall"

// sysProcAttr enables process-group isolation so SIGTERM to the
// group reaches every descendant the child spawned (most notably
// any MCP servers Claude Code launches).
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
