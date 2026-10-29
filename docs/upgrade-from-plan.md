# Upgrade From an Explicit Installer Plan (v4.2)

> Status: **IMPLEMENTED (2026-10-29)** — unit-tested on Linux; README
> updated. Implementation note: the from installers are resolved per entry
> (`ResolvedInstaller.UpgradeFrom`, shared by the entry's targets); a
> malformed `upgrade_from` glob is a load error, a non-matching one is not.
> Baseline: v4.1 (`docs/windows-defaults-cleanup-watcher-plan.md`) on top of
> the v3 upgrade task (`docs/upgrade-task-plan.md`), the v3.4 upgrade pool
> and the v4 `version_format` (`docs/windows-environment-plan.md` §8.2).
> Scope: one new optional installer key, **`upgrade_from`**, that names the
> exact old installer file(s) the `upgrade` task starts from — no version
> parsing, no pairing rules. Linux and Windows alike. Configs without the key
> behave exactly as today.

## 1. Goal

Today the `upgrade` task finds the old installer itself: it parses versions
from file names (built-in `.deb` / `.rpm` rules or `version_format`) and
pairs every installer with every **older version of the same package**.
That needs parsable file names and can't express "upgrade from exactly
this file".

With `upgrade_from` the user says it directly:

```
bin/
  app-0.1.1.rpm
  app-0.1.2.rpm
```

```yaml
installers:
  - pattern: "bin/app-*.rpm"
    upgrade_from: "bin/app-0.1.1.rpm"
```

→ exactly one upgrade case: **`app-0.1.1.rpm -> app-0.1.2.rpm`**. The
target `app-0.1.1.rpm` itself gets **no** `app-0.1.1.rpm -> app-0.1.1.rpm`
case (it is skipped, §3.3). Versions are never parsed for this entry.

## 2. Configuration

### 2.1 `installers[].upgrade_from` (new, optional, upgrade task only)

| Key | Required? | Description |
|---|---|---|
| `upgrade_from` | optional | Path or glob (repo-root relative, like `pattern`) of the **old** installer file(s) the `upgrade` task starts from. Every match is a "from" installer; every file the entry's `pattern` matches is a target. Replaces the automatic version pairing for this entry. |

Rules:

- **One string** — a path or a glob, no list (§8 Q1). A glob like
  `bin/app-0.1.*.rpm` gives several from files → one case per
  (from × target) pair (§3.2).
- **`{VAR}` pattern variables are substituted** like in `pattern`, so the
  old version can be a run input:
  `upgrade_from: "bin/app-{OLD_VERSION}.rpm"` + `--var OLD_VERSION=0.1.1`.
  An unset variable substitutes the never-matching `{UNSET:NAME}` marker
  (v3.3) → no match (below).
- **No match is never an error** (§8 Q3):
  - runs **without** the `upgrade` task (`--task fresh-install,uninstall`)
    ignore `upgrade_from` completely;
  - with the `upgrade` task, every target of the entry is **skipped**
    (`case_skipped`, reason `no_upgrade_from_match`, §3.3) — like the
    existing upgrade skips, never a failure.
  A malformed glob (e.g. `bin/[`) is still a load error, like in `pattern`.
- **The from file does not have to match `pattern`** (e.g. an old release
  kept in another folder: `upgrade_from: "bin/old/app-0.1.1.rpm"`).
- **Preset-able** (§8 Q4): like every installer key except `pattern` /
  `use_preset`; entry wins over preset (v3.1 merge rule). Paths in a preset
  are cwd-relative like everywhere else, so a shared "previous release"
  path works for every config using the preset.
- **Any OS, any extension**: `.rpm`, `.deb`, `.msi`, `.exe`, `.sh`, …

### 2.2 Interaction with the other upgrade keys

| Entry has (after the preset merge) | Pairing for this entry |
|---|---|
| neither key | today's built-in rules (Linux) / `no_version_format` skip (Windows) — unchanged |
| `version_format` only | v4 template pairing — unchanged |
| `upgrade_from` (with or without `version_format`) | **explicit pairing only** (§3); `version_format` is not used for pairing |

Other installer entries of the same config are not affected; a from file is
never a target and never joins another entry's upgrade pool.

## 3. Case building

### 3.1 Commands and checks

The from installer is resolved with the **same entry's** settings, like a
pooled old version today:

- `install_command` (setup step): entry > preset > extension default of the
  **from file's** extension;
- in-container path: `<workdir>/<from basename>` (OS-aware join, v4 §4.4);
- `{installer}` / `{basename}` / `{package}` expand for the from file
  during setup, for the target during `upgrade`;
- `upgrade_command` and `upgrade_checks` come from the target (entry) as
  today.

The upgrade task itself is unchanged: copy old → setup-install old (unchecked)
→ copy new → `upgrade_command` → `upgrade_checks`.

### 3.2 Pairs

For every target `t` (pattern match, after the `--installer` filter) and
every from file `f` (upgrade_from match):

- `f` and `t` are the **same installer** → no case;
- otherwise one `upgrade` case `f -> t`.

**Same installer** = the same **base name** (compared case-insensitively on
Windows), wherever the files live (§8 Q2): `bin/old/app-0.1.1.rpm` and
`bin/app-0.1.1.rpm` are the same version, so there is nothing to upgrade.
(This also means two files never collide on `<workdir>/<basename>` in one
container.)

No version comparison otherwise: the direction is the user's choice (a
"downgrade" pair is allowed and simply runs).

