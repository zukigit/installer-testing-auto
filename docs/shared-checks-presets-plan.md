# Shared Presets Plan (v3.1) — `use_preset` and `configs/shared/presets.yaml`

> Status: **PROPOSED — awaiting final go-ahead** (core decisions confirmed by
> the user: presets carry every installer key, `use_preset` naming, multiple
> presets files with per-config selection, **no** environment-level key)
> Baseline: the implemented v3 design (see `docs/upgrade-task-plan.md`,
> `docs/container-per-task-plan.md`). Scope: **config loading only** — no event
> schema, runner, task, report or CLI changes; presets are applied inside
> `config.Load`, before `Resolve`.

## 1. Goal

The 13 `configs/jobarg-*.yaml` files duplicate the same checks **and commands**
per package (`server`, `agentd`, `manager`, `utils`) across every environment.
Only the `pattern` and the packaging family (`.rpm`/`.rpmnew` vs
`.deb`/`.new`) actually differ.

Introduce **named presets** in shared **presets files**, referenced per
installer entry:

```yaml
presets: configs/shared/presets.yaml    # which presets file this config uses
installers:
  - pattern: "bin/jobarranger-server*el8*.rpm"
    use_preset: server-rpm              # <- new key
```

- A preset can carry **every installer key** — the three check blocks
  (`install_checks`, `uninstall_checks`, `upgrade_checks`, each with its full
  shape: `must_exist`, `must_not_exist`, `commands`) **and the three command
  keys** (`install_command`, `uninstall_command`, `upgrade_command`).
- **Multiple presets files** may exist (e.g. per product/project); each
  environment config picks the one it uses via the top-level `presets:` key.
- One place to fix a wrong path or command; env files shrink to
  `pattern` + `use_preset`.
- **Backward compatible**: every existing config keeps working unchanged;
  migration to `use_preset` is opt-in and gradual.

Why not YAML anchors/aliases (`&x` / `*x`): they only work **within a single
file**, not across the 13 config files — a Go-side reference mechanism is
required.

## 2. Presets files — `configs/shared/*.yaml`

A preset value has the same shape as an installer entry, minus `pattern` and
`use_preset` — **every other key is allowed and optional**. Jobarg's natural
split is per package **and packaging family** (commands and
`upgrade_checks` differ between rpm and deb):

```yaml
# configs/shared/presets.yaml
# Named presets, referenced from environment configs via use_preset.
# Not an environment config: kept under configs/shared/ which Discover
# never treats as environments (see section 6).
presets:
  server-rpm:
    install_checks:
      must_exist:
        - /usr/share/doc/jobarranger-server/database
    uninstall_checks:
      must_not_exist:
        - /usr/share/doc/jobarranger-server/database
        - /var/log/jobarranger
        - /etc/jobarranger/jobarg_server.conf
        - /var/lib/jobarranger/server
    upgrade_command: "echo new >> /etc/jobarranger/jobarg_server.conf && rpm -Uvh {installer}"
    upgrade_checks:
      must_exist:
        - /etc/jobarranger/jobarg_server.conf.rpmnew
  server-deb:
    install_checks:
      must_exist:
        - /usr/share/doc/jobarranger-server/database
    uninstall_checks:
      must_not_exist:
        - /usr/share/doc/jobarranger-server/database
        - /var/log/jobarranger
        - /etc/jobarranger/jobarg_server.conf
        - /var/lib/jobarranger/server
    uninstall_command: "apt-get purge -y {package}"
    upgrade_command: "echo new >> /etc/jobarranger/jobarg_server.conf && dpkg -i {installer}"
    upgrade_checks:
      must_exist:
        - /etc/jobarranger/jobarg_server.conf.new
  # agentd-rpm / agentd-deb / manager-rpm / manager-deb / utils-* follow
  # the same shape (commands and/or check blocks, each optional).
```

Rules:

- Preset keys are the installer keys minus `pattern`/`use_preset`: the three
  command keys and the three check blocks — **each optional, any
  combination**. Check blocks use the exact existing `Checks` shape
  (`must_exist`, `must_not_exist`, `commands`).
- **Multiple presets files are allowed** — e.g. `configs/shared/presets.yaml`
  (the default) plus `configs/shared/presets-legacy.yaml` for another
  product's layout. Each environment config names **one** file via its
  top-level `presets:` key; preset names resolve within that file only.
- Parsed with the same strictness as environment configs
  (`dec.KnownFields(true)`), with its own top-level struct (`presets:` map) —
  it deliberately has no `environment`/`installers`, so if anything ever
  loads one as an environment it fails validation loudly.
- Paths are cwd-relative (like installer patterns `bin/*.rpm`), so a config
  may point anywhere — but the convention is `configs/shared/` (see the
  `Discover` safeguard, §6).

## 3. Schema additions

Two new keys: **one per config file, one per installer entry**.

