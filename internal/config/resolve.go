package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Bundle is one fully resolved config: the environment spec plus the
// wildcard-resolved package list (the installers to test).
type Bundle struct {
	Path       string               `json:"-" yaml:"-"`
	Config     *Config              `json:"-" yaml:"-"`
	Installers []*ResolvedInstaller `json:"-" yaml:"-"`
}

// EnvName returns the configured environment name, falling back to the
// config file basename.
func (b *Bundle) EnvName() string {
	if b.Config.Environment.Name != "" {
		return b.Config.Environment.Name
	}
	base := filepath.Base(b.Path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// ResolvedInstaller is one installer file matched by a pattern, with
// concrete commands (placeholders still expandable via Expand).
type ResolvedInstaller struct {
	LocalPath       string  // local path, e.g. bin/myapp_1.2.3_amd64.deb
	ContainerPath   string  // path inside the environment
	Basename        string  // file name without directories
	PackageName     string  // package name derived from the file name
	InstallCmd      string  // template (may be empty -> no default for ext)
	UninstallCmd    string  // template (may be empty)
	InstallChecks   *Checks `yaml:"install_checks"`
	UninstallChecks *Checks `yaml:"uninstall_checks"`
}

// Expand substitutes {installer}, {basename} and {package} in a command
// template.
func (ri *ResolvedInstaller) Expand(tmpl string) string {
	return strings.NewReplacer(
		"{installer}", ri.ContainerPath,
		"{basename}", ri.Basename,
		"{package}", ri.PackageName,
	).Replace(tmpl)
}

// Resolve globs every installer pattern and builds the package list.
// A pattern that matches nothing is an error (usually a typo or a missing
// file in bin/).
func (c *Config) Resolve(path string) (*Bundle, error) {
	b := &Bundle{Path: path, Config: c}
	workdir := c.Environment.Workdir
	if workdir == "" {
		workdir = DefaultWorkdir
		c.Environment.Workdir = workdir // write the default back so list output shows it
	}
	for i, spec := range c.Installers {
		matches, err := filepath.Glob(spec.Pattern)
		if err != nil {
			return nil, fmt.Errorf("bad installer pattern %q: %w", spec.Pattern, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("installers[%d].pattern %q matched no files", i, spec.Pattern)
		}
		for _, local := range matches {
			ri, err := resolveInstaller(spec, local, workdir)
			if err != nil {
				return nil, fmt.Errorf("installers[%d]: %w", i, err)
			}
			b.Installers = append(b.Installers, ri)
		}
	}
	return b, nil
}

// LoadBundle loads, validates and resolves one config file in one step.
func LoadBundle(path string) (*Bundle, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return cfg.Resolve(path)
}

func resolveInstaller(spec InstallerSpec, local, workdir string) (*ResolvedInstaller, error) {
	base := filepath.Base(local)
	ri := &ResolvedInstaller{
		LocalPath:       local,
		ContainerPath:   filepath.ToSlash(filepath.Join(workdir, base)),
		Basename:        base,
		PackageName:     derivePackage(base),
		InstallCmd:      spec.InstallCommand,
		UninstallCmd:    spec.UninstallCommand,
		InstallChecks:   spec.InstallChecks,
		UninstallChecks: spec.UninstallChecks,
	}
	if ri.InstallCmd == "" {
		ri.InstallCmd = defaultInstallCmd(base)
	}
	if ri.UninstallCmd == "" {
		ri.UninstallCmd = defaultUninstallCmd(base)
	}
	return ri, nil
}

// defaultInstallCmd returns the extension-based install command default.
func defaultInstallCmd(base string) string {
	switch strings.ToLower(filepath.Ext(base)) {
	case ".deb":
		return "dpkg -i {installer}"
	case ".rpm":
		return "rpm -ivh {installer}"
	case ".sh":
		return "sh {installer}"
	default:
		return ""
	}
}

// defaultUninstallCmd returns the extension-based uninstall command default.
// ".sh" has no default and must be set explicitly in the config.
func defaultUninstallCmd(base string) string {
	switch strings.ToLower(filepath.Ext(base)) {
	case ".deb":
		return "dpkg -r {package}"
	case ".rpm":
		return "rpm -e {package}"
	default:
		return ""
	}
}

// derivePackage extracts the package name from an installer file name.
//
//   - .deb: Debian layout name_version_arch.deb -> strip from the first "_"
//   - .rpm: name-version-release.arch.rpm -> strip the .arch part, then the
//     last two dash-separated segments (release + version)
//   - anything else: basename without extension
func derivePackage(base string) string {
	lower := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lower, ".deb"):
		return strings.SplitN(base[:len(base)-4], "_", 2)[0]
	case strings.HasSuffix(lower, ".rpm"):
		trimmed := base[:len(base)-4]
		// strip the ".arch" suffix if present (name-1.2.3-1.el9.x86_64)
		if idx := strings.LastIndex(trimmed, "."); idx > strings.LastIndex(trimmed, "-") {
			trimmed = trimmed[:idx]
		}
		parts := strings.Split(trimmed, "-")
		if len(parts) >= 3 {
			// release + version are the last two segments
			return strings.Join(parts[:len(parts)-2], "-")
		}
		return trimmed
	default:
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
}
