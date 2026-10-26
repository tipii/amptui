//go:build unix

package chafa

import (
	"os/exec"
	"syscall"
)

// detached runs cmd in a new session, without a controlling terminal.
// By default chafa probes the terminal through the controlling tty and
// waits up to 5s for an answer — but amptui owns that tty in raw mode,
// so the answer never comes (it would arrive as amptui's input, and the
// query would land on its screen). Every render then hung until killed.
// Detached, there's no tty for chafa to reach, whatever its version.
func detached(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