```yaml
# top level of the config file
presets: configs/shared/presets.yaml   # NEW, optional — which presets file
                                      # the entries below reference;
                                      # default: configs/shared/presets.yaml

installers:
  - pattern: "bin/jobarranger-server*el8*.rpm"
    use_preset: server-rpm             # NEW, optional — preset name
    install_command:   "..."           # existing — local override beats the preset
    uninstall_command: "..."          # existing — local override beats the preset
    upgrade_command:   "..."          # existing — local override beats the preset
    install_checks:     { ... }       # existing — local override beats the preset
    uninstall_checks:   { ... }
    upgrade_checks:     { ... }
```

- `Config` gains `Presets string \`yaml:"presets"\``; `InstallerSpec` gains
  `UsePreset string \`yaml:"use_preset"\`` — required because `Load` decodes
  with `KnownFields(true)` (unknown keys are rejected as typos).
- An empty `presets:` is treated as the default path; empty `use_preset` is
  treated as unset (no preset).

## 4. Merge semantics — per key, local wins

For each key independently (three command keys + three check blocks):

| Key kind | Entry defines it? | Resulting value |
|---|---|---|
| command key (`install_command`, …) | non-empty | the **local** command |
| command key | empty / omitted | the preset's command; if the preset omits it too → the **extension default** (unchanged behavior) |
| check block | present | the **local** block — replaces the preset's block entirely |
| check block | omitted | the preset's block (may be absent → no checks, as today) |

- **Precedence for commands: entry > preset > extension default.**
  An empty string is "unset" everywhere (existing semantics) — you cannot
  "clear" a preset command with `""`; it just falls through.
- Check-block override is **per whole block**, not per sub-key: if the entry
  defines `install_checks`, the entry's block wins completely (the preset's
  `must_exist`/`commands` for that block do not merge in). Small,
  predictable, no surprises.
- **No list concatenation/union** — rejected: unioning `must_exist` lists
  silently duplicates paths and makes reports confusing.
- Exactly one preset per entry, from the config's **single** selected
  presets file; no chaining, no preset-inheriting-preset, no multi-select
  (non-goals, §10).

Worked example — local `upgrade_checks` on top of a preset:

```yaml
presets: configs/shared/presets.yaml
installers:
  - pattern: "bin/jobarranger-server*ubuntu22.04*.deb"
    use_preset: server-deb
    upgrade_checks:                   # local -> replaces the preset's upgrade block
      must_exist:
        - /etc/jobarranger/jobarg_server.conf.new
    # install_checks / uninstall_checks / commands not set -> from the preset
```

## 5. Validation & error cases

| Case | Behavior |
|---|---|
| `use_preset: "xyz"` — no such preset in the selected file | **load error**: `invalid config configs/jobarg-almalinux-8.yaml: installers[0]: use_preset "xyz" not defined in configs/shared/presets.yaml` |
| selected presets file missing, some entry uses `use_preset` | **load error** naming the presets path |
| **explicit** `presets:` path, file missing, **no** entry uses `use_preset` | **load error** — an explicitly named file that doesn't exist is a typo |
| default path, no entry uses `use_preset` | **OK** — the presets file is never even read (opt-in) |
| presets file malformed / unknown keys | **load error** (strict decode, like environment configs) |
| `use_preset: ""` / `presets: ""` | treated as unset / default |
| entry has `use_preset` **and** local keys | valid — per-key override (§4) |
| preset defines `upgrade_command`, entry omits it | preset's command is used; the **extension default is not applied** (the preset beats defaults) |

Typo protection stays end-to-end: unknown key → strict decoder; known key,
unknown preset name → the load error above; explicit missing file → error.

## 6. Where presets are applied

Inside `config.Load(path)`, **after `Validate()`, before `Resolve()`**:

- `Load` determines the presets file (explicit `presets:` or the default),
  and scans `Installers` for any non-empty `use_preset`. The presets file is
  read only when needed (§5).
- The merge **fills the blanks** in each `InstallerSpec`: empty command
  strings and nil check-block pointers get the preset's values; non-empty
  local values are left alone. Because `resolveInstaller` then runs its
  existing "empty → extension default" logic, the §4 precedence
  (entry > preset > default) falls out **without touching `resolve.go`**.
- `Resolve`, `ResolvedInstaller`, `list`, `run`, events and report see a
  fully normal config — zero downstream changes.

`Discover` safeguard: **the whole `configs/shared/` directory is never
treated as environment configs** — even a custom glob (e.g. `--config
"configs/**/*.yaml"`) that matches presets files there is skipped by
`Discover`. This is what makes multiple presets files safe: drop as many
into `configs/shared/` as needed without polluting the environment list.
(Presets files pointed at from elsewhere still work, but aren't covered by
this safeguard.)

## 7. Migration example (jobarg)

Before (`configs/jobarg-almalinux-8.yaml`, server entry — 14 lines):

