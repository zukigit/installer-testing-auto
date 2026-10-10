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
	"strings"
	"syscall"

	"github.com/zukigit/installer-testing-auto/internal/report"
	"github.com/zukigit/installer-testing-auto/internal/runner"
	"github.com/zukigit/installer-testing-auto/internal/task"
)

const usage = `installer-test — test installer files on target environments

Usage:
  installer-test run    [--config GLOB] [--task LIST] [--installer GLOB] [--parallel N] [--keep]
  installer-test list   [--config GLOB]
  installer-test report --from FILE|-

Subcommands:
  run     test installer files: every (installer file x task) pair is an independent
          case with its own container; cases run in parallel through a global pool
          (--parallel, default 5) that caps the total containers running at once.
          Output is live JSON log lines (NDJSON); no summary is printed.
          Tasks are independent: uninstall is standalone — it installs first
          (setup) inside its own container, then uninstalls and verifies.
  list    dry-run: show environments, matched installers and resolved commands
  report  render a text report from captured run logs (NDJSON)

Flags (run):
  --config GLOB      config glob; default: configs/*.yaml (all environments)
  --task LIST        comma-separated tasks; default: all tasks
                     available tasks: fresh-install, uninstall (standalone)
  --installer GLOB   filter the package list, e.g. "bin/my-app-*.rpm"
  --parallel N       max containers running at the same time (default 5, min 1)
  --keep             keep containers of failed cases for debugging
                     (container IDs are always logged)

Flags (report):
  --from FILE|-      NDJSON log file, or stdin with "-"

Examples:
  installer-test run | tee run.jsonl
  installer-test run --task fresh-install --parallel 8 | tee run.jsonl
  installer-test run --config configs/rocky-9.yaml --installer "bin/*.rpm"
  installer-test report --from run.jsonl
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
	configPattern := fs.String("config", "", "config glob (default: configs/*.yaml)")
	taskList := fs.String("task", "", "comma-separated tasks (default: all)")
	installer := fs.String("installer", "", "glob filter for the package list")
	parallel := fs.Int("parallel", runner.DefaultParallel, "max containers running at the same time")
	keep := fs.Bool("keep", false, "keep containers of failed cases for debugging")
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
		Parallel:      *parallel,
		Keep:          *keep,
	})
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	configPattern := fs.String("config", "", "config glob (default: configs/*.yaml)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument(s): %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if err := runner.List(*configPattern); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR   %v\n", err)
		return 1
	}
	return 0
}

func cmdReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	from := fs.String("from", "-", "NDJSON log file, or \"-\" for stdin")
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
	run.RenderText(os.Stdout)
	return 0
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
