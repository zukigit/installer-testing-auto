package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPresetsPath is the presets file a config uses when it does not set
// the top-level presets key.
const DefaultPresetsPath = "configs/shared/presets.yaml"

// NewStrictDecoder returns a YAML decoder that rejects unknown fields
// (typo protection), shared by config and presets-file parsing.
func NewStrictDecoder(data []byte) *yaml.Decoder {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec
}

// PresetsFile is the top level of a shared presets file. Unlike a config it
// has no environment and no installers: preset values may carry anything an
// InstallerSpec carries except pattern (and use_preset itself).
type PresetsFile struct {
	Presets map[string]InstallerSpec `yaml:"presets"`
}

// loadPresetsFile reads and strictly parses one presets file. An absent file
// is a hard error here; callers decide whether that is allowed.
func loadPresetsFile(path string) (map[string]InstallerSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read presets file %s: %w", path, err)
	}
	dec := NewStrictDecoder(data)
	pf := &PresetsFile{}
	if err := dec.Decode(pf); err != nil {
		return nil, fmt.Errorf("parse presets file %s: %w", path, err)
	}
	if pf.Presets == nil {
		return nil, fmt.Errorf("presets file %s has no presets map", path)
	}
	for name, preset := range pf.Presets {
		if strings.TrimSpace(preset.Pattern) != "" {
			return nil, fmt.Errorf("presets file %s: preset %q must not set pattern", path, name)
		}
		if strings.TrimSpace(preset.UsePreset) != "" {
			return nil, fmt.Errorf("presets file %s: preset %q must not set use_preset", path, name)
		}
	}
	return pf.Presets, nil
}

// applyPresets fills blanks in the installers from their named presets.
// Per key, non-empty local values win; presets fill only what is unset.
// Commands stay empty when neither entry nor preset defines them, so the
// resolve step still applies its extension defaults afterwards.
func (c *Config) applyPresets() error {
	used := false
	for _, inst := range c.Installers {
		if inst.UsePreset != "" {
			used = true
			break
		}
	}
	if !used && c.Presets == "" {
		// Opt-in: without use_preset entries and without an explicit presets
		// path, the (possibly nonexistent) default file is never read.
		return nil
	}

	path := c.Presets
	if path == "" {
		path = DefaultPresetsPath
	}
	presets, err := loadPresetsFile(path)
	if err != nil {
		return err
	}
	for i, inst := range c.Installers {
		if inst.UsePreset == "" {
			continue
		}
		preset, ok := presets[inst.UsePreset]
		if !ok {
			return fmt.Errorf("installers[%d]: use_preset %q not defined in %s", i, inst.UsePreset, path)
		}
		applyPreset(&c.Installers[i], &preset)
	}
	return nil
}

// applyPreset merges one preset into one installer spec, local values left
// untouched.
func applyPreset(spec *InstallerSpec, preset *InstallerSpec) {
	if spec.InstallCommand == "" {
		spec.InstallCommand = preset.InstallCommand
	}
	if spec.UninstallCommand == "" {
		spec.UninstallCommand = preset.UninstallCommand
	}
	if spec.UpgradeCommand == "" {
		spec.UpgradeCommand = preset.UpgradeCommand
	}
	if spec.VersionFormat == "" {
		spec.VersionFormat = preset.VersionFormat
	}
	if spec.InstallChecks == nil {
		spec.InstallChecks = preset.InstallChecks
	}
	if spec.UninstallChecks == nil {
		spec.UninstallChecks = preset.UninstallChecks
	}
	if spec.UpgradeChecks == nil {
		spec.UpgradeChecks = preset.UpgradeChecks
	}
}
