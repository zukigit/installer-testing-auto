// Package checks runs the per-installer verifications: file presence,
// file absence and shell commands. All check types are optional and are
// driven entirely by the config's check blocks. Results are emitted as NDJSON
// events; the runner only receives aggregated counts.
package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/env"
	"github.com/zukigit/installer-testing-auto/internal/logging"
)

// Run executes every check in cs against the environment and returns the
// passed/failed totals. cs may be nil (no checks configured for this phase).
func Run(ctx context.Context, e env.Environment, ri *config.ResolvedInstaller, cs *config.Checks, cl *logging.CaseLogger) (passed, failed int) {
	if cs == nil {
		return 0, 0
	}
	for _, path := range cs.MustExist {
		exists, code, output, err := probeFile(ctx, e, path)
		fields := map[string]any{
			"path":        path,
			"expectation": expectationMustExist,
			"exit_code":   code,
		}
		switch {
		case err != nil:
			fields["detail"] = err.Error()
		case !exists:
			fields["detail"] = "file does not exist"
		}
		pass := err == nil && exists
		cl.Check("file", "", pass, withStdout(fields, output))
		if pass {
			passed++
		} else {
			failed++
		}
	}
	for _, path := range cs.MustNotExist {
		exists, code, output, err := probeFile(ctx, e, path)
		fields := map[string]any{
			"path":        path,
			"expectation": expectationMustNotExist,
			"exit_code":   code,
		}
		switch {
		case err != nil:
			fields["detail"] = err.Error()
		case exists:
			fields["detail"] = "file still exists"
		}
		pass := err == nil && !exists
		cl.Check("file", "", pass, withStdout(fields, output))
		if pass {
			passed++
		} else {
			failed++
		}
	}
	for _, c := range cs.Commands {
		cmd := ri.Expand(c.Command)
		stdout, stderr, code, err := e.Exec(ctx, []string{"/bin/sh", "-c", cmd})
		expected := 0
		if c.ExpectedExitCode != nil {
			expected = *c.ExpectedExitCode
		}
		pass := err == nil && code == expected
		fields := map[string]any{
			"command":            cmd,
			"exit_code":          code,
			"expected_exit_code": expected,
			"stdout":             stdout,
			"stderr":             stderr,
		}
		switch {
		case err != nil:
			fields["detail"] = err.Error()
			pass = false
		case code != expected:
			fields["detail"] = fmt.Sprintf("expected exit code %d, got %d", expected, code)
			pass = false
		case c.ExpectedStdoutContains != "" && !strings.Contains(stdout, c.ExpectedStdoutContains):
			fields["detail"] = fmt.Sprintf("stdout does not contain %q", c.ExpectedStdoutContains)
			pass = false
		}
		if c.ExpectedStdoutContains != "" {
			fields["expected_stdout_contains"] = c.ExpectedStdoutContains
		}
		cl.Check("command", "", pass, fields)
		if pass {
			passed++
		} else {
			failed++
		}
	}
	return passed, failed
}

// Expectation values for file checks (event `expectation` field).
const (
	expectationMustExist    = "must_exist"
	expectationMustNotExist = "must_not_exist"
)

func withStdout(fields map[string]any, output string) map[string]any {
	if out := strings.TrimSpace(output); out != "" {
		fields["stdout"] = out
	}
	return fields
}

// probeFile reports whether path exists inside the environment by running
// `test -e` through the shell.
func probeFile(ctx context.Context, e env.Environment, path string) (exists bool, code int, output string, err error) {
	stdout, _, code, err := e.Exec(ctx, []string{"/bin/sh", "-c", "test -e " + quote(path)})
	if err != nil {
		return false, -1, stdout, fmt.Errorf("exec test -e: %w", err)
	}
	switch code {
	case 0:
		return true, 0, stdout, nil
	case 1:
		return false, 1, stdout, nil
	default:
		return false, code, stdout, fmt.Errorf("unexpected exit code %d from test -e", code)
	}
}

func quote(s string) string {
	return "'" + s + "'"
}
