// Package report has two jobs: `list` dry-run rendering, and building a text
// report from captured NDJSON run logs (see `report --from`).
package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/zukigit/installer-testing-auto/internal/config"
)

// ---------- list (dry-run) rendering ----------

// ListError reports a config that could not be loaded/resolved.
func ListError(path string, err error) {
	fmt.Printf("ERROR   config %s: %v\n", path, err)
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

// ---------- NDJSON log parsing ----------

// Event is one NDJSON log line.
type Event struct {
	TS        string         `json:"ts"`
	Level     string         `json:"level"`
	Event     string         `json:"event"`
	Env       string         `json:"env"`
	Task      string         `json:"task"`
	Installer string         `json:"installer"`
	Data      map[string]any `json:"data"`
}

func (ev *Event) str(key string) string {
	v, _ := ev.Data[key].(string)
	return v
}

func (ev *Event) num(key string) (int, bool) {
	f, ok := ev.Data[key].(float64)
	return int(f), ok
}

func (ev *Event) flag(key string) bool {
	v, _ := ev.Data[key].(bool)
	return v
}

type stepRec struct {
	kind, command  string
	exitCode       int
	pass           bool
	stdout, stderr string
}

type checkRec struct {
	kind           string // "file" or "command"
	path           string // file checks
	expectation    string // file checks: must_exist / must_not_exist
	command        string // command checks (placeholders expanded)
	pass           bool
	exitCode       int
	expected       int
	stdoutContains string
	detail         string
	stdout, stderr string
}

type caseRec struct {
	env, task, installer string
	containerID          string
	containerName        string
	steps                []stepRec
	checks               []checkRec
	ended                bool
	pass                 bool
	passed, failed       int
	durationMs           float64
	kept                 bool
}

type envRec struct {
	name    string
	image   string
	workdir string
	cases   []*caseRec
	sawEnd  bool
	passed  int
	failed  int
}

type errorRec struct {
	env, task, installer, message string
}

// Run aggregates parsed NDJSON log events for report rendering.
type Run struct {
	sawRunStart  bool
	configs      int
	casesPlanned int
	parallel     int
	sawRunEnd    bool
	runPassed    int
	runFailed    int

	envs     []*envRec
	envByKey map[string]*envRec

	allCases  []*caseRec
	caseByKey map[string]*caseRec

	errors []errorRec
}

// Parse reads NDJSON log events, tolerating incomplete logs.
func Parse(r io.Reader) (*Run, error) {
	run := &Run{
		envByKey:  map[string]*envRec{},
		caseByKey: map[string]*caseRec{},
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		run.apply(&ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return run, nil
}

func (r *Run) env(name string) *envRec {
	if e, ok := r.envByKey[name]; ok {
		return e
	}
	e := &envRec{name: name}
	r.envByKey[name] = e
	r.envs = append(r.envs, e)
	return e
}

func (r *Run) caseCtx(ev *Event) *caseRec {
	key := ev.Env + "\x00" + ev.Task + "\x00" + ev.Installer
	if c, ok := r.caseByKey[key]; ok {
		return c
	}
	c := &caseRec{env: ev.Env, task: ev.Task, installer: ev.Installer}
	r.caseByKey[key] = c
	r.allCases = append(r.allCases, c)
	e := r.env(ev.Env)
	e.cases = append(e.cases, c)
	return c
}

func (r *Run) apply(ev *Event) {
	switch ev.Event {
	case "run_start":
		r.sawRunStart = true
		r.configs, _ = ev.num("configs")
		r.casesPlanned, _ = ev.num("cases")
		r.parallel, _ = ev.num("parallel")
	case "env_start":
		e := r.env(ev.Env)
		e.image = ev.str("image")
		e.workdir = ev.str("workdir")
	case "env_end":
		e := r.env(ev.Env)
		e.sawEnd = true
		e.passed, _ = ev.num("passed")
		e.failed, _ = ev.num("failed")
	case "case_start":
		c := r.caseCtx(ev)
		c.containerID = ev.str("container_id")
		c.containerName = ev.str("container_name")
	case "step":
		c := r.caseCtx(ev)
		code, _ := ev.num("exit_code")
		c.steps = append(c.steps, stepRec{
			kind: ev.str("kind"), command: ev.str("command"),
			exitCode: code, pass: ev.flag("pass"),
			stdout: ev.str("stdout"), stderr: ev.str("stderr"),
		})
	case "check":
		c := r.caseCtx(ev)
		exitCode, _ := ev.num("exit_code")
		expected, _ := ev.num("expected_exit_code")
		c.checks = append(c.checks, checkRec{
			kind:           ev.str("kind"),
			path:           ev.str("path"),
			expectation:    ev.str("expectation"),
			command:        ev.str("command"),
			pass:           ev.flag("pass"),
			exitCode:       exitCode,
			expected:       expected,
			stdoutContains: ev.str("expected_stdout_contains"),
			detail:         ev.str("detail"),
			stdout:         ev.str("stdout"), stderr: ev.str("stderr"),
		})
	case "case_end":
		c := r.caseCtx(ev)
		c.ended = true
		c.pass = ev.flag("pass")
		c.passed, _ = ev.num("passed")
		c.failed, _ = ev.num("failed")
		if ms, ok := ev.num("duration_ms"); ok {
			c.durationMs = float64(ms)
		}
		c.containerID = ev.str("container_id")
		c.kept = ev.flag("kept")
	case "run_end":
		r.sawRunEnd = true
		r.runPassed, _ = ev.num("passed")
		r.runFailed, _ = ev.num("failed")
	case "error":
		r.errors = append(r.errors, errorRec{
			env: ev.Env, task: ev.Task, installer: ev.Installer, message: ev.str("message"),
		})
	}
}
