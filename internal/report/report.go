// Package report has two jobs: `list` dry-run rendering, and building a text
// report from captured NDJSON run logs (see `report --from`).
package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"

	"github.com/zukigit/installer-testing-auto/internal/config"
)

// ---------- list (dry-run) rendering ----------

// ListError reports a config that could not be loaded/resolved.
func ListError(path string, err error) {
	fmt.Printf("ERROR   config %s: %v\n", path, err)
}

// ListSkippedWindows reports an os: windows config left out of the default
// config set (no --config given).
func ListSkippedWindows(path string) {
	fmt.Printf("SKIPPED config %s: os windows is not in the default set (use --config)\n", path)
}

// ListBundle prints the resolved bundle for the `list` subcommand.
func ListBundle(b *config.Bundle) {
	env := b.Config.Environment
	fmt.Printf("env %s\n", b.EnvName())
	fmt.Printf("  config:   %s\n", b.Path)
	fmt.Printf("  type:     %s\n", env.Type)
	fmt.Printf("  os:       %s\n", osOrDefault(env.OS))
	if env.Isolation != "" {
		fmt.Printf("  isolation: %s\n", env.Isolation)
	}
	fmt.Printf("  image:    %s\n", env.Image)
	fmt.Printf("  workdir:  %s\n", env.Workdir)
	if len(b.Installers) == 0 {
		fmt.Println("  installers: (none)")
		return
	}
	for _, ri := range b.Installers {
		fmt.Printf("  installer %s\n", ri.LocalPath)
		fmt.Printf("    in-container: %s\n", ri.ContainerPath)
		fmt.Printf("    install:      %s\n", indentCont(ri.Expand(ri.InstallCmd)))
		fmt.Printf("    uninstall:    %s\n", indentCont(ri.Expand(ri.UninstallCmd)))
		fmt.Printf("    upgrade:      %s\n", indentCont(ri.Expand(ri.UpgradeCmd)))
		for _, line := range pairingLines(ri, env.IsWindows()) {
			fmt.Printf("    pairing:      %s\n", line)
		}
		if c := ri.InstallChecks; c != nil {
			fmt.Printf("    install_checks:  must_exist=%d must_not_exist=%d commands=%d\n",
				len(c.MustExist), len(c.MustNotExist), len(c.Commands))
		}
		if c := ri.UninstallChecks; c != nil {
			fmt.Printf("    uninstall_checks: must_exist=%d must_not_exist=%d commands=%d\n",
				len(c.MustExist), len(c.MustNotExist), len(c.Commands))
		}
		if c := ri.UpgradeChecks; c != nil {
			fmt.Printf("    upgrade_checks:   must_exist=%d must_not_exist=%d commands=%d\n",
				len(c.MustExist), len(c.MustNotExist), len(c.Commands))
		}
	}
}

func osOrDefault(os string) string {
	if os == "" {
		return config.OSLinux
	}
	return os
}

// indentCont aligns the continuation lines of multi-line commands (e.g. the
// .msi defaults) under the first line.
func indentCont(s string) string {
	return strings.ReplaceAll(s, "\n", "\n"+strings.Repeat(" ", len("    install:      ")))
}

// pairingText is the upgrade-pairing detail of one installer: the parsed
// version and identity, or the skip reason the upgrade task would give it
// (no_older_version depends on the pool and is not predicted here).
// pairingLines is pairingText, or for upgrade_from entries one line per
// from installer (or the skip reason).
func pairingLines(ri *config.ResolvedInstaller, windows bool) []string {
	if !ri.UpgradeFromSet {
		return []string{pairingText(ri)}
	}
	if len(ri.UpgradeFrom) == 0 {
		return []string{"skipped by upgrade (no_upgrade_from_match)"}
	}
	froms := ri.UpgradeFromPairs(windows)
	if len(froms) == 0 {
		return []string{"skipped by upgrade (is_upgrade_from)"}
	}
	var lines []string
	for _, f := range froms {
		lines = append(lines, "upgrade from "+f.LocalPath)
	}
	return lines
}

func pairingText(ri *config.ResolvedInstaller) string {
	switch {
	case ri.NoVersionFormat:
		return "skipped by upgrade (no_version_format)"
	case ri.Version == "":
		return "skipped by upgrade (nonstandard_version)"
	default:
		return fmt.Sprintf("version=%s package=%s", ri.Version, ri.UpgradeIdentity())
	}
}

// ---------- NDJSON log parsing ----------

// Event is one NDJSON log line.
type Event struct {
	TS            string         `json:"ts"`
	Level         string         `json:"level"`
	Event         string         `json:"event"`
	Env           string         `json:"env"`
	Task          string         `json:"task"`
	Installer     string         `json:"installer"`
	FromInstaller string         `json:"from_installer"`
	Data          map[string]any `json:"data"`
}

func (ev *Event) str(key string) string {
	v, _ := ev.Data[key].(string)
	return v
}

func (ev *Event) num(key string) (int, bool) {
	f, ok := ev.Data[key].(float64)
	return int(f), ok
}

// strs reads a JSON array of strings (e.g. glob-check `matches`). An
// empty array parses as nil so "matches omitted" and "matches: []" are
// indistinguishable.
func (ev *Event) strs(key string) []string {
	items, ok := ev.Data[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
	glob           bool     // file checks: glob entry (v3.2)
	matches        []string // glob entry: matched names (log evidence; stored, not rendered)
}

type caseRec struct {
	env, task, installer string
	fromInstaller        string // upgrade cases: the OLD installer
	skipped              bool
	skipReason           string
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
	os      string // "" for linux (env_start omits it)
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
	r, err := decodeLogEncoding(r)
	if err != nil {
		return nil, err
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

// decodeLogEncoding returns UTF-8 log text. Windows PowerShell 5.1 writes
// `> run.jsonl` as UTF-16LE with a byte-order mark; UTF-16BE and UTF-8 with
// a BOM are accepted too. Input without a BOM is passed through unchanged.
func decodeLogEncoding(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)
	head, _ := br.Peek(3)
	switch {
	case len(head) >= 3 && head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF:
		_, _ = br.Discard(3)
		return br, nil
	case len(head) >= 2 && (head[0] == 0xFF && head[1] == 0xFE || head[0] == 0xFE && head[1] == 0xFF):
		data, err := io.ReadAll(br)
		if err != nil {
			return nil, err
		}
		little := data[0] == 0xFF
		data = data[2:]
		units := make([]uint16, len(data)/2)
		for i := range units {
			lo, hi := data[2*i], data[2*i+1]
			if !little {
				lo, hi = hi, lo
			}
			units[i] = uint16(lo) | uint16(hi)<<8
		}
		return strings.NewReader(string(utf16.Decode(units))), nil
	default:
		return br, nil
	}
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
	key := ev.Env + "\x00" + ev.Installer + "\x00" + ev.Task + "\x00" + ev.FromInstaller
	if c, ok := r.caseByKey[key]; ok {
		return c
	}
	c := &caseRec{env: ev.Env, task: ev.Task, installer: ev.Installer, fromInstaller: ev.FromInstaller}
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
		e.os = ev.str("os")
	case "env_end":
		e := r.env(ev.Env)
		e.sawEnd = true
		e.passed, _ = ev.num("passed")
		e.failed, _ = ev.num("failed")
	case "case_start":
		c := r.caseCtx(ev)
		c.containerID = ev.str("container_id")
		c.containerName = ev.str("container_name")
	case "case_skipped":
		c := r.caseCtx(ev)
		c.skipped = true
		c.skipReason = ev.str("reason")
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
			glob:    ev.flag("glob"),
			matches: ev.strs("matches"),
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
