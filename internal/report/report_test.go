package report

import (
	"strings"
	"testing"
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
		"install: rpm -ivh /tmp/a.rpm (exit 0)",
		"install_checks:",
		"must exist", "app --version", "exit code 0",
		"stdout: app 1.0", "stderr: (empty)",
		"setup-install: rpm -ivh /tmp/a.rpm (exit 0)",
		"uninstall: rpm -e app (exit 0)",
		"detail: file still exists",
		"(case interrupted — no steps/check results recorded yet)",
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
		"(no older version)",
		"  myapp-0.1.2-1.rpm",
		"✓ PASS", "3/3",
		"(from myapp-0.1.1-1.rpm)",
		"TOTAL: 2 cases (1 passed, 0 failed, 1 skipped)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("upgrade compact report missing %q\nreport:\n%s", want, out)
		}
	}

	var db strings.Builder
	run.RenderText(&db, true)
	down := db.String()
	for _, want := range []string{
		"container: ita-redhat-9-myapp-0-1-2-1-rpm-from-0-1-1-1-upgrade",
		"setup-install: rpm -ivh /tmp/myapp-0.1.1-1.rpm (exit 0)",
		"upgrade: rpm -Uvh /tmp/myapp-0.1.2-1.rpm (exit 0)",
		"upgrade_checks:",
		"skipped: no older version",
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
