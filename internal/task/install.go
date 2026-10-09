package task

import (
	"context"
	"fmt"
	"strings"

	"github.com/zukigit/installer-testing-auto/internal/checks"
	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/env"
)

// FreshInstall copies the package list into the environment, runs the
// install command per installer and executes the install checks.
type FreshInstall struct{}

func init() { register(FreshInstall{}) }

func (FreshInstall) Name() string { return FreshInstallName }

func (t FreshInstall) Run(ctx context.Context, e env.Environment, b *config.Bundle) Result {
	res := Result{Task: t.Name()}

	// 1) copy all installers into the environment
	for _, ri := range b.Installers {
		if err := e.Copy(ctx, ri.LocalPath, ri.ContainerPath); err != nil {
			res.Checks = append(res.Checks, checks.Result{
				Zone: res.Task, Installer: ri.Basename, Kind: "copy",
				Name: fmt.Sprintf("copy %s -> %s", ri.LocalPath, ri.ContainerPath),
				Pass: false, Detail: err.Error(),
			})
		}
	}

	// 2) install + check per installer
	for _, ri := range b.Installers {
		res.Checks = append(res.Checks, t.runInstaller(ctx, e, res.Task, ri)...)
	}
	return res
}

func (FreshInstall) runInstaller(ctx context.Context, e env.Environment, zone string, ri *config.ResolvedInstaller) []checks.Result {
	// install command (a missing default/override for the extension is a failure)
	if ri.InstallCmd == "" {
		return []checks.Result{{
			Zone: zone, Installer: ri.Basename, Kind: "install",
			Name:   "install " + ri.Basename,
			Pass:   false,
			Detail: fmt.Sprintf("no install command for %s: set install_command in the config", ri.Basename),
		}}
	}
	r := runCommand(ctx, e, zone, ri, ri.InstallCmd, "install")
	if !r.Pass {
		return []checks.Result{r} // skip checks when the install itself failed
	}
	return append([]checks.Result{r}, checks.Run(ctx, e, zone, ri, ri.InstallChecks)...)
}

// runCommand expands a command template, executes it through the shell and
// wraps the outcome as a check result.
func runCommand(ctx context.Context, e env.Environment, zone string, ri *config.ResolvedInstaller, cmd, kind string) checks.Result {
	name := ri.Expand(cmd)
	stdout, stderr, code, err := e.Exec(ctx, []string{"/bin/sh", "-c", name})
	if err != nil {
		return checks.Result{
			Zone: zone, Installer: ri.Basename, Kind: kind, Name: name,
			Pass: false, Detail: err.Error(),
			Output: combine(stdout, stderr),
		}
	}
	if code != 0 {
		return checks.Result{
			Zone: zone, Installer: ri.Basename, Kind: kind, Name: name,
			Pass:   false,
			Detail: fmt.Sprintf("exit code %d (expected 0)", code),
			Output: combine(stdout, stderr),
		}
	}
	return checks.Result{Zone: zone, Installer: ri.Basename, Kind: kind, Name: name, Pass: true}
}

func combine(stdout, stderr string) string {
	out := strings.TrimSpace(stdout + stderr)
	const max = 2000
	if len(out) > max {
		out = out[:max] + "..."
	}
	return out
}
