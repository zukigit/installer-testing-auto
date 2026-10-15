# Upgrade Pool vs Pattern Variables Plan (v3.4) — widen `{VAR}` to `*` for the upgrade-pairing pool

> Status: **CONFIRMED — ready to implement** (user-reported bug: "for the
> upgrade task perspective, even though the variable is 0.2.0, the upgrade
> task should be done if the older versions exist in bin").
> Baseline: the implemented v3.3 (`docs/pattern-vars-unset-no-match-plan.md`),
> which itself builds on v3.2 (`docs/wildcard-checks-pattern-vars-plan.md`)
> and the v3 upgrade task (`docs/upgrade-task-plan.md`).
> Scope: **one behavior change** — where the upgrade task's old-version pool
> comes from. No new config keys, no CLI changes, no event/report/check
> changes, no changes to what `--var` pins.

## 1. What happened (verified root cause)

Reproduced with a throwaway test against the current code (deleted after
verification): `bin/` holds `myapp-0.1.0-1.rpm` and `myapp-0.2.0-1.rpm`, the
config pattern is `bin/myapp-{VERSION}*.rpm`, and the run is
`run --var VERSION=0.2.0`:

```text
resolved installers: 1
  myapp-0.2.0-1.rpm (version=0.2.0-1 package=myapp)
skip: myapp-0.2.0-1.rpm (no_older_version)
BUG CONFIRMED: upgrade case 0.1.0 -> 0.2.0 was not built
```

The trace:

1. `Load` substitutes `{VERSION}` → `0.2.0`
   (`internal/config/config.go`, `Load`): the pattern becomes
   `bin/myapp-0.2.0*.rpm` — exactly the v3.2 feature working as designed.
2. `Resolve` globs it: the resolved package list contains **only**
   `myapp-0.2.0-1.rpm`. The 0.1.0 file in `bin/` is invisible to the config.
3. `buildQueue` → `buildUpgradeCases(b, ri, allInstallers(b), …)`
   (`internal/runner/runner.go`): the old-version pool is
   `allInstallers(b)` = `b.Installers` — **the post-substitution resolved
   list**. 0.1.0 is not in it, so `older` is empty.
4. Result: a `no_older_version` skip instead of the upgrade case
   `0.1.0 -> 0.2.0`.

The root cause is a **rule collision between two implemented plans**:

- v3 (`docs/upgrade-task-plan.md` §2, "Old-version pool"): *the pool of
  available old versions is always the config's full resolved list.*
- v3.2 (`--var`): the resolved list is now **pinned by the variable**.

v3's rule predates variables. After v3.2, "the full resolved list" is no
longer "every version of the package in `bin/`" — it is "only the version
the user pinned". The upgrade task silently lost its "from" versions.

## 2. The change

**The upgrade-pairing pool is resolved from the same patterns widened —
each provided `{VAR}` token substituted as `*` instead of its value. The
pinned resolved list (what gets install/uninstall/upgrade cases) is
untouched.**

| `bin/` contents | Run | v3.3 (today) | v3.4 (this plan) |
|---|---|---|---|
| 0.1.0, 0.2.0 | `run --var VERSION=0.2.0` | resolved list = {0.2.0}; pool = {0.2.0} → **skip `no_older_version`**, no upgrade case | resolved list = {0.2.0} (targets unchanged); pool = {0.1.0, 0.2.0} → **upgrade case 0.1.0 → 0.2.0** |
| 0.1.0, 0.2.0 | `run --var VERSION=0.1.0` | pool = {0.1.0} → skip `no_older_version` | pool = {0.1.0, 0.2.0}; older-than-0.1.0 = none → **skip `no_older_version`** (no upgrade *to* an older version — unchanged) |
| 0.2.0 only | `run --var VERSION=0.2.0` | skip `no_older_version` | pool = {0.2.0} → **skip `no_older_version`** (no older file exists — unchanged) |
| 0.1.0, 0.1.1, 0.2.0 | `run --var VERSION=0.2.0` | skip | one case **per older version** (0.1.0 → 0.2.0, 0.1.1 → 0.2.0) — the per-pair rule, unchanged |
| any | pattern with **no** `{VAR}` (e.g. `bin/myapp-*.rpm`) | pool = resolved list, both versions pair | pool = widened = same list → **byte-identical behavior** |
| any | `run` (no `--var`, or `--var VERSION=`) | v3.3: `{UNSET:VERSION}` → matched-no-files load error, config rejected | **unchanged** — rejection happens before any pool exists |

For the motivating case:

```text
$ ./installer-test-exec run --config configs/myapp.yaml --var VERSION=0.2.0
{"event":"case_start","env":"myapp","task":"upgrade","installer":"myapp-0.2.0-1.rpm",
 "data":{"container_name":"ita-myapp-myapp-0-2-0-1-from-0-1-0-1-upgrade","from_installer":"myapp-0.1.0-1.rpm"}}
…setup-install myapp-0.1.0-1.rpm … upgrade "rpm -Uvh …" … upgrade_checks…
```

- The upgrade case now appears with `from_installer: myapp-0.1.0-1.rpm`
  (the envelope, container naming and steps are the existing v3 shapes —
  zero new events).
- 0.1.0 itself still gets **no** `fresh-install`/`uninstall` cases and no
  skip line: it is not part of the pinned resolved list. The variable pins
  **what is under test**; it no longer hides **where upgrades start**.

## 3. The widened pool pattern

`widenPattern(pattern, vars)` (in `internal/config/vars.go`, next to
`Substitute`): like `Substitute`, but a token with a **provided non-empty
value** substitutes `*` instead of the value. Unset (or explicitly empty)
names keep the v3.3 `{UNSET:NAME}` marker, mirroring `Substitute`:

```text
pattern                    bin/myapp-{VERSION}*.rpm
Substitute  (targets)  --var VERSION=0.2.0  ->  bin/myapp-0.2.0*.rpm
widenPattern (pool)     --var VERSION=0.2.0  ->  bin/myapp-*{*}.rpm  ≡ bin/myapp-*.rpm
```

Two properties make the pool safe by construction:

| Property | Why it holds |
|---|---|
| **superset**: every widened match set ⊇ the pinned match set | `*` matches any string, including the provided value, so every file the pinned pattern matched is also matched by the widened one → the pool glob can never fail with "matched no files" when the pinned glob succeeded → **no new load errors** |
| values can't break the pool glob | the only glob syntax risk is a malformed `[`-class; the widened pattern's brackets come from the raw pattern unchanged (values are replaced by `*`, never kept), so if the pinned glob ran, the widened glob runs |

Pooling rules (all reuse existing machinery):

- **Pool = pinned resolved list ∪ widened extras**, deduplicated by
  `LocalPath` across all installer entries of the config, stored on the
  bundle as `Bundle.UpgradePool` (pinned installers first, as the same
  pointers, then extras).
- **Extras are resolved with the same `InstallerSpec`** — the "from"
  installer needs the entry's `install_command` for its setup install and
  the entry's checks; a file matched by widening the entry's own pattern
  belongs to that entry.
- **Per-spec widening, whole-config pool**: each entry widens its own
  pattern, but the pool is the union over entries — preserving v3 decision
  3 ("the whole config's resolved list"), so e.g. an entry matching el8
  files still pools with an entry matching el9 files of the same package.
- Widened extras are **never targets**: they get no cases and no skip
  events; they exist only as `from` candidates in `buildUpgradeCases`.
- The pool glob is **best-effort**: an (impossible-in-practice) glob error
  or empty extra set adds nothing and errors nothing — the pool is a
  widening, never a validation.

## 4. What does not change

- **The case matrix stays the pinned resolved list**: `--var VERSION=0.2.0`
  still means only 0.2.0 gets `fresh-install`/`uninstall`/`upgrade` cases.
  Widening affects **pairing input only**, never the target selection.
- v3 pairing rules: package identity (`PackageName`), version extraction
  and ordering (`CompareInstallerVersions`, mtime tie-breaks), one case per
  (older, newer) pair, skip reasons `no_older_version` /
  `nonstandard_version` — all reused verbatim.
- v3 decision: "`--installer` filters which **new** installers get upgrade
  cases, but the pool of available old versions is always the full list" —
  the pool is not filtered by `--installer`.
- v3.3: unset/empty variables reject the config through the
  `{UNSET:NAME}` marker before any pool is built; no prompting; no
  defaults.
- v3.2: `--var` flag semantics, substitution in patterns only, literal
  `{VAR}` elsewhere, `run_start.vars` recording.
- Events, report rendering, `list` output, CLI flags: no shape changes —
  new upgrade cases simply appear in the existing `case_start` /
  `case_skipped` / `from_installer` streams.

## 5. Code impact

| File | Change |
|------|--------|
| `internal/config/vars.go` | add `widenPattern(pattern, vars)` — like `Substitute`, provided non-empty values → `*`, unset/empty → `{UNSET:NAME}` marker (~10 lines) |
| `internal/config/config.go` | `InstallerSpec` gains unexported `poolPattern string` (YAML ignores unexported fields); `Load` computes it from the **raw** pattern before the `Substitute` overwrite: `spec.poolPattern = widenPattern(raw, vars)` (~3 lines) |
| `internal/config/resolve.go` | `Resolve` additionally globs each `spec.poolPattern`, resolves extras via the existing `resolveInstaller(spec, …)`, and builds `Bundle.UpgradePool` = pinned list (same pointers) + path-deduped extras; pool errors add nothing (~20 lines) |
| `internal/runner/runner.go` | `allInstallers(b)` → `upgradePool(b)`: return `b.UpgradePool`, falling back to `b.Installers` when nil (hand-built bundles in tests); optional `o.LocalPath == ri.LocalPath` guard next to the existing `o == ri` self-pair check (pointer identity already holds: pinned targets are in the pool as the same pointers, extras are distinct paths) |
| `internal/runner/runner_test.go` (new) | **regression test** (the deleted scratch verification, formalized): `bin/myapp-{VERSION}*.rpm` with 0.1.0 + 0.2.0 files, `VERSION=0.2.0`, upgrade task → exactly one case with `from = myapp-0.1.0-1.rpm`, target `myapp-0.2.0-1.rpm`, and **no** `no_older_version` skip; plus the pinned-matrix assertion (0.1.0 gets no install/uninstall cases) and the no-older-file skip case |
| `internal/config/vars_test.go`, `resolve_test.go` | `widenPattern` unit cases; pool resolution: extras resolved with the entry's spec, path dedup, `UpgradePool ⊇ Installers` |
| `internal/checks/`, `internal/task/`, `internal/env/`, `internal/logging/`, `internal/report/`, CLI | **unchanged** |
| `README.md` | §"Upgrade pairing" + §"Pattern variables" (below) |
| `docs/upgrade-task-plan.md` | amendment note: §2 "Old-version pool" is superseded by this plan (the pool is the resolved list **widened past variable pinning**) |

## 6. README.md updates

1. **"Upgrade pairing"**: replace "finds every older version of the same
   package from the config's full resolved list" with "from the config's
   **upgrade pool** — the resolved package list **plus** every older
   version of the same package discoverable by widening the pattern
   variables (`{VAR}` → `*`). Pinning a version with
   `--var VERSION=0.2.0` selects what gets **tested**, but older versions
   in `bin/` still pair as upgrade starting points."
2. **"Pattern variables"**: add one bullet: *variables pin the target list
   only — the `upgrade` task still pairs from older versions of the same
   package found via the widened pattern, so
   `run --var VERSION=0.2.0` performs `0.1.0 -> 0.2.0` whenever
   `myapp-0.1.0*.rpm` exists in `bin/`.*

## 7. Decisions

| # | Question | Decision |
|---|----------|----------|
| 1 | Pool source | the **widened pattern** (each provided `{VAR}` → `*`), not the pinned resolved list — the fix itself |
| 2 | Widening rule | only **provided non-empty** values widen to `*`; unset/empty keep the v3.3 `{UNSET:NAME}` marker (the config is rejected before a pool exists — mirroring keeps a rejected config from ever widening) |
| 3 | Pool scope | whole config (all entries' pinned matches ∪ all entries' widened extras), dedup by `LocalPath` — preserves v3 decision 3 (whole-list pool, cross-entry pairing) |
| 4 | Extras' commands & checks | resolved from the **same installer entry** whose pattern widened to them (the `from` installer runs the entry's `install_command` in setup) |
| 5 | Pool resolution failures | never a config error — best-effort widening; the superset property makes a no-match pool impossible when the pinned glob succeeded |
| 6 | Case matrix | **pinned list only** — extras are `from` candidates, never targets; 0.1.0 gets no cases and no skip event when 0.2.0 is pinned |
| 7 | `--installer` filter | targets only; the pool stays unfiltered (v3: "the pool of available old versions is always the full list") |
| 8 | `list` output | unchanged — it shows the pinned list (the dry-run matrix); the pool affects pairing only |
| 9 | New config keys (`pool_pattern`, `upgrade_from`, …) | **rejected** — the pool is derived, never configured; no schema additions |
| 10 | Reuse vs. new events | full reuse — `from_installer`, `case_skipped`, container naming (`-from-<old>-upgrade`) already carry everything the new pairs need |

## 8. Non-goals

- Letting variables select **multiple** target versions (e.g. ranges) —
  the pinned list stays exactly what the substituted pattern matches.
- Upgrade pairs from versions **newer** than the pinned target, or between
  two non-pinned extras (0.1.0 → 0.3.0 when 0.2.0 is pinned) — a target
  must be part of the pinned list; extras pair upward into targets only.
- Showing the upgrade pool in `list` or in the report — the pool is
  pairing input; its effect is visible as the emitted upgrade cases.
- Any change to `--var` semantics, substitution scope, unset-variable
  handling (v3.3), wildcard checks (v3.2 feature A), or the pairing/skip
  rules of v3.
