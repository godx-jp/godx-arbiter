//go:build !linux && !windows

package runner

import "syscall"

// applyPdeathsig is a no-op outside Linux. macOS / BSD don't have a
// process-death-signal mechanism comparable to PR_SET_PDEATHSIG;
// users on those platforms relying on long-running runs should wrap
// arbiter in launchd or `tmux new-session -d` for survivability.
func applyPdeathsig(_ *syscall.SysProcAttr) {}
