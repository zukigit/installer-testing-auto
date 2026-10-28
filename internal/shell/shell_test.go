package shell

import (
	"reflect"
	"strings"
	"testing"
)

func TestPOSIXExactStrings(t *testing.T) {
	if got := POSIX.Command("echo hi"); !reflect.DeepEqual(got, []string{"/bin/sh", "-c", "echo hi"}) {
		t.Errorf("Command = %q", got)
	}
	cases := map[string][2]string{
		"PathExists": {POSIX.PathExists("/usr/bin/app"), "test -e '/usr/bin/app'"},
		"Glob":       {POSIX.Glob("/usr/share/man/man8/x.8.*"), "ls -d -- /usr/share/man/man8/x.8.*"},
		"MkdirAll":   {POSIX.MkdirAll("/tmp/a b"), "mkdir -p '/tmp/a b'"},
		"Quote":      {POSIX.Quote("it's"), "'it's'"}, // unescaped, as before the refactor
	}
	for name, c := range cases {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
	if POSIX.Name() != "posix" {
		t.Errorf("Name = %q", POSIX.Name())
	}
}

func TestPowerShellQuote(t *testing.T) {
	cases := map[string]string{
		`C:\ita\x.msi`:                    `'C:\ita\x.msi'`,
		`C:\Program Files\My App`:         `'C:\Program Files\My App'`,
		`it's`:                            `'it''s'`,
		`a[1]*.dll`:                       `'a[1]*.dll'`,
		`$env:TEMP "x"`:                   `'$env:TEMP "x"'`,
		"typo\u2019graphic":               "'typo\u2019\u2019graphic'",
		``:                                `''`,
		`HKLM:\SOFTWARE\Job Arranger's\x`: `'HKLM:\SOFTWARE\Job Arranger''s\x'`,
	}
	for in, want := range cases {
		if got := PowerShell.Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPowerShellProbes(t *testing.T) {
	cases := map[string][2]string{
		"PathExists": {PowerShell.PathExists(`C:\Program Files\App`),
			`if (Test-Path -LiteralPath 'C:\Program Files\App') { exit 0 } else { exit 1 }`},
		"Glob": {PowerShell.Glob(`C:\App\*.dll`),
			`Get-Item -Path 'C:\App\*.dll' -Force -ErrorAction SilentlyContinue | ForEach-Object { $_.PSPath -replace '^.*::', '' }`},
		"MkdirAll": {PowerShell.MkdirAll(`C:\ita`),
			`New-Item -ItemType Directory -Force -Path 'C:\ita' | Out-Null`},
	}
	for name, c := range cases {
		if c[0] != c[1] {
			t.Errorf("%s =\n  %q\nwant\n  %q", name, c[0], c[1])
		}
	}
	if PowerShell.Name() != "powershell" {
		t.Errorf("Name = %q", PowerShell.Name())
	}
}

func TestPowerShellCommandEncoding(t *testing.T) {
	script := "$p = Start-Process msiexec.exe -Wait -PassThru -ArgumentList '/i \"C:\\ita\\x.msi\" /qn'\nexit $p.ExitCode # ü"
	argv := PowerShell.Command(script)
	wantHead := []string{"powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand"}
	if !reflect.DeepEqual(argv[:len(argv)-1], wantHead) {
		t.Fatalf("argv head = %q", argv[:len(argv)-1])
	}
	if strings.ContainsAny(argv[len(argv)-1], " \"'\n") {
		t.Fatalf("encoded arg is not opaque: %q", argv[len(argv)-1])
	}
	full, ok := decodePS(argv[len(argv)-1])
	if !ok {
		t.Fatal("encoded arg does not decode")
	}
	if full != psPrelude+script+psEpilogue {
		t.Fatalf("decoded script =\n%s", full)
	}
	got, ok := ScriptOf(argv)
	if !ok || got != script {
		t.Fatalf("ScriptOf = %q, %v", got, ok)
	}
}

// TestPowerShellWrapper pins the prelude/epilogue text (exit-code contract).
func TestPowerShellWrapper(t *testing.T) {
	for _, want := range []string{
		"$ProgressPreference = 'SilentlyContinue'",
		"[Console]::OutputEncoding",
		"$global:LASTEXITCODE = 0",
	} {
		if !strings.Contains(psPrelude, want) {
			t.Errorf("prelude lacks %q", want)
		}
	}
	if strings.Contains(psPrelude+psEpilogue, "ErrorActionPreference") {
		t.Error("wrapper must not set $ErrorActionPreference (script keeps going like sh -c)")
	}
	if !strings.HasPrefix(psEpilogue, "\n$__itaOk = $?\n") {
		t.Errorf("epilogue must capture $? first: %q", psEpilogue)
	}
	native := strings.Index(psEpilogue, "LASTEXITCODE) { exit")
	failed := strings.Index(psEpilogue, "if (-not $__itaOk) { exit 1 }")
	if native < 0 || failed < 0 || native > failed || !strings.HasSuffix(psEpilogue, "exit 0\n") {
		t.Errorf("epilogue order must be native exit code > failed statement > 0: %q", psEpilogue)
	}
}

func TestScriptOf(t *testing.T) {
	if s, ok := ScriptOf(POSIX.Command("test -e '/x'")); !ok || s != "test -e '/x'" {
		t.Errorf("posix ScriptOf = %q, %v", s, ok)
	}
	if _, ok := ScriptOf([]string{"cmd", "/c", "ver"}); ok {
		t.Error("unknown argv must not decode")
	}
	if _, ok := ScriptOf(append(append([]string{}, psArgs...), "!!notbase64")); ok {
		t.Error("bad base64 must not decode")
	}
}
