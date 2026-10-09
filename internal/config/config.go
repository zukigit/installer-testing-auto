// Package config loads, validates and resolves the YAML environment
// configuration files (one file = one environment).
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultWorkdir is the in-container directory installers are copied to
	// when environment.workdir is not set.
	DefaultWorkdir = "/tmp"
	// DefaultType is the environment type used when not set in the config.
	DefaultType = "docker"
	// DefaultConfigPattern is the config discovery glob.
	DefaultConfigPattern = "configs/*.yaml"
)

// EnvironmentSpec describes the target environment for one config file.
type EnvironmentSpec struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	Image   string `yaml:"image"`
	Workdir string `yaml:"workdir"`
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
	Pattern          string  `yaml:"pattern"`
	InstallCommand   string  `yaml:"install_command"`
	UninstallCommand string  `yaml:"uninstall_command"`
	InstallChecks    *Checks `yaml:"install_checks"`
	UninstallChecks  *Checks `yaml:"uninstall_checks"`
}

// Config is the top level of one YAML config file.
type Config struct {
	Environment EnvironmentSpec `yaml:"environment"`
	Installers  []InstallerSpec `yaml:"installers"`
}

// Load reads and validates one config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject unknown fields (typo protection)
	cfg := &Config{}
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
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
	if len(matches) == 0 {
		return nil, fmt.Errorf("no config files matched %q", pattern)
	}
	return matches, nil
}
