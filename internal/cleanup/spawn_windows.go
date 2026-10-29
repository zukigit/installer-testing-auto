//go:build windows

package cleanup

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess        = 0x00000008
	createBreakawayFromJob = 0x01000000
)

// startDetached starts the watcher without a console and in its own process
// group, so Ctrl+C and closing the console window never reach it. It also
// tries to leave the caller's job object (some terminals / ssh sessions kill
// the whole job on close); when the job forbids that, it starts inside it.
func startDetached(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	base := uint32(syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess)
	cmd := newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: base | createBreakawayFromJob, HideWindow: true}
	if err := cmd.Start(); err == nil {
		return cmd, nil
	}
	cmd = newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: base, HideWindow: true}
	return cmd, cmd.Start()
}