### 3.3 Skips (upgrade task only, `case_skipped`, never failures)

| Situation | Skip reason | Report text |
|---|---|---|
| `upgrade_from` matches no file (incl. an unset `{VAR}`) | **`no_upgrade_from_match`** (new) — for every target of the entry | `upgrade_from matched no files` |
| every from file is the same installer as the target (e.g. target `app-0.1.1.rpm`, `upgrade_from: bin/app-0.1.1.rpm`) | **`is_upgrade_from`** (new) | `this file is the upgrade_from installer` |
| target is one of several from files | — no skip: it gets cases from the **other** from files (`0.1.0 -> 0.1.1` when both 0.1.0 and 0.1.1 match); only the self-pair is dropped | |

`no_version_format`, `nonstandard_version` and `no_older_version` never
occur for an `upgrade_from` entry.

### 3.4 Container names

Today: `ita-<env>-<new-stem>-from-<old-version>-upgrade`. With
`upgrade_from` the old version may not be parsable, so the name uses the
old version when the from file has one, else the **from file's stem**:
`ita-<env>-app-0-1-2-from-app-0-1-1-upgrade` (sanitized as today, numeric
suffix on collisions).

## 4. `list`, logging, report

- `list`: the per-installer `pairing:` line becomes
  `pairing:      upgrade from bin/app-0.1.1.rpm` (one line per from file),
  `pairing:      skipped by upgrade (is_upgrade_from)` for the from file
  itself, or `pairing:      skipped by upgrade (no_upgrade_from_match)`.
- Logging: no new fields — upgrade cases already carry `from_installer`;
  the new skips are normal `case_skipped` events.
- Report: two new `skipReasonText` entries (§3.3); nothing else changes.

## 5. Examples

```yaml
# Linux: one fixed old release, upgrade to whatever the pattern pins
installers:
  - pattern: "bin/jobarranger-agentd-{VERSION}*el9*.rpm"
    upgrade_from: "bin/jobarranger-agentd-7.2.1-1.el9.x86_64.rpm"
    use_preset: agentd-rpm
```

```sh
./installer-test-exec run --task upgrade --var VERSION=7.2.2
# -> jobarranger-agentd-7.2.1-1.el9.x86_64.rpm -> jobarranger-agentd-7.2.2-1.el9.x86_64.rpm
```

