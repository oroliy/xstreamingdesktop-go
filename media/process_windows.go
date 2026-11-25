//go:build windows

package media

import (
	"os/exec"
	"syscall"
)

// Media processes are background workers for the graphical application.
func configureMediaProcess(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
