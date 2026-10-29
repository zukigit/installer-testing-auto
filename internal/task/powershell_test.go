package task

// Tasks speak the environment's dialect (docs/windows-environment-plan.md §5).

import (
	"context"
	"testing"

	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/shell"
)

// psEnv records every Exec argv and speaks PowerShell.
type psEnv struct{ args [][]string }

func (s *psEnv) Name() string                                     { return "ps" }
func (s *psEnv) Start(ctx context.Context) error                  { return nil }
func (s *psEnv) Shell() shell.Dialect                             { return shell.PowerShell }
func (s *psEnv) Stop(ctx context.Context) error                   { return nil }
func (s *psEnv) Copy(ctx context.Context, src, dest string) error { return nil }
func (s *psEnv) Exec(ctx context.Context, cmd []string) (string, string, int, error) {
	s.args = append(s.args, cmd)
	return "", "", 0, nil
}

func TestTasksUsePowerShellDialect(t *testing.T) {
	ri := &config.ResolvedInstaller{
		LocalPath: "bin/app-1.1.0.msi", ContainerPath: `C:\ita\app-1.1.0.msi`, Basename: "app-1.1.0.msi",
		InstallCmd: "install {installer}", UninstallCmd: "uninstall {installer}", UpgradeCmd: "upgrade {installer}",
	}
	from := &config.ResolvedInstaller{
		LocalPath: "bin/app-1.0.0.msi", ContainerPath: `C:\ita\app-1.0.0.msi`, Basename: "app-1.0.0.msi",
		InstallCmd: "install {installer}",
	}
	runs := []struct {
		name string
		run  func(e *psEnv) Result
		want []string
	}{
		{"fresh-install", func(e *psEnv) Result {
			return FreshInstall{}.Run(context.Background(), e, Input{Installer: ri}, testLogger())
		},
			[]string{`install C:\ita\app-1.1.0.msi`}},
		{"uninstall", func(e *psEnv) Result {
			return Uninstall{}.Run(context.Background(), e, Input{Installer: ri}, testLogger())
		},
			[]string{`install C:\ita\app-1.1.0.msi`, `uninstall C:\ita\app-1.1.0.msi`}},
		{"upgrade", func(e *psEnv) Result {
			return Upgrade{}.Run(context.Background(), e, Input{Installer: ri, From: from}, testLogger())
		}, []string{`install C:\ita\app-1.0.0.msi`, `upgrade C:\ita\app-1.1.0.msi`}},
	}
	for _, r := range runs {
		e := &psEnv{}
		if res := r.run(e); res.Failed != 0 {
			t.Errorf("%s: failed=%d", r.name, res.Failed)
		}
		if len(e.args) != len(r.want) {
			t.Fatalf("%s: %d execs, want %d", r.name, len(e.args), len(r.want))
		}
		for i, argv := range e.args {
			got, ok := shell.ScriptOf(argv)
			if !ok || argv[0] != "powershell.exe" || got != r.want[i] {
				t.Errorf("%s exec %d = %q (%v), want PowerShell %q", r.name, i, got, ok, r.want[i])
			}
		}
	}
}
