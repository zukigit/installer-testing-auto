package report

import (
	"fmt"
	"io"
	"strings"
)

// RenderText writes the human-readable text report of a parsed run to w.
// It tolerates incomplete logs: unfinished cases/branches are flagged, not
// dropped.
func (r *Run) RenderText(w io.Writer) {
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
			detailParts := fmt.Sprintf("%d passed, %d failed", c.passed, c.failed)
			if c.durationMs > 0 {
				detailParts += fmt.Sprintf(", %.1fs", c.durationMs/1000)
			}
			fmt.Fprintf(w, "  %-30s %-10s %s  [%s / %s]\n",
				c.installer, c.task, status, detailParts, c.containerName)
			switch {
			case !c.ended:
				incomplete++
			case !c.pass:
				failedCases++
			default:
				passedCases++
			}
			TextCaseDetail(w, c)
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

// TextCaseDetail prints the steps and failed checks of one case.
func TextCaseDetail(w io.Writer, c *caseRec) {
	var hasFailures bool
	for _, s := range c.steps {
		if !s.pass {
			hasFailures = true
		}
	}
	for _, ck := range c.checks {
		if !ck.pass {
			hasFailures = true
		}
	}
	if !hasFailures && c.ended && c.pass {
		return // passing case: summary line is enough
	}
	if !hasFailures && !c.ended {
		fmt.Fprintf(w, "      (case interrupted — no steps/check results recorded yet)\n")
		return
	}
	fmt.Fprintln(w, "    steps / checks:")
	for _, s := range c.steps {
		mark := "PASS"
		if !s.pass {
			mark = "FAIL"
		}
		line := fmt.Sprintf("      %s %-14s %s", mark, s.kind, s.command)
		if s.exitCode >= 0 {
			line += fmt.Sprintf(" (exit %d)", s.exitCode)
		}
		fmt.Fprintln(w, line)
		if s.output != "" {
			indentOutput(w, s.output)
		}
	}
	for _, ck := range c.checks {
		mark := "PASS"
		if !ck.pass {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "      %s %-14s %s\n", mark, ck.kind, ck.name)
		if ck.detail != "" {
			fmt.Fprintf(w, "        detail: %s\n", ck.detail)
		}
		if ck.output != "" {
			indentOutput(w, ck.output)
		}
	}
}

func indentOutput(w io.Writer, output string) {
	for _, l := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		fmt.Fprintf(w, "        | %s\n", l)
	}
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