```yaml
# old version as a run input; shared through a preset
presets:
  agentd-msi:
    upgrade_from: "bin/old/Job_Arranger_Agent_{OLD_VERSION}_x64.msi"
```

```powershell
.\installer-test-exec.exe run --config "configs/jobarg-windows-*.yaml" --task upgrade --var OLD_VERSION=7.2.1
# without --var OLD_VERSION: every target is skipped (no_upgrade_from_match)
```

Windows MSIs with **identical** names in two folders
(`bin/old/Job_Arranger_Agent_x64.msi` and `bin/Job_Arranger_Agent_x64.msi`)
count as the same installer and are not paired (§3.2) — give the old file a
distinct name (e.g. with its version) to test that upgrade.

## 6. Code impact

| File | Change |
|---|---|
| `internal/config/config.go` | `InstallerSpec.UpgradeFrom` (`yaml:"upgrade_from"`); `{VAR}` substitution in `Load`; malformed-glob check |
| `internal/config/presets.go` | merge `upgrade_from` (entry wins) |
| `internal/config/resolve.go` | glob `upgrade_from` per entry (no match → empty, not an error); resolve each match with the entry's settings; on every target of that entry: `UpgradeFromSet bool` + `UpgradeFrom []*ResolvedInstaller` |
| `internal/runner/runner.go` | `buildUpgradeCases`: entries with `UpgradeFromSet` pair explicitly (§3.2) with the `no_upgrade_from_match` / `is_upgrade_from` skips; `containerRawName` falls back to the from stem |
| `internal/report/report.go` | `pairing:` lines (§4) |
| `internal/report/text.go` | `skipReasonText` for the two new reasons |
| `internal/logging/logging.go` | skip-reason doc comment |
| tests | config (substitution, no match = empty, preset merge + entry wins, from outside the pattern, bad glob error), runner (the `0.1.1 -> 0.1.2` example, no self-pair by path **and** by base name in another folder, glob of several from files, no-match skip only with the upgrade task, `--installer` filter only on targets, version_format ignored), report (skip texts, list lines) |
| `README.md` | `installers` table row; Shared presets key list; Upgrade pairing section: the third way (explicit), the two new skip reasons, examples |

## 7. Decisions

| # | Decision | Rationale |
|---|---|---|
| U1 | One key, `upgrade_from`: a single path/glob string with `{VAR}` | same mental model as `pattern`; old version can be a run input; no list (Q1) |
| U2 | Explicit pairing replaces automatic pairing for that entry | "exact installer" means no extra pairs from the version rules |
| U3 | No version comparison, downgrades allowed | the user picked the pair; the tool doesn't second-guess |
| U4 | Same base name = same installer = no pair | same name means the exact same version — nothing to upgrade (Q2) |
| U5 | No match is never an error: ignored without the upgrade task, `no_upgrade_from_match` skip with it | consistent with the other upgrade skips (Q3) |
| U6 | Preset-able, entry wins | a shared "previous release" path (Q4) |

## 8. Open questions — answered

| # | Question | Answer | Effect |
|---|---|---|---|
| Q1 | Should `upgrade_from` also accept a **list**? | **No.** | single path/glob string (U1) |
| Q2 | Same base name in different folders: pair with a `from-` copy name? | **No** — same name means the exact same version, which needs no upgrade. | same base name = self-pair, dropped (§3.2, U4) |
| Q3 | No match an error, even without the upgrade task? | **No** — ignored when the upgrade task isn't run; with the upgrade task, skipped like the existing skips. | `no_upgrade_from_match` skip (§3.3, U5) |
| Q4 | Allow `upgrade_from` in presets? | **Yes.** | preset merge, entry wins (U6) |

## 9. Non-goals

- Chains (`0.1.0 -> 0.1.1 -> 0.1.2` in one container).
- Choosing from files across installer entries by name (from files are
  per entry).
- Changes to `configs/`.
