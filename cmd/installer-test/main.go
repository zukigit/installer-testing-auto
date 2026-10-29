// installer-test is the entrypoint of the installer testing CLI.
//
// Usage:
//
//	insttester run    [--config GLOB] [--task LIST] [--installer GLOB] [--parallel N] [--keep]
//	insttester list   [--config GLOB]
//	insttester report --from FILE|-
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/zukigit/installer-testing-auto/internal/cleanup"
	"github.com/zukigit/installer-testing-auto/internal/config"
	"github.com/zukigit/installer-testing-auto/internal/report"
	"github.com/zukigit/installer-testing-auto/internal/runner"
	"github.com/zukigit/installer-testing-auto/internal/task"
)

const usage = `installer-test — test installer files on target environments

Usage:
  installer-test run    [--config GLOB] [--task LIST] [--installer GLOB] [--parallel N] [--keep]
                        [--var NAME=VALUE ...]
  installer-test list   [--config GLOB] [--var NAME=VALUE ...]
  installer-test report --from FILE|-
  installer-test cleanup [--run ID] [--include-kept]

Subcommands:
  run     test installer files: every installer x task pair is an independent
          case with its own container; cases run in parallel through a global
          pool (--parallel, default 5) that caps the total containers running
          at once. Output is live JSON log lines (NDJSON); no summary is
          printed. Tasks are independent of each other:
          fresh-install, uninstall (standalone) and upgrade (requires
          MAJOR.MINOR.PATCH[-suffix] file names; pairs every installer with
          the older versions of the same package; skipped otherwise).
  list    dry-run: show environments, matched installers and resolved commands
  report  render a text report from captured run logs (NDJSON)
  cleanup remove leftover containers and files of earlier runs (normally
          not needed: every run with Windows environments starts a watcher
          process that cleans up when the run ends, however it ends; Linux
          containers are removed by testcontainers). Containers kept with --keep
          stay unless --include-kept is given.

Flags (run):
  --config GLOB      config glob; default: configs/*.yaml (all Linux environments;
                     os: windows configs need an explicit --config)
  --task LIST        comma-separated tasks; default: all tasks
                     available: fresh-install, uninstall, upgrade (standalone)
  --installer GLOB   filter the package list, e.g. "bin/my-app-*.rpm"
  --parallel N       max containers running at the same time (default 5, min 1)
  --keep             keep containers of failed cases for debugging
                     (container IDs are always logged)
  --var NAME=VALUE   set a pattern variable ({NAME} in installer patterns);
                     repeatable; an unset variable substitutes as ""
                     (the pattern then usually matches no files -> load error)

Flags (list):
  --config GLOB      config glob; default: configs/*.yaml (all Linux environments;
                     os: windows configs need an explicit --config)
  --var NAME=VALUE   same as for run

Flags (report):
  --from FILE|-      NDJSON log file, or stdin with "-"
  --details          show full per-task details (steps + install_checks /
                     uninstall_checks sections) for every case, pass and fail

Flags (cleanup):
  --run ID           only this run (ID from the ita-run-<ID> folder in the temp
                     directory, or the ita.run container label)
  --include-kept     also remove containers kept with --keep

Examples:
  installer-test run | tee run.jsonl
  installer-test run --task fresh-install --parallel 8 | tee run.jsonl
  installer-test run --config configs/rocky-9.yaml --installer "bin/*.rpm"
  installer-test run --var VERSION=7.2.2 | tee run.jsonl
  installer-test report --from run.jsonl
  installer-test report --details --from run.jsonl
`

func main() {
	os.Exit(realMain())
}

