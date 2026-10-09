// Package report renders run and list output on the console.
package report

import (
	"fmt"
	"strings"

	"github.com/zukigit/installer-testing-auto/internal/checks"
	"github.com/zukigit/installer-testing-auto/internal/config"
)

const (
	markPass = "PASS"
	markFail = "FAIL"
)

// BundleError reports a config that could not be loaded/resolved.
func BundleError(path string, err error) {
	fmt.Printf("ERROR   config %s: %v\n", path, err)
}

// EnvHeader announces the environment being tested.
func EnvHeader(envName, image string, installers int) {
	fmt.Printf("== env %s (image: %s, installers: %d)\n", envName, image, installers)
}

// EnvStartError reports a failed environment startup.
func EnvStartError(envName string, err error) {
	fmt.Printf("ERROR   env %s: %v\n", envName, err)
}

// Check prints one check result as it happens.
func Check(r checks.Result) {
	mark := markPass
	line := fmt.Sprintf("%-8s [%s] %s: %s", mark, r.Installer, r.Kind, r.Name)
	if !r.Pass {
		mark = markFail
		line = fmt.Sprintf("%-8s [%s] %s: %s", mark, r.Installer, r.Kind, r.Name)
		if r.Detail != "" {
			line += " — " + r.Detail
		}
	}
	fmt.Println(line)
	if !r.Pass && r.Output != "" {
		for _, l := range strings.Split(strings.TrimRight(r.Output, "\n"), "\n") {
			fmt.Printf("         | %s\n", l)
		}
	}
}

// TaskSummary prints the per-task totals.
func TaskSummary(task string, passed, failed int) {
	fmt.Printf("== task %s: %d passed, %d failed\n", task, passed, failed)
}

// Overall prints the final line and reports whether the run failed.
func Overall(failed int) {
	if failed == 0 {
		fmt.Println("ALL CHECKS PASSED")
		return
	}
	fmt.Printf("%d CHECK(S) FAILED\n", failed)
}

// ListBundle prints the resolved bundle for the `list` subcommand.
func ListBundle(b *config.Bundle) {
	env := b.Config.Environment
	fmt.Printf("env %s\n", b.EnvName())
	fmt.Printf("  config:   %s\n", b.Path)
	fmt.Printf("  type:     %s\n", env.Type)
	fmt.Printf("  image:    %s\n", env.Image)
	fmt.Printf("  workdir:  %s\n", env.Workdir)
	if len(b.Installers) == 0 {
		fmt.Println("  installers: (none)")
		return
	}
	for _, ri := range b.Installers {
		fmt.Printf("  installer %s\n", ri.LocalPath)
		fmt.Printf("    in-container: %s\n", ri.ContainerPath)
		fmt.Printf("    install:      %s\n", ri.Expand(ri.InstallCmd))
		fmt.Printf("    uninstall:    %s\n", ri.Expand(ri.UninstallCmd))
		if c := ri.InstallChecks; c != nil {
			fmt.Printf("    install_checks:  must_exist=%d must_not_exist=%d commands=%d\n",
				len(c.MustExist), len(c.MustNotExist), len(c.Commands))
		}
		if c := ri.UninstallChecks; c != nil {
			fmt.Printf("    uninstall_checks: must_exist=%d must_not_exist=%d commands=%d\n",
				len(c.MustExist), len(c.MustNotExist), len(c.Commands))
		}
	}
}
