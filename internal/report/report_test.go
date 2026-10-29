package report

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/zukigit/installer-testing-auto/internal/config"
)

// Sample log: fresh-install of a.rpm passes (copy+install steps, 2 checks);
// uninstall of a.rpm fails one check (kept container, copy step passed);
// b.rpm's uninstall never finishes (incomplete).
const sampleNDJSON = `
{"ts":"2026-10-09T12:00:00.000Z","level":"info","event":"run_start","data":{"configs":1,"cases":3,"parallel":5}}
{"ts":"2026-10-09T12:00:00.010Z","level":"info","event":"env_start","env":"ubuntu-24.04","data":{"image":"ubuntu:24.04","workdir":"/tmp"}}
{"ts":"2026-10-09T12:00:00.400Z","level":"info","event":"case_start","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"container_id":"a1b2","container_name":"ita-ubuntu-24-04-a-rpm-fresh-install"}}
{"ts":"2026-10-09T12:00:00.600Z","level":"info","event":"step","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"kind":"copy","command":"bin/a.rpm -> /tmp/a.rpm","exit_code":0,"pass":true}}
{"ts":"2026-10-09T12:00:01.000Z","level":"info","event":"step","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"kind":"install","command":"rpm -ivh /tmp/a.rpm","exit_code":0,"pass":true,"stdout":"install ok"}}
{"ts":"2026-10-09T12:00:01.100Z","level":"info","event":"check","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"kind":"file","path":"/usr/bin/app","expectation":"must_exist","exit_code":0,"pass":true}}
{"ts":"2026-10-09T12:00:01.200Z","level":"info","event":"check","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"kind":"command","command":"app --version","exit_code":0,"expected_exit_code":0,"pass":true,"stdout":"app 1.0"}}
{"ts":"2026-10-09T12:00:01.500Z","level":"info","event":"case_end","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"container_id":"a1b2","pass":true,"passed":3,"failed":0,"duration_ms":1100}}
{"ts":"2026-10-09T12:00:02.000Z","level":"info","event":"case_start","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"container_id":"c3d4","container_name":"ita-ubuntu-24-04-a-rpm-uninstall"}}
{"ts":"2026-10-09T12:00:02.100Z","level":"info","event":"step","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"kind":"copy","command":"bin/a.rpm -> /tmp/a.rpm","exit_code":0,"pass":true}}
{"ts":"2026-10-09T12:00:02.200Z","level":"info","event":"step","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"kind":"setup-install","command":"rpm -ivh /tmp/a.rpm","exit_code":0,"pass":true}}
{"ts":"2026-10-09T12:00:02.300Z","level":"info","event":"step","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"kind":"uninstall","command":"rpm -e app","exit_code":0,"pass":true,"stdout":"removed app"}}
{"ts":"2026-10-09T12:00:02.400Z","level":"info","event":"check","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"kind":"file","path":"/usr/bin/app","expectation":"must_not_exist","exit_code":0,"pass":false,"detail":"file still exists"}}
{"ts":"2026-10-09T12:00:02.600Z","level":"error","event":"case_end","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"container_id":"c3d4","pass":false,"passed":2,"failed":1,"duration_ms":600,"kept":true}}
{"ts":"2026-10-09T12:00:03.000Z","level":"info","event":"case_start","env":"ubuntu-24.04","task":"uninstall","installer":"b.rpm","data":{"container_id":"e5f6","container_name":"ita-ubuntu-24-04-b-rpm-uninstall"}}
{"ts":"2026-10-09T12:00:03.500Z","level":"info","event":"env_end","env":"ubuntu-24.04","data":{"passed":5,"failed":1}}
{"ts":"2026-10-09T12:00:03.500Z","level":"info","event":"run_end","data":{"passed":5,"failed":1,"config_errors":0}}
`

