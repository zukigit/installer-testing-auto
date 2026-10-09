// Package task defines the Task abstraction. A task is one testing phase
// (e.g. fresh-install, uninstall) executed against an environment.
package task

import (
	"context"

	"github.com/zukigit/installer-testing-auto/internal/checks"
	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/env"
)

const (
	// FreshInstallName is the fresh-install task.
	FreshInstallName = "fresh-install"
	// UninstallName is the uninstall task.
	UninstallName = "uninstall"
)

// Result aggregates the check results of one task run.
type Result struct {
	Task   string
	Checks []checks.Result
}

// Failed reports whether any check failed.
func (r Result) Failed() bool {
	for _, c := range r.Checks {
		if !c.Pass {
			return true
		}
	}
	return false
}

// Failures returns only the failed check results.
func (r Result) Failures() []checks.Result {
	var out []checks.Result
	for _, c := range r.Checks {
		if !c.Pass {
			out = append(out, c)
		}
	}
	return out
}

// Counts returns the number of passed and failed checks.
func (r Result) Counts() (passed, failed int) {
	for _, c := range r.Checks {
		if c.Pass {
			passed++
		} else {
			failed++
		}
	}
	return passed, failed
}

// Task is one testing phase executed against an environment. Uninstall is
// never run standalone: the runner always executes fresh-install first.
type Task interface {
	Name() string
	Run(ctx context.Context, e env.Environment, b *config.Bundle) Result
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
// (fresh-install first, uninstall second).
func All() []Task {
	return []Task{
		FreshInstall{},
		Uninstall{},
	}
}
