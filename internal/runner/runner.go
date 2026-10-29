// Package runner orchestrates a full test run: config discovery ->
// package-list resolution -> global case queue -> bounded worker pool ->
// NDJSON logging -> exit code. It prints nothing but JSON log lines.
package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zukigit/installer-testing-auto/internal/cleanup"
	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/env"
	"github.com/zukigit/installer-testing-auto/internal/logging"
	"github.com/zukigit/installer-testing-auto/internal/report"
	"github.com/zukigit/installer-testing-auto/internal/task"
)

// DefaultParallel is the global worker-pool size used when --parallel is not given.
const DefaultParallel = 5

// Options configures one `run` invocation.
type Options struct {
	// ConfigPattern is the config glob; "" discovers all configs/*.yaml.
	ConfigPattern string
	// Tasks is the requested task-name list; empty means all tasks.
	Tasks []string
	// Installer is an optional glob filtering the package list (e.g. "bin/my-app-*.rpm").
	Installer string
	// Vars are the pattern variables (--var NAME=VALUE) substituted into
	// installer patterns at load time (v3.2). Unset names substitute the
	// never-matching {UNSET:NAME} marker (v3.3); provided values also widen
	// to "*" in the upgrade-pairing pool pattern (v3.4).
	Vars map[string]string
	// Parallel is the global max number of simultaneously running cases/containers.
	Parallel int
	// Keep keeps failed cases' containers alive for debugging.
	Keep bool
	// StartCleanup starts the cleanup watcher and returns the run id and run
	// directory (container label ita.run=<id>, host files under the run
	// dir). Called only when the run has os: windows cases — Linux
	// containers are reaped by testcontainers (Ryuk) and leave no host
	// files. nil = no watcher (tests, direct use).
	StartCleanup func() (runID, runDir string, err error)

	// RunID and RunDir are set by Run from StartCleanup.
	RunID  string
	RunDir string
}

// hasWindowsCases reports whether any case runs in an os: windows environment.
func hasWindowsCases(cases []caseDef) bool {
	for _, c := range cases {
		if c.bundle.Config.Environment.IsWindows() {
			return true
		}
	}
	return false
}

// List resolves and prints every config without touching any environment.
func List(configPattern string, vars map[string]string) error {
	paths, err := config.Discover(configPattern)
	if err != nil {
		return err
	}
	if configPattern == "" {
		var windows []string
		paths, windows = config.FilterDefault(paths)
		for _, w := range windows {
			report.ListSkippedWindows(w)
		}
	}
	for _, path := range paths {
		b, err := config.LoadBundle(path, vars)
		if err != nil {
			report.ListError(path, err)
			continue
		}
		report.ListBundle(b)
	}
	return nil
}

type caseDef struct {
	bundle *config.Bundle
	ri     *config.ResolvedInstaller
	t      task.Task
	from   *config.ResolvedInstaller // upgrade task: the OLD installer (nil otherwise)
}

// skipDef is an installer that cannot participate in the upgrade task.
type skipDef struct {
	bundle *config.Bundle
	ri     *config.ResolvedInstaller
	reason string // no_older_version | nonstandard_version | no_version_format | no_upgrade_from_match | is_upgrade_from
}