func TestParseAndRenderCompact(t *testing.T) {
	run, err := Parse(strings.NewReader(sampleNDJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !run.sawRunStart || !run.sawRunEnd {
		t.Fatalf("expected run_start and run_end to be seen")
	}
	if got := len(run.allCases); got != 3 {
		t.Fatalf("expected 3 cases, got %d", got)
	}
	env := run.envs[0]
	if env.image != "ubuntu:24.04" || env.workdir != "/tmp" {
		t.Errorf("env image/workdir wrong: %q %q", env.image, env.workdir)
	}

	var sb strings.Builder
	run.RenderText(&sb, false)
	out := sb.String()
	for _, want := range []string{
		"installer-testing-auto report",
		// grouping: header per installer, tasks nested in canonical order
		"  a.rpm\n",
		"    fresh-install",
		"    uninstall",
		// marks
		"✓ PASS", "✗ FAIL", "? INCOMPLETE",
		// counts (copy excluded: install = 1, uninstall/setup = 2, + checks)
		"3/3", "2/3",
		// failure detail: failing check row + detail under the task line
		"uninstall_checks:",
		"/usr/bin/app", "must not exist",
		"detail: file still exists",
		// totals: TOTAL is a single case-only line (no checks summary)
		"TOTAL: 3 cases (1 passed, 1 failed, 1 incomplete)",
		// kept containers section
		"kept containers (--keep, failed cases):",
		"ita-ubuntu-24-04-a-rpm-uninstall",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compact report missing %q\nreport:\n%s", want, out)
		}
	}
	// container names must not appear on task lines in the compact view
	if strings.Contains(out, "ita-ubuntu-24-04-a-rpm-fresh-install") {
		t.Errorf("compact view should not show container names\ndetails view:\n%s", out)
	}
	// passing-case check details must not leak into compact view
	if strings.Contains(out, "\n      install_checks:") {
		t.Errorf("compact view should not render passing-case detail sections\nreport:\n%s", out)
	}
	// the TOTAL footnote must not have a separate checks line, and per-env
	// checks summaries are removed too (v2.4) — only check *sections*
	// ("install_checks:" / "uninstall_checks:") may contain the word
	if strings.Contains(out, "\n  checks:") {
		t.Errorf("report should not contain any checks summary line\nreport:\n%s", out)
	}
}

// Glob file checks (v3.2) render via the existing detail lines: the configured
// pattern is the row label, the failure detail comes from the event. The
// matches/glob fields are parsed and stored without changing rendering, and
// the run_start vars key is ignored (no report changes for feature B).
func TestGlobCheckRendering(t *testing.T) {
	log := `
{"ts":"2026-10-14T12:00:00.000Z","level":"info","event":"run_start","data":{"configs":1,"cases":1,"parallel":5,"vars":{"VERSION":"7.2.2"}}}
{"ts":"2026-10-14T12:00:00.010Z","level":"info","event":"env_start","env":"rocky-9","data":{"image":"rocky:9","workdir":"/tmp"}}
{"ts":"2026-10-14T12:00:00.400Z","level":"info","event":"case_start","env":"rocky-9","task":"fresh-install","installer":"server.rpm","data":{"container_id":"a1","container_name":"ita-rocky-9-server-rpm-fresh-install"}}
{"ts":"2026-10-14T12:00:00.500Z","level":"info","event":"step","env":"rocky-9","task":"fresh-install","installer":"server.rpm","data":{"kind":"copy","exit_code":0,"pass":true}}
{"ts":"2026-10-14T12:00:01.000Z","level":"info","event":"check","env":"rocky-9","task":"fresh-install","installer":"server.rpm","data":{"kind":"file","path":"/usr/share/man/man8/jobarg_server.8.*","expectation":"must_exist","glob":true,"exit_code":2,"pass":false,"detail":"no files match pattern","matches":[]}}
{"ts":"2026-10-14T12:00:01.500Z","level":"error","event":"case_end","env":"rocky-9","task":"fresh-install","installer":"server.rpm","data":{"container_id":"a1","pass":false,"passed":0,"failed":1,"duration_ms":1100}}
{"ts":"2026-10-14T12:00:02.000Z","level":"info","event":"env_end","env":"rocky-9","data":{"passed":0,"failed":1}}
{"ts":"2026-10-14T12:00:02.000Z","level":"error","event":"run_end","data":{"passed":0,"failed":1}}
`
	run, err := Parse(strings.NewReader(log))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// glob/matches are stored on the parsed record (log consumers see them
	// via NDJSON; rendering needs no change)
	if len(run.allCases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(run.allCases))
	}
	cks := run.allCases[0].checks
	if len(cks) != 1 || !cks[0].glob {
		t.Fatalf("glob check not parsed: %+v", cks)
	}
	if cks[0].matches != nil {
		t.Errorf("empty matches should parse as nil, got %v", cks[0].matches)
	}

	var sb strings.Builder
	run.RenderText(&sb, false)
	out := sb.String()
	for _, want := range []string{
		"/usr/share/man/man8/jobarg_server.8.*", // the pattern is the row label
		"must exist",
		"detail: no files match pattern", // failure detail renders like any other
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\nreport:\n%s", want, out)
		}
	}
	if strings.Contains(out, "vars:") {
		t.Errorf("report must not render a vars line (decision 17)\nreport:\n%s", out)
	}
}

