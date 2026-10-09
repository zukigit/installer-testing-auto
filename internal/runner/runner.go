// Package runner orchestrates a full test run: config discovery ->
// package-list resolution -> environment provisioning -> tasks -> reporting.
package runner

import (
	"context"
	"fmt"

	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/env"
	"github.com/zukigit/installer-testing-auto/internal/report"
	"github.com/zukigit/installer-testing-auto/internal/task"
)

// Options configures one `run` invocation.
type Options struct {
	// ConfigPattern is the config glob; "" discovers all configs/*.yaml.
	ConfigPattern string
	// Tasks is the requested task-name list; empty means all tasks.
	Tasks []string
	// Keep keeps the environment alive for debugging and prints its ID.
	Keep bool
}

// List resolves and prints every config without touching any environment.
func List(configPattern string) error {
	paths, err := config.Discover(configPattern)
	if err != nil {
		return err
	}
	for _, path := range paths {
		b, err := config.LoadBundle(path)
		if err != nil {
			report.BundleError(path, err)
			continue
		}
		report.ListBundle(b)
	}
	return nil
}

// Run executes the requested tasks on every discovered environment and
// returns the process exit code (0 = all checks passed).
func Run(ctx context.Context, opts Options) int {
	// Uninstall is never standalone: selecting it implies fresh-install first.
	install := true
	uninstall := true
	if len(opts.Tasks) == 1 && opts.Tasks[0] == task.FreshInstallName {
		uninstall = false
	}

	cfgPaths, err := config.Discover(opts.ConfigPattern)
	if err != nil {
		fmt.Println("ERROR  ", err)
		return 2
	}

	totalFailed := 0
	for _, path := range cfgPaths {
		totalFailed += runOneEnv(ctx, opts, path, install, uninstall)
	}

	report.Overall(totalFailed)
	if totalFailed > 0 {
		return 1
	}
	return 0
}

func runOneEnv(ctx context.Context, opts Options, path string, install, uninstall bool) int {
	b, err := config.LoadBundle(path)
	if err != nil {
		report.BundleError(path, err)
		return 1
	}
	spec := b.Config.Environment
	report.EnvHeader(b.EnvName(), spec.Image, len(b.Installers))

	e, err := env.New(spec.Type, b.EnvName(), spec.Image, spec.Workdir)
	if err != nil {
		report.EnvStartError(b.EnvName(), err)
		return 1
	}
	if err := e.Start(ctx); err != nil {
		report.EnvStartError(b.EnvName(), err)
		return 1
	}
	if opts.Keep {
		if ider, ok := e.(interface{ ContainerID() string }); ok {
			fmt.Printf("NOTE    --keep: container id %s (not torn down)\n", ider.ContainerID())
		}
	} else {
		defer func() {
			if err := e.Stop(context.Background()); err != nil {
				fmt.Printf("WARN    teardown of %s failed: %v\n", b.EnvName(), err)
			}
		}()
	}

	failed := 0
	if install {
		failed += runTask(ctx, e, task.FreshInstall{}, b)
	}
	if uninstall {
		failed += runTask(ctx, e, task.Uninstall{}, b)
	}
	return failed
}

func runTask(ctx context.Context, e env.Environment, t task.Task, b *config.Bundle) int {
	res := t.Run(ctx, e, b)
	for _, c := range res.Checks {
		report.Check(c)
	}
	passed, failed := res.Counts()
	report.TaskSummary(t.Name(), passed, failed)
	return failed
}
