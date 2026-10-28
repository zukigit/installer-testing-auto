package config

import (
	"strings"
	"testing"
)

func TestVersionFormatMatch(t *testing.T) {
	cases := []struct {
		tmpl, base     string
		ci             bool
		version, ident string
		ok             bool
	}{
		{"{package}-{version}-x64.msi", "jobarranger-agentd-7.2.2-x64.msi", true, "7.2.2", "jobarranger-agentd", true},
		{"{package}-{version}-x64.msi", "JobArranger-Agentd-7.2.2-X64.MSI", true, "7.2.2", "JobArranger-Agentd", true},
		{"{package}-{version}-x64.msi", "JobArranger-Agentd-7.2.2-X64.MSI", false, "", "", false}, // case-sensitive on linux
		{"{package}-{version}.msi", "foo-bar-1.2.3-beta.msi", false, "1.2.3-beta", "foo-bar", true},
		{"MyAppSetup_{version}.exe", "MyAppSetup_7.2.2.1234.exe", true, "7.2.2-1234", "MyAppSetup", true},
		{"MyAppSetup_{version}.exe", "MyAppSetup_7.2.exe", true, "", "", false},   // not MAJOR.MINOR.PATCH
		{"MyAppSetup_{version}.exe", "MyAppSetupX7.2.2.exe", true, "", "", false}, // "_" is literal
		{"app-{version}*.msi", "app-2.0.1-beta-x86.msi", true, "2.0.1-beta-x86", "app", true},
		{"{version}.zip", "1.0.0.zip", true, "1.0.0", "{version}.zip", true}, // no prefix: template is the identity
		{"a+b(c)[d]-{version}.msi", "a+b(c)[d]-1.2.3.msi", false, "1.2.3", "a+b(c)[d]", true},
		{"a+b-{version}.msi", "aab-1.2.3.msi", false, "", "", false},                          // "+" escaped
		{"a.b-{version}.msi", "axb-1.2.3.msi", false, "", "", false},                          // "." escaped
		{"{VERSION}-{version}.msi", "{VERSION}-1.2.3.msi", false, "1.2.3", "{VERSION}", true}, // {UPPER} literal
		{"app-{version}.sh", "app-0.1.1-1.sh", false, "0.1.1-1", "app", true},
	}
	for _, c := range cases {
		vf, err := CompileVersionFormat(c.tmpl, c.ci)
		if err != nil {
			t.Fatalf("compile %q: %v", c.tmpl, err)
		}
		v, id, ok := vf.Match(c.base)
		if ok != c.ok || v != c.version || id != c.ident {
			t.Errorf("%q on %q = (%q, %q, %v), want (%q, %q, %v)", c.tmpl, c.base, v, id, ok, c.version, c.ident, c.ok)
		}
	}
}

func TestVersionFormatValidation(t *testing.T) {
	cases := map[string]string{
		"{package}.msi":                   "exactly once",
		"{version}-{version}.msi":         "exactly once",
		"{package}-{package}-{version}.x": "at most once",
		"{name}-{version}.msi":            "unknown token {name}",
	}
	for tmpl, want := range cases {
		_, err := CompileVersionFormat(tmpl, false)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", tmpl, err, want)
		}
	}
}

func TestNormalizeFormatVersion(t *testing.T) {
	for in, want := range map[string]string{
		"7.2.2":       "7.2.2",
		"7.2.2-1":     "7.2.2-1",
		"7.2.2.1234":  "7.2.2-1234",
		"7.2.2-1.el9": "7.2.2-1.el9",
		"7.2.2.1.2":   "7.2.2-1.2",
	} {
		if got := normalizeFormatVersion(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