func TestRenderDetailsView(t *testing.T) {
	run, err := Parse(strings.NewReader(sampleNDJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var sb strings.Builder
	run.RenderText(&sb, true)
	out := sb.String()
	wantStrings := []string{
		// container names are shown in the details view (name only, no id)
		"container: ita-ubuntu-24-04-a-rpm-fresh-install",
		// steps: passing copy steps hidden, full paths kept
		"install: rpm -ivh /tmp/a.rpm",
		"exit code: 0",
		"stdout: app 1.0",
		"stderr: empty",
		"install_checks:",
		"must exist", "app --version", "exit code 0",

		"setup-install: rpm -ivh /tmp/a.rpm",
		"uninstall: rpm -e app",
		"detail: file still exists",
		"case interrupted — no steps/check results recorded yet",
	}
	for _, want := range wantStrings {
		if !strings.Contains(out, want) {
			t.Errorf("--details report missing %q\nreport:\n%s", want, out)
		}
	}
	// passing copy steps hidden in the details view
	if strings.Contains(out, "bin/a.rpm -> /tmp/a.rpm") {
		t.Errorf("--details view should hide passing copy steps\nreport:\n%s", out)
	}
}

func TestParseIncompleteRun(t *testing.T) {
	in := strings.NewReader(`{"ts":"x","level":"info","event":"run_start","data":{"configs":1,"cases":2,"parallel":5}}` + "\n")
	run, err := Parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if run.sawRunEnd {
		t.Error("run_end should not be seen")
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, err := Parse(strings.NewReader("not json\n")); err == nil {
		t.Fatal("expected parse error for invalid json")
	}
}

const upgradeNDJSON = `
{"ts":"2026-10-10T06:00:00.000Z","level":"info","event":"run_start","data":{"configs":1,"cases":2,"parallel":5}}
{"ts":"2026-10-10T06:00:00.010Z","level":"info","event":"env_start","env":"redhat-9","data":{"image":"rhel9","workdir":"/tmp"}}

{"ts":"2026-10-10T06:00:00.400Z","level":"info","event":"case_skipped","env":"redhat-9","task":"upgrade","installer":"myapp-0.1.1-1.rpm","data":{"reason":"no_older_version"}}

{"ts":"2026-10-10T06:00:01.000Z","level":"info","event":"case_start","env":"redhat-9","task":"upgrade","installer":"myapp-0.1.2-1.rpm","from_installer":"myapp-0.1.1-1.rpm","data":{"container_id":"a1","container_name":"ita-redhat-9-myapp-0-1-2-1-rpm-from-0-1-1-1-upgrade"}}
{"ts":"2026-10-10T06:00:01.100Z","level":"info","event":"step","env":"redhat-9","task":"upgrade","installer":"myapp-0.1.2-1.rpm","from_installer":"myapp-0.1.1-1.rpm","data":{"kind":"setup-install","command":"rpm -ivh /tmp/myapp-0.1.1-1.rpm","exit_code":0,"pass":true}}
{"ts":"2026-10-10T06:00:01.200Z","level":"info","event":"step","env":"redhat-9","task":"upgrade","installer":"myapp-0.1.2-1.rpm","from_installer":"myapp-0.1.1-1.rpm","data":{"kind":"upgrade","command":"rpm -Uvh /tmp/myapp-0.1.2-1.rpm","exit_code":0,"pass":true}}
{"ts":"2026-10-10T06:00:01.300Z","level":"info","event":"check","env":"redhat-9","task":"upgrade","installer":"myapp-0.1.2-1.rpm","from_installer":"myapp-0.1.1-1.rpm","data":{"kind":"file","path":"/usr/bin/myapp","expectation":"must_exist","exit_code":0,"pass":true}}
{"ts":"2026-10-10T06:00:01.500Z","level":"info","event":"case_end","env":"redhat-9","task":"upgrade","installer":"myapp-0.1.2-1.rpm","from_installer":"myapp-0.1.1-1.rpm","data":{"container_id":"a1","pass":true,"passed":3,"failed":0,"duration_ms":2700}}
{"ts":"2026-10-10T06:00:02.000Z","level":"info","event":"env_end","env":"redhat-9","data":{"passed":3,"failed":0}}
{"ts":"2026-10-10T06:00:02.000Z","level":"info","event":"run_end","data":{"passed":3,"failed":0}}
`

func TestParseAndRenderUpgrade(t *testing.T) {
	run, err := Parse(strings.NewReader(upgradeNDJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var sb strings.Builder
	run.RenderText(&sb, false)
	out := sb.String()
	for _, want := range []string{
		"  myapp-0.1.1-1.rpm",
		"⏸ SKIPPED",
		"reason: no older version",
		"  myapp-0.1.2-1.rpm",
		"✓ PASS", "3/3",
		"from: myapp-0.1.1-1.rpm",
		"TOTAL: 2 cases (1 passed, 0 failed, 1 skipped)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("upgrade compact report missing %q\nreport:\n%s", want, out)
		}
	}
	// no parenthesized annotations may appear (TOTAL keeps its parens)
	if strings.Contains(out, "(no older version)") || strings.Contains(out, "(from ") || strings.Contains(out, "(exit ") {
		t.Errorf("compact report still contains a parenthesized annotation\nreport:\n%s", out)
	}

	var db strings.Builder
	run.RenderText(&db, true)
	down := db.String()
	for _, want := range []string{
		"container: ita-redhat-9-myapp-0-1-2-1-rpm-from-0-1-1-1-upgrade",
		"setup-install: rpm -ivh /tmp/myapp-0.1.1-1.rpm",
		"upgrade: rpm -Uvh /tmp/myapp-0.1.2-1.rpm",
		"exit code: 0",
		"upgrade_checks:",
		"reason: no older version",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("--details upgrade report missing %q\nreport:\n%s", want, down)
		}
	}
}

func TestCheckSectionTitle(t *testing.T) {
	cases := map[string]string{
		"fresh-install": "install_checks",
		"uninstall":     "uninstall_checks",
		"upgrade":       "upgrade_checks",
		"install-twice": "install_twice_checks",
	}
	for task, want := range cases {
		if got := checkSectionTitle(task); got != want {
			t.Errorf("checkSectionTitle(%q) = %q, want %q", task, got, want)
		}
	}
}

// ListBundle shows the resolved upgrade command and upgrade_checks next to
// install/uninstall (v3.5): commands are placeholder-expanded, the checks
// line is the same counts summary as the other blocks and is omitted when
// the block is nil. An empty upgrade command prints an empty line (e.g. a
// .sh entry without upgrade_command — the run would fail in setup).
func TestListBundle(t *testing.T) {
	bundle := func(upgradeChecks *config.Checks) *config.Bundle {
		return &config.Bundle{
			Path:   "configs/test.yaml",
			Config: &config.Config{Environment: config.EnvironmentSpec{Name: "test-9", Type: "docker", Image: "test:9", Workdir: "/tmp"}},
			Installers: []*config.ResolvedInstaller{{
				LocalPath:       "bin/myapp-1.2.3-1.el9.x86_64.rpm",
				ContainerPath:   "/tmp/myapp-1.2.3-1.el9.x86_64.rpm",
				Basename:        "myapp-1.2.3-1.el9.x86_64.rpm",
				PackageName:     "myapp",
				InstallCmd:      "rpm -ivh {installer}",
				UninstallCmd:    "rpm -e {package}",
				UpgradeCmd:      "echo new >> /etc/myapp.conf && rpm -Uvh {installer}",
				InstallChecks:   &config.Checks{MustExist: []string{"/a", "/b"}, Commands: []config.CommandCheck{{}}},
				UninstallChecks: &config.Checks{MustNotExist: []string{"/a", "/b", "/c"}},
				UpgradeChecks:   upgradeChecks,
			}},
		}
	}

	withChecks := bundle(&config.Checks{MustExist: []string{"/etc/myapp.conf", "/etc/myapp.conf.rpmnew", "/db/upgrade"}})
	out := captureStdout(t, func() { ListBundle(withChecks) })
	for _, want := range []string{
		"    install:      rpm -ivh /tmp/myapp-1.2.3-1.el9.x86_64.rpm\n",
		"    uninstall:    rpm -e myapp\n",
		"    upgrade:      echo new >> /etc/myapp.conf && rpm -Uvh /tmp/myapp-1.2.3-1.el9.x86_64.rpm\n",
		"    install_checks:  must_exist=2 must_not_exist=0 commands=1\n",
		"    uninstall_checks: must_exist=0 must_not_exist=3 commands=0\n",
		"    upgrade_checks:   must_exist=3 must_not_exist=0 commands=0\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q\noutput:\n%s", want, out)
		}
	}

	// nil upgrade_checks: the line is omitted, everything else unchanged
	out = captureStdout(t, func() { ListBundle(bundle(nil)) })
	if strings.Contains(out, "upgrade_checks:") {
		t.Errorf("nil upgrade_checks must omit the line\noutput:\n%s", out)
	}
	if !strings.Contains(out, "uninstall_checks: must_exist=0 must_not_exist=3 commands=0\n") {
		t.Errorf("nil upgrade_checks must not affect the other lines\noutput:\n%s", out)
	}

	// empty upgrade command (a .sh entry without upgrade_command): empty line
	emptyCmd := bundle(nil)
	emptyCmd.Installers[0].Basename = "myapp.sh"
	emptyCmd.Installers[0].UpgradeCmd = ""
	out = captureStdout(t, func() { ListBundle(emptyCmd) })
	if !strings.Contains(out, "    upgrade:      \n") {
		t.Errorf("empty UpgradeCmd must print an empty line\noutput:\n%q", out)
	}
}

// captureStdout runs fn with os.Stdout swapped for a pipe and returns what
// it printed (ListBundle prints to stdout directly).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = old
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestWindowsReportAndList(t *testing.T) {
	const logs = `
{"ts":"2026-10-28T12:00:00.000Z","level":"info","event":"run_start","data":{"configs":1,"cases":1,"parallel":5}}
{"ts":"2026-10-28T12:00:00.010Z","level":"info","event":"env_start","env":"windows-2022","data":{"image":"servercore:ltsc2022","workdir":"C:\\ita","os":"windows"}}
{"ts":"2026-10-28T12:00:00.020Z","level":"info","event":"case_skipped","env":"windows-2022","task":"upgrade","installer":"a.msi","data":{"reason":"no_version_format"}}
{"ts":"2026-10-28T12:00:00.500Z","level":"info","event":"env_end","env":"windows-2022","data":{"passed":0,"failed":0}}
{"ts":"2026-10-28T12:00:00.500Z","level":"info","event":"run_end","data":{"passed":0,"failed":0}}
`
	run, err := Parse(strings.NewReader(logs))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	run.RenderText(&b, false)
	out := b.String()
	for _, want := range []string{
		`env windows-2022 (windows, image servercore:ltsc2022, workdir C:\ita)`,
		"no version_format configured",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n%s", want, out)
		}
	}

	bundle := &config.Bundle{
		Path:   "configs/win.yaml",
		Config: &config.Config{Environment: config.EnvironmentSpec{Name: "win", Type: "docker", OS: "windows", Isolation: "process", Image: "x", Workdir: `C:\ita`}},
		Installers: []*config.ResolvedInstaller{
			{LocalPath: "bin/a.msi", ContainerPath: `C:\ita\a.msi`, Basename: "a.msi", NoVersionFormat: true, InstallCmd: "line1\nline2"},
			{LocalPath: "bin/b-1.2.3.msi", ContainerPath: `C:\ita\b-1.2.3.msi`, Basename: "b-1.2.3.msi", Version: "1.2.3", UpgradeKey: "b"},
			{LocalPath: "bin/c.msi", ContainerPath: `C:\ita\c.msi`, Basename: "c.msi"},
		},
	}
	out = captureStdout(t, func() { ListBundle(bundle) })
	for _, want := range []string{
		"  os:       windows\n",
		"  isolation: process\n",
		"    in-container: C:\\ita\\a.msi\n",
		"    install:      line1\n                  line2\n",
		"    pairing:      skipped by upgrade (no_version_format)\n",
		"    pairing:      version=1.2.3 package=b\n",
		"    pairing:      skipped by upgrade (nonstandard_version)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q\n%s", want, out)
		}
	}
}

