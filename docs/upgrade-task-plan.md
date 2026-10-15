# Upgrade Task Plan (v3) — `upgrade` task with `upgrade_checks` and version pairing

> Status: **CONFIRMED — ready to implement**
> Baseline: the implemented v2.x design (task-per-case isolation, global pool,
> NDJSON logs, grouped report — see `docs/container-per-task-plan.md` and the
> v2.3/v2.4 follow-ups).

## 1. Goal

A third task, `upgrade`, alongside `fresh-install` and `uninstall`:

- Installers in `bin/` follow **MAJOR.MINOR.PATCH[-Suffix]** versioning.
- For every installer that has **older versions of the same package** in the
  resolved list, the tool creates one upgrade **case per (older, newer) pair**,
  each in its own container (consistent with v2 isolation):
  install the old version (setup) → upgrade to the new version → run
  **`upgrade_checks`** (`must_exist`, `must_not_exist`, `commands` — same
  shape as the other check blocks).
- Installers that **can't participate** are **skipped** and shown as
  `SKIPPED` in the report:
  - version does not follow the standard, or
  - no older version of the same package exists.

## 2. Version pairing (the example, formalized)

Given `bin/` resolved to:

```
myapp-0.1.1-1.rpm     -> no older myapp          -> upgrade SKIPPED
myapp-0.1.1-2.rpm     -> older: 0.1.1-1          -> upgrade case: 0.1.1-1 -> 0.1.1-2
myapp-0.1.2-1.rpm     -> older: 0.1.1-1, 0.1.1-2 -> upgrade cases:
                                                         0.1.1-1 -> 0.1.2-1
                                                         0.1.1-2 -> 0.1.2-1
```

- **Package identity:** the derived package name (existing heuristics:
  `.deb` strips from the first `_`, `.rpm` strips version+release+arch).
- **Version extraction:**
  - `.rpm` `name-MAJOR.MINOR.PATCH-SUFFIX.rpm` → version = `MAJOR.MINOR.PATCH-SUFFIX`
  - `.deb` `name_VERSION_arch.deb` → version = the `_VERSION_` segment
  - anything that doesn't yield a valid `MAJOR.MINOR.PATCH[-Suffix]` → non-standard → skipped
- **Ordering:** MAJOR, MINOR, PATCH compared numerically first. If they are
  equal, the suffix decides:
  1. **both suffixes numeric** → compared numerically (`0.1.1-1` < `0.1.1-2`)
  2. **one without suffix, other numeric** → the no-suffix version ranks above
     (`0.1.1` > `0.1.1-1`)
  3. **any non-numeric suffix involved** (`-beta`, `-rc1`, mixed numeric/non-numeric)
     → the **installer files' created dates** decide: older file = older version
     (confirmed). Equal dates → equal versions → no upgrade pair.
- **Created date:** the file's modification timestamp (`os.Stat` mtime) is the
  portable "created date" on Linux. Caveat: copying/moving a file can refresh
  its mtime — dates only matter for same-version non-numeric suffix ties.
- **Old-version pool:** the config's full resolved list (all installer
  entries). `--installer GLOB` filters which **new** installers get upgrade
  cases, but the pool of available old versions is always the full list.
  > **Amendment (v3.4):** superseded by
  > `docs/upgrade-pool-pattern-vars-plan.md` — since v3.2 the resolved list is
  > pinned by pattern variables, so the pool is now the resolved list
  > **widened past variable pinning**: each installer entry also globs its
  > pattern with every provided `{VAR}` replaced by `*`, letting the upgrade
  > task pair from older versions of the same package in `bin/` even when
  > `--var VERSION=0.2.0` pins the target list.
- Upgrade cases exist per environment like other tasks (same config image).

## 3. Case lifecycle (own container, per pair)

```
create container -> copy OLD installer -> install OLD (SETUP, must succeed,
                    no checks, logged as step kind "setup-install")
                  -> copy NEW installer -> upgrade_command (default by extension)
                  -> upgrade_checks -> teardown
```

- The setup install failing aborts the case (nothing to upgrade from).
- Copies are excluded from result counts (v2.3 decision 9); counted results:
  `setup-install` + `upgrade` command + `upgrade_checks`.
- `{installer}` in commands/placeholders refers to the **NEW** installer
  (the upgrade target). No new placeholders.

### Defaults by extension (overridable)

| Extension | `upgrade_command` default |
|-----------|---------------------------|
| `.rpm`    | `rpm -Uvh {installer}` (the upgrade flag, vs `rpm -ivh` for fresh-install) |
| `.deb`    | `dpkg -i {installer}` (dpkg -i upgrades in place) |
| `.sh` / other | *(none — must be set in config)* |

## 4. Config schema additions (per installer entry)

```yaml
installers:
  - pattern: "bin/myapp-*.rpm"
    install_command:   "rpm -ivh {installer}"     # existing
    uninstall_command: "rpm -e {package}"         # existing
    upgrade_command:   "rpm -Uvh {installer}"     # NEW, optional (default above)
    install_checks:     { ... }                   # existing
    uninstall_checks:   { ... }                   # existing
    upgrade_checks:     { ... }                   # NEW, optional, same shape:
    #   must_exist / must_not_exist / commands (all optional)
```

