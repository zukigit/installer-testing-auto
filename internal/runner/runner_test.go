package runner

// Regression tests for the v3.4 upgrade-pool widening
// (docs/upgrade-pool-pattern-vars-plan.md): the upgrade task pairs from the
// older versions of the same package present in bin/ even when a --var
// variable pins the resolved list to one version. Pinning still drives the
// case matrix: pooled older versions are from-candidates only.

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/zukigit/installer-testing-auto/internal/logging"
	"github.com/zukigit/installer-testing-auto/internal/task"
)

// writeScenario creates files relative to a temp root (bin/, configs/) and
// chdirs into it — installer pattern globs are relative paths, like in a run
// from the repo root.
func writeScenario(t *testing.T, files map[string]string) {
	t.Helper()
	tmp := t.TempDir()
	for path, content := range files {
		full := filepath.Join(tmp, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
}

const testConfigYAML = `
environment:
  name: rhel9
  image: test:latest
installers:
  - pattern: "bin/myapp-{VERSION}*.rpm"
`

// buildTestQueue runs buildQueue with discarded event output.
func buildTestQueue(configs string, opts Options) (cases []caseDef, skips []skipDef, cfgErrs int) {
	cases, _, skips, cfgErrs = buildQueue([]string{configs}, opts, logging.New(io.Discard))
	return cases, skips, cfgErrs
}

// Regression test for the motivating v3.4 case: bin/ holds 0.1.0 and 0.2.0,
// --var VERSION=0.2.0 — before v3.4 this emitted no_older_version and built
// no case; now exactly one upgrade case pairs 0.1.0 -> 0.2.0.
func TestUpgradePairsFromOlderVersionBeyondVariablePin(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/myapp-0.1.0-1.rpm": "",
		"bin/myapp-0.2.0-1.rpm": "",
		"configs/myapp.yaml":    testConfigYAML,
	})
	cases, skips, cfgErrs := buildTestQueue("configs/myapp.yaml", Options{
		Tasks: []string{task.UpgradeName},
		Vars:  map[string]string{"VERSION": "0.2.0"},
	})
	if cfgErrs != 0 {
		t.Fatalf("unexpected config errors: %d", cfgErrs)
	}
	if len(cases) != 1 {
		t.Fatalf("want exactly 1 upgrade case, got %d", len(cases))
	}
	c := cases[0]
	if filepath.Base(c.ri.LocalPath) != "myapp-0.2.0-1.rpm" {
		t.Errorf("upgrade target must be the pinned 0.2.0 file, got %s", c.ri.LocalPath)
	}
	if c.from == nil || filepath.Base(c.from.LocalPath) != "myapp-0.1.0-1.rpm" {
		t.Errorf("upgrade must pair from the older 0.1.0 file in bin/, got %+v", c.from)
	}
	if len(skips) != 0 {
		t.Errorf("no skip expected now that the older version pairs, got %+v", skips)
	}
	// the from installer is resolved from the same installer entry, so it
	// carries the entry's (default rpm) install command for the setup step
	if c.from.InstallCmd != "rpm -ivh {installer}" {
		t.Errorf("from installer must carry the entry's install command, got %q", c.from.InstallCmd)
	}
}

// Pinning still drives the case matrix: only the pinned 0.2.0 file becomes a
// fresh-install/uninstall/upgrade target. The pooled 0.1.0 extra gets no
// cases and no skip events.
func TestVariablePinStillDrivesTheCaseMatrix(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/myapp-0.1.0-1.rpm": "",
		"bin/myapp-0.2.0-1.rpm": "",
		"configs/myapp.yaml":    testConfigYAML,
	})
	vars := map[string]string{"VERSION": "0.2.0"}
	for _, name := range []string{task.FreshInstallName, task.UninstallName} {
		cases, skips, cfgErrs := buildTestQueue("configs/myapp.yaml", Options{Tasks: []string{name}, Vars: vars})
		if cfgErrs != 0 {
			t.Fatalf("%s: unexpected config errors: %d", name, cfgErrs)
		}
		if len(cases) != 1 {
			t.Fatalf("%s: want exactly 1 case, got %d", name, len(cases))
		}
		if filepath.Base(cases[0].ri.LocalPath) != "myapp-0.2.0-1.rpm" {
			t.Errorf("%s: target must be the pinned 0.2.0 file, got %s", name, cases[0].ri.LocalPath)
		}
		if len(skips) != 0 {
			t.Errorf("%s: no skips expected for the pinned list, got %+v", name, skips)
		}
	}
}

