package runner

// Explicit upgrade pairing with upgrade_from (docs/upgrade-from-plan.md).

import (
	"sort"
	"testing"

	"github.com/zukigit/installer-testing-auto/internal/task"
)

func upgradePairs(cases []caseDef) []string {
	var out []string
	for _, c := range cases {
		if c.t.Name() == task.UpgradeName {
			out = append(out, c.from.Basename+" -> "+c.ri.Basename)
		}
	}
	sort.Strings(out)
	return out
}

func skipReasons(skips []skipDef) map[string]string {
	m := map[string]string{}
	for _, s := range skips {
		m[s.ri.Basename] = s.reason
	}
	return m
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// the plan's example: 0.1.1 -> 0.1.2 only, no 0.1.1 -> 0.1.1
func TestUpgradeFromExample(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/app-0.1.1.rpm": "",
		"bin/app-0.1.2.rpm": "",
		"configs/c.yaml": `
environment:
  image: x
installers:
  - pattern: "bin/app-*.rpm"
    upgrade_from: "bin/app-0.1.1.rpm"
`,
	})
	cases, skips, errs := buildTestQueue("configs/c.yaml", Options{})
	if errs != 0 {
		t.Fatalf("config errors: %d", errs)
	}
	if got := upgradePairs(cases); !eq(got, []string{"app-0.1.1.rpm -> app-0.1.2.rpm"}) {
		t.Errorf("pairs = %v", got)
	}
	if r := skipReasons(skips); len(r) != 1 || r["app-0.1.1.rpm"] != "is_upgrade_from" {
		t.Errorf("skips = %v", r)
	}
}

// unversioned names, a from file outside the pattern, a glob of several
// from files, same base name in another folder = same installer
func TestUpgradeFromGlobAndSameName(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/setup-new.msi":     "",
		"bin/setup-old.msi":     "",
		"bin/old/setup-old.msi": "", // same name as bin/setup-old.msi
		"bin/old/setup-a.msi":   "",
		"configs/w.yaml": `
environment:
  os: windows
  image: x
installers:
  - pattern: "bin/setup-*.msi"
    upgrade_from: "bin/old/*.msi"
`,
	})
	cases, skips, _ := buildTestQueue("configs/w.yaml", Options{})
	want := []string{
		"setup-a.msi -> setup-new.msi",
		"setup-a.msi -> setup-old.msi",
		"setup-old.msi -> setup-new.msi",
	}
	if got := upgradePairs(cases); !eq(got, want) {
		t.Errorf("pairs = %v, want %v (no setup-old -> setup-old)", got, want)
	}
	if len(skips) != 0 {
		t.Errorf("no skips expected (no_version_format must not apply): %v", skipReasons(skips))
	}
	for _, c := range cases {
		if c.t.Name() == task.UpgradeName && c.from.Basename == "setup-a.msi" && c.ri.Basename == "setup-new.msi" {
			if got := containerRawName("w", c); got != "ita-w-setup-new-from-setup-a-upgrade" {
				t.Errorf("container name = %q", got)
			}
		}
	}
}

// no match: skipped with the upgrade task, ignored without it
func TestUpgradeFromNoMatch(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/app-0.1.2.rpm": "",
		"configs/c.yaml": `
environment:
  image: x
installers:
  - pattern: "bin/app-*.rpm"
    upgrade_from: "bin/app-{OLD_VERSION}.rpm"
`,
	})
	_, skips, errs := buildTestQueue("configs/c.yaml", Options{})
	if errs != 0 || skipReasons(skips)["app-0.1.2.rpm"] != "no_upgrade_from_match" {
		t.Errorf("errs=%d skips=%v", errs, skipReasons(skips))
	}
	cases, skips, errs := buildTestQueue("configs/c.yaml", Options{Tasks: []string{task.FreshInstallName}})
	if errs != 0 || len(skips) != 0 || len(cases) != 1 {
		t.Errorf("without upgrade: errs=%d skips=%v cases=%d", errs, skips, len(cases))
	}
	// --var fills the old version in
	cases, _, _ = buildTestQueue("configs/c.yaml", Options{Vars: map[string]string{"OLD_VERSION": "0.1.2"}})
	if len(upgradePairs(cases)) != 0 {
		t.Errorf("0.1.2 -> 0.1.2 must not be a pair: %v", upgradePairs(cases))
	}
}

// --installer filters targets only; version rules are off for the entry
func TestUpgradeFromInstallerFilter(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/app-0.1.0-1.rpm": "",
		"bin/app-0.1.1-1.rpm": "",
		"bin/app-0.1.2-1.rpm": "",
		"configs/c.yaml": `
environment:
  image: x
installers:
  - pattern: "bin/app-*.rpm"
    upgrade_from: "bin/app-0.1.1-1.rpm"
`,
	})
	cases, _, _ := buildTestQueue("configs/c.yaml", Options{Installer: "bin/app-0.1.0-1.rpm"})
	// a downgrade pair is allowed (no version comparison); 0.1.0 is not a from file
	if got := upgradePairs(cases); !eq(got, []string{"app-0.1.1-1.rpm -> app-0.1.0-1.rpm"}) {
		t.Errorf("pairs = %v", got)
	}
}
