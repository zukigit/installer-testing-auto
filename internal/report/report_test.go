package report

import (
	"strings"
	"testing"
)

// Sample log (new field shape only): fresh-install passes with a file check
// and a command check; uninstall fails at a file check (kept); b.rpm never ends.
const sampleNDJSON = `
{"ts":"2026-10-09T12:00:00.000Z","level":"info","event":"run_start","data":{"configs":1,"cases":3,"parallel":5}}
{"ts":"2026-10-09T12:00:00.010Z","level":"info","event":"env_start","env":"ubuntu-24.04","data":{"image":"ubuntu:24.04","workdir":"/tmp"}}
{"ts":"2026-10-09T12:00:00.400Z","level":"info","event":"case_start","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"container_id":"a1b2","container_name":"ita-ubuntu-24-04-a-rpm-fresh-install"}}
{"ts":"2026-10-09T12:00:01.000Z","level":"info","event":"step","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"kind":"install","command":"rpm -ivh /tmp/a.rpm","exit_code":0,"pass":true,"stdout":"install ok"}}
{"ts":"2026-10-09T12:00:01.100Z","level":"info","event":"check","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"kind":"file","path":"/usr/bin/app","expectation":"must_exist","exit_code":0,"pass":true}}
{"ts":"2026-10-09T12:00:01.200Z","level":"info","event":"check","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"kind":"command","command":"app --version","exit_code":0,"expected_exit_code":0,"pass":true,"stdout":"app 1.0"}}
{"ts":"2026-10-09T12:00:01.500Z","level":"info","event":"case_end","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"container_id":"a1b2","pass":true,"passed":3,"failed":0,"duration_ms":1100}}
{"ts":"2026-10-09T12:00:02.000Z","level":"info","event":"case_start","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"container_id":"c3d4","container_name":"ita-ubuntu-24-04-a-rpm-uninstall"}}
{"ts":"2026-10-09T12:00:02.100Z","level":"info","event":"check","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"kind":"file","path":"/usr/bin/app","expectation":"must_not_exist","exit_code":0,"pass":false,"detail":"file still exists"}}
{"ts":"2026-10-09T12:00:02.500Z","level":"error","event":"case_end","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"container_id":"c3d4","pass":false,"passed":0,"failed":1,"duration_ms":600,"kept":true}}
{"ts":"2026-10-09T12:00:03.000Z","level":"info","event":"case_start","env":"ubuntu-24.04","task":"uninstall","installer":"b.rpm","data":{"container_id":"e5f6","container_name":"ita-ubuntu-24-04-b-rpm-uninstall"}}
{"ts":"2026-10-09T12:00:03.500Z","level":"info","event":"env_end","env":"ubuntu-24.04","data":{"passed":3,"failed":1}}
{"ts":"2026-10-09T12:00:03.500Z","level":"info","event":"run_end","data":{"passed":3,"failed":1,"config_errors":0}}
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
	if !env.sawEnd || env.passed != 3 || env.failed != 1 {
		t.Errorf("env end not aggregated: %+v", env)
	}

	var fresh, un, incomplete *caseRec
	for _, c := range run.allCases {
		switch {
		case c.installer == "a.rpm" && c.task == "fresh-install":
			fresh = c
		case c.installer == "a.rpm" && c.task == "uninstall":
			un = c
		case c.installer == "b.rpm":
			incomplete = c
		}
	}
	if fresh == nil || !fresh.ended || !fresh.pass || fresh.passed != 3 {
		t.Errorf("fresh-install case wrong: %+v", fresh)
	}
	if un == nil || !un.ended || un.pass || un.failed != 1 || !un.kept {
		t.Errorf("uninstall case wrong: %+v", un)
	}
	if len(un.checks) != 1 || un.checks[0].path != "/usr/bin/app" || un.checks[0].expectation != "must_not_exist" || un.checks[0].detail != "file still exists" {
		t.Errorf("uninstall checks wrong: %+v", un.checks)
	}
	if len(fresh.steps) != 1 || fresh.steps[0].stdout != "install ok" {
		t.Errorf("fresh-install steps wrong: %+v", fresh.steps)
	}
	if incomplete == nil || incomplete.ended {
		t.Errorf("incomplete case wrong: %+v", incomplete)
	}

	// compact view: failing case detail present, passing-case detail absent
	var sb strings.Builder
	run.RenderText(&sb, false)
	out := sb.String()
	for _, want := range []string{
		"installer-testing-auto report",
		"fresh-install PASS",
		"uninstall  FAIL",
		"INCOMPLETE",
		"uninstall_checks:", // failing case detail grouped in its section
		"/usr/bin/app",      // failure detail shown
		"file still exists", // failure detail
		"kept containers (--keep, failed cases):",
		"ita-ubuntu-24-04-a-rpm-uninstall",
		"TOTAL: 3 cases: 1 passed, 1 failed, 1 incomplete",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compact report missing %q\nreport:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\n    install_checks:") {
		t.Errorf("compact view should not render passing-case detail sections\nreport:\n%s", out)
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
	for _, want := range []string{
		"steps:",
		"install_checks:",
		"uninstall_checks:",
		"must exist",    // file check row in --details
		"app --version", // command check row
		"exit code 0",
		"stdout: app 1.0", // passing command check shows streams in --details
		"stderr: (empty)",
		"must not exist",
		"detail: file still exists",
		"TOTAL: 3 cases: 1 passed, 1 failed, 1 incomplete",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--details report missing %q\nreport:\n%s", want, out)
		}
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
