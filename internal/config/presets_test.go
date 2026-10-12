package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTestFiles creates the given relative path -> content files under a
// temp dir and chdirs into it (all files must be passed in one call).
func writeTestFiles(t *testing.T, files map[string]string) {
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

const testPresetsYAML = `
presets:
  pkg-rpm:
    install_checks:
      must_exist: ["/opt/pkg"]
    uninstall_checks:
      must_not_exist: ["/var/log/pkg"]
    upgrade_command: "echo custom && rpm -Uvh {installer}"
  pkg-deb:
    uninstall_command: "apt-get purge -y {package}"
    uninstall_checks:
      must_not_exist: ["/var/log/pkg"]
`

// withPresets merges the standard presets files into a file map.
func withPresets(files map[string]string) map[string]string {
	out := map[string]string{
		"configs/shared/presets.yaml": testPresetsYAML,
		"configs/shared/other.yaml":   testPresetsYAML,
	}
	for k, v := range files {
		out[k] = v
	}
	return out
}

// Preset checks and commands are applied to the installer entry.
func TestApplyPresetFillsBlanks(t *testing.T) {
	writeTestFiles(t, withPresets(map[string]string{
		"configs/test.yaml": `
presets: configs/shared/presets.yaml
environment:
  image: test:latest
installers:
  - pattern: "bin/*.rpm"
    use_preset: pkg-rpm
`,
	}))
	cfg, err := Load("configs/test.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	inst := cfg.Installers[0]
	if inst.InstallChecks == nil || !reflect.DeepEqual(inst.InstallChecks.MustExist, []string{"/opt/pkg"}) {
		t.Errorf("install_checks from preset not applied: %+v", inst.InstallChecks)
	}
	if inst.UninstallChecks == nil || !reflect.DeepEqual(inst.UninstallChecks.MustNotExist, []string{"/var/log/pkg"}) {
		t.Errorf("uninstall_checks from preset not applied: %+v", inst.UninstallChecks)
	}
	if inst.UpgradeCommand != "echo custom && rpm -Uvh {installer}" {
		t.Errorf("upgrade_command from preset not applied: %q", inst.UpgradeCommand)
	}
}

// A preset command beats the extension default; missing keys still fall
// through to the extension defaults.
func TestPresetBeatsExtensionDefault(t *testing.T) {
	writeTestFiles(t, withPresets(map[string]string{
		"configs/shared/presets.yaml": `
presets:
  pkg-rpm:
    upgrade_command: "echo custom && rpm -Uvh {installer}"
`,
		"configs/test.yaml": `
presets: configs/shared/presets.yaml
environment:
  image: test:latest
installers:
  - pattern: "bin/*.rpm"
    use_preset: pkg-rpm
`,
		"bin/pkg-1.0.0-1.el9.rpm": "",
	}))
	b, err := LoadBundle("configs/test.yaml")
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	ri := b.Installers[0]
	if ri.UpgradeCmd != "echo custom && rpm -Uvh {installer}" {
		t.Errorf("preset upgrade_command should beat extension default, got %q", ri.UpgradeCmd)
	}
	if ri.InstallCmd != "rpm -ivh {installer}" {
		t.Errorf("unset install_command should fall back to extension default, got %q", ri.InstallCmd)
	}
	if ri.UninstallCmd != "rpm -e {package}" {
		t.Errorf("unset uninstall_command should fall back to extension default, got %q", ri.UninstallCmd)
	}
}

// A locally defined key beats the preset for that key only.
func TestLocalKeyBeatsPreset(t *testing.T) {
	writeTestFiles(t, withPresets(map[string]string{
		"configs/shared/presets.yaml": `
presets:
  pkg-rpm:
    install_checks:
      must_exist: ["/from/preset"]
    install_command: "rpm -ivh {installer} --from-preset"
`,
		"configs/test.yaml": `
presets: configs/shared/presets.yaml
environment:
  image: test:latest
installers:
  - pattern: "bin/*.rpm"
    use_preset: pkg-rpm
    install_checks:
      must_exist: ["/local"]
`,
	}))
	cfg, err := Load("configs/test.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	inst := cfg.Installers[0]
	if !reflect.DeepEqual(inst.InstallChecks.MustExist, []string{"/local"}) {
		t.Errorf("local install_checks should replace the preset block: %+v", inst.InstallChecks)
	}
	if inst.InstallCommand != "rpm -ivh {installer} --from-preset" {
		t.Errorf("entry install_command was empty, should be filled from the preset: %q", inst.InstallCommand)
	}
}

// Unknown preset names are a load error naming both files.
func TestUnknownPresetError(t *testing.T) {
	writeTestFiles(t, withPresets(map[string]string{
		"configs/test.yaml": `
presets: configs/shared/presets.yaml
environment:
  image: test:latest
installers:
  - pattern: "bin/*.rpm"
    use_preset: nope
`,
	}))
	_, err := Load("configs/test.yaml")
	if err == nil {
		t.Fatal("expected load error for unknown preset")
	}
	for _, want := range []string{`use_preset "nope" not defined`, "configs/shared/presets.yaml", "configs/test.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err.Error(), want)
		}
	}
}

// Missing presets file: error when used, error when explicitly named but
// unused, and OK when the default path is not needed.
func TestMissingPresetsFile(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		config  string
		wantErr bool
	}{
		{
			name:    "default path used, file missing",
			files:   map[string]string{},
			config:  "\nenvironment:\n  image: test:latest\ninstallers:\n  - pattern: \"bin/*.rpm\"\n    use_preset: pkg-rpm\n",
			wantErr: true,
		},
		{
			name:    "explicit path used, file missing",
			files:   map[string]string{},
			config:  "\npresets: configs/shared/other.yaml\nenvironment:\n  image: test:latest\ninstallers:\n  - pattern: \"bin/*.rpm\"\n    use_preset: pkg-rpm\n",
			wantErr: true,
		},
		{
			name:    "explicit path unused",
			files:   map[string]string{},
			config:  "\npresets: configs/shared/missing-on-purpose.yaml\nenvironment:\n  image: test:latest\ninstallers:\n  - pattern: \"bin/*.rpm\"\n",
			wantErr: true,
		},
		{
			name:    "default path unused",
			files:   withPresets(nil),
			config:  "\nenvironment:\n  image: test:latest\ninstallers:\n  - pattern: \"bin/*.rpm\"\n",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"configs/test.yaml": tt.config}
			for k, v := range tt.files {
				files[k] = v
			}
			writeTestFiles(t, files)
			_, err := Load("configs/test.yaml")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// Presets files are parsed strictly: unknown keys and pattern are rejected.
func TestPresetsFileStrict(t *testing.T) {
	tests := []struct {
		name    string
		presets string
		wantErr string
	}{
		{
			name:    "pattern key",
			presets: "presets:\n  p:\n    pattern: \"x\"\n",
			wantErr: "must not set pattern",
		},
		{
			name:    "unknown top key",
			presets: "environment:\n  image: x\npresets:\n  p: {}\n",
			wantErr: "parse presets file",
		},
		{
			name:    "unknown nested key",
			presets: "presets:\n  p:\n    unknowable: 1\n",
			wantErr: "parse presets file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeTestFiles(t, map[string]string{
				"configs/shared/presets.yaml": tt.presets,
				"configs/test.yaml": `
presets: configs/shared/presets.yaml
environment:
  image: test:latest
installers:
  - pattern: "bin/*.rpm"
    use_preset: p
`,
			})
			_, err := Load("configs/test.yaml")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// Discover never treats anything under configs/shared/ as an environment.
// (Go globs do not match files at non-terminal ** levels, so a subdirectory
// env config plus the pattern configs/*/*.yaml is used to exercise it.)
func TestDiscoverSkipsSharedDir(t *testing.T) {
	writeTestFiles(t, withPresets(map[string]string{
		"configs/sub/env.yaml": "",
	}))
	paths, err := Discover("configs/*/*.yaml")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	want := []string{"configs/sub/env.yaml"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("Discover = %v, want %v (shared/ entries must be skipped)", paths, want)
	}
}

// The whole feature is opt-in: defaults remain valid without presets.
func TestNoPresetsStillWorks(t *testing.T) {
	writeTestFiles(t, map[string]string{
		"bin/pkg-1.0.0-1.el9.rpm": "",
		"configs/test.yaml": `
environment:
  image: test:latest
installers:
  - pattern: "bin/*.rpm"
`,
	})
	b, err := LoadBundle("configs/test.yaml")
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if len(b.Installers) != 1 || b.Installers[0].UpgradeCmd != "rpm -Uvh {installer}" {
		t.Fatalf("baseline resolution changed: %+v", b.Installers[0])
	}
}
