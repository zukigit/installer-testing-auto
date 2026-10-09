// Package checks runs the per-installer verifications: file presence,
// file absence and shell commands. All check types are optional and are
// driven entirely by the config's check blocks.
package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/env"
)

// Result is one executed check.
type Result struct {
	Zone      string // task/phase, e.g. "fresh-install", "uninstall"
	Installer string // installer basename the check belongs to
	Kind      string // "copy" | "file" | "command" | "install" | "uninstall"
	Name      string // human-readable check name
	Pass      bool
	Output    string // captured command output (truncated)
	Detail    string // failure description
}

func pass(zone, installer, kind, name string) Result {
	return Result{Zone: zone, Installer: installer, Kind: kind, Name: name, Pass: true}
}

func fail(zone, installer, kind, name, detail, output string) Result {
	return Result{Zone: zone, Installer: installer, Kind: kind, Name: name, Pass: false, Detail: detail, Output: output}
}

// Run executes every check in cs against the environment. cs may be nil
// (no checks configured for this phase).
func Run(ctx context.Context, e env.Environment, zone string, ri *config.ResolvedInstaller, cs *config.Checks) []Result {
	if cs == nil {
		return nil
	}
	var out []Result
	for _, path := range cs.MustExist {
		out = append(out, fileExists(ctx, e, zone, ri, path))
	}
	for _, path := range cs.MustNotExist {
		out = append(out, fileAbsent(ctx, e, zone, ri, path))
	}
	for _, c := range cs.Commands {
		out = append(out, command(ctx, e, zone, ri, c))
	}
	return out
}

func fileExists(ctx context.Context, e env.Environment, zone string, ri *config.ResolvedInstaller, path string) Result {
	name := fmt.Sprintf("must_exist: %s", path)
	exists, output, err := probeFile(ctx, e, path)
	switch {
	case err != nil:
		return fail(zone, ri.Basename, "file", name, err.Error(), output)
	case !exists:
		return fail(zone, ri.Basename, "file", name, "file does not exist", output)
	default:
		return pass(zone, ri.Basename, "file", name)
	}
}

func fileAbsent(ctx context.Context, e env.Environment, zone string, ri *config.ResolvedInstaller, path string) Result {
	name := fmt.Sprintf("must_not_exist: %s", path)
	exists, output, err := probeFile(ctx, e, path)
	switch {
	case err != nil:
		return fail(zone, ri.Basename, "file", name, err.Error(), output)
	case exists:
		return fail(zone, ri.Basename, "file", name, "file still exists", output)
	default:
		return pass(zone, ri.Basename, "file", name)
	}
}

// probeFile reports whether path exists inside the environment by running
// `test -e` through the shell.
func probeFile(ctx context.Context, e env.Environment, path string) (exists bool, output string, err error) {
	stdout, _, code, err := e.Exec(ctx, []string{"/bin/sh", "-c", "test -e " + quote(path)})
	if err != nil {
		return false, stdout, fmt.Errorf("exec test -e: %w", err)
	}
	switch code {
	case 0:
		return true, stdout, nil
	case 1:
		return false, stdout, nil
	default:
		return false, stdout, fmt.Errorf("unexpected exit code %d from test -e", code)
	}
}

func command(ctx context.Context, e env.Environment, zone string, ri *config.ResolvedInstaller, c config.CommandCheck) Result {
	name := c.Command
	stdout, stderr, code, err := e.Exec(ctx, []string{"/bin/sh", "-c", ri.Expand(c.Command)})
	if err != nil {
		return fail(zone, ri.Basename, "command", name, err.Error(), combine(stdout, stderr))
	}
	expected := 0
	if c.ExpectedExitCode != nil {
		expected = *c.ExpectedExitCode
	}
	if code != expected {
		return fail(zone, ri.Basename, "command", name,
			fmt.Sprintf("expected exit code %d, got %d", expected, code), combine(stdout, stderr))
	}
	if c.ExpectedStdoutContains != "" && !strings.Contains(stdout, c.ExpectedStdoutContains) {
		return fail(zone, ri.Basename, "command", name,
			fmt.Sprintf("stdout does not contain %q", c.ExpectedStdoutContains), combine(stdout, stderr))
	}
	return pass(zone, ri.Basename, "command", name)
}

func combine(stdout, stderr string) string {
	out := strings.TrimSpace(stdout + stderr)
	const max = 2000
	if len(out) > max {
		out = out[:max] + "..."
	}
	return out
}

func quote(s string) string {
	return "'" + s + "'"
}
