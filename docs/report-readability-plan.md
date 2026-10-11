# Report Readability Plan (v2.3) — grouped, aligned text report

> Status: **CONFIRMED — ready to implement**
> Baseline: the implemented v2.1 design in `docs/detailed-reporting-plan.md`.
> Scope: **report rendering only** — no event schema, runner, task or config changes; existing `run.jsonl` files render with the new layout. HTML formats are explicitly **out of scope** (not needed yet).

## 1. What this fixes

Problems observed in real reports (`report.txt`):

1. Lines too long (case line carries filename + task + status + counts + duration + container name) — wraps in normal terminals.
2. `copy` step noise: printed for every case, with a redundant full `src -> dst`.
3. No visual grouping — cases run together, sections don't stand out.
4. Column widths computed **per case** — labels/exit columns jump between cases.
5. Long versioned filenames repeated on every line.
6. Confusing `TOTAL` (counts cases) vs `run_end` footnote (counts checks): `8 cases: 8 passed` + `(run_end: 25 passed…)`.

## 2. The new layout

### 2.1 Group by installer (confirmed)

Within an environment, cases are grouped by **installer file**; the group
header is the **full installer filename** (confirmed — no short package-name
label). Tasks are nested under the group, in canonical order
(fresh-install, then uninstall), one short line each.

### 2.2 Default (compact) view

Status column uses **`✓` / `✗` marks** (confirmed) plus the word
(`✓ PASS`, `✗ FAIL`) so reports stay greppable.

```text
installer-testing-auto report
  logs of run: configs=1 cases=8 parallel=5

env redhat-9 (image docker.io/zukidocker/solar:rhel9, workdir /tmp)

  jobarranger-agentd-7.2.2-1.el9.x86_64.rpm
    fresh-install   ✓ PASS   2/2   14.3s
    uninstall       ✓ PASS   3/3   15.1s

  jobarranger-manager-7.2.2-1.el9.x86_64.rpm
    fresh-install   ✓ PASS   2/2   14.5s
    uninstall       ✓ PASS   3/3   28.3s

  jobarranger-server-7.2.2-1.el9.x86_64.rpm
    fresh-install   ✓ PASS   3/3   24.8s
    uninstall       ✗ FAIL   6/7   24.8s
      uninstall_checks:
        /usr/bin/jobarranger-serverd   must not exist   ✗ FAIL
            detail: file still exists

  jobarranger-utils-7.2.2-1.el9.x86_64.rpm
    fresh-install   ✓ PASS   2/2   27.6s
    uninstall       ✓ PASS   3/3   27.1s

  checks: 25 passed, 0 failed

TOTAL: 8 cases (8 passed, 0 failed)
```

- Task lines: `task` + mark/status + `passed/total checks` + `duration` — short, never wraps.
- Failures keep their detail block directly under the failing task line (as today).
- Container names move **out of the task lines**: shown only for kept containers and in the `--details` view (name only — see 2.3).
- Blank line between installer groups; groups in first-seen order.

### 2.3 `--details` view

Same grouping, plus per-task detail. Changes vs today:

- **Passing `copy` steps are hidden** (confirmed; a failed copy still shows).
- Step rows keep `kind`, the **full command — full paths, no `…` abbreviation**
  (confirmed) — and exit code; check sections unchanged
  (`install_checks:` / `uninstall_checks:` with path/expectation/command, exit
  codes, stdout/stderr).
- The container line shows the **container name only** (confirmed) — no `(id …)`
  suffix; the id stays available in the logs (`case_start` / `case_end` events):
  `container: ita-redhat-9-jobarranger-utils-7-2-2-1-el9-x86-64-rpm-fresh-install`

```text
  jobarranger-agentd-7.2.2-1.el9.x86_64.rpm
    fresh-install   ✓ PASS   2/2   14.3s
      install: rpm -ivh /tmp/jobarranger-agentd-7.2.2-1.el9.x86_64.rpm  (exit 0)
      install_checks:
        /etc/jobarranger/jobarranger-agentd.conf   must exist    ✓ PASS
    uninstall       ✓ PASS   3/3   15.1s
      setup-install: rpm -ivh /tmp/jobarranger-agentd-7.2.2-1.el9.x86_64.rpm  (exit 0)
      uninstall:     rpm -e jobarranger-agentd  (exit 0)
      uninstall_checks:
        /usr/bin/jobarranger-agentd    must not exist   ✓ PASS
        rpm -q jobarranger-agentd     expected exit 1  ✓ PASS
            stdout: jobarranger-agentd-7.2.2-1.el9.x86_64
```

