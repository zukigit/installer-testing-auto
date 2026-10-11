package config

import (
	"testing"
	"time"
)

func TestExtractVersionFromFilename(t *testing.T) {
	cases := []struct {
		file string
		want string
		ok   bool
	}{
		{"myapp-0.1.1-1.rpm", "0.1.1-1", true},
		{"myapp-0.1.2-1.rpm", "0.1.2-1", true},
		{"myapp-0.1.1.rpm", "0.1.1", true},
		{"zabbix-agent2-7.2.2-1.el9.x86_64.rpm", "7.2.2-1.el9", true}, // rpms: version-release, non-numeric suffix ok
		{"app-fast-0.1.1-1.rpm", "0.1.1-1", true},                     // multi-dash package name
		{"myapp_0.1.1-1_amd64.deb", "0.1.1-1", true},
		{"myapp_0.1.1-1.deb", "0.1.1-1", true},
		{"myapp-latest.rpm", "", false},
		{"demoapp_1.0_amd64.deb", "", false}, // 2-part version -> non-standard
		{"install.sh", "", false},
	}
	for _, c := range cases {
		v, ok := ExtractVersionFromFilename(c.file)
		if ok != c.ok || (ok && v.String() != c.want) {
			t.Errorf("ExtractVersionFromFilename(%q) = %q,%v; want %q,%v", c.file, v.String(), ok, c.want, c.ok)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if v, ok := ParseVersion("1.2.3"); !ok || v.Major != 1 || v.Minor != 2 || v.Patch != 3 || v.HasSuffix {
		t.Errorf("ParseVersion(1.2.3) = %+v %v", v, ok)
	}
	if v, ok := ParseVersion("1.2.3-4"); !ok || v.Suffix != "4" || !v.HasSuffix {
		t.Errorf("ParseVersion(1.2.3-4) = %+v %v", v, ok)
	}
	if _, ok := ParseVersion("1.2"); ok {
		t.Error("ParseVersion(1.2) should not parse")
	}
	if _, ok := ParseVersion("1.2.3.4"); ok {
		t.Error("ParseVersion(1.2.3.4) should not parse")
	}
}

func TestCompareInstallerVersions(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	now := time.Now()

	// simple cases without mtime involvement
	simple := []struct {
		a, b string
		want int
	}{
		{"0.1.1-1", "0.1.1-2", -1},
		{"0.1.1-2", "0.1.1-1", 1},
		{"0.1.1-1", "0.1.2-1", -1}, // patch decides
		{"0.0.9-9", "0.1.1-1", -1}, // minor decides
		{"1.0.0-1", "0.9.9-1", 1},  // major decides
		{"0.1.1-1", "0.1.1-1", 0},
		{"0.1.1-1", "0.1.1", -1}, // no-suffix ranks above
		{"0.1.1", "0.1.1-1", 1},
		{"2.0.0", "1.99.99-9", 1},
	}
	for _, c := range simple {
		if got := CompareInstallerVersions(c.a, c.b, now, now); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	// identical suffix strings are identical versions even with different mtimes
	if got := CompareInstallerVersions("0.1.1-1", "0.1.1-1", now, old); got != 0 {
		t.Errorf("identical version = %d, want 0", got)
	}

	// non-numeric, DIFFERING suffixes fall back to the file dates
	if got := CompareInstallerVersions("0.1.1-beta", "0.1.1-rc1", old, now); got != -1 {
		t.Errorf("beta by date (a older) = %d, want -1", got)
	}
	if got := CompareInstallerVersions("0.1.1-beta", "0.1.1-rc1", now, old); got != 1 {
		t.Errorf("beta by date (a newer) = %d, want 1", got)
	}
	if got := CompareInstallerVersions("0.1.1-beta", "0.1.1-1", old, now); got != -1 {
		t.Errorf("mixed numeric/non-numeric by date = %d, want -1", got)
	}

	// mixed numeric/non-numeric suffixes: the numeric prefix decides
	if got := CompareInstallerVersions("0.1.1-1.el9", "0.1.1-2.el9", now, now); got != -1 {
		t.Errorf("1.el9 < 2.el9 = %d, want -1", got)
	}

	// invalid versions never compare
	if got := CompareInstallerVersions("junk", "0.1.1-1", now, now); got != 0 {
		t.Errorf("invalid a = %d, want 0", got)
	}
}
