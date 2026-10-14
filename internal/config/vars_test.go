// Tests for pattern variables (v3.2): {NAME} -> --var values, patterns only.
package config

import (
	"strings"
	"testing"
)

// Substitute replaces uppercase tokens with provided values; unset or empty
// tokens become the never-matching {UNSET:NAME} marker, lowercase built-in
// placeholders stay untouched.
func TestSubstitute(t *testing.T) {
	vars := map[string]string{"VERSION": "7.2.2", "EMPTY": ""}
	cases := []struct{ pattern, want string }{
		{"bin/jobarranger-server-{VERSION}*el8*.rpm", "bin/jobarranger-server-7.2.2*el8*.rpm"},
		{"bin/app-{EMPTY}-1.rpm", "bin/app-{UNSET:EMPTY}-1.rpm"}, // empty value: never-matching marker
		{"bin/app-{UNSET}.rpm", "bin/app-{UNSET:UNSET}.rpm"},     // unset: never-matching marker
		{"bin/jobarranger_server-{VERSION}-8-{VERSION}*", "bin/jobarranger_server-7.2.2-8-7.2.2*"},
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

// Unset or empty variable substitutes the never-matching {UNSET:NAME} marker
// — the pattern matches nothing and the existing "matched no files" load
// error fires, showing the marker ("act like there is no installer file",
// v3.3). A typo'd variable name behaves the same and is visible in the error.
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
	if !strings.Contains(err.Error(), `{UNSET:VERSION}`) {
		t.Errorf("error should show the {UNSET:VERSION} marker, got %v", err)
	}

	// An explicitly empty --var value is exactly like no value at all.
	_, err = LoadBundle("configs/test.yaml", map[string]string{"VERSION": ""})
	if err == nil || !strings.Contains(err.Error(), "matched no files") ||
		!strings.Contains(err.Error(), `{UNSET:VERSION}`) {
		t.Fatalf("empty --var value must behave like unset, got %v", err)
	}

	// A typo'd variable name behaves the same way and is visible in the error.
	files["configs/test.yaml"] = strings.ReplaceAll(files["configs/test.yaml"], "{VERSION}", "{VRSION}")
	writeTestFiles(t, files)
	_, err = LoadBundle("configs/test.yaml", nil)
	if err == nil || !strings.Contains(err.Error(), "matched no files") ||
		!strings.Contains(err.Error(), `{UNSET:VRSION}`) {
		t.Fatalf("expected matched-no-files error showing the typo name, got %v", err)
	}
}

// Regression test for the motivating v3.3 case: a wildcard right after the
// token ("...-{VERSION}*el9*.rpm") must NOT silently widen an unset variable
// to all versions — it matches nothing and the config is rejected (v3.2
// resolved both version files here).
func TestUnsetVariableNeverWidensToAllVersions(t *testing.T) {
	writeTestFiles(t, map[string]string{
		"bin/pkg-7.2.1-1.el9.x86_64.rpm": "",
		"bin/pkg-7.2.2-1.el9.x86_64.rpm": "",
		"configs/test.yaml": `
environment:
  image: test:latest
installers:
  - pattern: "bin/pkg-{VERSION}*el9*.rpm"
`,
	})
	_, err := LoadBundle("configs/test.yaml", nil)
	if err == nil || !strings.Contains(err.Error(), "matched no files") {
		t.Fatalf("unset variable must match nothing (v3.2 silently matched both files here), got %v", err)
	}
	if !strings.Contains(err.Error(), `{UNSET:VERSION}`) {
		t.Errorf("error should show the {UNSET:VERSION} marker, got %v", err)
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
