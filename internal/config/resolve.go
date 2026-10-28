package config

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Bundle is one fully resolved config: the environment spec plus the
// wildcard-resolved package list (the installers to test).
type Bundle struct {
	Path       string               `json:"-" yaml:"-"`
	Config     *Config              `json:"-" yaml:"-"`
	Installers []*ResolvedInstaller `json:"-" yaml:"-"`

	// UpgradePool is the old-version pool for the upgrade task (v3.4): the
	// resolved list widened past pattern-variable pinning — each installer
	// entry also globs its pattern with every provided {VAR} replaced by
	// "*", so older versions of the same package in bin/ still pair even
	// when --var pins one version. Pinned installers appear as the same
	// pointers, then path-deduplicated extras; extras are from-candidates
	// only, never targets.
	UpgradePool []*ResolvedInstaller `json:"-" yaml:"-"`
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
	LocalPath     string // local path, e.g. bin/myapp_1.2.3_amd64.deb
	ContainerPath string // path inside the environment
	Basename      string // file name without directories
	PackageName   string // package name derived from the file name ({package} placeholder)
	Version       string // MAJOR.MINOR.PATCH[-Suffix], "" if not standard
	// UpgradeKey is the upgrade-pairing identity: two installers pair only
	// when their keys are equal. From version_format when set, else
	// PackageName. Never used for command placeholders.
	UpgradeKey string
	// NoVersionFormat marks a windows installer without version_format: the
	// upgrade task skips it (no built-in version rule on windows).
	NoVersionFormat bool
	InstallCmd      string  // template (may be empty -> no default for ext)
	UninstallCmd    string  // template (may be empty)
	UpgradeCmd      string  // template (may be empty)
	InstallChecks   *Checks `yaml:"install_checks"`
	UninstallChecks *Checks `yaml:"uninstall_checks"`
	UpgradeChecks   *Checks `yaml:"upgrade_checks"`
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
		workdir = DefaultWorkdirFor(c.Environment.OS)
		c.Environment.Workdir = workdir // write the default back so list output shows it
	}
	var vfs []*VersionFormat // per installer entry, nil = no version_format
	for i, spec := range c.Installers {
		var vf *VersionFormat
		if spec.VersionFormat != "" {
			var err error
			if vf, err = CompileVersionFormat(spec.VersionFormat, c.Environment.IsWindows()); err != nil {
				return nil, fmt.Errorf("installers[%d]: %w", i, err)
			}
		}
		vfs = append(vfs, vf)
	}
	osName := c.Environment.OS
	poolSeen := map[string]bool{}
	// Pass 1: the pinned list drives the case matrix (unchanged) and starts
	// the upgrade pool as the same pointers, deduplicated by path.
	for i, spec := range c.Installers {
		matches, err := filepath.Glob(spec.Pattern)
		if err != nil {
			return nil, fmt.Errorf("bad installer pattern %q: %w", spec.Pattern, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("installers[%d].pattern %q matched no files", i, spec.Pattern)
		}
		for _, local := range matches {
			ri, err := resolveInstaller(spec, local, workdir, osName, vfs[i])
			if err != nil {
				return nil, fmt.Errorf("installers[%d]: %w", i, err)
			}
			b.Installers = append(b.Installers, ri)
			if !poolSeen[local] {
				poolSeen[local] = true
				b.UpgradePool = append(b.UpgradePool, ri)
			}
		}
	}
	// Pass 2 (v3.4): widen each entry's pattern past variable pinning and add
	// every additionally matched file to the upgrade pool as a from-candidate
	// only — never a target. Best-effort: the widened pattern is always a
	// superset of the pinned one, so a glob error or zero extras can never
	// invalidate the config.
	for i, spec := range c.Installers {
		if spec.poolPattern == "" || spec.poolPattern == spec.Pattern {
			continue
		}
		extras, err := filepath.Glob(spec.poolPattern)
		if err != nil {
			continue
		}
		for _, local := range extras {
			if poolSeen[local] {
				continue
			}
			ri, err := resolveInstaller(spec, local, workdir, osName, vfs[i])
			if err != nil {
				continue
			}
			poolSeen[local] = true
			b.UpgradePool = append(b.UpgradePool, ri)
		}
	}
	return b, nil
}

// LoadBundle loads, validates and resolves one config file in one step.
// vars are the pattern variables (see Load).
func LoadBundle(path string, vars map[string]string) (*Bundle, error) {
	cfg, err := Load(path, vars)
	if err != nil {
		return nil, err
	}
	return cfg.Resolve(path)
}

