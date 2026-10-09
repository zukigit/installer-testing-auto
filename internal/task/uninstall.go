package task

import (
	"context"
	"fmt"

	"github.com/zukigit/installer-testing-auto/internal/checks"
	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/env"
)

// Uninstall runs the uninstall command per installer and executes the
// uninstall checks. It is never executed standalone: the runner always runs
// fresh-install first, in the same environment.
type Uninstall struct{}

func init() { register(Uninstall{}) }

func (Uninstall) Name() string { return UninstallName }

func (t Uninstall) Run(ctx context.Context, e env.Environment, b *config.Bundle) Result {
	res := Result{Task: t.Name()}
	for _, ri := range b.Installers {
		res.Checks = append(res.Checks, t.runInstaller(ctx, e, res.Task, ri)...)
	}
	return res
}

func (Uninstall) runInstaller(ctx context.Context, e env.Environment, zone string, ri *config.ResolvedInstaller) []checks.Result {
	if ri.UninstallCmd == "" {
		return []checks.Result{{
			Zone: zone, Installer: ri.Basename, Kind: "uninstall",
			Name:   "uninstall " + ri.Basename,
			Pass:   false,
			Detail: fmt.Sprintf("no uninstall command for %s: set uninstall_command in the config", ri.Basename),
		}}
	}
	r := runCommand(ctx, e, zone, ri, ri.UninstallCmd, "uninstall")
	if !r.Pass {
		return []checks.Result{r} // skip checks when the uninstall itself failed
	}
	return append([]checks.Result{r}, checks.Run(ctx, e, zone, ri, ri.UninstallChecks)...)
}
