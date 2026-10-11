package report

import (
	"fmt"
	"io"
	"strings"
)

// RenderText writes the human-readable text report of a parsed run to w.
//
// With details=false (default, `report`) it renders compact case one-liners
// and shows steps/check details only for failed or incomplete cases.
// With details=true (`report --details`) every case gets its full detail:
// steps plus the grouped check sections (install_checks / uninstall_checks / ...).
// It tolerates incomplete logs: unfinished cases are flagged, not dropped.
func (r *Run) RenderText(w io.Writer, details bool) {
	fmt.Fprintln(w, "installer-testing-auto report")
	if r.sawRunStart {
		fmt.Fprintf(w, "  logs of run: configs=%d cases=%d parallel=%d\n", r.configs, r.casesPlanned, r.parallel)
	} else {
		fmt.Fprintf(w, "  WARNING: no run_start event found in the logs\n")
	}
	if !r.sawRunEnd {
		fmt.Fprintf(w, "  WARNING: run did not finish (no run_end event in the logs)\n")
	}
	fmt.Fprintln(w)

	var passedCases, failedCases, incomplete int
	for _, e := range r.envs {
		fmt.Fprintf(w, "env %s (image %s, workdir %s)\n", e.name, e.image, e.workdir)
		for _, c := range e.cases {
			status := "PASS"
			if !c.pass {
				status = "FAIL"
			}
			if !c.ended {
				status = "INCOMPLETE"
			}
			fmt.Fprintf(w, "  %-30s %-10s %s  [%d passed, %d failed",
				c.installer, c.task, status, c.passed, c.failed)
			if c.durationMs > 0 {
				fmt.Fprintf(w, ", %.1fs", c.durationMs/1000)
			}
			fmt.Fprintf(w, " / %s]\n", c.containerName)
			switch {
			case !c.ended:
				incomplete++
			case !c.pass:
				failedCases++
			default:
				passedCases++
			}
			printDetail := details || cNeedsDetail(c)
			if printDetail {
				renderDetail(w, c)
			}
		}
		if e.sawEnd {
			fmt.Fprintf(w, "  env total: %d passed, %d failed\n", e.passed, e.failed)
		}
		fmt.Fprintln(w)
	}

	if kept := r.keptNames(); len(kept) > 0 {
		fmt.Fprintf(w, "kept containers (--keep, failed cases):\n")
		for _, n := range kept {
			fmt.Fprintf(w, "  %s\n", n)
		}
		fmt.Fprintln(w)
	}
	if len(r.errors) > 0 {
		fmt.Fprintf(w, "infrastructure errors:\n")
		for _, er := range r.errors {
			fmt.Fprintf(w, "  ERROR %s: %s\n", scope(er), er.message)
		}
		fmt.Fprintln(w)
	}

	total := passedCases + failedCases + incomplete
	fmt.Fprintf(w, "TOTAL: %d cases: %d passed, %d failed", total, passedCases, failedCases)
	if incomplete > 0 {
		fmt.Fprintf(w, ", %d incomplete", incomplete)
	}
	if r.sawRunEnd && (r.runPassed != passedCases || r.runFailed != failedCases) {
		fmt.Fprintf(w, " (run_end: %d passed, %d failed)", r.runPassed, r.runFailed)
	}
	fmt.Fprintln(w)
}

// caseNeedsDetail: failed or unfinished cases always show their detail in the
// compact (default) view.
func cNeedsDetail(c *caseRec) bool {
	if !c.ended || !c.pass {
		return true
	}
	for _, s := range c.steps {
		if !s.pass {
			return true
		}
	}
	for _, ck := range c.checks {
		if !ck.pass {
			return true
		}
	}
	return false
}