### 2.4 Alignment rules

- Column widths (task name, status, counts, duration) computed **once per
  report**, not per case/group — columns never jump.
- Status and counts are fixed-width; duration right-aligned.
- Kept containers section unchanged (container names live there).

### 2.5 Marks & colors

- `✓ PASS` / `✗ FAIL` / `? <status>` marks in **every** output (plain UTF-8,
  also fine in text files).
- **ANSI colors** (green/red) on the mark+status **only when stdout is a
  terminal** (auto-detected); piped/redirected output (files, `tee`, CI logs)
  stays plain — no color codes in saved reports.

### 2.6 Totals footnote (fixed, refined)

- The `env total:` line is **removed**; each environment ends with a compact checks
  summary instead: `checks: 29 passed, 1 failed`.
- `TOTAL:` is a **single line counting cases only** — the separate `checks:` line
  under it is removed (it duplicated the per-env summaries):
  `TOTAL: 8 cases (7 passed, 1 failed)`.

## 3. Group header

- Group header = **full installer filename**, taken from the event envelope's
  `installer` field (already parsed — no schema change).
- No package-name short-label derivation (dropped per decision) — **no
  `internal/config` changes needed**.

## 4. Code impact

| File | Change |
|------|--------|
| `internal/report/text.go` | group cases by installer (header = full filename); report-wide column widths; `✓/✗` marks; TTY-detected ANSI colors; hide passing copy steps in `--details`; full paths in step commands; totals footnote; container names out of task lines |
| `internal/report/report.go` | **unchanged** (installer field already parsed) |
| `internal/report/report_test.go` | update expectations to the new layout (compact + `--details`) |
| events / runner / tasks / CLI / config | **unchanged** |

## 5. Trade-offs

- Grouping hides the container-per-case pairing visually (container name no longer on every line) — acceptable: IDs are always in the logs, kept containers are listed, and `--details`/`docker ps` cover debugging.
- Passing copy steps hidden in `--details`: slightly less explicit, but the step remains in the logs and failures still render.
- Marks are UTF-8 (`✓`/`✗`) — fine for modern terminals and text files; unlikely to matter for grep since the PASS/FAIL words stay.

## 6. Decisions & remaining open question

### 6.1 Confirmed decisions

| # | Question | Decision |
|---|----------|----------|
| 1 | Group header | **full installer filename only** — no package-name label |
| 2 | Step commands in `--details` | **full paths, no `…` abbreviation** |
| 3 | Marks & colors | **`✓`/`✗` marks always** + ANSI colors when stdout is a TTY (auto-off when piped/redirected) |
| 4 | Copy steps in `--details` | **hide passing copies** (failed copies still show) |
| 5 | "No case_end" status label | **keep `INCOMPLETE`** — neutral, accurate for interruption, live runs and truncated logs |
| 6 | Per-env summary | `env total:` line removed; replaced by compact `checks: 29 passed, 1 failed` |
| 7 | TOTAL footnote | **single line, cases only** (`TOTAL: 8 cases (7 passed, 1 failed)`) — separate checks line removed |
| 8 | Container line in `--details` | **name only** — no `(id …)` suffix; the id stays in the logs |

### 6.2 The "no case_end" status label — explained and confirmed

**What this is about:** a case in the logs can have a beginning but no end —
a `case_start` (container created) but **no `case_end`**. This happens when:

- the run was **interrupted** (Ctrl-C / SIGTERM, docker daemon down, host crash) mid-case,
- or the report is generated **while the run is still going** (`report --from run.jsonl` on a live file),
- or the log file is truncated/corrupted.

It is *not* the same as FAIL — nothing was verified as broken; the case simply
never finished. **Confirmed: the label stays `INCOMPLETE`** (neutral, accurate
for all causes).