// Run executes every selected (installer, task) case from every discovered
// environment through one global worker pool and returns the process exit
// code (0 = every case passed).
func Run(ctx context.Context, opts Options) int {
	if opts.Parallel <= 0 {
		opts.Parallel = DefaultParallel
	}
	log := logging.New(os.Stdout)

	paths, err := config.Discover(opts.ConfigPattern)
	if err != nil {
		log.Error(err.Error())
		return 2
	}
	// Default set (no --config): os: windows configs run only when named
	// explicitly (docs/windows-defaults-cleanup-watcher-plan.md §1).
	var windowsLeftOut []string
	if opts.ConfigPattern == "" {
		paths, windowsLeftOut = config.FilterDefault(paths)
	}

	cases, envs, skips, configErrors := buildQueue(paths, opts, log)
	if len(cases) == 0 {
		msg := "no cases to run (check --task/--installer filters and the package list in bin/)"
		if len(windowsLeftOut) > 0 {
			msg += " (os: windows configs run only with an explicit --config)"
		}
		log.Error(msg)
		return 1
	}

	// The watcher starts before any container; no watcher -> no run
	// (docs/windows-defaults-cleanup-watcher-plan.md §3).
	if opts.StartCleanup != nil && hasWindowsCases(cases) {
		id, dir, err := opts.StartCleanup()
		if err != nil {
			log.Error(err.Error())
			return 2
		}
		opts.RunID, opts.RunDir = id, dir
	}

	log.RunStart(len(paths), len(cases), opts.Parallel, opts.Vars)

	// upgrade skips are progress events, not failures — emitted before any case
	for _, sk := range skips {
		cl := log.Case(sk.bundle.EnvName(), task.UpgradeName, sk.ri.Basename, "")
		cl.CaseSkipped(sk.reason)
	}

	tr := &envTracker{log: log, info: envs}
	names := &nameAssigner{used: map[string]int{}}

	var totals struct {
		mu             sync.Mutex
		passed, failed int
	}
	var wg sync.WaitGroup
	queue := make(chan caseDef)
	for i := 0; i < opts.Parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range queue {
				p, f := runCase(ctx, log, tr, names, c, opts)
				totals.mu.Lock()
				totals.passed += p
				totals.failed += f
				totals.mu.Unlock()
			}
		}()
	}
	for _, c := range cases {
		queue <- c
	}
	close(queue)
	wg.Wait()

	log.RunEnd(totals.passed, totals.failed, configErrors)
	if totals.failed > 0 || configErrors > 0 {
		return 1
	}
	return 0
}

// buildQueue resolves every config and expands the case matrix
// (installer files x selected tasks), applying the --installer filter.
// Bad configs are counted and logged as `error` events, not fatal.
// The upgrade task additionally pairs every installable with the older
// versions of the same package (whole resolved list of the config) and
// emits `case_skipped` events for the ones that cannot participate.
func buildQueue(paths []string, opts Options, log *logging.Emitter) ([]caseDef, map[string]*envInfo, []skipDef, int) {
	var cases []caseDef
	envs := map[string]*envInfo{}
	configErrors := 0
	var skips []skipDef
	upgradeSelected := taskSelected(opts.Tasks, task.UpgradeName)
	for _, path := range paths {
		b, err := config.LoadBundle(path, opts.Vars)
		if err != nil {
			log.Error(fmt.Sprintf("config %s: %v", path, err))
			configErrors++
			continue
		}
		envName := b.EnvName()
		info, ok := envs[envName]
		if !ok {
			info = &envInfo{spec: b.Config.Environment}
			envs[envName] = info
		}
		for _, ri := range b.Installers {
			inScope := true
			if opts.Installer != "" {
				ok, merr := filepath.Match(opts.Installer, ri.LocalPath)
				if merr != nil {
					log.Error(fmt.Sprintf("bad --installer glob %q: %v", opts.Installer, merr))
					configErrors++
					continue
				}
				inScope = ok
			}
			for _, t := range task.All() {
				if !taskSelected(opts.Tasks, t.Name()) {
					continue
				}
				if t.Name() == task.UpgradeName {
					buildUpgradeCases(b, ri, upgradePool(b), inScope, upgradeSelected, &cases, &skips, info)
					continue
				}
				if !inScope {
					continue
				}
				cases = append(cases, caseDef{bundle: b, ri: ri, t: t})
				info.total++
			}
		}
	}
	return cases, envs, skips, configErrors
}

// upgradePool is the whole-config old-version pool for upgrade pairing —
// the resolved package list widened past pattern-variable pinning (v3.4, see
// docs/upgrade-pool-pattern-vars-plan.md: each installer entry also globs its
// pattern with provided variables as "*", so older versions in bin/ still
// pair even when --var pins one version). Hand-built bundles (tests) fall
// back to the resolved list.
func upgradePool(b *config.Bundle) []*config.ResolvedInstaller {
	if b.UpgradePool != nil {
		return b.UpgradePool
	}
	return b.Installers
}

