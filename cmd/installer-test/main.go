// installer-test is the entrypoint of the installer testing CLI.
//
// Usage:
//
//	insttester run [--config GLOB] [--task LIST] [--keep]
//	insttester list [--config GLOB]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/zukigit/installer-testing-auto/internal/runner"
	"github.com/zukigit/installer-testing-auto/internal/task"
)

const usage = `installer-test — test installer files on target environments

Usage:
  installer-test run  [--config GLOB] [--task LIST] [--keep]
  installer-test list [--config GLOB]

Subcommands:
  run    test the installer files (default: install + uninstall on all envs)
  list   dry-run: show environments, matched installers and resolved commands

Flags:
  --config GLOB   config glob; default: configs/*.yaml (all environments)
  --task LIST     comma-separated task names; default: all tasks
                  available tasks: fresh-install, uninstall
                  (uninstall always runs fresh-install first)
  --keep          keep the environment alive for debugging (run only)

Examples:
  installer-test run
  installer-test run --task fresh-install
  installer-test run --config configs/rocky-9.yaml
  installer-test run --config "configs/*.yaml" --keep
  installer-test list
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
	keep := fs.Bool("keep", false, "keep the environment alive for debugging")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument(s): %s\n", strings.Join(fs.Args(), " "))
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