// ContainerJoin joins a container directory and a file name with the
// container OS's separator — never the host's.
func ContainerJoin(osName, dir, base string) string {
	if osName == OSWindows {
		return strings.TrimRight(dir, `\`) + `\` + base
	}
	return path.Join(dir, base)
}

func resolveInstaller(spec InstallerSpec, local, workdir, osName string, vf *VersionFormat) (*ResolvedInstaller, error) {
	base := filepath.Base(local)
	ri := &ResolvedInstaller{
		LocalPath:       local,
		ContainerPath:   ContainerJoin(osName, workdir, base),
		Basename:        base,
		PackageName:     derivePackage(base),
		InstallCmd:      spec.InstallCommand,
		UninstallCmd:    spec.UninstallCommand,
		UpgradeCmd:      spec.UpgradeCommand,
		InstallChecks:   spec.InstallChecks,
		UninstallChecks: spec.UninstallChecks,
		UpgradeChecks:   spec.UpgradeChecks,
	}
	if ri.InstallCmd == "" {
		ri.InstallCmd = defaultInstallCmd(base)
	}
	if ri.UninstallCmd == "" {
		ri.UninstallCmd = defaultUninstallCmd(base)
	}
	if ri.UpgradeCmd == "" {
		ri.UpgradeCmd = defaultUpgradeCmd(base)
	}
	// Upgrade pairing (version + identity): version_format when set (any
	// OS), else the built-in .deb/.rpm rules on linux, none on windows.
	ri.UpgradeKey = ri.PackageName
	switch {
	case vf != nil:
		if v, id, ok := vf.Match(base); ok {
			ri.Version = v
			ri.UpgradeKey = id
			if osName == OSWindows {
				ri.UpgradeKey = strings.ToLower(id) // matching is case-insensitive on windows
			}
		}
	case osName == OSWindows:
		ri.NoVersionFormat = true
	default:
		if v, ok := ExtractVersionFromFilename(base); ok {
			ri.Version = v.String()
		}
	}
	return ri, nil
}

// UpgradeIdentity is the pairing identity (UpgradeKey, falling back to
// PackageName for hand-built installers).
func (ri *ResolvedInstaller) UpgradeIdentity() string {
	if ri.UpgradeKey != "" {
		return ri.UpgradeKey
	}
	return ri.PackageName
}

// msiCommand builds the default msiexec command for one action (/i or /x).
// msiexec is a GUI-subsystem program, so Start-Process -Wait is required;
// 3010/1641 (reboot required/initiated) count as success because containers
// cannot reboot; on failure the tail of the verbose log is printed.
func msiCommand(action, logName string) string {
	log := "{installer}." + logName + ".log"
	return "$p = Start-Process msiexec.exe -Wait -PassThru -ArgumentList '" + action + ` "{installer}" /qn /norestart /l*v "` + log + `"'` + "\n" +
		`if ($p.ExitCode -in 0, 3010, 1641) { if ($p.ExitCode) { "msiexec exit $($p.ExitCode) (reboot required) treated as success" }; exit 0 }` + "\n" +
		"Get-Content '" + log + "' -Tail 40; exit $p.ExitCode"
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
	case ".msi":
		return msiCommand("/i", "install")
	case ".ps1":
		return "& '{installer}'"
	default:
		return ""
	}
}

// defaultUpgradeCmd returns the extension-based upgrade command default.
func defaultUpgradeCmd(base string) string {
	switch strings.ToLower(filepath.Ext(base)) {
	case ".rpm":
		return "rpm -Uvh {installer}" // the upgrade flag, vs -ivh for fresh-install
	case ".deb":
		return "dpkg -i {installer}" // dpkg -i upgrades in place
	case ".msi":
		return msiCommand("/i", "upgrade") // major upgrades; minor upgrades need an override
	default:
		return ""
	}
}

// defaultUninstallCmd returns the extension-based uninstall command default.
// ".sh", ".ps1" and non-MSI Windows types have no default and must be set
// explicitly in the config.
func defaultUninstallCmd(base string) string {
	switch strings.ToLower(filepath.Ext(base)) {
	case ".deb":
		return "dpkg -r {package}"
	case ".rpm":
		return "rpm -e {package}"
	case ".msi":
		return msiCommand("/x", "uninstall") // by MSI file: no ProductCode lookup needed
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
