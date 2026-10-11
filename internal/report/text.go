package report

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// ---------- ANSI colors (stdout is a terminal?) ----------

const (
	ansiGreen  = "\033[32m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiReset  = "\033[0m"
)

// stdoutIsTerminal: colors only when rendering to a terminal; piped or
// redirected output (files, tee, CI logs) stays plain.
func stdoutIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

type palette struct{ enabled bool }

func (p palette) colorize(code, s string) string {
	if !p.enabled {
		return s
	}
	return code + s + ansiReset
}

// ---------- status cells ----------

func statusText(c *caseRec) string {
	switch {
	case c.skipped:
		return "⏸ SKIPPED"
	case !c.ended:
		return "? INCOMPLETE"
	case c.pass:
		return "✓ PASS"
	default:
		return "✗ FAIL"
	}
}

func statusColor(c *caseRec) string {
	switch {
	case c.skipped:
		return ansiYellow
	case !c.ended:
		return ansiYellow
	case c.pass:
		return ansiGreen
	default:
		return ansiRed
	}
}

func countsText(c *caseRec) string {
	if !c.ended {
		return "-"
	}
	return fmt.Sprintf("%d/%d", c.passed, c.passed+c.failed)
}

func durationText(c *caseRec) string {
	if !c.ended {
		return "-"
	}
	return fmt.Sprintf("%.1fs", c.durationMs/1000)
}

// taskLineSuffix appends the upgrade source / skip reason to a task line.
func taskLineSuffix(c *caseRec) string {
	switch {
	case c.skipped:
		return fmt.Sprintf("  (%s)", skipReasonText(c.skipReason))
	case c.fromInstaller != "":
		return fmt.Sprintf("  (from %s)", c.fromInstaller)
	default:
		return ""
	}
}

// skipReasonText humanizes the case_skipped reason.
func skipReasonText(reason string) string {
	switch reason {
	case "no_older_version":
		return "no older version"
	case "nonstandard_version":
		return "non-standard version"
	default:
		return strings.ReplaceAll(reason, "_", " ")
	}
}

// ---------- layout (widths computed once per report) ----------

type reportLayout struct {
	taskW, statusW, countsW, durW int
	labelW, rowW                  int // check-row columns, report-wide
}

func (r *Run) layout() *reportLayout {
	lay := &reportLayout{}
	for _, e := range r.envs {
		for _, c := range e.cases {
			lay.taskW = maxInt(lay.taskW, utf8.RuneCountInString(c.task))
			lay.statusW = maxInt(lay.statusW, utf8.RuneCountInString(statusText(c)))
			lay.countsW = maxInt(lay.countsW, utf8.RuneCountInString(countsText(c)))
			lay.durW = maxInt(lay.durW, utf8.RuneCountInString(durationText(c)))
			for _, ck := range c.checks {
				lay.labelW = maxInt(lay.labelW, utf8.RuneCountInString(checkLabel(ck)))
				lay.rowW = maxInt(lay.rowW, utf8.RuneCountInString(rowLabel(ck)))
			}
		}
	}
	if lay.statusW < 6 {
		lay.statusW = 6 // "✓ PASS"
	}
	lay.durW = maxInt(lay.durW, 4)
	return lay
}

// padDisplay pads by display width (runes), not bytes — marks are multi-byte.
func padDisplay(s string, n int) string {
	return s + strings.Repeat(" ", maxInt(0, n-utf8.RuneCountInString(s)))
}

func padLeft(s string, n int) string {
	return strings.Repeat(" ", maxInt(0, n-utf8.RuneCountInString(s))) + s
}

// ---------- grouping (by installer, canonical task order) ----------

type caseGroup struct {
	installer string
	cases     []*caseRec
}

func taskRank(task string) int {
	switch task {
	case "fresh-install":
		return 0
	case "uninstall":
		return 1
	default:
		return 2
	}
}

func (r *Run) groups(e *envRec) []*caseGroup {
	var groups []*caseGroup
	index := map[string]*caseGroup{}
	for _, c := range e.cases {
		g, ok := index[c.installer]
		if !ok {
			g = &caseGroup{installer: c.installer}
			index[c.installer] = g
			groups = append(groups, g)
		}
		g.cases = append(g.cases, c)
	}
	for _, g := range groups {
		sort.SliceStable(g.cases, func(i, j int) bool {
			return taskRank(g.cases[i].task) < taskRank(g.cases[j].task)
		})
	}
	return groups
}

// ---------- rendering ----------

// RenderText writes the human-readable text report of a parsed run to w.
//
// Case grouping/group headers (full installer filename), marks (✓/✗), alignment
// and totals follow the confirmed v2.3 layout. With details=false the report
// renders one short line per task; failed/incomplete cases get their failure
// details underneath. With details=true every case renders its full detail
func (r *Run) RenderText(w io.Writer, details bool) {
	p := palette{stdoutIsTerminal()}
	lay := r.layout()

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

	var passedCases, failedCases, incomplete, skippedCases int

	for _, e := range r.envs {
		fmt.Fprintf(w, "env %s (image %s, workdir %s)\n", e.name, e.image, e.workdir)
		fmt.Fprintln(w)
		for _, g := range r.groups(e) {
			fmt.Fprintf(w, "  %s\n", g.installer)
			for _, c := range g.cases {
				r.printTaskLine(w, c, p, lay)
				switch {
				case c.skipped:
					skippedCases++
					if details {
						r.printTaskDetail(w, c, p, lay)
					}
				case !c.ended:
					incomplete++
					if details {
						r.printTaskDetail(w, c, p, lay)
					} else {
						r.printFailureDetail(w, c, p, lay)
					}
				case !c.pass:
					failedCases++
					if details {
						r.printTaskDetail(w, c, p, lay)
					} else {
						r.printFailureDetail(w, c, p, lay)
					}
				default:
					passedCases++
					if details {
						r.printTaskDetail(w, c, p, lay)
					}
				}
			}
			fmt.Fprintln(w)
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

	totalLine := fmt.Sprintf("TOTAL: %d cases (%d passed, %d failed", len(r.allCases), passedCases, failedCases)
	if incomplete > 0 {
		totalLine += fmt.Sprintf(", %d incomplete", incomplete)
	}
	if skippedCases > 0 {
		totalLine += fmt.Sprintf(", %d skipped", skippedCases)
	}
	totalLine += ")"
	fmt.Fprintln(w, totalLine)
}
func (r *Run) printTaskLine(w io.Writer, c *caseRec, p palette, lay *reportLayout) {
	taskCell := padDisplay(c.task, lay.taskW)
	statusCell := p.colorize(statusColor(c), padDisplay(statusText(c), lay.statusW))
	counts := padDisplay(countsText(c), lay.countsW)
	dur := padLeft(durationText(c), lay.durW)
	fmt.Fprintf(w, "    %s   %s   %s   %s%s\n", taskCell, statusCell, counts, dur, taskLineSuffix(c))
}

// printTaskDetail renders the full detail of one task (--details view):
// container, steps (passing copies hidden), and all check rows.
func (r *Run) printTaskDetail(w io.Writer, c *caseRec, p palette, lay *reportLayout) {
	if c.skipped {
		fmt.Fprintf(w, "      skipped: %s\n", skipReasonText(c.skipReason))
		return
	}
	if c.containerName != "" {
		// name only — the id stays available in the logs (case_start/case_end)
		fmt.Fprintf(w, "      container: %s\n", c.containerName)
	}
	if !c.ended {
		fmt.Fprintf(w, "      (case interrupted — no steps/check results recorded yet)\n")
		return
	}
	visible := visibleSteps(c, false)
	if len(visible) > 0 {
		for _, s := range visible {
			r.printStep(w, s, p)
		}
	}
	if len(c.checks) > 0 {
		fmt.Fprintf(w, "      %s:\n", checkSectionTitle(c.task))
		for _, ck := range c.checks {
			r.printCheckRow(w, ck, p, lay)
		}
	}
}

// printFailureDetail shows only the failures of one case (compact view)
// — failed steps plus the check rows that failed.
func (r *Run) printFailureDetail(w io.Writer, c *caseRec, p palette, lay *reportLayout) {
	if !c.ended {
		fmt.Fprintf(w, "      (case interrupted — no steps/check results recorded yet)\n")
		return
	}
	var failedSteps []stepRec
	for _, s := range c.steps {
		if !s.pass {
			failedSteps = append(failedSteps, s)
		}
	}
	var failedChecks []checkRec
	for _, ck := range c.checks {
		if !ck.pass {
			failedChecks = append(failedChecks, ck)
		}
	}
	if len(failedSteps) > 0 {
		fmt.Fprintln(w, "    steps:")
		for _, s := range failedSteps {
			r.printStep(w, s, p)
		}
	}
	if len(failedChecks) > 0 {
		fmt.Fprintf(w, "      %s:\n", checkSectionTitle(c.task))
		for _, ck := range failedChecks {
			r.printCheckRow(w, ck, p, lay)
		}
	}
}

// visibleSteps filters steps for the details view: passing copy steps are
// hidden (confirmed); includeCopyFailedOnly toggles for the failure view.
func visibleSteps(c *caseRec, failuresOnly bool) []stepRec {
	var out []stepRec
	for _, s := range c.steps {
		if s.kind == "copy" && s.pass {
			continue
		}
		if failuresOnly && s.pass {
			continue
		}
		out = append(out, s)
	}
	if !failuresOnly && len(c.steps) > 0 {
		return out
	}
	return out
}

func (r *Run) printStep(w io.Writer, s stepRec, p palette) {
	mark := ""
	if !s.pass {
		mark = p.colorize(ansiRed, "✗ FAIL") + "  "
	}
	line := fmt.Sprintf("      %s%s: %s", mark, s.kind, s.command)
	if s.exitCode >= 0 {
		line += fmt.Sprintf(" (exit %d)", s.exitCode)
	}
	fmt.Fprintln(w, line)
	if !s.pass {
		printStream(w, "stdout", s.stdout, false)
		printStream(w, "stderr", s.stderr, false)
	}
}

func (r *Run) printCheckRow(w io.Writer, ck checkRec, p palette, lay *reportLayout) {
	mark := "✓ PASS"
	code := ansiGreen
	if !ck.pass {
		mark = "✗ FAIL"
		code = ansiRed
	}
	fmt.Fprintf(w, "        %s  %s  %s\n",
		padDisplay(checkLabel(ck), lay.labelW),
		padDisplay(rowLabel(ck), lay.rowW),
		p.colorize(code, mark))
	if ck.detail != "" {
		fmt.Fprintf(w, "            detail: %s\n", ck.detail)
	}
	if ck.kind == "command" {
		printStream(w, "stdout", ck.stdout, true)
		printStream(w, "stderr", ck.stderr, true)
	} else {
		printStream(w, "stdout", ck.stdout, false)
		printStream(w, "stderr", ck.stderr, false)
	}
}

// ---------- helpers shared with the compact view ----------

// printStream writes one stream line unless it is empty and showEmpty is false.
func printStream(w io.Writer, name, value string, showEmpty bool) {
	if value == "" && !showEmpty {
		return
	}
	if value == "" {
		fmt.Fprintf(w, "            %s: (empty)\n", name)
		return
	}
	for _, l := range strings.Split(strings.TrimRight(value, "\n"), "\n") {
		fmt.Fprintf(w, "            %s: %s\n", name, l)
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