func TestUpgradeFromListAndSkipText(t *testing.T) {
	from := &config.ResolvedInstaller{LocalPath: "bin/old/a.msi", Basename: "a.msi"}
	other := &config.ResolvedInstaller{LocalPath: "bin/b.msi", Basename: "B.MSI"}
	bundle := &config.Bundle{
		Path:   "configs/w.yaml",
		Config: &config.Config{Environment: config.EnvironmentSpec{Name: "w", OS: "windows", Image: "x", Workdir: `C:\ita`}},
		Installers: []*config.ResolvedInstaller{
			{LocalPath: "bin/new.msi", Basename: "new.msi", UpgradeFromSet: true, UpgradeFrom: []*config.ResolvedInstaller{from, other}},
			{LocalPath: "bin/b.msi", Basename: "b.msi", UpgradeFromSet: true, UpgradeFrom: []*config.ResolvedInstaller{other}},
			{LocalPath: "bin/c.msi", Basename: "c.msi", UpgradeFromSet: true},
		},
	}
	out := captureStdout(t, func() { ListBundle(bundle) })
	for _, want := range []string{
		"    pairing:      upgrade from bin/old/a.msi\n    pairing:      upgrade from bin/b.msi\n",
		"    pairing:      skipped by upgrade (is_upgrade_from)\n", // b.msi vs B.MSI on windows
		"    pairing:      skipped by upgrade (no_upgrade_from_match)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q\n%s", want, out)
		}
	}
	if skipReasonText("no_upgrade_from_match") != "upgrade_from matched no files" ||
		skipReasonText("is_upgrade_from") != "this file is the upgrade_from installer" {
		t.Error("skip texts")
	}
}