// buildUpgradeCases resolves one installer's upgrade participation:
// skip events (no older version / non-standard version) or one case per older
// version of the same package. Skips are emitted only for in-scope installers.
func buildUpgradeCases(b *config.Bundle, ri *config.ResolvedInstaller, pool []*config.ResolvedInstaller, inScope, upgradeSelected bool, cases *[]caseDef, skips *[]skipDef, info *envInfo) {
	if !upgradeSelected {
		return
	}
	if ri.UpgradeFromSet {
		// explicit pairing (docs/upgrade-from-plan.md): no version rules
		if !inScope {
			return
		}
		if len(ri.UpgradeFrom) == 0 {
			*skips = append(*skips, skipDef{bundle: b, ri: ri, reason: "no_upgrade_from_match"})
			return
		}
		froms := ri.UpgradeFromPairs(b.Config.Environment.IsWindows())
		if len(froms) == 0 {
			*skips = append(*skips, skipDef{bundle: b, ri: ri, reason: "is_upgrade_from"})
			return
		}
		for _, f := range froms {
			*cases = append(*cases, caseDef{bundle: b, ri: ri, t: task.Upgrade{}, from: f})
			info.total++
		}
		return
	}
	var older []*config.ResolvedInstaller
	if _, ok := config.ParseVersion(ri.Version); ok {
		aMtime := fileMtime(ri.LocalPath)
		for _, o := range pool {
			if o == ri || o.LocalPath == ri.LocalPath || o.UpgradeIdentity() != ri.UpgradeIdentity() {
				continue
			}
			if _, ok := config.ParseVersion(o.Version); !ok {
				continue
			}
			if config.CompareInstallerVersions(o.Version, ri.Version, fileMtime(o.LocalPath), aMtime) < 0 {
				older = append(older, o)
			}
		}
	}
	if !inScope {
		return // filtered out: not part of this run at all
	}
	if len(older) == 0 {
		// skip event only for installers that are otherwise part of the run
		if ri.NoVersionFormat {
			*skips = append(*skips, skipDef{bundle: b, ri: ri, reason: "no_version_format"})
		} else if _, ok := config.ParseVersion(ri.Version); !ok {
			*skips = append(*skips, skipDef{bundle: b, ri: ri, reason: "nonstandard_version"})
		} else {
			*skips = append(*skips, skipDef{bundle: b, ri: ri, reason: "no_older_version"})
		}
		return
	}
	for _, o := range older {
		*cases = append(*cases, caseDef{bundle: b, ri: ri, t: task.Upgrade{}, from: o})
		info.total++
	}
}

// taskSelected: empty request means all tasks.
func taskSelected(requested []string, name string) bool {
	if len(requested) == 0 {
		return true
	}
	for _, r := range requested {
		if r == name {
			return true
		}
	}
	return false
}

