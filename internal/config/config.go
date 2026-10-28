// Package config loads, validates and resolves the YAML environment
// configuration files (one file = one environment).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// DefaultWorkdir is the in-container directory installers are copied to
	// when environment.workdir is not set (linux environments).
	DefaultWorkdir = "/tmp"
	// DefaultWindowsWorkdir is the workdir default for os: windows.
	DefaultWindowsWorkdir = `C:\ita`
	// OSLinux and OSWindows are the environment.os values; "" means linux.
	OSLinux   = "linux"
	OSWindows = "windows"
	// DefaultType is the environment type used when not set in the config.
	DefaultType = "docker"
	// DefaultConfigPattern is the config discovery glob.
	DefaultConfigPattern = "configs/*.yaml"
)

// EnvironmentSpec describes the target environment for one config file.
type EnvironmentSpec struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	OS      string `yaml:"os"` // linux (default) | windows
	Image   string `yaml:"image"`
	Workdir string `yaml:"workdir"`
	// Isolation is the Windows container isolation: process | hyperv |
	// "" (daemon default). Windows only.
	Isolation string `yaml:"isolation"`
}

// IsWindows reports whether the environment is os: windows.
func (s EnvironmentSpec) IsWindows() bool { return s.OS == OSWindows }

// DefaultWorkdirFor returns the OS-dependent workdir default.
func DefaultWorkdirFor(os string) string {
	if os == OSWindows {
		return DefaultWindowsWorkdir
	}
	return DefaultWorkdir
}

// CommandCheck is one shell command check. ExpectedExitCode defaults to 0.
type CommandCheck struct {
	Command                string `yaml:"command"`
	ExpectedExitCode       *int   `yaml:"expected_exit_code"`
	ExpectedStdoutContains string `yaml:"expected_stdout_contains"`
}

// Checks is a check block for one phase. All fields are optional.
type Checks struct {
	MustExist    []string       `yaml:"must_exist"`
	MustNotExist []string       `yaml:"must_not_exist"`
	Commands     []CommandCheck `yaml:"commands"`
}

// InstallerSpec declares a wildcard pattern resolving to the package list,
// plus optional command overrides and per-phase checks.
type InstallerSpec struct {
	Pattern          string `yaml:"pattern"`
	UsePreset        string `yaml:"use_preset"` // preset name in the config's presets file
	InstallCommand   string `yaml:"install_command"`
	UninstallCommand string `yaml:"uninstall_command"`
	UpgradeCommand   string `yaml:"upgrade_command"`
	// VersionFormat is the file-name template the upgrade task uses to pair
	// old and new installers (e.g. "{package}-{version}-x64.msi").
	VersionFormat   string  `yaml:"version_format"`
	InstallChecks   *Checks `yaml:"install_checks"`
	UninstallChecks *Checks `yaml:"uninstall_checks"`
	UpgradeChecks   *Checks `yaml:"upgrade_checks"`

	// poolPattern is the post-substitution pattern widened past variable
	// pinning (v3.4): each provided {VAR} replaced by "*". It drives the
	// upgrade-pairing pool — see Bundle.UpgradePool — and is never YAML.
	poolPattern string
}

// Config is the top level of one YAML config file.
type Config struct {
	Presets     string          `yaml:"presets"` // presets file; "" -> DefaultPresetsPath
	Environment EnvironmentSpec `yaml:"environment"`
	Installers  []InstallerSpec `yaml:"installers"`
}

