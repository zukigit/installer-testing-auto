// Package task defines the Task abstraction. A task is one testing phase
// (e.g. fresh-install, uninstall) executed against an environment. Tasks are
// independent: each (installer file, task) pair is a case with its own
// container, and uninstall is valid standalone.
package task

import (
	"context"

	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/env"
	"github.com/zukigit/installer-testing-auto/internal/logging"
)

const (
	// FreshInstallName is the fresh-install task.
	FreshInstallName = "fresh-install"
	// UninstallName is the uninstall task.
	UninstallName = "uninstall"
	// UpgradeName is the upgrade task.
	UpgradeName = "upgrade"
)

// Result holds the check/step totals of one task run. The case passes when
// Failed == 0.
type Result struct {
	Passed int
	Failed int
}

// Input is one case's task input. Installer is the case's target installer
// file; From is the OLD installer for upgrade cases (pairing built by the
// runner), nil for all other tasks.
type Input struct {
	Installer *config.ResolvedInstaller
	From      *config.ResolvedInstaller
}

// Task is one testing phase executed against one installer in its own
// environment container. Tasks emit their steps and check results as NDJSON
// events through the CaseLogger and return the aggregated counts.
type Task interface {
	Name() string
	Run(ctx context.Context, e env.Environment, in Input, cl *logging.CaseLogger) Result
}

var registry = map[string]Task{}

func register(t Task) {
	registry[t.Name()] = t
}

// ByName looks a task up by name.
func ByName(name string) (Task, bool) {
	t, ok := registry[name]
	return t, ok
}

// All returns every registered task in canonical run order
// (fresh-install first, then uninstall, then upgrade).
func All() []Task {
	return []Task{
		FreshInstall{},
		Uninstall{},
		Upgrade{},
	}
}