// Without any older version of the package in bin/, the pinned target is
// still skipped with no_older_version (unchanged).
func TestNoOlderVersionSkipUnchanged(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/myapp-0.2.0-1.rpm": "",
		"configs/myapp.yaml":    testConfigYAML,
	})
	cases, skips, cfgErrs := buildTestQueue("configs/myapp.yaml", Options{
		Tasks: []string{task.UpgradeName},
		Vars:  map[string]string{"VERSION": "0.2.0"},
	})
	if cfgErrs != 0 {
		t.Fatalf("unexpected config errors: %d", cfgErrs)
	}
	if len(cases) != 0 {
		t.Fatalf("want no upgrade cases, got %d", len(cases))
	}
	if len(skips) != 1 || skips[0].reason != "no_older_version" ||
		filepath.Base(skips[0].ri.LocalPath) != "myapp-0.2.0-1.rpm" {
		t.Fatalf("want a no_older_version skip for myapp-0.2.0-1.rpm, got %+v", skips)
	}
}

// One case per older version: bin/ holds 0.1.0 and 0.1.1, both older than
// the pinned 0.2.0 target — the per-pair rule (unchanged), now fed from the
// widened pool.
func TestOneUpgradeCasePerOlderVersion(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/myapp-0.1.0-1.rpm": "",
		"bin/myapp-0.1.1-1.rpm": "",
		"bin/myapp-0.2.0-1.rpm": "",
		"configs/myapp.yaml":    testConfigYAML,
	})
	cases, skips, cfgErrs := buildTestQueue("configs/myapp.yaml", Options{
		Tasks: []string{task.UpgradeName},
		Vars:  map[string]string{"VERSION": "0.2.0"},
	})
	if cfgErrs != 0 {
		t.Fatalf("unexpected config errors: %d", cfgErrs)
	}
	if len(cases) != 2 {
		t.Fatalf("want 2 upgrade cases (one per older version), got %d", len(cases))
	}
	if len(skips) != 0 {
		t.Fatalf("no skips expected, got %+v", skips)
	}
	got := map[string]string{}
	for _, c := range cases {
		if filepath.Base(c.ri.LocalPath) != "myapp-0.2.0-1.rpm" {
			t.Errorf("all targets must be the pinned 0.2.0 file, got %s", c.ri.LocalPath)
		}
		got[filepath.Base(c.from.LocalPath)] = c.from.Version
	}
	if _, ok := got["myapp-0.1.0-1.rpm"]; !ok {
		t.Errorf("missing pair from myapp-0.1.0-1.rpm, got %v", got)
	}
	if _, ok := got["myapp-0.1.1-1.rpm"]; !ok {
		t.Errorf("missing pair from myapp-0.1.1-1.rpm, got %v", got)
	}
}

// Pinning the OLDER version pairs nothing (no upgrade to an older target):
// the target is 0.1.0, nothing in the widened pool is older than it.
func TestPinningOlderVersionStillSkips(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/myapp-0.1.0-1.rpm": "",
		"bin/myapp-0.2.0-1.rpm": "",
		"configs/myapp.yaml":    testConfigYAML,
	})
	cases, skips, cfgErrs := buildTestQueue("configs/myapp.yaml", Options{
		Tasks: []string{task.UpgradeName},
		Vars:  map[string]string{"VERSION": "0.1.0"},
	})
	if cfgErrs != 0 {
		t.Fatalf("unexpected config errors: %d", cfgErrs)
	}
	if len(cases) != 0 {
		t.Fatalf("no upgrade to an older target, got %d cases", len(cases))
	}
	if len(skips) != 1 || skips[0].reason != "no_older_version" {
		t.Fatalf("want a no_older_version skip for the pinned 0.1.0 target, got %+v", skips)
	}
}

// Without a --var the matrix is byte-identical to pre-v3.4 behavior: the
// config pattern has no {VAR} token at all, both files are targets, 0.2.0
// pairs from 0.1.0, and 0.1.0 is skipped (no_older_version). (A pattern that
// still *contains* an unsubstituted {VAR} is a v3.3 rejection before any
// queue exists.)
func TestNoVariablesBehaviorUnchanged(t *testing.T) {
	writeScenario(t, map[string]string{
		"bin/myapp-0.1.0-1.rpm": "",
		"bin/myapp-0.2.0-1.rpm": "",
		"configs/myapp.yaml": `
environment:
  name: rhel9
  image: test:latest
installers:
  - pattern: "bin/myapp-*.rpm"
`,
	})
	cases, skips, cfgErrs := buildTestQueue("configs/myapp.yaml", Options{
		Tasks: []string{task.UpgradeName},
	})
	if cfgErrs != 0 {
		t.Fatalf("unexpected config errors: %d", cfgErrs)
	}
	if len(cases) != 1 {
		t.Fatalf("want 1 upgrade case (0.1.0 -> 0.2.0), got %d", len(cases))
	}
	if filepath.Base(cases[0].ri.LocalPath) != "myapp-0.2.0-1.rpm" ||
		filepath.Base(cases[0].from.LocalPath) != "myapp-0.1.0-1.rpm" {
		t.Errorf("want the pair 0.1.0 -> 0.2.0, got %+v", cases[0])
	}
	if len(skips) != 1 || skips[0].reason != "no_older_version" {
		t.Fatalf("want the no_older_version skip for myapp-0.1.0-1.rpm, got %+v", skips)
	}
}
