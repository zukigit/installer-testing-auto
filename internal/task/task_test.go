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

func testLogger() *logging.CaseLogger {
	return logging.New(io.Discard).Case("env", "x", "y")
}

func testInstaller() *config.ResolvedInstaller {
	return &config.ResolvedInstaller{
		LocalPath:     "bin/app-1.0.rpm",
		ContainerPath: "/tmp/app-1.0.rpm",
		Basename:      "app-1.0.rpm",
		PackageName:   "app",
		InstallCmd:    "rpm -ivh {installer}",
		UninstallCmd:  "rpm -e {package}",
	}
}

func TestFreshInstallCountsStepsWithoutChecks(t *testing.T) {
	// no checks configured: copy + install must count as 2 passed
	res := FreshInstall{}.Run(context.Background(), &stubEnv{execCode: 0}, testInstaller(), testLogger())
	if res.Passed != 2 || res.Failed != 0 {
		t.Errorf("fresh-install = %+v, want Passed=2 Failed=0", res)
	}
}

func TestFreshInstallCountsFailedInstall(t *testing.T) {
	// install fails: copy passed, install failed
	res := FreshInstall{}.Run(context.Background(), &stubEnv{execCode: 1}, testInstaller(), testLogger())
	if res.Passed != 1 || res.Failed != 1 {
		t.Errorf("fresh-install = %+v, want Passed=1 Failed=1", res)
	}
}

func TestUninstallCountsStepsWithoutChecks(t *testing.T) {
	// no checks configured: copy + setup-install + uninstall = 3 passed
	res := Uninstall{}.Run(context.Background(), &stubEnv{execCode: 0}, testInstaller(), testLogger())
	if res.Passed != 3 || res.Failed != 0 {
		t.Errorf("uninstall = %+v, want Passed=3 Failed=0", res)
	}
}

func TestUninstallFailsAtSetup(t *testing.T) {
	res := Uninstall{}.Run(context.Background(), &stubEnv{execCode: 1}, testInstaller(), testLogger())
	if res.Passed != 1 || res.Failed != 1 {
		t.Errorf("uninstall = %+v, want Passed=1 (copy) Failed=1 (setup)", res)
	}
}

func TestUninstallWithOneCheck(t *testing.T) {
	ri := testInstaller()
	ri.UninstallChecks = &config.Checks{Commands: []config.CommandCheck{{Command: "rpm -q app"}}}
	// copy + setup-install + uninstall + 1 check = 4 passed
	res := Uninstall{}.Run(context.Background(), &stubEnv{execCode: 0}, ri, testLogger())
	if res.Passed != 4 || res.Failed != 0 {
		t.Errorf("uninstall = %+v, want Passed=4 Failed=0", res)
	}
}
