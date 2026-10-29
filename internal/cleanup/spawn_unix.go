//go:build !windows

package cleanup

import (
	"os/exec"
	"syscall"
)

// startDetached starts the watcher in its own session, so terminal signals
// (Ctrl+C, SIGHUP on a closed terminal) never reach it.
func startDetached(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, cmd.Start()
}
