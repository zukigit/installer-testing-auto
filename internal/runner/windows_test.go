package runner

// Upgrade pairing on windows envs (docs/windows-environment-plan.md §8.2).

import (
	"context"
	"errors"
	"testing"

	"github.com/zukigit/installer-testing-auto/internal/task"
)

const winConfig = `
environment:
  name: win
  os: windows
  image: x
installers:
  - pattern: "bin/*.msi"
`

func TestWindowsNoVersionFormatSkip(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/app-1.0.0-x64.msi": "",
		"bin/app-1.1.0-x64.msi": "",
		"configs/win.yaml":      winConfig,
	})
	cases, skips, errs := buildTestQueue("configs/win.yaml", Options{})
	if errs != 0 {
		t.Fatalf("config errors: %d", errs)
	}
	for _, c := range cases {
		if c.t.Name() == task.UpgradeName {
			t.Errorf("no upgrade case expected without version_format, got %s", c.ri.Basename)
		}
	}
	if len(cases) != 4 { // 2 files x (fresh-install, uninstall)
		t.Errorf("want 4 cases, got %d", len(cases))
	}
	if len(skips) != 2 || skips[0].reason != "no_version_format" || skips[1].reason != "no_version_format" {
		t.Fatalf("want 2 no_version_format skips, got %+v", skips)
	}
}

func TestWindowsVersionFormatPairs(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/app-1.0.0-x64.msi":   "",
		"bin/app-1.1.0-x64.msi":   "",
		"bin/other-0.9.0-x64.msi": "",
		"bin/app-nightly.msi":     "",
		"configs/win.yaml": winConfig + `    version_format: "{package}-{version}-x64.msi"
`,
	})
	cases, skips, errs := buildTestQueue("configs/win.yaml", Options{})
	if errs != 0 {
		t.Fatalf("config errors: %d", errs)
	}
	var upgrades []caseDef
	for _, c := range cases {
		if c.t.Name() == task.UpgradeName {
			upgrades = append(upgrades, c)
		}
	}
	if len(upgrades) != 1 || upgrades[0].ri.Basename != "app-1.1.0-x64.msi" || upgrades[0].from.Basename != "app-1.0.0-x64.msi" {
		t.Fatalf("want one 1.0.0 -> 1.1.0 upgrade case, got %+v", upgrades)
	}
	if got := containerRawName("win", upgrades[0]); got != "ita-win-app-1.1.0-x64-from-1.0.0-upgrade" {
		t.Errorf("container name = %q", got)
	}
	reasons := map[string]string{}
	for _, s := range skips {
		reasons[s.ri.Basename] = s.reason
	}
	want := map[string]string{
		"app-1.0.0-x64.msi":   "no_older_version",
		"other-0.9.0-x64.msi": "no_older_version", // different identity: never pairs with app
		"app-nightly.msi":     "nonstandard_version",
	}
	for k, v := range want {
		if reasons[k] != v {
			t.Errorf("skip %s = %q, want %q (all: %v)", k, reasons[k], v, reasons)
		}
	}
	// fresh-install / uninstall cases are unaffected by version_format
	if n := len(cases) - len(upgrades); n != 8 {
		t.Errorf("want 8 fresh-install/uninstall cases, got %d", n)
	}
}

// --task fresh-install ignores version_format entirely: no skips.
func TestWindowsNoUpgradeTaskNoSkips(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/app-1.0.0-x64.msi": "",
		"configs/win.yaml":      winConfig,
	})
	_, skips, _ := buildTestQueue("configs/win.yaml", Options{Tasks: []string{task.FreshInstallName}})
	if len(skips) != 0 {
		t.Errorf("want no skips without the upgrade task, got %+v", skips)
	}
}

func TestWatcherOnlyForWindowsCases(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/app-1.0.0-x64.msi": "",
		"bin/myapp-0.1.0-1.rpm": "",
		"configs/win.yaml":      winConfig,
		"configs/linux.yaml":    "environment:\n  image: x\ninstallers:\n  - pattern: \"bin/*.rpm\"\n",
	})
	win, _, _ := buildTestQueue("configs/win.yaml", Options{})
	lin, _, _ := buildTestQueue("configs/linux.yaml", Options{})
	if !hasWindowsCases(win) || hasWindowsCases(lin) {
		t.Errorf("hasWindowsCases: windows=%v linux=%v", hasWindowsCases(win), hasWindowsCases(lin))
	}

	// a watcher that cannot start aborts the run before any container
	called := 0
	code := Run(context.Background(), Options{
		ConfigPattern: "configs/win.yaml",
		StartCleanup:  func() (string, string, error) { called++; return "", "", errors.New("no watcher") },
	})
	if code != 2 || called != 1 {
		t.Errorf("windows run: exit=%d watcher calls=%d, want exit 2 and 1 call", code, called)
	}
}