- Validation: an entry with `upgrade_command`/`upgrade_checks` whose files
  can't participate still just skips (no config error).

## 5. Events & report

### New event: `case_skipped` (info)

```json
{"ts":"...","level":"info","event":"case_skipped","env":"redhat-9","task":"upgrade","installer":"myapp-0.1.1-1.rpm","data":{"reason":"no_older_version"}}
```

`reason` is `no_older_version` or `nonstandard_version`. Emitted when the
upgrade task is selected and an installer can't participate.

### Case start/end envelope

Upgrade cases carry the **new** installer in the envelope. The old version is
part of the case identity — new `case_start`/`case_end` data field
`from_installer` (basename of the old installer), e.g.:

```json
{"event":"case_start","env":"redhat-9","task":"upgrade","installer":"myapp-0.1.2-1.rpm",
 "data":{"container_id":"a1","container_name":"ita-redhat-9-myapp-0-1-2-1-from-0-1-1-1-upgrade","from_installer":"myapp-0.1.1-1.rpm"}}
```

- **Container names** include both versions:
  `ita-<env>-<new-basename>-from-<old-version>-upgrade`.

### Report

- Task order in a group: `fresh-install`, `uninstall`, `upgrade`.
- `upgrade` already maps to the `upgrade_checks` section title
  (`checkSectionTitle` handles it today — no report change needed there).
- **SKIPPED status** in the task line, with the reason:

```text
  myapp-0.1.1-1.rpm
    fresh-install   ✓ PASS   2/2   14.3s
    uninstall       ✓ PASS   2/2   15.1s
    upgrade         ⏸ SKIPPED  -    -        (no older version)

  myapp-0.1.2-1.rpm
    fresh-install   ✓ PASS   2/2   13.9s
    uninstall       ✓ PASS   2/2   14.0s
    upgrade         ✓ PASS   3/3   27.2s   (from myapp-0.1.1-1.rpm)
    upgrade         ✓ PASS   3/3   27.0s   (from myapp-0.1.1-2.rpm)
```

- `--details`: `from_installer` line per upgrade case; skip reason line for skipped.
- `TOTAL:` gains skipped when present: `TOTAL: 8 cases (6 passed, 1 failed, 1 incomplete, 4 skipped)`.
- Skipped never counts as passed or failed.

## 6. CLI

- `--task upgrade` becomes valid (default run = all tasks = fresh-install + uninstall + upgrade).
- Everything else unchanged (`--parallel`, `--keep`, `--installer`, `--config`).
- `--keep` applies to failed upgrade cases like any other.

## 7. Code impact

| File | Change |
|------|--------|
| `internal/config/` | `upgrade_command` + `upgrade_checks` fields; **version parsing** (extract + validate `MAJOR.MINOR.PATCH[-Suffix]`, compare — numeric suffixes numerically, non-numeric via file mtime) — exported from config, reused by resolve |
| `internal/config/resolve.go` | resolved installer gains `UpgradeCmd`, `UpgradeChecks` |
| `internal/task/upgrade.go` (new) | the upgrade task: setup-install old → copy new → upgrade_command → upgrade_checks |
| `internal/task/task.go` | registry + canonical order gains `upgrade` |
| `internal/runner/runner.go` | version-pairing matrix builder (older×newer pairs); `case_skipped` emission; `from_installer` passed to env naming (`-from-<version>-upgrade`); task selection default includes upgrade |
| `internal/logging/logging.go` | `CaseSkipped` event; `from_installer` in `case_start`/`case_end` |
| `internal/report/` | parse `case_skipped` + `from_installer`; SKIPPED status, `from` suffix on task lines, skip reasons, skipped in TOTAL |
| `internal/env/` | unchanged |

## 8. Confirmed decisions

| # | Question | Decision |
|---|----------|----------|
| 1 | Default `upgrade_command` | **yes** — `rpm -Uvh {installer}` / `dpkg -i {installer}` by extension, overridable in config |
| 2 | Suffix semantics | numeric suffixes → **numeric compare**; non-numeric suffixes → **file created date** decides older/newer (confirmed) |
| 3 | Old-version pool | **whole config's resolved list** (all installer entries) |
| 4 | Skipped in TOTAL | **yes** — `TOTAL: N cases (… , S skipped)` when present |
| 5 | `upgrade` by default | **included when no `--task` is given** (default = all tasks) |

### 8.2 The suffix rule — final

1. MAJOR, MINOR, PATCH are compared numerically — decides the pair when they differ.
2. Same MAJOR.MINOR.PATCH, then the suffix:
   - both numeric → numeric compare (`0.1.1-1` < `0.1.1-2`)
   - one without suffix, other numeric → no-suffix ranks above (`0.1.1` > `0.1.1-1`)
   - any non-numeric suffix involved (`-beta`, `-rc1`, mixed) → the installer
     files' **created dates** (mtime) decide: older file = older version;
     equal dates → no upgrade pair
3. A version that doesn't parse as `MAJOR.MINOR.PATCH[-Suffix]` at all
   (e.g. `myapp-latest.rpm`) remains **non-standard → skipped**.
