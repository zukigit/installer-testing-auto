# Detailed Reporting Plan (v2.1) — per-task check details in the report

> Status: **DRAFT — awaiting confirmation**
> Baseline: the implemented v2 design in `docs/container-per-task-plan.md`
> (runner, parallel cases, NDJSON logs, `report` subcommand).

## 1. Goal

The text report currently shows a one-line summary per case and full details
only for **failed** cases. This plan makes the report show the **details of
every task**, grouped by check phase, for **all cases (pass and fail)**:

- file checks: path → `must exist` / `must not exist` → pass or fail
- command checks: command → exit code, **stdout and stderr** (separately)

```text
install_checks:
  file1 -> must exist -> pass or fail
  file2 -> must not exist -> pass or fail
  command1 -> exit code, stdout and stderr

uninstall_checks:
  file1 -> must exist -> pass or fail
  file2 -> must not exist -> pass or fail
  command1 -> exit code, stdout and stderr

upgrade_checks:            (future task — see section 5)
```

## 2. What changes

| Topic | Current | This plan |
|-------|---------|-----------|
| Detail visibility | only failed cases show steps/checks | **every case** shows its full detail |
| Detail grouping | flat "steps / checks" list | grouped per task: **`install_checks` / `uninstall_checks`** sections |
| Command output | single combined `output` field | separate **`stdout` and `stderr`** fields (event + report) |
| File check data | `name` = "must_exist: /path" | structured `path` + `expectation` (`must_exist`/`must_not_exist`) |
| Summary | per-case one-liner + totals | kept, on top of the details |

## 3. Event schema changes (NDJSON)

### `check` event — new fields

| Field | Applies to | Content |
|-------|------------|---------|
| `path` + `expectation` | file checks | `/usr/bin/myapp` + `must_exist` or `must_not_exist` (replaces the stringly `name`) |
| `stdout` | command checks | captured stdout (truncated, see section 6) |
| `stderr` | command checks | captured stderr (truncated) |
| `command` | command checks | the executed command (with placeholders expanded) |
| `exit_code`, `expected_exit_code` | command checks | as today |
| `detail` | failures | as today |

### `step` event — new fields

- `stdout` and `stderr` replace the combined `output` field
(install/uninstall/setup-install/copy commands produce real output worth
inspecting in the report).

### Where stdout/stderr come from (env change)

- `env.Exec` already declares separate `stdout, stderr` returns, but the docker
  implementation currently returns the **merged** stream in both.
- Fix in `internal/env/docker.go`: drop the `Multiplexed()` option and decode
  docker's raw multiplexed stream with `github.com/docker/docker/pkg/stdcopy.StdCopy`
  into two buffers → real separated stdout/stderr (already a transitive dependency).
- The `Environment` interface does **not** change.

## 4. Report layout

```text
installer-testing-auto report
  logs of run: configs=1 cases=2 parallel=5

env redhat-9 (image docker.io/zukidocker/solar:rhel9, workdir /tmp)

  jobarranger-server-7.2.2-1.el9.x86_64.rpm — fresh-install — PASS  (2 passed, 0 failed, 17.9s)
    steps:
      copy:        bin/jobarranger-server-7.2.2-1.el9.x86_64.rpm -> /tmp/...  PASS
      install:     rpm -ivh /tmp/jobarranger-server-7.2.2-1.el9.x86_64.rpm     PASS (exit 0)
    install_checks:
      /etc/jobarranger/jobarranger-serverd.conf   must exist       PASS
      /tmp/legacy.conf                            must not exist   PASS
      jobarranger-serverd --version               exit code 0      PASS
          stdout: jobarranger-serverd 7.2.2
          stderr: (empty)

  jobarranger-server-7.2.2-1.el9.x86_64.rpm — uninstall — PASS  (3 passed, 0 failed, 18.1s)
    steps:
      copy:          ...                          PASS
      setup-install: rpm -ivh /tmp/...            PASS (exit 0)
      uninstall:     rpm -e jobarranger-server    PASS (exit 0)
    uninstall_checks:
      /usr/bin/jobarranger-serverd                must not exist   PASS
      jobarranger-serverd --version               expected exit 1 FAIL
          stdout: (empty)
          stderr: command not found

  env total: 5 passed, 1 failed
TOTAL: 2 cases: 1 passed, 1 failed
```

- File-check rows: `path` → `must exist`/`must not exist` → `PASS`/`FAIL`
- Command-check rows: `command` → `exit code` (with expected, if not 0) → `PASS`/`FAIL`,
  then indented `stdout:` / `stderr:` lines
- Empty streams are shown as `(empty)` so the row shape is stable
- Kept containers / infra errors sections unchanged

## 5. Task → check-section mapping

The check-section title is derived from the task name, so **new tasks need no
report changes**:

| Task (registry) | Check section in report |
|-----------------|-------------------------|
| `fresh-install` | `install_checks` |
| `uninstall` | `uninstall_checks` |
| `upgrade` *(future)* | `upgrade_checks` |

Implemented as a small map in the report package; unknown task names fall back
to `<task>_checks`.

## 6. Trade-offs

- **Log size:** stdout/stderr are already captured; splitting them into two
  fields keeps the same volume. Truncation stays (2000 chars per stream today,
  noted in the events) — the report shows a `...` marker when truncated.
- **Backward compatibility:** old logs (combined `output`, stringly `name`)
  still render — the report keeps a fallback path for both. New fields are
  preferred when present.
- **Rendering noise:** showing every passing check's output makes reports
  longer; that is the point of this plan (opt-out would be a `--summary` flag —
  open question 1).

## 7. Code impact

| File | Change |
|------|--------|
| `internal/env/docker.go` | separate stdout/stderr via `stdcopy.StdCopy` instead of `Multiplexed()` |
| `internal/checks/checks.go` | emit structured file-check fields (`path`, `expectation`) and command fields (`command`, `exit_code`, separate `stdout`/`stderr`) |
| `internal/task/install.go`, `uninstall.go` | pass separated streams to `CaseLogger.Step` |
| `internal/logging/logging.go` | `Step`/`Check` take `stdout`/`stderr` (drop combined `output`); truncation per stream |
| `internal/report/report.go` | parse new fields (with backward-compatible fallbacks) |
| `internal/report/text.go` | per-case detail rendering for **all** cases: steps + grouped check sections (`install_checks` / `uninstall_checks` / ...) |
| runner / CLI / config | **unchanged** |

## 8. Open questions (need your input)

1. **Detail by default** — as proposed (every case, pass and fail), or keep the
   one-liner default and add a `--details` flag (or the inverse `--summary`)?
2. **Steps in the detail** — your example lists only checks; the proposal also
   shows the executed steps (`copy` / `install` / `setup-install` / `uninstall`)
   above the check section. Keep steps in, or checks only?
3. **Truncation** — 2000 chars per stream (current) is fine, or a different
   limit for stdout/stderr in events and the report?
4. **Old logs** — support both field shapes in `report` (proposed), or clean
   break: new fields only?
