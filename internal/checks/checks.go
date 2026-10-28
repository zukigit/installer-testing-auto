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
	"github.com/zukigit/installer-testing-auto/internal/shell"
)

// Run executes every check in cs against the environment and returns the
// passed/failed totals. cs may be nil (no checks configured for this phase).
// must_exist / must_not_exist entries containing any of * ? [ are glob
// patterns expanded by the environment's own shell (v3.2); exact entries keep
// the literal existence probe (test -e / Test-Path).
func Run(ctx context.Context, e env.Environment, ri *config.ResolvedInstaller, cs *config.Checks, cl *logging.CaseLogger) (passed, failed int) {
	if cs == nil {
		return 0, 0
	}
	for _, path := range cs.MustExist {
		if config.IsGlobPath(path) {
			matched, code, err := probeGlob(ctx, e, path)
			fields := map[string]any{
				"path":        path,
				"expectation": expectationMustExist,
				"glob":        true,
				"exit_code":   code,
			}
			if m := capMatches(matched, err == nil); m != nil {
				fields["matches"] = m
			}
			pass := err == nil && len(matched) > 0
			switch {
			case err != nil:
				fields["detail"] = err.Error()
			case !pass:
				fields["detail"] = "no files match pattern"
			}
			cl.Check("file", "", pass, fields)
			if pass {
				passed++
			} else {
				failed++
			}
			continue
		}
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
		if config.IsGlobPath(path) {
			matched, code, err := probeGlob(ctx, e, path)
			fields := map[string]any{
				"path":        path,
				"expectation": expectationMustNotExist,
				"glob":        true,
				"exit_code":   code,
			}
			if m := capMatches(matched, err == nil); m != nil {
				fields["matches"] = m
			}
			pass := err == nil && len(matched) == 0
			switch {
			case err != nil:
				fields["detail"] = err.Error()
			case !pass:
				fields["detail"] = fmt.Sprintf("%d file(s) still match pattern: %s", len(matched), strings.Join(capMatches(matched, true), ", "))
			}
			cl.Check("file", "", pass, fields)
			if pass {
				passed++
			} else {
				failed++
			}
			continue
		}
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
		stdout, stderr, code, err := e.Exec(ctx, e.Shell().Command(cmd))
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

// maxMatchedNames caps the matched-names list in check events; beyond it the
// list ends with "... (N more)".
const maxMatchedNames = 20

// capMatches returns the matched names for the event, capped at
// maxMatchedNames with a "... (N more)" tail entry. On exec error matched is
// meaningless, so the list is dropped (nil).
func capMatches(matched []string, valid bool) []string {
	if !valid || len(matched) == 0 {
		return nil
	}
	if len(matched) <= maxMatchedNames {
		return matched
	}
	out := append([]string{}, matched[:maxMatchedNames]...)
	return append(out, fmt.Sprintf("... (%d more)", len(matched)-maxMatchedNames))
}

func withStdout(fields map[string]any, output string) map[string]any {
	if out := strings.TrimSpace(output); out != "" {
		fields["stdout"] = out
	}
	return fields
}

// probeFile reports whether path exists inside the environment with the
// dialect's existence probe (`test -e` on POSIX, `Test-Path` on PowerShell).
func probeFile(ctx context.Context, e env.Environment, path string) (exists bool, code int, output string, err error) {
	sh := e.Shell()
	probe := probeName(sh, "test -e", "Test-Path")
	stdout, _, code, err := e.Exec(ctx, sh.Command(sh.PathExists(path)))
	if err != nil {
		return false, -1, stdout, fmt.Errorf("exec %s: %w", probe, err)
	}
	switch code {
	case 0:
		return true, 0, stdout, nil
	case 1:
		return false, 1, stdout, nil
	default:
		return false, code, stdout, fmt.Errorf("unexpected exit code %d from %s", code, probe)
	}
}

// probeName picks the probe name used in error details per dialect.
func probeName(sh shell.Dialect, posix, powershell string) string {
	if sh.Name() == shell.PowerShell.Name() {
		return powershell
	}
	return posix
}

// probeGlob expands a glob check path with the environment's shell. POSIX:
// the pattern is passed unquoted so /bin/sh expands it, and `ls -d` lists the
// matches (files and directories, non-recursively). PowerShell: the pattern
// is passed quoted and Get-Item expands it. PASS is determined by the number
// of matched names on stdout — NOT by the exit code, which conflates
// "no match" (pattern passed through literally, ls exits 2) with real errors
// such as unreadable directories.
func probeGlob(ctx context.Context, e env.Environment, pattern string) (matched []string, code int, err error) {
	sh := e.Shell()
	stdout, _, code, err := e.Exec(ctx, sh.Command(sh.Glob(pattern)))
	if err != nil {
		return nil, -1, fmt.Errorf("exec %s: %w", probeName(sh, "ls -d", "Get-Item"), err)
	}
	if s := strings.TrimSpace(stdout); s != "" {
		matched = strings.Split(s, "\n")
	}
	return matched, code, nil
}
