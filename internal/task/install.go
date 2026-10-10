package task

import (
	"context"
	"fmt"

	"github.com/zukigit/installer-testing-auto/internal/checks"
	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/env"
	"github.com/zukigit/installer-testing-auto/internal/logging"
)

// FreshInstall copies the installer into the environment, runs the install
// command and executes the install checks.
type FreshInstall struct{}

func init() { register(FreshInstall{}) }

func (FreshInstall) Name() string { return FreshInstallName }

func (t FreshInstall) Run(ctx context.Context, e env.Environment, ri *config.ResolvedInstaller, cl *logging.CaseLogger) Result {
	var passed int
	if !copyInstaller(ctx, e, cl, ri) {
		return Result{Failed: 1}
	}
	passed++ // copy step
	if ri.InstallCmd == "" {
		cl.Step("install", "", -1, false,
			fmt.Sprintf("no install command for %s: set install_command in the config", ri.Basename))
		return Result{Passed: passed, Failed: 1}
	}
	p, f := runPhase(ctx, e, cl, ri, ri.InstallCmd, "install", ri.InstallChecks)
	return Result{Passed: passed + p, Failed: f}
}

// copyInstaller uploads the installer file; returns false and logs a failed
// copy step when it errors.
func copyInstaller(ctx context.Context, e env.Environment, cl *logging.CaseLogger, ri *config.ResolvedInstaller) bool {
	copyCmd := fmt.Sprintf("%s -> %s", ri.LocalPath, ri.ContainerPath)
	if err := e.Copy(ctx, ri.LocalPath, ri.ContainerPath); err != nil {
		cl.Step("copy", copyCmd, -1, false, err.Error())
		return false
	}
	cl.Step("copy", copyCmd, 0, true, "")
	return true
}

// runPhase runs one command phase (install / uninstall) through the shell and
// then the phase's checks. The command execution itself counts as one passed
// result; a failed command aborts the case immediately.
func runPhase(ctx context.Context, e env.Environment, cl *logging.CaseLogger, ri *config.ResolvedInstaller, tmpl, kind string, cs *config.Checks) (int, int) {
	cmd := ri.Expand(tmpl)
	stdout, stderr, code, err := e.Exec(ctx, []string{"/bin/sh", "-c", cmd})
	pass := err == nil && code == 0
	cl.Step(kind, cmd, code, pass, combineOut(stdout, stderr))
	if !pass {
		return 0, 1
	}
	passed, failed := checks.Run(ctx, e, ri, cs, cl)
	return passed + 1, failed // +1 for the successful phase command
}

// combineOut joins docker's merged output; truncation happens in the emitter.
func combineOut(stdout, stderr string) string {
	return stdout + stderr
}
