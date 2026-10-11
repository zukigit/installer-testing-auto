package task

import (
	"context"
	"fmt"

	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/env"
	"github.com/zukigit/installer-testing-auto/internal/logging"
)

// Uninstall is a standalone task: in its own container it installs the
// package (setup — must succeed, no checks), runs the uninstall command and
// executes the uninstall checks.
type Uninstall struct{}

func init() { register(Uninstall{}) }

func (Uninstall) Name() string { return UninstallName }

func (t Uninstall) Run(ctx context.Context, e env.Environment, ri *config.ResolvedInstaller, cl *logging.CaseLogger) Result {
	// setup: copy + install (unchecked — install_checks belong to fresh-install)
	var passed int
	copyCmd := fmt.Sprintf("%s -> %s", ri.LocalPath, ri.ContainerPath)
	if err := e.Copy(ctx, ri.LocalPath, ri.ContainerPath); err != nil {
		cl.Step("copy", copyCmd, -1, false, "", err.Error())
		return Result{Failed: 1}
	}
	cl.Step("copy", copyCmd, 0, true, "", "")
	passed++ // copy step
	if ri.InstallCmd == "" {
		cl.Step("setup-install", "", -1, false, "",
			fmt.Sprintf("no install command for %s: set install_command in the config", ri.Basename))
		return Result{Passed: passed, Failed: 1}
	}
	setupCmd := ri.Expand(ri.InstallCmd)
	stdout, stderr, code, err := e.Exec(ctx, []string{"/bin/sh", "-c", setupCmd})
	cl.Step("setup-install", setupCmd, code, err == nil && code == 0, stdout, stderr)
	if err != nil || code != 0 {
		return Result{Passed: passed, Failed: 1} // cannot uninstall what is not installed
	}
	passed++ // setup-install step

	if ri.UninstallCmd == "" {
		cl.Step("uninstall", "", -1, false, "",
			fmt.Sprintf("no uninstall command for %s: set uninstall_command in the config", ri.Basename))
		return Result{Passed: passed, Failed: 1}
	}
	p, f := runPhase(ctx, e, cl, ri, ri.UninstallCmd, "uninstall", ri.UninstallChecks)
	return Result{Passed: passed + p, Failed: f}
}
