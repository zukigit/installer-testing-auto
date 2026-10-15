# List Upgrade Checks Plan (v3.5) — show the `upgrade` command and `upgrade_checks` in the `list` dry-run

> Status: **IMPLEMENTED** (user-reported gap: "current
> list command is not showing upgrade_checks — we will add that").
> Baseline: the implemented v3.4 (`docs/upgrade-pool-pattern-vars-plan.md`).
> Scope: **one output addition** in the `list` dry-run — the resolved
> `upgrade` command line and the `upgrade_checks` counts line, completing
> the install/uninstall/**upgrade** symmetry. No config keys, no CLI flags,
> no events, no report changes, no run-behavior changes.

## 1. What's missing (verified root cause)

```text
$ ./installer-test-exec list --var VERSION=7.2.2
env almalinux-8
  ...
  installer bin/jobarranger-server-7.2.2-1.el8.x86_64.rpm
    in-container: /tmp/jobarranger-server-7.2.2-1.el8.x86_64.rpm
    install:      rpm -ivh /tmp/jobarranger-server-7.2.2-1.el8.x86_64.rpm
    uninstall:    rpm -e jobarranger-server
    install_checks:  must_exist=66 must_not_exist=0 commands=0
    uninstall_checks: must_exist=0 must_not_exist=13 commands=0
                                    <-- no upgrade command, no upgrade_checks
```

The trace:

1. `list` resolves every config (`runner.List` → `config.LoadBundle`) and
   prints each bundle via `report.ListBundle`
   (`internal/report/report.go` L23–48).
2. `ListBundle` prints, per installer, the resolved **install** and
   **uninstall** commands and the `install_checks` / `uninstall_checks`
   counts — **and stops there**. There is no `upgrade` command line and no
   `upgrade_checks` line.
3. The data is already on the bundle: `ResolvedInstaller.UpgradeCmd`
   (resolved with the extension defaults — `rpm -Uvh {installer}` /
   `dpkg -i {installer}` — by `resolveInstaller`,
   `internal/config/resolve.go`) and `ResolvedInstaller.UpgradeChecks`
   (from the entry or its preset). Nothing needs resolving that `list`
   doesn't already have — **it is purely a printing gap**.

The root cause is the same kind of rule drift as v3.4: `ListBundle` is a
**v1 renderer** (install/uninstall world, see
`docs/installer-testing-tool-plan.md`). The v3 upgrade task
(`docs/upgrade-task-plan.md`) added `upgrade_command` + `upgrade_checks`
to the spec, the resolved installer, the run events and the text report —
but its code-impact table never included the `list` renderer, so the dry-run
was never extended. The result: `list` previews 2 of the 3 phases.

## 2. The change

Two printed lines per installer, completing the symmetry with
install/uninstall:

```text
$ ./installer-test-exec list --var VERSION=7.2.2
env almalinux-8
  ...
  installer bin/jobarranger-server-7.2.2-1.el8.x86_64.rpm
    in-container: /tmp/jobarranger-server-7.2.2-1.el8.x86_64.rpm
    install:      rpm -ivh /tmp/jobarranger-server-7.2.2-1.el8.x86_64.rpm
    uninstall:    rpm -e jobarranger-server
    upgrade:      echo new >> /etc/jobarranger/jobarg_server.conf && rpm -Uvh /tmp/jobarranger-server-7.2.2-1.el8.x86_64.rpm   <-- NEW
    install_checks:  must_exist=66 must_not_exist=0 commands=0
    uninstall_checks: must_exist=0 must_not_exist=13 commands=0
    upgrade_checks:   must_exist=3 must_not_exist=0 commands=0          <-- NEW
```

- **`upgrade:` line** — `ri.Expand(ri.UpgradeCmd)`, placed after
  `uninstall:` in the resolved-commands group. Companion to the ask:
  previewing an installer's `upgrade_checks` without the upgrade command
  that produces them would be half a dry-run; the line exists for
  install/uninstall and the data is one field away.
- **`upgrade_checks:` line** — the same
  `must_exist=N must_not_exist=N commands=N` summary as the other check
  blocks, placed after `uninstall_checks`.

Placement and padding follow the existing conventions exactly
(labels padded to the `uninstall:` column; check lines padded like
`install_checks:`):

| Line | Format |
|---|---|
| `upgrade:` | 6 spaces after the label (8-char label, same column as `install:`/`uninstall:`) |
| `upgrade_checks:` | 2 spaces after the label, like `install_checks:` |

## 3. Semantics

| Case | Behavior |
|---|---|
| preset carries `upgrade_command` + `upgrade_checks` (e.g. `server-rpm`) | both lines shown, commands expanded (`{installer}` → in-container path) |
| no `upgrade_command` in the entry/preset | extension default applies (`.rpm` → `rpm -Uvh {installer}`, `.deb` → `dpkg -i {installer}`, same table as `run`) |
| `.sh` (or other) with no `upgrade_command` anywhere | the `upgrade:` line prints **empty**, exactly like an empty `install:`/`uninstall:` today — and it flags the run-time failure ("no upgrade command for %s: set upgrade_command in the config") before any container starts |
| no `upgrade_checks` block (nil) | the `upgrade_checks:` line is **omitted**, exactly like nil `install_checks`/`uninstall_checks` lines today |

## 4. What does not change

- `run` output, events, the text report, `report --details`: untouched.
- CLI: no new flags — `list` still accepts only `--config` and `--var`
  (`--task` on `list` remains unsupported; that is a separate gap, not this
  plan).
- v3.4 decision 8 stands: the `list` dry-run still shows the **pinned**
  resolved list only — no upgrade pool, no (from → to) pair preview.
- The `list` output for entries without any upgrade involvement keeps its
  current shape (minus the two new lines when nil).
- README: no change — it documents no full `list` output sample (usage
  lines only).

## 5. Code impact

| File | Change |
|------|--------|
| `internal/report/report.go` | `ListBundle`: add the `upgrade:` line after `uninstall:` and the `upgrade_checks:` line after `uninstall_checks` (~4 lines, both reusing the existing print patterns verbatim) |
| `internal/report/report_test.go` | new `TestListBundle`: hand-built bundle → captured stdout asserts the `upgrade:` line (expanded `UpgradeCmd`), the `upgrade_checks:` counts line, nil-omission (no line when `UpgradeChecks == nil`), and the empty-command case (`.sh` without `upgrade_command` → empty `upgrade:` line) |
| everything else | **unchanged** — `internal/config/`, `internal/runner/`, `internal/task/`, `internal/checks/`, `internal/env/`, `internal/logging/`, CLI, configs, presets |

## 6. Decisions

| # | Question | Decision |
|---|----------|----------|
| 1 | `upgrade_checks` line | **add** — the reported ask |
| 2 | `upgrade:` command line | **add as companion** — completes the resolved-commands group (install/uninstall/upgrade) and gives the checks their context; one `Printf`, zero behavior risk |
| 3 | nil `upgrade_checks` | omit the line, like nil `install_checks`/`uninstall_checks` today |
| 4 | empty `UpgradeCmd` (`.sh` without `upgrade_command`) | print the empty line like empty `install:`/`uninstall:` — consistent, and it surfaces the would-be run failure in the dry-run |
| 5 | padding/alignment | follow the existing columns verbatim (`upgrade:` on the `uninstall:` column; `upgrade_checks:` like `install_checks:`) |
| 6 | new `list` flags (`--task`, pool/pairs preview) | **rejected** — separate gaps; v3.4 decision 8 (no pool/pairs in `list`) stands |

## 7. Non-goals

- `--task` support on `list` (or any CLI change).
- Showing the upgrade pool or the (from → to) pairs — pairing remains
  run-observable only (v3.4 decision 8).
- Printing full check **paths**/commands instead of counts — the counts
  summary is the established dry-run granularity for all check blocks.
- Any change to run events, the text report, or config/preset schemas.