func realMain() int {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch os.Args[1] {
	case "run":
		return cmdRun(os.Args[2:])
	case "list":
		return cmdList(os.Args[2:])
	case "report":
		return cmdReport(os.Args[2:])
	case "cleanup":
		return cmdCleanup(os.Args[2:])
	case cleanup.WatchCommand:
		return cmdWatch(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", os.Args[1], usage)
		return 2
	}
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPattern := fs.String("config", "", "config glob (default: configs/*.yaml, excluding os: windows configs)")
	taskList := fs.String("task", "", "comma-separated tasks (default: all)")
	installer := fs.String("installer", "", "glob filter for the package list")
	parallel := fs.Int("parallel", runner.DefaultParallel, "max containers running at the same time")
	keep := fs.Bool("keep", false, "keep containers of failed cases for debugging")
	vars := varFlags{}
	fs.Var(vars, "var", "pattern variable NAME=VALUE ({NAME} in installer patterns); repeatable")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument(s): %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if *parallel < 1 {
		fmt.Fprintln(os.Stderr, "--parallel must be at least 1")
		return 2
	}
	tasks, err := parseTasks(*taskList)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runner.Run(ctx, runner.Options{
		ConfigPattern: *configPattern,
		Tasks:         tasks,
		Installer:     *installer,
		Vars:          vars,
		Parallel:      *parallel,
		Keep:          *keep,
		StartCleanup:  startCleanup,
	})
}

// startCleanup creates the run directory and starts the detached cleanup
// watcher for runs with Windows cases.
func startCleanup() (string, string, error) {
	runID := cleanup.NewRunID()
	runDir := cleanup.RunDir(os.TempDir(), runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create run directory: %w", err)
	}
	if _, err := cleanup.Spawn(runID, os.TempDir()); err != nil {
		os.RemoveAll(runDir)
		return "", "", err
	}
	return runID, runDir, nil
}

// cmdWatch is the hidden watcher process: it waits for the runner to exit
// (EOF on stdin) and cleans up the run.
func cmdWatch(args []string) int {
	fs := flag.NewFlagSet(cleanup.WatchCommand, flag.ContinueOnError)
	runID := fs.String("run", "", "run id")
	tempDir := fs.String("temp", "", "temp directory holding the run directory")
	if err := fs.Parse(args); err != nil || *runID == "" {
		return 2
	}
	// a watcher must never die from the runner's console signals
	signal.Ignore(os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	if err := cleanup.Watch(os.Stdin, cleanup.Options{RunID: *runID, TempDir: *tempDir}); err != nil {
		return 1
	}
	return 0
}

func cmdCleanup(args []string) int {
	fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	runID := fs.String("run", "", "only this run id (default: all runs)")
	includeKept := fs.Bool("include-kept", false, "also remove containers kept with --keep")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument(s): %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	err := cleanup.Clean(context.Background(), cleanup.Options{
		RunID: *runID, IncludeKept: *includeKept, Out: os.Stdout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR   %v\n", err)
		return 1
	}
	return 0
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	configPattern := fs.String("config", "", "config glob (default: configs/*.yaml, excluding os: windows configs)")
	vars := varFlags{}
	fs.Var(vars, "var", "pattern variable NAME=VALUE ({NAME} in installer patterns); repeatable")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument(s): %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if err := runner.List(*configPattern, vars); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR   %v\n", err)
		return 1
	}
	return 0
}

func cmdReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	from := fs.String("from", "-", "NDJSON log file, or \"-\" for stdin")
	details := fs.Bool("details", false, "show full per-task details for every case")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument(s): %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	var in io.Reader = os.Stdin
	if *from != "-" {
		f, err := os.Open(*from)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR   %v\n", err)
			return 2
		}
		defer f.Close()
		in = f
	}
	run, err := report.Parse(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR   parse logs: %v\n", err)
		return 2
	}
	run.RenderText(os.Stdout, *details)
	return 0
}

// varFlags collects repeatable --var NAME=VALUE flags into a map (last value
// wins on duplicate names). Values participate in pattern substitution
// literally; unused names are silently ignored.
type varFlags map[string]string

func (v varFlags) String() string {
	names := make([]string, 0, len(v))
	for n := range v {
		names = append(names, n+"="+v[n])
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func (v varFlags) Set(s string) error {
	name, value, ok := strings.Cut(s, "=")
	if !ok || !config.ValidVarName(name) {
		return fmt.Errorf("--var expects NAME=VALUE with NAME matching [A-Z][A-Z0-9_]* (got %q)", s)
	}
	v[name] = value
	return nil
}

// parseTasks validates the --task list. Empty means "all tasks".
func parseTasks(list string) ([]string, error) {
	list = strings.TrimSpace(list)
	if list == "" {
		return nil, nil
	}
	var out []string
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := task.ByName(name); !ok {
			known := make([]string, 0, 2)
			for _, t := range task.All() {
				known = append(known, t.Name())
			}
			return nil, fmt.Errorf("unknown task %q (available: %s)", name, strings.Join(known, ", "))
		}
		out = append(out, name)
	}
	return out, nil
}
