package task

import (
	"context"
	"fmt"

	"github.com/zukigit/installer-testing-auto/internal/checks"
	"github.com/zukigit/installer-testing-auto/internal/env"
	"github.com/zukigit/installer-testing-auto/internal/logging"
)

// Upgrade is a version-pair task: in its own container it installs the OLD
// version (setup — must succeed, no checks, counted), upgrades to the NEW
// version with the upgrade command, and runs the upgrade checks. The pair is
// built by the runner from the versioned installer files.
type Upgrade struct{}

func init() { register(Upgrade{}) }

func (Upgrade) Name() string { return UpgradeName }

func (t Upgrade) Run(ctx context.Context, e env.Environment, in Input, cl *logging.CaseLogger) Result {
	from, ri := in.From, in.Installer
	if from == nil || ri == nil {
		cl.Step("upgrade", "", -1, false, "", "upgrade case started without an installer pair (internal error)")
		return Result{Failed: 1}
	}

	// setup: copy + install the OLD version (unchecked, like uninstall's setup)
	oldCopy := fmt.Sprintf("%s -> %s", from.LocalPath, from.ContainerPath)
	if err := e.Copy(ctx, from.LocalPath, from.ContainerPath); err != nil {
		cl.Step("copy", oldCopy, -1, false, "", err.Error())
		return Result{Failed: 1}
	}
	cl.Step("copy", oldCopy, 0, true, "", "")
	if from.InstallCmd == "" {
		cl.Step("setup-install", "", -1, false, "",
			fmt.Sprintf("no install command for %s: set install_command in the config", from.Basename))
		return Result{Failed: 1}
	}
	setupCmd := from.Expand(from.InstallCmd)
	stdout, stderr, code, err := e.Exec(ctx, e.Shell().Command(setupCmd))
	cl.Step("setup-install", setupCmd, code, err == nil && code == 0, stdout, stderr)
	if err != nil || code != 0 {
		return Result{Failed: 1} // nothing to upgrade from
	}
	passed := 1 // setup-install step

	// copy + upgrade NEW version to the target
	newCopy := fmt.Sprintf("%s -> %s", ri.LocalPath, ri.ContainerPath)
	if err := e.Copy(ctx, ri.LocalPath, ri.ContainerPath); err != nil {
		cl.Step("copy", newCopy, -1, false, "", err.Error())
		return Result{Passed: passed, Failed: 1}
	}
	cl.Step("copy", newCopy, 0, true, "", "")
	if ri.UpgradeCmd == "" {
		cl.Step("upgrade", "", -1, false, "",
			fmt.Sprintf("no upgrade command for %s: set upgrade_command in the config", ri.Basename))
		return Result{Passed: passed, Failed: 1}
	}
	upCmd := ri.Expand(ri.UpgradeCmd)
	out2, err2out, code2, err2 := e.Exec(ctx, e.Shell().Command(upCmd))
	cl.Step("upgrade", upCmd, code2, err2 == nil && code2 == 0, out2, err2out)
	if err2 != nil || code2 != 0 {
		return Result{Passed: passed, Failed: 1}
	}
	passed++ // upgrade step

	p, f := checks.Run(ctx, e, ri, ri.UpgradeChecks, cl)
	return Result{Passed: passed + p, Failed: f}
}
