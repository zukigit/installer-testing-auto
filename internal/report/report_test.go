package report

import (
	"strings"
	"testing"
)

const sampleNDJSON = `
{"ts":"2026-10-09T12:00:00.000Z","level":"info","event":"run_start","data":{"configs":1,"cases":3,"parallel":5}}
{"ts":"2026-10-09T12:00:00.010Z","level":"info","event":"env_start","env":"ubuntu-24.04","data":{"image":"ubuntu:24.04","workdir":"/tmp"}}
{"ts":"2026-10-09T12:00:00.400Z","level":"info","event":"case_start","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"container_id":"a1b2","container_name":"ita-ubuntu-24-04-a-rpm-fresh-install"}}
{"ts":"2026-10-09T12:00:01.000Z","level":"info","event":"step","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"kind":"install","command":"rpm -ivh /tmp/a.rpm","exit_code":0,"pass":true}}
{"ts":"2026-10-09T12:00:01.100Z","level":"info","event":"check","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"kind":"command","name":"rpm -q a","pass":true,"exit_code":0}}
{"ts":"2026-10-09T12:00:01.500Z","level":"info","event":"case_end","env":"ubuntu-24.04","task":"fresh-install","installer":"a.rpm","data":{"container_id":"a1b2","pass":true,"passed":1,"failed":0,"duration_ms":1100}}
{"ts":"2026-10-09T12:00:02.000Z","level":"info","event":"case_start","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"container_id":"c3d4","container_name":"ita-ubuntu-24-04-a-rpm-uninstall"}}
{"ts":"2026-10-09T12:00:02.100Z","level":"error","event":"step","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"kind":"setup-install","command":"rpm -ivh /tmp/a.rpm","exit_code":1,"pass":false,"output":"error: unpacking archive failed"}}
{"ts":"2026-10-09T12:00:02.600Z","level":"error","event":"case_end","env":"ubuntu-24.04","task":"uninstall","installer":"a.rpm","data":{"container_id":"c3d4","pass":false,"passed":0,"failed":1,"duration_ms":600,"kept":true}}
{"ts":"2026-10-09T12:00:03.000Z","level":"info","event":"case_start","env":"ubuntu-24.04","task":"fresh-install","installer":"b.rpm","data":{"container_id":"e5f6","container_name":"ita-ubuntu-24-04-b-rpm-fresh-install"}}
{"ts":"2026-10-09T12:00:03.500Z","level":"info","event":"env_end","env":"ubuntu-24.04","data":{"passed":1,"failed":1}}
{"ts":"2026-10-09T12:00:03.500Z","level":"info","event":"run_end","data":{"passed":1,"failed":1,"config_errors":0}}
`

func TestParseAndRenderText(t *testing.T) {
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
	if got := len(run.envs); got != 1 {
		t.Fatalf("expected 1 env, got %d", got)
	}
	env := run.envs[0]
	if env.image != "ubuntu:24.04" || env.workdir != "/tmp" {
		t.Errorf("env image/workdir wrong: %q %q", env.image, env.workdir)
	}
	if !env.sawEnd || env.passed != 1 || env.failed != 1 {
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
	if fresh == nil || !fresh.ended || !fresh.pass || fresh.passed != 1 {
		t.Errorf("fresh-install case wrong: %+v", fresh)
	}
	if un == nil || !un.ended || un.pass || un.failed != 1 || !un.kept {
		t.Errorf("uninstall case wrong: %+v", un)
	}
	if len(un.steps) != 1 || un.steps[0].output != "error: unpacking archive failed" {
		t.Errorf("uninstall steps wrong: %+v", un.steps)
	}
	if incomplete == nil || incomplete.ended {
		t.Errorf("incomplete case wrong: %+v", incomplete)
	}

	var sb strings.Builder
	run.RenderText(&sb)
	out := sb.String()
	for _, want := range []string{
		"installer-testing-auto report",
		"a.rpm",
		"fresh-install PASS",
		"uninstall  FAIL",
		"INCOMPLETE",
		"kept containers (--keep, failed cases):",
		"ita-ubuntu-24-04-a-rpm-uninstall",
		"error: unpacking archive failed",
		"TOTAL: 3 cases: 1 passed, 1 failed, 1 incomplete",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered report missing %q\nreport:\n%s", want, out)
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