func runCase(ctx context.Context, log *logging.Emitter, tr *envTracker, names *nameAssigner, c caseDef, opts Options) (int, int) {
	if ctx.Err() != nil {
		return 0, 0 // interrupted: drop remaining cases
	}
	envName := c.bundle.EnvName()
	spec := c.bundle.Config.Environment
	cl := log.Case(envName, c.t.Name(), c.ri.Basename, fromName(c))
	started := time.Now()

	tr.caseStarted(envName)

	envOpts := env.Options{
		Name:          envName,
		Image:         spec.Image,
		Workdir:       spec.Workdir,
		ContainerName: names.assign(containerRawName(envName, c)),
		OS:            spec.OS,
		Isolation:     spec.Isolation,
		RunID:         opts.RunID,
		RunDir:        opts.RunDir,
	}
	e, err := env.New(spec.Type, envOpts)
	if err == nil {
		err = e.Start(ctx)
	}

	var passed, failed int
	var kept bool
	var containerID string
	if err != nil {
		cl.Error(fmt.Sprintf("start %s environment: %v", spec.Type, err))
		failed = 1
	} else {
		containerID = containerIDOf(e)
		cl.CaseStart(containerID, envOpts.ContainerName)
		res := c.t.Run(ctx, e, task.Input{Installer: c.ri, From: c.from}, cl)
		passed, failed = res.Passed, res.Failed
		if opts.Keep && failed > 0 {
			kept = true // container stays up for debugging
			if opts.RunDir != "" {
				// before case_end: the watcher must never remove a kept container
				entry := cleanup.KeptEntry{ID: containerID, Name: envOpts.ContainerName}
				if hp, ok := e.(interface{ HostPaths() []string }); ok {
					entry.Paths = hp.HostPaths()
				}
				if kerr := cleanup.RecordKept(opts.RunDir, entry); kerr != nil {
					cl.Error(fmt.Sprintf("record kept container: %v", kerr))
				}
			}
		} else if serr := e.Stop(context.WithoutCancel(ctx)); serr != nil {
			cl.Error(fmt.Sprintf("teardown: %v", serr))
		}
	}

	cl.CaseEnd(containerID, failed == 0, passed, failed, time.Since(started).Milliseconds(), kept)
	tr.caseFinished(envName, passed, failed)
	return passed, failed
}

// fromName is the OLD installer basename for upgrade cases (envelope field).
func fromName(c caseDef) string {
	if c.from == nil {
		return ""
	}
	return c.from.Basename
}

// containerRawName builds the deterministic raw container name; upgrade cases
// carry both versions: ita-<env>-<new-stem>-from-<old-version>-upgrade.
func containerRawName(envName string, c caseDef) string {
	base := c.ri.Basename
	taskPart := c.t.Name()
	if c.from != nil {
		base = strings.TrimSuffix(base, filepath.Ext(base))
		fromPart := c.from.Version
		if fromPart == "" { // upgrade_from without a parsable version
			fromPart = strings.TrimSuffix(c.from.Basename, filepath.Ext(c.from.Basename))
		}
		taskPart = "from-" + fromPart + "-" + c.t.Name()
	}
	return strings.ToLower("ita-" + envName + "-" + base + "-" + taskPart)
}

// fileMtime is the portable "created date" (modification time) of a file.
func fileMtime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func containerIDOf(e env.Environment) string {
	if ider, ok := e.(interface{ ContainerID() string }); ok {
		return ider.ContainerID()
	}
	return ""
}

// envInfo tracks per-environment case aggregation for env_start/env_end.
type envInfo struct {
	spec           config.EnvironmentSpec
	total          int
	started        int
	finished       int
	passed, failed int
	startedEmitted bool
}

type envTracker struct {
	mu   sync.Mutex
	log  *logging.Emitter
	info map[string]*envInfo
}

func (t *envTracker) caseStarted(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	i, ok := t.info[name]
	if !ok {
		return
	}
	i.started++
	if !i.startedEmitted {
		i.startedEmitted = true
		workdir := i.spec.Workdir
		if workdir == "" {
			workdir = config.DefaultWorkdirFor(i.spec.OS)
		}
		t.log.EnvStart(name, i.spec.Image, workdir, i.spec.OS)
	}
}

func (t *envTracker) caseFinished(name string, passed, failed int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	i, ok := t.info[name]
	if !ok {
		return
	}
	i.finished++
	i.passed += passed
	i.failed += failed
	if i.finished == i.total {
		t.log.EnvEnd(name, i.passed, i.failed)
	}
}

// nameAssigner builds meaningful, deterministic container names and appends a
// numeric suffix on collisions.
type nameAssigner struct {
	mu   sync.Mutex
	used map[string]int
}

var nameSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func (n *nameAssigner) assign(raw string) string {
	clean := strings.Trim(nameSanitizer.ReplaceAllString(strings.ToLower(raw), "-"), "-")
	n.mu.Lock()
	defer n.mu.Unlock()
	n.used[clean]++
	if n.used[clean] == 1 {
		return clean
	}
	return fmt.Sprintf("%s-%d", clean, n.used[clean])
}
