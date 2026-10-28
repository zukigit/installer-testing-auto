// Tests for wildcard file checks (v3.2): glob entries probe with `ls -d --`
// via the environment's shell; exact entries keep the literal `test -e`.
package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/logging"
	"github.com/zukigit/installer-testing-auto/internal/shell"
)

// execEnv returns one scripted exec result per call and records the argv of
// each Exec, so tests can assert the exact probe command.
type execEnv struct {
	results []execResult
	args    [][]string
	dialect shell.Dialect // nil = POSIX
}

type execResult struct {
	stdout string
	stderr string
	code   int
	err    error
}

func (e *execEnv) Name() string { return "exec" }
func (e *execEnv) Shell() shell.Dialect {
	if e.dialect == nil {
		return shell.POSIX
	}
	return e.dialect
}
func (e *execEnv) Start(ctx context.Context) error {
	return nil
}
func (e *execEnv) Stop(ctx context.Context) error { return nil }
func (e *execEnv) Copy(ctx context.Context, src, dest string) error {
	return nil
}
func (e *execEnv) Exec(ctx context.Context, cmd []string) (string, string, int, error) {
	e.args = append(e.args, append([]string{}, cmd...))
	if len(e.results) == 0 {
		return "", "", 0, nil
	}
	r := e.results[0]
	e.results = e.results[1:]
	return r.stdout, r.stderr, r.code, r.err
}

func testChecks() *config.Checks {
	return &config.Checks{}
}

// lastCheckEvent parses the trailing check event from the captured log.
func lastCheckEvent(t *testing.T, buf string) map[string]any {
	t.Helper()
	var last map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf), "\n") {
		if line == "" {
			continue
		}
		var ev struct {
			Event string         `json:"event"`
			Data  map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal log line %q: %v", line, err)
		}
		if ev.Event == "check" {
			last = ev.Data
		}
	}
	if last == nil {
		t.Fatalf("no check event in log:\n%s", buf)
	}
	return last
}

func runChecks(t *testing.T, e *execEnv, cs *config.Checks) (passed, failed int, buf string) {
	t.Helper()
	logBuf := &strings.Builder{}
	cl := logging.New(logBuf).Case("env", "task", "inst", "")
	passed, failed = Run(context.Background(), e, &config.ResolvedInstaller{}, cs, cl)
	return passed, failed, logBuf.String()
}

// runOne executes Run with a single must_expect list entry.
func runOne(t *testing.T, e *execEnv, field string, paths []string) (passed, failed int, buf string) {
	cs := testChecks()
	if field == "must_exist" {
		cs.MustExist = paths
	} else {
		cs.MustNotExist = paths
	}
	return runChecks(t, e, cs)
}

// --- must_exist glob ---

func TestMustExistGlobPass(t *testing.T) {
	e := &execEnv{results: []execResult{{stdout: "/usr/share/man/man8/jobarg_server.8.gz\n"}}}
	p, f, buf := runOne(t, e, "must_exist", []string{"/usr/share/man/man8/jobarg_server.8.*"})
	if p != 1 || f != 0 {
		t.Errorf("glob must_exist with one match should pass, got p=%d f=%d", p, f)
	}
	// the probe: shell-expandable `ls -d -- <pattern>`, unquoted
	want := []string{"/bin/sh", "-c", "ls -d -- /usr/share/man/man8/jobarg_server.8.*"}
	if !reflect.DeepEqual(e.args[0], want) {
		t.Errorf("glob probe = %v, want %v", e.args[0], want)
	}
	ev := lastCheckEvent(t, buf)
	if ev["glob"] != true {
		t.Errorf("check event should mark glob=true, got %v", ev["glob"])
	}
	if got := ev["path"]; got != "/usr/share/man/man8/jobarg_server.8.*" {
		t.Errorf("check event path = %v", got)
	}
	if matches, ok := ev["matches"].([]any); !ok || len(matches) != 1 || matches[0] != "/usr/share/man/man8/jobarg_server.8.gz" {
		t.Errorf("check event matches = %v (want the matched name)", ev["matches"])
	}
}

func TestMustExistGlobFail(t *testing.T) {
	// no match: POSIX sh passes the pattern through, ls exits 2, stdout empty
	e := &execEnv{results: []execResult{{code: 2}}}
	p, f, buf := runOne(t, e, "must_exist", []string{"/usr/share/man/man8/jobarg_server.8.*"})
	if p != 0 || f != 1 {
		t.Errorf("glob must_exist without match should fail, got p=%d f=%d", p, f)
	}
	ev := lastCheckEvent(t, buf)
	if ev["detail"] != "no files match pattern" {
		t.Errorf("detail = %v, want no-files-match message", ev["detail"])
	}
	if _, ok := ev["matches"]; ok {
		t.Errorf("no match: matches should be omitted, got %v", ev["matches"])
	}
}

// --- must_not_exist glob ---

