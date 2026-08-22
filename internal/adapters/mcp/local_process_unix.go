//go:build !windows

package mcp

import (
	"os/exec"
	"syscall"
)

// configureLocalCommand puts each CLI in its own process group. CommandContext
// invokes Cancel when the turn context ends; killing the group also stops
// descendants spawned by Python or shell-based wrappers.
func configureLocalCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
