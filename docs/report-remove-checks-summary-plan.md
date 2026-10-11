# Remove Checks Summary Plan (v2.4) — drop the per-env `checks:` line

> Status: **CONFIRMED — ready to implement** (one interpretation note below)
> Baseline: the implemented v2.3 report (`docs/report-readability-plan.md`, decisions 6–7).
> Scope: **report rendering only** — no event schema, runner, task or config changes; existing `run.jsonl` files render with the new layout.

## 1. What changes

Current output:

```text
  jobarranger-utils-7.2.2-1.el9.x86_64.rpm
    fresh-install   ✗ FAIL   0/1   15.5s
    uninstall       ✗ FAIL   0/1   15.0s

  checks: 0 passed, 8 failed          <-- removed

TOTAL: 8 cases (0 passed, 8 failed)
```

New output:

```text
  jobarranger-utils-7.2.2-1.el9.x86_64.rpm
    fresh-install   ✗ FAIL   0/1   15.5s
    uninstall       ✗ FAIL   0/1   15.0s

TOTAL: 8 cases (0 passed, 8 failed)
```

- The per-env `checks: N passed, M failed` summary line is **removed** from both views (compact and `--details`) — it duplicated what the task lines already show (`N/M` per task).
- The final `TOTAL: N cases (…) case summary line — it counts **cases**, not checks) **stays**.
- Everything else (grouping, marks/colors, failure details, kept containers, infra errors, warnings) is unchanged.

## 2. Interpretation note

The request pasted both lines and said "remove checks". This plan removes only the **`checks:` line** (a checks summary); `TOTAL: 8 cases (0 passed, 8 failed)` remains, since it counts cases and is the run's bottom line. If you want the `TOTAL:` line gone too, say so — one-line change before implementing.

## 3. Code impact

| File | Change |
|------|--------|
| `internal/report/text.go` | remove the per-env checks summary `Fprintf` from the env loop |
| `internal/report/report_test.go` | drop the `"\n  checks: …\n"` expectation |
| everything else (events / runner / tasks / CLI / config) | **unchanged** |

## 4. Decisions

| # | Item | Decision |
|---|------|----------|
| 1 | Per-env `checks:` summary | **removed** from all views |
| 2 | `TOTAL:` line | **kept** (counts cases only) — see interpretation note |