func TestMustNotExistGlobPassAndFail(t *testing.T) {
	// PASS: nothing matches (stdout empty)
	e := &execEnv{results: []execResult{{code: 2}}}
	p, f, _ := runOne(t, e, "must_not_exist", []string{"/etc/x*"})
	if p != 1 || f != 0 {
		t.Errorf("must_not_exist glob with zero matches should pass, got p=%d f=%d", p, f)
	}

	// FAIL: files (and directories) still match
	e = &execEnv{results: []execResult{{stdout: "/etc/x\n/etc/x.d\n"}}}
	p, f, buf := runOne(t, e, "must_not_exist", []string{"/etc/x*"})
	if p != 0 || f != 1 {
		t.Errorf("must_not_exist glob with matches should fail, got p=%d f=%d", p, f)
	}
	ev := lastCheckEvent(t, buf)
	want := "2 file(s) still match pattern: /etc/x, /etc/x.d"
	if ev["detail"] != want {
		t.Errorf("detail = %v, want %q", ev["detail"], want)
	}
	if matches, ok := ev["matches"].([]any); !ok || len(matches) != 2 {
		t.Errorf("failing must_not_exist should carry the offenders, got %v", ev["matches"])
	}
}

// More names than the cap: the list is truncated with "... (N more)".
func TestGlobMatchesCap(t *testing.T) {
	names := make([]string, 22)
	for i := range names {
		names[i] = fmt.Sprintf("/etc/x%d", i)
	}
	e := &execEnv{results: []execResult{{stdout: strings.Join(names, "\n") + "\n"}}}
	_, _, buf := runOne(t, e, "must_not_exist", []string{"/etc/x*"})
	ev := lastCheckEvent(t, buf)
	matches, ok := ev["matches"].([]any)
	if !ok || len(matches) != 21 || matches[20] != "... (2 more)" {
		t.Errorf("matches should cap at 20 + count tail, got %v", ev["matches"])
	}
}

// exec failure is a failing check with the error as detail (as for exact
// paths).
func TestGlobExecError(t *testing.T) {
	e := &execEnv{results: []execResult{{err: fmt.Errorf("connection lost"), code: -1}}}
	_, f, buf := runOne(t, e, "must_exist", []string{"/opt/app*"})
	if f != 1 {
		t.Fatalf("exec error must fail the check, got failed=%d", f)
	}
	ev := lastCheckEvent(t, buf)
	if !strings.Contains(ev["detail"].(string), "connection lost") {
		t.Errorf("detail should carry the exec error, got %v", ev["detail"])
	}
	if _, ok := ev["matches"]; ok {
		t.Errorf("exec error: matches should be omitted, got %v", ev["matches"])
	}
}

// --- exact paths: behavior unchanged, probe still quoted `test -e` ---

func TestExactPathsUnchanged(t *testing.T) {
	e := &execEnv{results: []execResult{{code: 0}}}
	p, f, _ := runOne(t, e, "must_exist", []string{"/usr/bin/app"})
	if p != 1 || f != 0 {
		t.Errorf("exact must_exist with test -e exit 0 should pass")
	}
	want := []string{"/bin/sh", "-c", "test -e '/usr/bin/app'"}
	if !reflect.DeepEqual(e.args[0], want) {
		t.Errorf("exact probe = %v, want %v", e.args[0], want)
	}

	e = &execEnv{results: []execResult{{code: 1}}}
	p, f, buf := runOne(t, e, "must_exist", []string{"/usr/bin/app"})
	if p != 0 || f != 1 {
		t.Errorf("exact must_exist with test -e exit 1 should fail")
	}
	ev := lastCheckEvent(t, buf)
	if ev["detail"] != "file does not exist" {
		t.Errorf("detail = %v, want file does not exist", ev["detail"])
	}
	if _, ok := ev["glob"]; ok {
		t.Errorf("exact check event must not carry the glob field: %v", ev["glob"])
	}

	e = &execEnv{results: []execResult{{code: 0}}}
	p, f, _ = runOne(t, e, "must_not_exist", []string{"/usr/bin/app"})
	if p != 0 || f != 1 {
		t.Errorf("exact must_not_exist with test -e exit 0 should fail")
	}
}

// A leftover {VAR} in a check path is not substituted: it is literal text and
// behaves like any exact path (variables are pattern-only, v3.2 §12).
func TestLeftoverVarInPathIsLiteral(t *testing.T) {
	e := &execEnv{results: []execResult{{code: 1}}}
	p, f, buf := runChecks(t, e, &config.Checks{
		MustExist: []string{"/opt/pkg-{VERSION}/database"},
	})
	if p != 0 || f != 1 {
		t.Fatalf("literal {VAR} path that does not exist should fail, got p=%d f=%d", p, f)
	}
	want := []string{"/bin/sh", "-c", "test -e '/opt/pkg-{VERSION}/database'"}
	if !reflect.DeepEqual(e.args[0], want) {
		t.Errorf("probe = %v, want %v (no substitution happened)", e.args[0], want)
	}
	ev := lastCheckEvent(t, buf)
	if ev["detail"] != "file does not exist" {
		t.Errorf("detail = %v, want file does not exist", ev["detail"])
	}
}

// sanity: the checks package stays silent even for the nil-checks call used
// by tasks without checks.
func TestRunNilChecks(t *testing.T) {
	p, f, buf := runChecks(t, &execEnv{}, nil)
	if p != 0 || f != 0 {
		t.Errorf("nil checks should be a no-op, got p=%d f=%d", p, f)
	}
	if buf != "" {
		t.Errorf("nil checks should emit nothing, got %q", buf)
	}
}