// renderDetail prints the executed steps and the grouped check section of one
// case (used for every case with --details, and for failures in the default view).
func renderDetail(w io.Writer, c *caseRec) {
	if len(c.steps) == 0 && len(c.checks) == 0 {
		if !c.ended {
			fmt.Fprintf(w, "      (case interrupted — no steps/check results recorded yet)\n")
		}
		return
	}
	if len(c.steps) > 0 {
		fmt.Fprintln(w, "    steps:")
	}
	for _, s := range c.steps {
		mark := "PASS"
		if !s.pass {
			mark = "FAIL"
		}
		line := fmt.Sprintf("      %s  %-14s %s", mark, s.kind, s.command)
		if s.exitCode >= 0 {
			line += fmt.Sprintf(" (exit %d)", s.exitCode)
		}
		fmt.Fprintln(w, line)
		// steps: only non-empty streams (copy steps are usually empty)
		printStream(w, "stdout", s.stdout, false)
		printStream(w, "stderr", s.stderr, false)
	}
	if len(c.checks) > 0 {
		fmt.Fprintf(w, "    %s:\n", checkSectionTitle(c.task))

		labelW := 0
		for _, ck := range c.checks {
			labelW = maxInt(labelW, len(checkLabel(ck)))
		}
		markW := 0
		for _, ck := range c.checks {
			markW = maxInt(markW, len(rowLabel(ck)))
		}
		for _, ck := range c.checks {
			mark := "PASS"
			if !ck.pass {
				mark = "FAIL"
			}
			fmt.Fprintf(w, "      %-*s  %-*s  %s\n", labelW, checkLabel(ck), markW, rowLabel(ck), mark)
			if ck.detail != "" {
				fmt.Fprintf(w, "          detail: %s\n", ck.detail)
			}
			// command checks: always both streams (empty shown as (empty))
			if ck.kind == "command" {
				printStream(w, "stdout", ck.stdout, true)
				printStream(w, "stderr", ck.stderr, true)
			} else {
				printStream(w, "stdout", ck.stdout, false)
				printStream(w, "stderr", ck.stderr, false)
			}
		}
	}
}

// checkLabel is the first column of a check row: file path or command.
func checkLabel(ck checkRec) string {
	if ck.kind == "file" {
		return ck.path
	}
	return ck.command
}

// rowLabel is the second column: "must exist"/"must not exist" for file
// checks, "exit code N" ("expected exit N" when a non-zero code is expected)
// for command checks.
func rowLabel(ck checkRec) string {
	if ck.kind == "file" {
		switch ck.expectation {
		case "must_exist":
			return "must exist"
		case "must_not_exist":
			return "must not exist"
		default:
			return ck.expectation
		}
	}
	if ck.expected != 0 {
		return fmt.Sprintf("expected exit %d", ck.expected)
	}
	return fmt.Sprintf("exit code %d", ck.exitCode)
}

// printStream writes one stream line unless it is empty and allowOmitted is set.
func printStream(w io.Writer, name, value string, showEmpty bool) {
	if value == "" && !showEmpty {
		return
	}
	if value == "" {
		fmt.Fprintf(w, "          %s: (empty)\n", name)
		return
	}
	for _, l := range strings.Split(strings.TrimRight(value, "\n"), "\n") {
		fmt.Fprintf(w, "          %s: %s\n", name, l)
	}
}

// checkSectionTitle derives the check-section title from the task name.
// New tasks need no report changes: unknown names fall back to "<task>_checks".
func checkSectionTitle(taskName string) string {
	switch taskName {
	case "fresh-install":
		return "install_checks"
	case "uninstall":
		return "uninstall_checks"
	default:
		sanitized := strings.Map(func(r rune) rune {
			if r == '-' || r == ' ' {
				return '_'
			}
			return r
		}, taskName)
		return sanitized + "_checks"
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (r *Run) keptNames() []string {
	var out []string
	for _, c := range r.allCases {
		if c.kept {
			out = append(out, c.containerName)
		}
	}
	return out
}

func scope(er errorRec) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{er.env, er.task, er.installer} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "run"
	}
	return strings.Join(parts, " / ")
}
