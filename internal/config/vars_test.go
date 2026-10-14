// Tests for pattern variables (v3.2): {NAME} -> --var values, patterns only.
package config

import (
	"strings"
	"testing"
)

// Substitute replaces uppercase tokens with provided values or "" and leaves
// lowercase built-in placeholders untouched.
func TestSubstitute(t *testing.T) {
	vars := map[string]string{"VERSION": "7.2.2", "EMPTY": ""}
	cases := []struct{ pattern, want string }{
		{"bin/jobarranger-server-{VERSION}*el8*.rpm", "bin/jobarranger-server-7.2.2*el8*.rpm"},
		{"bin/app-{EMPTY}-1.rpm", "bin/app--1.rpm"}, // empty value: literal
		{"bin/app-{UNSET}.rpm", "bin/app-.rpm"},     // unset: "" (no error)
		{"bin/jobarg_server.{VERSION}.8.{VERSION}*", "bin/jobarg_server.7.2.2.8.7.2.2*"},
		{"bin/{installer}*.rpm", "bin/{installer}*.rpm"},               // built-ins untouched
		{"bin/{Installer}-{VERSION}.rpm", "bin/{Installer}-7.2.2.rpm"}, // mixed case: not a var
		{"bin/plain*.rpm", "bin/plain*.rpm"},                           // no tokens at all
	}
	for _, c := range cases {
		if got := Substitute(c.pattern, vars); got != c.want {
			t.Errorf("Substitute(%q) = %q, want %q", c.pattern, got, c.want)
		}
	}
}

// Patterns substitute at load: the right installer files match; unused vars
// are irrelevant.
func TestLoadSubstitutesPattern(t *testing.T) {
	writeTestFiles(t, map[string]string{
		"bin/pkg-7.2.2-1.el8.x86_64.rpm": "",
		"bin/pkg-7.3.0-1.el8.x86_64.rpm": "",
		"configs/test.yaml": `
environment:
  image: test:latest
installers:
  - pattern: "bin/pkg-{VERSION}*el8*.rpm"
`,
	})
	b, err := LoadBundle("configs/test.yaml", map[string]string{"VERSION": "7.2.2"})
	if err != nil {
		t.Fatalf("load with VERSION=7.2.2: %v", err)
	}
	if len(b.Installers) != 1 || !strings.HasSuffix(b.Installers[0].LocalPath, "pkg-7.2.2-1.el8.x86_64.rpm") {
		t.Errorf("variable did not select the right installers: %+v", b.Installers)
	}
}

// Unset/empty variable substitutes as "" — the glob decides; common shapes
// yield the well-defined "matched no files" load error. The error shows the
// substituted pattern (typo visibility).
func TestLoadUnsetVariableNoMatch(t *testing.T) {
	files := map[string]string{
		"bin/pkg-7.2.2-1.el8.x86_64.rpm": "",
		"configs/test.yaml": `
environment:
  image: test:latest
installers:
  - pattern: "bin/pkg-{VERSION}-1.el8.x86_64.rpm"
`,
	}
	writeTestFiles(t, files)
	_, err := LoadBundle("configs/test.yaml", nil)
	if err == nil || !strings.Contains(err.Error(), "matched no files") {
		t.Fatalf("expected matched-no-files error for unset variable, got %v", err)
	}
	if !strings.Contains(err.Error(), `bin/pkg--1.el8.x86_64.rpm`) {
		t.Errorf("error should show the substituted pattern, got %v", err)
	}
	// A typo'd variable name behaves the same way.
	cfgErr := strings.ReplaceAll(files["configs/test.yaml"], "{VERSION}", "{VRSION}")
	files["configs/test.yaml"] = cfgErr
	writeTestFiles(t, files)
	if _, err := LoadBundle("configs/test.yaml", nil); err == nil || !strings.Contains(err.Error(), "matched no files") {
		t.Fatalf("expected matched-no-files error for typo variable, got %v", err)
	}
}

// Lowercase built-in placeholders in a pattern are literal text and match
// nothing (no special error path).
func TestLoadLowercaseBuiltInInPatternNoMatch(t *testing.T) {
	writeTestFiles(t, map[string]string{
		"bin/pkg-1.0.rpm": "",
		"configs/test.yaml": `
environment:
  image: test:latest
installers:
  - pattern: "bin/{installer}*.rpm"
`,
	})
	_, err := LoadBundle("configs/test.yaml", nil)
	if err == nil || !strings.Contains(err.Error(), "matched no files") {
		t.Fatalf("expected matched-no-files error for literal built-in token, got %v", err)
	}
}

// A variable is NOT substituted anywhere outside installer patterns: check
// paths keep their literal {VAR} text (zero guard code).
func TestVariablesArePatternOnly(t *testing.T) {
	writeTestFiles(t, map[string]string{
		"bin/pkg-1.0.rpm": "",
		"configs/test.yaml": `
environment:
  image: test:latest
installers:
  - pattern: "bin/*.rpm"
    install_checks:
      must_exist:
        - /opt/pkg-{VERSION}/database
`,
	})
	cfg, err := Load("configs/test.yaml", map[string]string{"VERSION": "7.2.2"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := cfg.Installers[0].InstallChecks.MustExist[0]
	if got != "/opt/pkg-{VERSION}/database" {
		t.Errorf("check path must keep the literal token, got %q", got)
	}
}

// Glob-mode check paths are validated (shell-active: the path is passed
// unquoted to the container shell). Whitespace and shell metacharacters are
// load errors; the safe charset (incl. literal braces) passes.
func TestGlobPathCharsetValidation(t *testing.T) {
	const tmpl = `
environment:
  image: test:latest
installers:
  - pattern: "bin/*.rpm"
    uninstall_checks:
      must_not_exist:
%s
`
	loadWith := func(entry string) error {
		writeTestFiles(t, map[string]string{
			"bin/pkg-1.0.rpm":   "",
			"configs/test.yaml": strings.Replace(tmpl, "%s", entry, 1),
		})
		_, err := Load("configs/test.yaml", nil)
		return err
	}
	// -- allowed: glob metachars, literal braces, plain safe chars
	if err := loadWith("        - /usr/share/man/man8/jobarg_server.8.*"); err != nil {
		t.Errorf("glob path with * should load, got %v", err)
	}
	if err := loadWith("        - /opt/app-{RELEASE}?.conf*"); err != nil {
		t.Errorf("glob path with braces/? should load, got %v", err)
	}
	// -- rejected: anything shell-active
	for _, bad := range []string{
		"        - /usr/share/my docs/*.conf",
		"        - /etc/$(evil)*",
		"        - /etc/x;*",
		"        - /opt/space\t*",
		"        - /etc/x'y*",
	} {
		err := loadWith(bad)
		want := "contains whitespace or shell metacharacters"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("glob path %q should be a charset error mentioning %q, got %v", bad, want, err)
		}
	}
}
