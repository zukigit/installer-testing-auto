package task

import (
	"context"
	"io"
	"testing"

	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/logging"
)

// stubEnv is a minimal Environment standing in for the docker environment.
type stubEnv struct {
	execCode int
}

func (s *stubEnv) Name() string                                     { return "stub" }
func (s *stubEnv) Start(ctx context.Context) error                  { return nil }
func (s *stubEnv) Stop(ctx context.Context) error                   { return nil }
func (s *stubEnv) Copy(ctx context.Context, src, dest string) error { return nil }
func (s *stubEnv) Exec(ctx context.Context, cmd []string) (string, string, int, error) {
	return "", "", s.execCode, nil
}

// seqEnv returns the exec codes in order (0 after the list is exhausted).
type seqEnv struct {
	codes []int
}

func (s *seqEnv) Name() string                                     { return "seq" }
func (s *seqEnv) Start(ctx context.Context) error                  { return nil }
func (s *seqEnv) Stop(ctx context.Context) error                   { return nil }
func (s *seqEnv) Copy(ctx context.Context, src, dest string) error { return nil }
func (s *seqEnv) Exec(ctx context.Context, cmd []string) (string, string, int, error) {
	if len(s.codes) == 0 {
		return "", "", 0, nil
	}
	code := s.codes[0]
	s.codes = s.codes[1:]
	return "", "", code, nil
}

// seqCopyEnv fails the N-th copy (1-based); every exec returns code 0.
type seqCopyEnv struct {
	failCopy int // 0 = never
	count    int
}

func (s *seqCopyEnv) Name() string { return "seqcopy" }
func (s *seqCopyEnv) Start(ctx context.Context) error {
	return nil
}
func (s *seqCopyEnv) Stop(ctx context.Context) error { return nil }
func (s *seqCopyEnv) Copy(ctx context.Context, src, dest string) error {
	s.count++
	if s.failCopy == s.count {
		return context.DeadlineExceeded
	}
	return nil
}
func (s *seqCopyEnv) Exec(ctx context.Context, cmd []string) (string, string, int, error) {
	return "", "", 0, nil
}

func testInput() Input { return Input{Installer: testInstaller()} }
func testUpgradeInput() Input {
	return Input{From: testInstaller2(), Installer: testInstaller()}
}

func testLogger() *logging.CaseLogger {
	return logging.New(io.Discard).Case("env", "x", "y", "")
}

func testInstaller() *config.ResolvedInstaller {
	return &config.ResolvedInstaller{
		LocalPath:     "bin/app-1.0.rpm",
		ContainerPath: "/tmp/app-1.0.rpm",
		Basename:      "app-1.0.rpm",
		PackageName:   "app",
		InstallCmd:    "rpm -ivh {installer}",
		UninstallCmd:  "rpm -e {package}",
		UpgradeCmd:    "rpm -Uvh {installer}",
	}
}

func testInstaller2() *config.ResolvedInstaller {
	return &config.ResolvedInstaller{
		LocalPath:     "bin/old-1.0.rpm",
		ContainerPath: "/tmp/old-1.0.rpm",
		Basename:      "old-1.0.rpm",
		PackageName:   "old",
		InstallCmd:    "rpm -ivh {installer}",
	}
}

// ---------- fresh-install ----------

func TestFreshInstallCountsInstallOnly(t *testing.T) {
	// copy excluded from counts: install = 1 passed
	res := FreshInstall{}.Run(context.Background(), &stubEnv{execCode: 0}, testInput(), testLogger())
	if res.Passed != 1 || res.Failed != 0 {
		t.Errorf("fresh-install = %+v, want Passed=1 Failed=0", res)
	}
}

func TestFreshInstallCountsFailedInstall(t *testing.T) {
	// copy passed but uncounted; install failed
	res := FreshInstall{}.Run(context.Background(), &stubEnv{execCode: 1}, testInput(), testLogger())
	if res.Passed != 0 || res.Failed != 1 {
		t.Errorf("fresh-install = %+v, want Passed=0 Failed=1", res)
	}
}

// ---------- uninstall ----------

func TestUninstallCountsSetupAndUninstall(t *testing.T) {
	// copy excluded: setup-install + uninstall = 2 passed
	res := Uninstall{}.Run(context.Background(), &stubEnv{execCode: 0}, testInput(), testLogger())
	if res.Passed != 2 || res.Failed != 0 {
		t.Errorf("uninstall = %+v, want Passed=2 Failed=0", res)
	}
}