// Windows PowerShell 5.1 `> run.jsonl` writes UTF-16LE with a BOM and CRLF.
func TestParseUTF16AndBOM(t *testing.T) {
	text := strings.ReplaceAll(strings.TrimSpace(sampleNDJSON), "\n", "\r\n\r\n") + "\r\n"
	enc := func(bigEndian bool) []byte {
		units := utf16.Encode([]rune(text))
		out := []byte{0xFF, 0xFE}
		if bigEndian {
			out = []byte{0xFE, 0xFF}
		}
		for _, u := range units {
			if bigEndian {
				out = append(out, byte(u>>8), byte(u))
			} else {
				out = append(out, byte(u), byte(u>>8))
			}
		}
		return out
	}
	inputs := map[string][]byte{
		"utf16le": enc(false),
		"utf16be": enc(true),
		"utf8bom": append([]byte{0xEF, 0xBB, 0xBF}, text...),
		"utf8":    []byte(text),
	}
	for name, in := range inputs {
		run, err := Parse(bytes.NewReader(in))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !run.sawRunStart || !run.sawRunEnd || len(run.allCases) != 3 {
			t.Errorf("%s: parsed run_start=%v run_end=%v cases=%d", name, run.sawRunStart, run.sawRunEnd, len(run.allCases))
		}
	}
}
