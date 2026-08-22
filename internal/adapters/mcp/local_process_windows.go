//go:build windows

package mcp

import "os/exec"

// Windows process-tree termination requires Job Objects. CommandContext still
// terminates the directly launched CLI; this hook keeps command construction
// platform-specific without adding a shell fallback.
func configureLocalCommand(command *exec.Cmd) {}