func TestUninstallFailsAtSetup(t *testing.T) {
	res := Uninstall{}.Run(context.Background(), &stubEnv{execCode: 1}, testInput(), testLogger())
	if res.Passed != 0 || res.Failed != 1 {
		t.Errorf("uninstall = %+v, want Passed=0 Failed=1 (setup failed)", res)
	}
}

func TestUninstallWithOneCheck(t *testing.T) {
	ri := testInstaller()
	ri.UninstallChecks = &config.Checks{Commands: []config.CommandCheck{{Command: "rpm -q app"}}}
	// setup-install + uninstall + 1 check = 3 passed
	res := Uninstall{}.Run(context.Background(), &stubEnv{execCode: 0}, Input{Installer: ri}, testLogger())
	if res.Passed != 3 || res.Failed != 0 {
		t.Errorf("uninstall = %+v, want Passed=3 Failed=0", res)
	}
}

// ---------- upgrade ----------

func TestUpgradeCountsSetupAndUpgrade(t *testing.T) {
	// copies excluded: setup-install (old) + upgrade = 2 passed
	res := Upgrade{}.Run(context.Background(), &stubEnv{execCode: 0}, testUpgradeInput(), testLogger())
	if res.Passed != 2 || res.Failed != 0 {
		t.Errorf("upgrade = %+v, want Passed=2 Failed=0", res)
	}
}

func TestUpgradeFailsAtSetupInstall(t *testing.T) {
	// old install fails -> nothing to upgrade from
	res := Upgrade{}.Run(context.Background(), &stubEnv{execCode: 1}, testUpgradeInput(), testLogger())
	if res.Passed != 0 || res.Failed != 1 {
		t.Errorf("upgrade = %+v, want Passed=0 Failed=1 (setup failed)", res)
	}
}

func TestUpgradeFailsAtUpgrade(t *testing.T) {
	// setup ok (exit 0), upgrade fails (exit 1)
	res := Upgrade{}.Run(context.Background(), &seqEnv{codes: []int{0, 1}}, testUpgradeInput(), testLogger())
	if res.Passed != 1 || res.Failed != 1 {
		t.Errorf("upgrade = %+v, want Passed=1 (setup) Failed=1 (upgrade)", res)
	}
}

func TestUpgradeMissingUpgradeCommand(t *testing.T) {
	ri := testInstaller()
	ri.UpgradeCmd = ""
	// setup ok, upgrade command missing
	res := Upgrade{}.Run(context.Background(), &stubEnv{execCode: 0}, Input{From: testInstaller2(), Installer: ri}, testLogger())
	if res.Passed != 1 || res.Failed != 1 {
		t.Errorf("upgrade = %+v, want Passed=1 (setup) Failed=1 (missing upgrade cmd)", res)
	}
}

func TestUpgradeMissingFrom(t *testing.T) {
	// defensive: no old installer -> the case cannot run
	res := Upgrade{}.Run(context.Background(), &stubEnv{execCode: 0}, Input{Installer: testInstaller()}, testLogger())
	if res.Passed != 0 || res.Failed != 1 {
		t.Errorf("upgrade = %+v, want Failed=1 (missing pair)", res)
	}
}

func TestUpgradeFailsAtCopy(t *testing.T) {
	// second copy (of the new installer) fails -> setup counted, upgrade fails
	res := Upgrade{}.Run(context.Background(), &seqCopyEnv{failCopy: 2}, testUpgradeInput(), testLogger())
	if res.Passed != 1 || res.Failed != 1 {
		t.Errorf("upgrade = %+v, want Passed=1 (setup) Failed=1 (copy new)", res)
	}
}

func TestUpgradeFirstCopyFail(t *testing.T) {
	// first copy (of the old installer) fails -> nothing counted
	res := Upgrade{}.Run(context.Background(), &seqCopyEnv{failCopy: 1}, testUpgradeInput(), testLogger())
	if res.Passed != 0 || res.Failed != 1 {
		t.Errorf("upgrade = %+v, want Passed=0 Failed=1 (copy old)", res)
	}
}

func TestUpgradeWithOneCheck(t *testing.T) {
	ri := testInstaller()
	ri.UpgradeChecks = &config.Checks{Commands: []config.CommandCheck{{Command: "rpm -q app"}}}
	// setup-install + upgrade + 1 check = 3 passed
	res := Upgrade{}.Run(context.Background(), &stubEnv{execCode: 0}, Input{From: testInstaller2(), Installer: ri}, testLogger())
	if res.Passed != 3 || res.Failed != 0 {
		t.Errorf("upgrade = %+v, want Passed=3 Failed=0", res)
	}
}
