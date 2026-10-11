# Report Keyed-Annotations Plan (v3.1) — no more parenthesized annotations

> Status: **CONFIRMED — ready to implement (not yet implemented)**
> Baseline: the implemented v2.x/v3 report (`docs/report-readability-plan.md`,
> `docs/upgrade-task-plan.md`). Scope: **report rendering only** — no event
> schema, runner, task or config changes; existing `run.jsonl` files render
> with the new layout.

## 1. Problem

The report annotates rows with **inline parenthesized** text:

```text
    upgrade         ⏸ SKIPPED   -        -  (no older version)
      skipped: no older version

    setup-install: rpm -ivh /tmp/jobarranger-server-7.2.1-1.el9.x86_64.rpm (exit 0)
    upgrade:     rpm -Uvh /tmp/jobarranger-server-7.2.2-1.el9.x86_64.rpm (exit 0)
```

Parentheses are dropped as an annotation style entirely — annotations become
**structured key lines**, the same style already used for `stdout:` / `stderr:`.

## 2. The new annotation style

Every annotation is an indented `key: value` line **under** the row it belongs
to (identical to the existing `stdout:`/`stderr:` lines):

| Old (parenthesized) | New (keyed line) |
|---------------------|------------------|
| `upgrade   ⏸ SKIPPED   -   -  (no older version)` | `reason: no older version` |
| `upgrade   ✓ PASS   3/3   2.7s  (from myapp-0.1.1-1.rpm)` | `from: myapp-0.1.1-1.rpm` |
| `setup-install: rpm -ivh … (exit 0)` | `exit code: 0` |
| `stdout: (empty)` | `stdout: empty` |
| `(case interrupted — no steps/check results recorded yet)` | `case interrupted — no steps/check results recorded yet` (plain line, no parens) |
| `TOTAL: 4 cases (0 passed, 3 failed, 1 skipped)` | **unchanged** — TOTAL keeps its parenthesized format (confirmed) |

## 3. New layout examples

### Skipped upgrade (compact and `--details`)

```text
  myapp-0.1.1-1.rpm
    fresh-install   ✓ PASS   2/2   14.3s
    uninstall       ✓ PASS   2/2   15.1s
    upgrade         ⏸ SKIPPED   -     -
      reason: no older version
```

### Upgrade pair (`--details`)

```text
  myapp-0.1.2-1.rpm
    fresh-install   ✓ PASS   2/2   13.9s
    uninstall       ✓ PASS   2/2   14.0s
    upgrade         ✓ PASS   3/3   27.2s
      from: myapp-0.1.1-1.rpm
      container: ita-redhat-9-myapp-0-1-2-1-rpm-from-0-1-1-1-upgrade
      setup-install: rpm -ivh /tmp/jobarranger-server-7.2.1-1.el9.x86_64.rpm
          exit code: 0
      upgrade: rpm -Uvh /tmp/jobarranger-server-7.2.2-1.el9.x86_64.rpm
          exit code: 0
          stdout: ...
          stderr: empty
```

- Task lines carry **no trailing annotation** — status, counts, duration only.
- `reason:` / `from:` lines sit directly under their task line; step sub-lines
  (`exit code:`, `stdout:`, `stderr:`) sit under their step row.
- `exit code:` is a key line for **every** step (including `0`) — matching the
  `stdout:`/`stderr:` convention of always-present keys.
- The old `skipped: no older version` detail line is replaced by `reason: …`.

## 4. Where keyed lines appear

| Key | Compact view | `--details` view |
|-----|--------------|------------------|
| `reason: …` (skipped cases) | yes — directly under the task line | yes |
| `from: …` (upgrade cases) | yes — directly under the task line | yes |
| `container: …` | no (unchanged) | yes (unchanged) |
| `exit code: …` (steps) | only for failed steps (failure detail, unchanged scope) | every step |
| `stdout:` / `stderr:` | as today (non-empty for steps, always for command checks) | as today |

## 5. Code impact

| File | Change |
|------|--------|
| `internal/report/text.go` | drop `taskLineSuffix` (`(no older version)` / `(from …)`) → `reason:` / `from:` lines; step rows lose `(exit N)` → `exit code:` sub-line; `(empty)` → `empty`; interrupted line loses parens. **TOTAL stays as-is** |
| `internal/report/report_test.go` | update expectations to the keyed layout |
| everything else (events / runner / tasks / CLI / config) | **unchanged** |

## 6. Confirmed decisions

| # | Question | Decision |
|---|----------|----------|
| 1 | Empty streams | **`stdout: empty`** — plain word |
| 2 | `exit code:` visibility | **always shown for every step, including `0`** |
| 3 | TOTAL format | **unchanged** — keeps `TOTAL: 4 cases (0 passed, 3 failed, 1 skipped)` |
| 4 | `from:` in the compact view | **yes** — under every upgrade task line (identifies the pair) |
