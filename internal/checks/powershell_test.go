package checks

// PowerShell-dialect probes (docs/windows-environment-plan.md §7).

import (
	"testing"

	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/shell"
)

func scriptOf(t *testing.T, argv []string) string {
	t.Helper()
	if argv[0] != "powershell.exe" {
		t.Fatalf("argv[0] = %q, want powershell.exe", argv[0])
	}
	s, ok := shell.ScriptOf(argv)
	if !ok {
		t.Fatalf("argv does not decode: %q", argv)
	}
	return s
}

func TestPowerShellExactProbe(t *testing.T) {
	e := &execEnv{dialect: shell.PowerShell, results: []execResult{{code: 0}, {code: 1}}}
	cs := &config.Checks{
		MustExist:    []string{`C:\Program Files\Job Arranger\agentd.exe`},
		MustNotExist: []string{`HKLM:\SOFTWARE\Job Arranger's`},
	}
	p, f, _ := runChecks(t, e, cs)
	if p != 2 || f != 0 {
		t.Fatalf("passed=%d failed=%d", p, f)
	}
	if got, want := scriptOf(t, e.args[0]), `if (Test-Path -LiteralPath 'C:\Program Files\Job Arranger\agentd.exe') { exit 0 } else { exit 1 }`; got != want {
		t.Errorf("must_exist probe = %q", got)
	}
	if got, want := scriptOf(t, e.args[1]), `if (Test-Path -LiteralPath 'HKLM:\SOFTWARE\Job Arranger''s') { exit 0 } else { exit 1 }`; got != want {
		t.Errorf("must_not_exist probe = %q", got)
	}
}

func TestPowerShellProbeUnexpectedCode(t *testing.T) {
	e := &execEnv{dialect: shell.PowerShell, results: []execResult{{code: 2}}}
	_, f, buf := runOne(t, e, "must_exist", []string{`C:\x`})
	if f != 1 {
		t.Fatalf("failed=%d", f)
	}
	if d := lastCheckEvent(t, buf)["detail"]; d != "unexpected exit code 2 from Test-Path" {
		t.Errorf("detail = %v", d)
	}
}

func TestPowerShellGlobProbe(t *testing.T) {
	// CRLF is normalized by the environment; the check sees LF lines
	e := &execEnv{dialect: shell.PowerShell, results: []execResult{
		{stdout: "C:\\Program Files\\App\\a.dll\nC:\\Program Files\\App\\b.dll\n"},
		{stdout: ""},
	}}
	cs := &config.Checks{
		MustExist:    []string{`C:\Program Files\App\*.dll`},
		MustNotExist: []string{`HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\App*`},
	}
	p, f, _ := runChecks(t, e, cs)
	if p != 2 || f != 0 {
		t.Fatalf("passed=%d failed=%d", p, f)
	}
	want := `Get-Item -Path 'C:\Program Files\App\*.dll' -Force -ErrorAction SilentlyContinue | ForEach-Object { $_.PSPath -replace '^.*::', '' }`
	if got := scriptOf(t, e.args[0]); got != want {
		t.Errorf("glob probe = %q", got)
	}
}

func TestPowerShellCommandCheck(t *testing.T) {
	e := &execEnv{dialect: shell.PowerShell, results: []execResult{{stdout: "Running\n"}}}
	cs := &config.Checks{Commands: []config.CommandCheck{{
		Command: "(Get-Service jobarg_agentd).Status", ExpectedStdoutContains: "Running",
	}}}
	p, f, buf := runChecks(t, e, cs)
	if p != 1 || f != 0 {
		t.Fatalf("passed=%d failed=%d", p, f)
	}
	if got := scriptOf(t, e.args[0]); got != "(Get-Service jobarg_agentd).Status" {
		t.Errorf("script = %q", got)
	}
	// the logged command stays the readable script, never the base64 blob
	if c := lastCheckEvent(t, buf)["command"]; c != "(Get-Service jobarg_agentd).Status" {
		t.Errorf("logged command = %v", c)
	}
}
