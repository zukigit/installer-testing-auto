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
	// Parallel is the global max number of simultaneously running cases/containers.
	Parallel int
	// Keep keeps failed cases' containers alive for debugging.
	Keep bool
}

// List resolves and prints every config without touching any environment.
func List(configPattern string) error {
	paths, err := config.Discover(configPattern)
	if err != nil {
		return err
	}
	for _, path := range paths {
		b, err := config.LoadBundle(path)
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

	cases, envs, configErrors := buildQueue(paths, opts, log)
	if len(cases) == 0 {
		log.Error("no cases to run (check --task/--installer filters and the package list in bin/)")
		return 1
	}

	log.RunStart(len(paths), len(cases), opts.Parallel)

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
func buildQueue(paths []string, opts Options, log *logging.Emitter) ([]caseDef, map[string]*envInfo, int) {
	var cases []caseDef
	envs := map[string]*envInfo{}
	configErrors := 0
	for _, path := range paths {
		b, err := config.LoadBundle(path)
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
			if opts.Installer != "" {
				ok, merr := filepath.Match(opts.Installer, ri.LocalPath)
				if merr != nil {
					log.Error(fmt.Sprintf("bad --installer glob %q: %v", opts.Installer, merr))
					configErrors++
					continue
				}
				if !ok {
					continue
				}
			}
			for _, t := range task.All() {
				if !taskSelected(opts.Tasks, t.Name()) {
					continue
				}
				cases = append(cases, caseDef{bundle: b, ri: ri, t: t})
				info.total++
			}
		}
	}
	return cases, envs, configErrors
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
	cl := log.Case(envName, c.t.Name(), c.ri.Basename)
	started := time.Now()

	tr.caseStarted(envName)

	envOpts := env.Options{
		Name:          envName,
		Image:         spec.Image,
		Workdir:       spec.Workdir,
		ContainerName: names.next(envName, c.ri.Basename, c.t.Name()),
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
		res := c.t.Run(ctx, e, c.ri, cl)
		passed, failed = res.Passed, res.Failed
		if opts.Keep && failed > 0 {
			kept = true // container stays up for debugging
		} else if serr := e.Stop(context.WithoutCancel(ctx)); serr != nil {
			cl.Error(fmt.Sprintf("teardown: %v", serr))
		}
	}

	cl.CaseEnd(containerID, failed == 0, passed, failed, time.Since(started).Milliseconds(), kept)
	tr.caseFinished(envName, passed, failed)
	return passed, failed
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
			workdir = config.DefaultWorkdir
		}
		t.log.EnvStart(name, i.spec.Image, workdir)
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

func (n *nameAssigner) next(envName, basename, taskName string) string {
	raw := strings.ToLower("ita-" + envName + "-" + basename + "-" + taskName)
	clean := strings.Trim(nameSanitizer.ReplaceAllString(raw, "-"), "-")
	n.mu.Lock()
	defer n.mu.Unlock()
	n.used[clean]++
	if n.used[clean] == 1 {
		return clean
	}
	return fmt.Sprintf("%s-%d", clean, n.used[clean])
}