// Load reads and validates one config file. vars are the pattern variables
// from --var NAME=VALUE: they substitute {NAME} tokens in installer patterns
// (patterns only — see docs/wildcard-checks-pattern-vars-plan.md).
func Load(path string, vars map[string]string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := NewStrictDecoder(data).Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	for i := range cfg.Installers {
		raw := cfg.Installers[i].Pattern
		// v3.4: widenPattern must see the raw token, not the substituted value.
		cfg.Installers[i].poolPattern = widenPattern(raw, vars)
		cfg.Installers[i].Pattern = Substitute(raw, vars)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	if err := cfg.applyPresets(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	if err := cfg.validateGlobPaths(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	if err := cfg.validateVersionFormats(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// Validate applies defaults and checks required values.
func (c *Config) Validate() error {
	if c.Environment.Image == "" {
		return fmt.Errorf("environment.image is required")
	}
	if c.Environment.Type == "" {
		c.Environment.Type = DefaultType
	}
	if err := c.Environment.validateOS(); err != nil {
		return err
	}
	if len(c.Installers) == 0 {
		return fmt.Errorf("at least one installers entry is required")
	}
	for i, inst := range c.Installers {
		if strings.TrimSpace(inst.Pattern) == "" {
			return fmt.Errorf("installers[%d].pattern is required", i)
		}
	}
	return nil
}

// windowsAbsPathRe matches an absolute Windows drive path (C:\...).
var windowsAbsPathRe = regexp.MustCompile(`^[A-Za-z]:\\`)

// validateOS defaults os to linux, rejects unknown values, checks isolation
// (windows only) and the windows workdir (absolute drive path; forward
// slashes normalized to backslashes).
func (s *EnvironmentSpec) validateOS() error {
	switch s.OS {
	case "":
		s.OS = OSLinux
	case OSLinux, OSWindows:
	default:
		return fmt.Errorf("environment.os %q is not supported (supported: %s, %s)", s.OS, OSLinux, OSWindows)
	}
	switch s.Isolation {
	case "":
	case "process", "hyperv":
		if !s.IsWindows() {
			return fmt.Errorf("environment.isolation is only valid with os: %s", OSWindows)
		}
	default:
		return fmt.Errorf("environment.isolation %q is not supported (supported: process, hyperv)", s.Isolation)
	}
	if s.IsWindows() && s.Workdir != "" {
		s.Workdir = strings.ReplaceAll(s.Workdir, "/", `\`)
		if !windowsAbsPathRe.MatchString(s.Workdir) {
			return fmt.Errorf("environment.workdir %q must be an absolute Windows path (e.g. C:\\ita) for os: %s", s.Workdir, OSWindows)
		}
	}
	return nil
}

// validateVersionFormats compiles every installer's version_format (after
// presets are merged): a broken template is a load error even on runs
// without the upgrade task.
func (c *Config) validateVersionFormats() error {
	for i, inst := range c.Installers {
		if inst.VersionFormat == "" {
			continue
		}
		if _, err := CompileVersionFormat(inst.VersionFormat, c.Environment.IsWindows()); err != nil {
			return fmt.Errorf("installers[%d]: %w", i, err)
		}
	}
	return nil
}

// validateGlobPaths checks glob-mode must_exist / must_not_exist entries.
//
// linux: against the restricted charset. An unquoted glob is shell-active:
// anything outside the charset would word-split or execute. Braces are
// allowed as literal characters (POSIX sh has no brace expansion), so a
// leftover {VAR} in a glob path is literal text that matches nothing.
//
// windows: globs are passed quoted to PowerShell, so any character is fine
// except control characters (newline, NUL, ...).
//
// Runs after applyPresets since preset bodies can carry check paths.
func (c *Config) validateGlobPaths() error {
	windows := c.Environment.IsWindows()
	for i, inst := range c.Installers {
		for _, bc := range []struct {
			name   string
			checks *Checks
		}{
			{"install_checks", inst.InstallChecks},
			{"uninstall_checks", inst.UninstallChecks},
			{"upgrade_checks", inst.UpgradeChecks},
		} {
			if bc.checks == nil {
				continue
			}
			for _, paths := range []struct {
				name  string
				items []string
			}{
				{"must_exist", bc.checks.MustExist},
				{"must_not_exist", bc.checks.MustNotExist},
			} {
				for j, path := range paths.items {
					if windows {
						if IsGlobPath(path) && hasControlChar(path) {
							return fmt.Errorf("installers[%d].%s.%s[%d]: glob path %q contains control characters",
								i, bc.name, paths.name, j, path)
						}
						continue
					}
					if IsGlobPath(path) && !validGlobCharset(path) {
						return fmt.Errorf("installers[%d].%s.%s[%d]: glob path %q contains whitespace or shell metacharacters; use a commands check for such paths",
							i, bc.name, paths.name, j, path)
					}
				}
			}
		}
	}
	return nil
}

// IsGlobPath reports whether a check path is a glob entry: it contains any
// glob metacharacter (*, ?, [). Exact paths keep the literal test -e probe.
func IsGlobPath(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

// validGlobCharset reports whether the glob path consists only of characters
// safe to hand to an unquoted shell expansion: letters, digits, '/ . - _ +',
// literal braces, and the glob metachars * ? [ ].
func validGlobCharset(path string) bool {
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/' || r == '.' || r == '-' || r == '_' || r == '+' ||
			r == '{' || r == '}' || r == '*' || r == '?' || r == '[' || r == ']':
		default:
			return false
		}
	}
	return true
}

// hasControlChar reports whether s contains an ASCII/Unicode control character.
func hasControlChar(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return true
		}
	}
	return false
}

// Discover resolves the config glob. An empty pattern falls back to
// configs/*.yaml (all available environments).
func Discover(pattern string) ([]string, error) {
	if pattern == "" {
		pattern = DefaultConfigPattern
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("bad config pattern %q: %w", pattern, err)
	}
	// Presets files under configs/shared/ are never environments, even when a
	// custom glob (e.g. configs/**/*.yaml) reaches into that directory.
	var kept []string
	for _, m := range matches {
		if !isSharedPresetsFile(m) {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("no config files matched %q", pattern)
	}
	return kept, nil
}

// isSharedPresetsFile reports whether path sits in the shared presets
// directory (configs/shared/), which Discover treats as never-environments.
func isSharedPresetsFile(path string) bool {
	return strings.HasPrefix(filepath.ToSlash(filepath.Clean(path)), "configs/shared/")
}