```yaml
- pattern: "bin/jobarranger-server*el8*.rpm"
  install_checks:
    must_exist: ["/usr/share/doc/jobarranger-server/database"]
  uninstall_checks:
    must_not_exist: [
      "/usr/share/doc/jobarranger-server/database",
      "/var/log/jobarranger",
      "/etc/jobarranger/jobarg_server.conf",
      "/var/lib/jobarranger/server"
    ]
  upgrade_command: "echo new >> /etc/jobarranger/jobarg_server.conf && rpm -Uvh {installer}"
  upgrade_checks:
    must_exist: ["/etc/jobarranger/jobarg_server.conf.rpmnew"]
```

After (with §2's presets file — **2 lines** per entry):

```yaml
- pattern: "bin/jobarranger-server*el8*.rpm"
  use_preset: server-rpm
```

Ubuntu envs use `use_preset: server-deb` for their entries (both can omit the
top-level `presets:` key — the default path points at the jobarg file, or
they set it explicitly). Same resolved commands and checks as before —
`list` output is identical, which also gives a ready-made migration test:
`list` before/after must match.

## 8. Code impact

| File | Change |
|------|--------|
| `internal/config/config.go` | `Presets` field on `Config`; `UsePreset` field on `InstallerSpec`; `Load` applies presets after `Validate`; `Discover` skips `configs/shared/` |
| `internal/config/presets.go` (new) | `DefaultPresetsPath` const (`configs/shared/presets.yaml`); presets-file load (strict decode, own struct); preset registry; fill-the-blanks merge into `Config.Installers` |
| `internal/config/presets_test.go` (new) | checks applied; command applied (beats extension default); per-key local override; unknown preset error; missing-file errors (used / explicit-unused / default-unused OK); `Discover` skip of `configs/shared/`; presets files parse strictly |
| `internal/config/resolve.go` | **unchanged** — the merge fills blanks before it, so entry > preset > default precedence falls out of the existing logic |
| everything else (`runner`, `task`, `env`, `logging`, `report`, CLI) | **unchanged** — presets resolved before anything downstream |
| `configs/shared/presets.yaml` (new) | the `-rpm` / `-deb` presets per package (§2) |
| `configs/*.yaml` (13 files) | opt-in migration per installer entry, gradual |
| `README.md` | see §9 |

## 9. README.md updates

1. **`installers` table**: new row —

   | `use_preset` | optional | Name of a preset defined in the config's presets file (e.g. `server-rpm`). A preset may carry any installer key except `pattern`: the three command keys and the three check blocks. Per key, a locally defined value **replaces** the preset's; commands fall back to the extension defaults only when neither entry nor preset sets them. |

2. **New top-level key** (documented above the `environment` table or in the
   new shared-presets section):

   | `presets` | optional | Path to the presets file this config's entries reference. Default: `configs/shared/presets.yaml`. Multiple presets files may exist under `configs/shared/`; each config picks one. |

3. **New section "Shared presets"** after the check-blocks reference:
   presets-file shape (§2), the `configs/shared/` placement rule (never
   treated as environments, even by custom `--config` globs), the per-key
   override rule and command precedence with the worked example (§4), and
   the error behavior (§5).
4. **"How it works" intro**: add this plan to the docs reference line
   (`docs/shared-checks-presets-plan.md` (v3.1 shared presets)).

## 10. Decisions

| # | Question | Decision |
|---|----------|----------|
| 1 | Presets files location | `configs/shared/*.yaml` — the whole directory is skipped by `Discover` (never environments), so multiple files are safe |
| 2 | Which presets file a config uses | top-level `presets:` key; default `configs/shared/presets.yaml`; **one file per config** — entries resolve names within it |
| 3 | Merge semantics | **per key/block, local replaces preset** — no list union (silent duplicates) |
| 4 | Preset contents | **every installer key except `pattern`** — commands (`install_command`, `uninstall_command`, `upgrade_command`) **and** check blocks, each optional |
| 5 | Command precedence | entry (non-empty) > preset > extension default; empty string = unset |
| 6 | Missing presets file | error when used; error when **explicitly** named but unused (typo); default-unused → never read, OK |
| 7 | Unknown preset name | load error naming both the config and the selected presets file |
| 8 | Custom `--config` globs | `Discover` skips `configs/shared/` entirely |
| 9 | Presets per entry | exactly one; no chaining / multi-select / preset-inheriting-preset |
| 10 | Naming | `use_preset` + `presets.yaml` (confirmed) — not `use_checks`/`checks.yaml` |
| 11 | Environment-level default preset | **rejected** — not needed; per-entry `use_preset` only |

## 11. Non-goals

- Merging **several** presets files into one namespace for a config
  (`presets:` stays a single path; a list could be added later).
- Environment-level / file-level default preset (rejected — jobarg presets
  differ per package, so a per-file default would almost never apply).
