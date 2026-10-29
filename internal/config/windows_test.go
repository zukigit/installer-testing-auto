package config

// Tests for os: windows (docs/windows-environment-plan.md §4, §8).

import (
	"strings"
	"testing"
)

func loadWin(t *testing.T, files map[string]string) (*Bundle, error) {
	t.Helper()
	writeTestFiles(t, files)
	return LoadBundle("configs/test.yaml", nil)
}

func TestWindowsDefaultsAndPaths(t *testing.T) {
	b, err := loadWin(t, map[string]string{
		"bin/app-7.2.2-x64.msi": "",
		"configs/test.yaml": `
environment:
  os: windows
  image: mcr.microsoft.com/windows/servercore:ltsc2022
  isolation: process
installers:
  - pattern: "bin/*.msi"
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := b.Config.Environment
	if env.OS != OSWindows || env.Workdir != `C:\ita` || env.Isolation != "process" {
		t.Fatalf("env = %+v", env)
	}
	ri := b.Installers[0]
	if ri.ContainerPath != `C:\ita\app-7.2.2-x64.msi` {
		t.Errorf("ContainerPath = %q", ri.ContainerPath)
	}
	if ri.PackageName != "app-7.2.2-x64" {
		t.Errorf("PackageName = %q", ri.PackageName)
	}
	if !ri.NoVersionFormat || ri.Version != "" {
		t.Errorf("windows without version_format: NoVersionFormat=%v Version=%q", ri.NoVersionFormat, ri.Version)
	}
}

func TestLinuxDefaultsUnchanged(t *testing.T) {
	b, err := loadWin(t, map[string]string{
		"bin/myapp_1.2.3_amd64.deb": "",
		"configs/test.yaml": `
environment:
  image: ubuntu:24.04
installers:
  - pattern: "bin/*.deb"
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	ri := b.Installers[0]
	if b.Config.Environment.OS != OSLinux || b.Config.Environment.Workdir != "/tmp" ||
		ri.ContainerPath != "/tmp/myapp_1.2.3_amd64.deb" || ri.Version != "1.2.3" ||
		ri.UpgradeKey != "myapp" || ri.NoVersionFormat {
		t.Fatalf("linux resolve changed: env=%+v ri=%+v", b.Config.Environment, ri)
	}
}

func TestWindowsWorkdirNormalized(t *testing.T) {
	b, err := loadWin(t, map[string]string{
		"bin/a.msi": "",
		"configs/test.yaml": `
environment:
  os: windows
  image: x
  workdir: D:/tests/ita/
installers:
  - pattern: "bin/a.msi"
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Installers[0].ContainerPath; got != `D:\tests\ita\a.msi` {
		t.Errorf("ContainerPath = %q", got)
	}
}

func TestEnvironmentValidation(t *testing.T) {
	cases := map[string]string{
		"os: macos":                     `environment.os "macos" is not supported`,
		"isolation: process":            "only valid with os: windows",
		"os: windows\n  isolation: vm":  `environment.isolation "vm" is not supported`,
		"os: windows\n  workdir: /tmp":  "must be an absolute Windows path",
		"os: windows\n  workdir: 'ita'": "must be an absolute Windows path",
		"os: windows\n  workdir: 'C:'":  "must be an absolute Windows path",
	}
	for envYAML, want := range cases {
		_, err := loadWin(t, map[string]string{
			"bin/a.msi": "",
			"configs/test.yaml": "environment:\n  image: x\n  " + envYAML + `
installers:
  - pattern: "bin/a.msi"
`,
		})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", envYAML, err, want)
		}
	}
}

func TestWindowsGlobValidation(t *testing.T) {
	cfg := func(os, path string) string {
		return "environment:\n  image: x\n  os: " + os + `
installers:
  - pattern: "bin/a.msi"
    install_checks:
      must_exist: ["` + path + `"]
`
	}
	// spaces and other non-POSIX-safe characters are fine on windows
	if _, err := loadWin(t, map[string]string{"bin/a.msi": "", "configs/test.yaml": cfg("windows", `C:\\Program Files\\Job Arranger\\*.dll`)}); err != nil {
		t.Errorf("windows glob with spaces: %v", err)
	}
	if _, err := loadWin(t, map[string]string{"bin/a.msi": "", "configs/test.yaml": cfg("windows", `C:\\x\\*\ty`)}); err == nil ||
		!strings.Contains(err.Error(), "control characters") {
		t.Errorf("control character: err = %v", err)
	}
	// linux keeps the charset restriction
	if _, err := loadWin(t, map[string]string{"bin/a.msi": "", "configs/test.yaml": cfg("linux", `/opt/my app/*.so`)}); err == nil {
		t.Error("linux glob with a space must be rejected")
	}
}

func TestMSIAndPS1Defaults(t *testing.T) {
	wantInstall := `$si = New-Object System.Diagnostics.ProcessStartInfo -ArgumentList 'msiexec.exe', '/i "{installer}" /qn /norestart /l*v "{installer}.install.log"'` + "\n" +
		"$si.UseShellExecute = $false\n" +
		"$p = [System.Diagnostics.Process]::Start($si); $p.WaitForExit()\n" +
		`if ($p.ExitCode -in 0, 3010, 1641) { if ($p.ExitCode) { "msiexec exit $($p.ExitCode) (reboot required) treated as success" }; exit 0 }` + "\n" +
		`Get-Content '{installer}.install.log' -Tail 40 -ErrorAction SilentlyContinue; exit $p.ExitCode`
	if strings.Contains(wantInstall, "Start-Process") {
		t.Fatal("msi default must not use Start-Process")
	}
	if got := defaultInstallCmd("App.MSI"); got != wantInstall {
		t.Errorf("msi install =\n%s\nwant\n%s", got, wantInstall)
	}
	uninstall := defaultUninstallCmd("app.msi")
	if !strings.Contains(uninstall, `'msiexec.exe', '/x "{installer}" /qn /norestart /l*v "{installer}.uninstall.log"'`) ||
		!strings.Contains(uninstall, `Get-Content '{installer}.uninstall.log' -Tail 40`) {
		t.Errorf("msi uninstall = %s", uninstall)
	}
	upgrade := defaultUpgradeCmd("app.msi")
	if !strings.Contains(upgrade, `'/i "{installer}" /qn /norestart /l*v "{installer}.upgrade.log"'`) {
		t.Errorf("msi upgrade = %s", upgrade)
	}
	if got := defaultInstallCmd("setup.ps1"); got != "& '{installer}'" {
		t.Errorf("ps1 install = %q", got)
	}
	for _, base := range []string{"setup.ps1", "setup.exe", "app.msix", "app.zip"} {
		if defaultUninstallCmd(base) != "" || defaultUpgradeCmd(base) != "" {
			t.Errorf("%s must have no uninstall/upgrade default", base)
		}
	}
	for _, base := range []string{"setup.exe", "app.msix", "app.zip"} {
		if defaultInstallCmd(base) != "" {
			t.Errorf("%s must have no install default", base)
		}
	}
}

func TestVersionFormatResolve(t *testing.T) {
	files := map[string]string{
		"bin/JobArranger-Agentd-7.2.1-x64.msi": "",
		"bin/jobarranger-agentd-7.2.2-x64.msi": "",
		"bin/jobarranger-agentd-latest.msi":    "",
		"configs/test.yaml": `
environment:
  os: windows
  image: x
installers:
  - pattern: "bin/*.msi"
    version_format: "{package}-{version}-x64.msi"
    install_command: "echo {package}"
`,
	}
	b, err := loadWin(t, files)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*ResolvedInstaller{}
	for _, ri := range b.Installers {
		got[ri.Basename] = ri
	}
	old, cur, odd := got["JobArranger-Agentd-7.2.1-x64.msi"], got["jobarranger-agentd-7.2.2-x64.msi"], got["jobarranger-agentd-latest.msi"]
	if old.Version != "7.2.1" || cur.Version != "7.2.2" || old.UpgradeKey != "jobarranger-agentd" || cur.UpgradeKey != old.UpgradeKey {
		t.Errorf("pairing fields: old=%+v cur=%+v", old, cur)
	}
	if odd.Version != "" || odd.NoVersionFormat {
		t.Errorf("non-matching file must be nonstandard: %+v", odd)
	}
	// {package} placeholder is unaffected by version_format
	if exp := cur.Expand(cur.InstallCmd); exp != "echo jobarranger-agentd-7.2.2-x64" {
		t.Errorf("{package} expansion = %q", exp)
	}
}

func TestVersionFormatLoadErrorAndPreset(t *testing.T) {
	_, err := loadWin(t, map[string]string{
		"bin/a.msi": "",
		"configs/test.yaml": `
environment:
  image: x
installers:
  - pattern: "bin/a.msi"
    version_format: "a-{vers}.msi"
`,
	})
	if err == nil || !strings.Contains(err.Error(), "installers[0]: version_format") {
		t.Errorf("broken version_format: err = %v", err)
	}

	b, err := loadWin(t, map[string]string{
		"bin/app-1.2.3.sh": "",
		"configs/shared/presets.yaml": `
presets:
  sh:
    version_format: "app-{version}.sh"
`,
		"configs/test.yaml": `
environment:
  image: x
installers:
  - pattern: "bin/*.sh"
    use_preset: sh
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ri := b.Installers[0]; ri.Version != "1.2.3" || ri.UpgradeKey != "app" {
		t.Errorf("preset version_format on linux: %+v", ri)
	}
}

func TestContainerJoin(t *testing.T) {
	cases := [][4]string{
		{OSLinux, "/tmp", "a.deb", "/tmp/a.deb"},
		{OSLinux, "/tmp/", "a.deb", "/tmp/a.deb"},
		{"", "/opt/x", "a.rpm", "/opt/x/a.rpm"},
		{OSWindows, `C:\ita`, "a.msi", `C:\ita\a.msi`},
		{OSWindows, `C:\ita\`, "a.msi", `C:\ita\a.msi`},
		{OSWindows, `C:\`, "a.msi", `C:\a.msi`},
	}
	for _, c := range cases {
		if got := ContainerJoin(c[0], c[1], c[2]); got != c[3] {
			t.Errorf("ContainerJoin(%q, %q, %q) = %q, want %q", c[0], c[1], c[2], got, c[3])
		}
	}
}
