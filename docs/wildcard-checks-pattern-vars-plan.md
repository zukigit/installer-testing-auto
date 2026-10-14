# Wildcard Check Paths & Pattern Variables Plan (v3.2) — glob `must_exist`/`must_not_exist` + `{VAR}` in installer patterns

> Status: **IMPLEMENTED** as of v3.3 — see the amendment below.
> (Confirmed decisions: variables in **patterns only**, unset/empty variable
> substituted `""`, `--var` flag only with no prompting, no scope guard —
> `{VAR}` elsewhere is literal text, glob checks accept files and
> directories, no report/`list` output changes.)
>
> **AMENDED by** `docs/pattern-vars-unset-no-match-plan.md` **(v3.3)**:
> an unset (or explicitly empty, `--var NAME=`) variable no longer
> substitutes the empty string — it substitutes the never-matching marker
> `{UNSET:NAME}`, so the pattern matches nothing and the existing
> "pattern matched no files" load error fires. Superseded here: decision 10
> and the §4 error-table row "{VERSION} empty but the pattern still matches
> files (e.g. a trailing `*` absorbs it)". All other v3.2 decisions survive.
> Baseline: the implemented v3.1 design (see `docs/shared-checks-presets-plan.md`,
> `docs/upgrade-task-plan.md`, `docs/container-per-task-plan.md`).
> Scope: **two independent features** — (A) wildcard support in file checks,
> (B) user-supplied variables in installer patterns. Each is implementable and
> shippable on its own; they share no code paths. No environment/container
> (`internal/env`) changes.

## 1. Goals

### Feature A — wildcard file checks

Today every `must_exist` / `must_not_exist` entry is probed with
`test -e '<path>'` — quoted, so a path like `/usr/share/man/man8/jobarg_server.8.*`
is tested **literally** and fails (no file is named `*`). Real installs produce
paths whose tail varies (gzipped man pages, versioned doc dirs, `*.rpmnew`
confiles), so per-version check lists have to be updated on every release.

```yaml
install_checks:
  must_exist:
    - /usr/share/man/man8/jobarg_server.8.*     # NEW: glob, at least one match
uninstall_checks:
  must_not_exist:
    - /usr/share/man/man8/jobarg_server.8.*     # NEW: glob, zero matches
```

### Feature B — variables in installer patterns

A pattern like `bin/jobarranger-server-{VERSION}*el8*.rpm` cannot be written
today: there is no way to say "test the version I'm currently validating".
The version becomes a **run input**, not a config edit:

```yaml
installers:
  - pattern: "bin/jobarranger-server-{VERSION}*el8*.rpm"
    use_preset: server-rpm
```

```sh
./installer-test-exec run --var VERSION=7.2.2 | tee run.jsonl
# no --var: the variable substitutes as "" -> the pattern matches no files
# -> the existing "pattern matched no files" load error (see section 4)
```

Decision: variables are received via the **`--var` flag only** — no
interactive prompting. An unprovided (or empty) variable is **not an error by
itself**: it substitutes as the empty string and the **glob decides** — for
`bin/jobarranger-server-{VERSION}*el8*.rpm` an empty `VERSION` yields a
pattern that matches no installer, and the existing no-match behavior
applies (section 4).

Why not just `--installer "bin/jobarranger-server-7.2.2*"` (already exists)?
Both work, but a variable in the config is **declarative**: the config states
"this run is about version X" as a required input, a wrong or missing version
**fails loudly at load time** (a typo'd `--installer` filter silently matches
nothing and runs zero cases), all entries in the file can share one variable,
and `list` shows exactly what the version resolves to.

## 2. Feature A — semantics

A `must_exist` / `must_not_exist` entry is a **glob entry** if it contains any
of `*`, `?`, `[`; otherwise it is an **exact entry** (behavior unchanged).

| Entry kind | Expectation | PASS iff |
|---|---|---|
| exact (no glob chars) | `must_exist` / `must_not_exist` | unchanged: `test -e` says exists / doesn't exist |
| glob | `must_exist` | the pattern expands to **≥ 1** name in the container |
| glob | `must_not_exist` | the pattern expands to **0** names in the container |

- **"At least one match"** (not "all matches") is the `must_exist` glob
  semantics: shell expansion only ever returns existing names, so "expand to
  at least one name" *is* "at least one existing entry matches". "All
  matches" would be vacuously true and meaningless.
- **Both files and directories count as matches** (confirmed): a matched
  directory satisfies `must_exist` exactly like a matched file — same as
  today's exact `test -e`, which also accepts both. `ls -d` lists directory
  matches non-recursively; no file-vs-dir refinement is layered on top.
- Globs are expanded by the **container's own `/bin/sh`** (the files live in
  the container; the host cannot glob them). Semantics are POSIX shell glob:
  `*`, `?`, `[...]`; `**` is **not** recursive (just `*`).
- Relative glob paths resolve against the container's working directory, same
  as exact paths today.

### Probe

`internal/checks/checks.go` currently runs one probe for both expectations:

```go
probeFile(ctx, e, path)          // sh -c "test -e '<path>'"   (quoted -> literal)
```

Split into two:

| Entry kind | Probe | PASS determined by |
|---|---|---|
| exact | unchanged `test -e '<path>'` (quoted) | exit code 0/1 (as today) |
| glob | `sh -c "ls -d -- <pattern>"` (**unquoted** — the shell expands it) | **stdout line count**, not the exit code |

Why stdout-based for globs: when nothing matches, POSIX sh passes the pattern
to `ls` literally, so `ls` exits 2 with "cannot access" on stderr and empty
stdout — but exit codes also conflate *real* errors (unreadable directory,
permission) with "no match". "stdout has ≥1 line" is the precise definition of
"≥1 name was printed", robust against exit-code variance:

- `must_exist` glob: PASS iff stdout has ≥1 line.
- `must_not_exist` glob: PASS iff stdout has 0 lines.
- exec error (env/ssh broken): check fails with the error as detail — as today.

`-d` keeps directory matches non-recursive; `--` guards leading-dash names.
Rejected alternatives: host-side globbing (files are in the container);
`compgen -G` (bash-only — dash on Debian-family images has no `compgen`);
`set -- <pattern>` positional tricks (POSIX passes the unmatched pattern
through literally, so the count is indistinguishable).

### Glob-path validation (load time)

An unquoted pattern is **shell-active**: whitespace word-splits, `;`/`$()`/backticks
would execute. Config is trusted (commands already run arbitrary shell), but
accidental activation must fail loudly, not silently misbehave. So a glob-mode
check path may only contain:

```
letters, digits, / . - _ + { }    and the glob metachars * ? [ ]
```

Braces are allowed as **literal** characters (POSIX `sh` has no brace
expansion), so a leftover `{VERSION}` in a glob check path behaves exactly
like in an exact check path — literal text that matches nothing (§12) —
instead of tripping this charset check.

Anything else (whitespace, `$`, quotes, `;`, `&`, `(`, …) → **load error**
naming the config, the entry and the offending path, with the workaround in
the message: *use a `commands` check (`ls`, `test`) for such paths*. Example
error:

```text
invalid config configs/rocky-9.yaml: installers[0].install_checks.must_exist[0]:
glob path "/usr/share/my docs/server *" contains whitespace or shell metacharacters;
use a commands check for such paths
```

### Known caveats (documented, accepted)

- **Broken symlinks**: `ls -d` lists a matched symlink without dereferencing,
  so a glob matches it while exact `test -e` would not. Rare (man-page /
  confile checks); documented behavior.
- A directory the container user cannot read makes `ls` print nothing for it —
  indistinguishable from "no match" (containers here run effectively as root;
  acceptable).

## 3. Feature A — events & report

The `check` event (`internal/logging`) gains two optional fields for glob
file checks; everything else is unchanged:

```json
{"event":"check","data":{"kind":"file","path":"/usr/share/man/man8/jobarg_server.8.*",
 "expectation":"must_exist","glob":true,"exit_code":0,
 "matches":["/usr/share/man/man8/jobarg_server.8.gz"],"pass":true}}
```

| Field | When present | Content |
|---|---|---|
| `glob` | glob entries | `true` |
| `matches` | glob entries (also on PASS — the report and log consumers want the evidence) | matched names; capped at 20 entries + `"... (N more)"`; empty list omitted for `must_not_exist` PASS |

Failure `detail` strings (self-describing, rendered by the report exactly like
today's "file still exists"):

| Case | detail |
|---|---|
| `must_exist` glob, 0 matches | `no files match pattern` |
| `must_not_exist` glob, N>0 matches | `N file(s) still match pattern: name1, name2, …` (capped) |

**Report: unchanged code for feature A.** The report already renders `path`,
the "must exist"/"must not exist" label and `detail:` lines from event data,
so glob checks render correctly with zero report changes; `matches` stays
machine-readable NDJSON (for `--details` it is already summarized in
`detail`). The one report test addition covers a glob check row end-to-end.

## 4. Feature B — variable syntax & substitution

| Rule | Value |
|---|---|
| Syntax | `{NAME}`, `NAME` matching `[A-Z][A-Z0-9_]*` (`VERSION`, `ARCH`, `MAJOR_RELEASE`, …) |
| Where | **installer `pattern` strings only** — anywhere else `{VAR}` is **literal text, not substituted, no error** (scope rule, §12) |
| Namespacing | uppercase = user variables; lowercase braces are built-in placeholders (`{installer}`, `{package}`, `{basename}`) which only exist **after** matching, in commands — they are meaningless in patterns |
| Substitution | literal, at **load time**, after strict decode and before `Validate()`/presets/`Resolve()` (so `Resolve`'s `filepath.Glob` sees the final pattern — `resolve.go` unchanged) |
| Value chars | anything; the substituted value participates in the pattern glob like any literal text |

### Validation & error cases

| Case | Behavior |
|---|---|
| `{VERSION}` in a pattern, value provided | substituted -> glob -> matched installers |
| `{VERSION}` in a pattern, **not provided** (or `--var VERSION=` empty) | substitutes as `""` -> **the glob decides**; if the resulting pattern matches no files, the **existing** `installers[i].pattern %q matched no files` load error fires (config error event, config skipped — unchanged behavior) |
| `{VERSION}` empty but the pattern still matches files (e.g. a trailing `*` absorbs it) | those files are tested — natural glob semantics, no special case |
| `{VRSION}` typo (no such `--var`) | same as "not provided": substitutes as `""` -> almost certainly no match -> the no-match load error shows the **substituted** pattern, making the mistake visible |
| lowercase `{installer}` / `{package}` / `{basename}` in a pattern | not substituted (built-ins apply to commands only) — **literal text**, same as `{UPPERCASE}` elsewhere (§12): braces are literal in Go's `filepath.Glob` too, so the pattern matches no files -> the no-match load error |
| `{UPPERCASE}` token **anywhere else** in the merged config (check paths, commands, `expected_stdout_contains`, image, ...) | **not substituted, no error, no warning** — literal text (scope rule, §12): a check path like `/usr/share/doc/jobarranger-server-{VERSION}/database` is probed literally, **acts like a path that does not exist** -> `must_exist` fails ("file does not exist"), `must_not_exist` passes |
| `--var NAME=VALUE` with malformed `NAME`/missing `=` | usage error, exit 2 |
| same `--var NAME` given twice | last value wins (documented) |
| `--var` names no selected config uses | silently ignored (the same var set composes across different `--config` selections) |

The substitution itself runs on the config's own `installers` entries
(patterns come only from there — presets carry no `pattern` key, so ordering
against `applyPresets` is irrelevant). There is **no scope guard**: nothing
validates `{...}` tokens outside patterns — they are ordinary literal text
(§12). The only post-`applyPresets` validation is the glob-path charset check
(§2), which runs on the merged config since preset bodies can carry check
paths.

## 5. Feature B — input: `--var` flag only

**No interactive prompting** (rejected — see decisions and non-goals): the
flag is the single input mechanism, CI-friendly and scriptable.

- `--var NAME=VALUE`, repeatable, on **both** `run` and `list` — same flag,
  same semantics (`list` is the dry-run of the same resolution path and needs
  to see the same substitution).
- A variable no `--var` provides, or provided empty (`--var VERSION=`),
  substitutes as `""` — "there is no installer matching that": the resulting
  pattern matches nothing and the **existing** no-match load error fires
  (section 4). No new error path, no TTY detection, no stdin reading —
  `run`'s stdout stays pure NDJSON and its stdin untouched.

### `run_start` event

For reproducibility the **provided** values end up in the logs (empty/
unprovided variables are omitted):

```json
{"event":"run_start","data":{"configs":13,"cases":26,"parallel":5,"vars":{"VERSION":"7.2.2"}}
```

- `logging.RunStart` gains the vars map (`vars` key present only when
  non-empty — old logs are unaffected).
- **Report and `list` output: unchanged.** `report.Parse` already ignores
  unknown `run_start` data keys, so no report code changes; `list` keeps its
  current output shape exactly (matched installers, paths, resolved
  commands, check counts) — the substitution is visible through the matched
  installers it produces. No unnecessary output changes anywhere.

## 6. Usage text (CLI)

```
installer-test run  [--config GLOB] [--task LIST] [--installer GLOB] [--parallel N] [--keep]
                    [--var NAME=VALUE ...]
installer-test list [--config GLOB] [--var NAME=VALUE ...]

  --var NAME=VALUE   set a pattern variable ({NAME} in installer patterns);
                     repeatable; an unset variable substitutes as "" (the
                     pattern then usually matches no files -> load error)
```

## 7. Worked example (both features together)

```yaml
# configs/rocky-9.yaml
presets: configs/shared/presets.yaml
installers:
  - pattern: "bin/jobarranger-server-{VERSION}*el8*.rpm"
    use_preset: server-rpm
```

```yaml
# configs/shared/presets.yaml (excerpt) — wildcards, no version hardcoding
presets:
  server-rpm:
    install_checks:
      must_exist:
        - /usr/share/man/man8/jobarg_server.8.*
    uninstall_checks:
      must_not_exist:
        - /usr/share/man/man8/jobarg_server.8.*
        - /etc/jobarranger/jobarg_server.conf*
```

```sh
$ ./installer-test-exec list --config configs/rocky-9.yaml --var VERSION=7.2.2
env rocky-9
  config:   configs/rocky-9.yaml
  type:     docker
  image:    rockylinux/rockylinux:9
  workdir:  /tmp
  installer bin/jobarranger-server-7.2.2-1.el8.x86_64.rpm
    in-container: /tmp/jobarranger-server-7.2.2-1.el8.x86_64.rpm
    install:      rpm -ivh /tmp/jobarranger-server-7.2.2-1.el8.x86_64.rpm
    ...
# (list output shape unchanged — shown here with the substituted result)
```

## 8. Code impact

| File | Change |
|------|--------|
| `internal/checks/checks.go` | split `probeFile` → exact (`test -e`, unchanged) + `probeGlob` (`ls -d --`, stdout-line-based); glob detection (`*?[`); `matches`/`glob` event fields; new failure `detail` strings |
| `internal/checks/checks_test.go` (new) | stub-env tests (like `internal/task/task_test.go`'s `stubEnv`): glob pass/fail both expectations, exact-path behavior unchanged, match list in fields |
| `internal/config/vars.go` (new) | variable name syntax; `Substitute` — every `{UPPERCASE}` token -> provided value or `""`; **no other token handling** (lowercase built-ins and `{UPPERCASE}` outside patterns are literal text — no error code paths, §12) |
| `internal/config/vars_test.go` (new) | substitution (provided/empty), typo-variable and lowercase-built-in natural no-match paths, `--var`-map plumbing |
| `internal/config/config.go` | `Load(path, vars)` signature (vars applied after decode, before `Validate`); post-`applyPresets` validation: glob-path charset check (braces allowed as literal text; **no scope guard** — §12) |
| `internal/config/resolve.go` | **unchanged** — substitution happens before `filepath.Glob` |
| `internal/config/presets_test.go`, `resolve_test.go` | call-site updates for the `Load` signature only |
| `internal/runner/runner.go` | `Options.Vars`; `List(configPattern, vars)`; pass vars into `LoadBundle` |
| `internal/logging/logging.go` | `RunStart` gains vars (omitted when empty) |
| `cmd/installer-test/main.go` | `--var NAME=VALUE` parsing (run + list), usage text |
| `internal/report/report.go` | parse `matches`/`glob` into `checkRec` (rendered via existing detail; stored for future use); `run_start` `vars` ignored as today — **no change for feature B** |
| `internal/report/text.go` | **unchanged** — no output changes for either feature (§3, §5) |
| `internal/report/report_test.go` | glob check row (detail rendering end-to-end) |
| `README.md` | §9 |

## 9. README.md updates

1. **`install_checks` / `uninstall_checks` table**: `must_exist` /
   `must_not_exist` description gains: entries containing `*`, `?` or `[` are
   glob patterns expanded by the container's shell — `must_exist` needs ≥1
   match, `must_not_exist` needs 0 matches; restricted charset note.
2. **New "Pattern variables" section** after "Command placeholders": `{NAME}`
   syntax, the `--var` flag (no prompting), the §4 error table (unset = `""`
   → no match), and the lowercase-vs-uppercase rule
   ({installer}/{package}/{basename} are command placeholders; {UPPERCASE} are
   run-input pattern variables).
3. **Usage block**: `--var NAME=VALUE` on run and list.
4. **"How it works" intro**: add this plan to the docs reference line
   (`docs/wildcard-checks-pattern-vars-plan.md` (v3.2 wildcard checks +
   pattern variables)).

## 10. Decisions

| # | Question | Decision |
|---|----------|----------|
| 1 | Glob detection | any of `* ? [` in the check path → glob entry; exact otherwise (back-compat by construction) |
| 2 | `must_exist` glob semantics | ≥1 match; **files and directories both count** (confirmed) — same as exact `test -e` |
| 3 | `must_not_exist` glob semantics | 0 matches (files and directories both count, confirmed) |
| 4 | Glob probe | `ls -d -- <pattern>` unquoted in-container; PASS by **stdout lines**, not exit code (exit codes conflate real errors with no-match) |
| 5 | Glob-path charset | restricted (letters/digits `/ . - _ +` + glob metachars); whitespace or shell metachars → load error with `commands`-check workaround |
| 6 | Broken symlinks in globs | counted as matches by `ls -d` (documented difference vs exact `test -e`) |
| 7 | Variable syntax | `{NAME}`, `[A-Z][A-Z0-9_]*` — uppercase only; lowercase braces (built-in placeholders) are never substituted, anywhere — literal text (no special error, per §12 minimalism) |
| 8 | Variable scope | **patterns only** (confirmed): `{UPPERCASE}` anywhere else is literal text — no substitution, no error, no warning (§12) |
| 9 | Input mechanism | **`--var NAME=VALUE` repeatable (run + list) only** — interactive prompting **rejected** (confirmed) |
| 10 | Unset/empty variable | substitutes as `""`; the glob decides — no match → the **existing** `pattern matched no files` load error (confirmed: "empty variable means there is no installers that matches") |
| 11 | Missing variable as usage error | **rejected** — no pre-flight exit-2 error; the natural no-match path decides |
| 12 | Unused `--var` values | silently ignored (compose across `--config` selections) |
| 13 | Duplicate `--var NAME` | last wins |
| 14 | Substitution point | `Load`, after decode, before `Validate`/presets — `resolve.go` untouched |
| 15 | Traceability | `run_start` gains `vars` (omitted when empty) — log-level only |
| 16 | Events for glob checks | `glob: true` + `matches` (capped at 20 + count) also on PASS; failures carry self-describing `detail` strings |
| 17 | Report / `list` output | **unchanged** for both features — no unnecessary output changes (confirmed); failures render via existing `detail` lines |
| 18 | `{VAR}` left in a check path | probed **literally** -> acts like a nonexistent path: `must_exist` fails ("file does not exist"), `must_not_exist` passes — accepted behavior (confirmed: "the tool does not support variables in any other places except in pattern") |
| 19 | Scope guard (error/warning on `{UPPERCASE}` outside patterns) | **rejected** (confirmed) — no guard of any kind; literal pass-through everywhere outside patterns |

## 11. Non-goals

- Variables in check paths / commands / `expected_stdout_contains` — not
  supported: `{VAR}` there is literal text that matches nothing (§12);
  wildcard checks (feature A) already cover "path tail varies by version"
  cases.
- **Scope guard** (load error or warning on `{UPPERCASE}` outside patterns) —
  rejected (confirmed): zero new validation; literal pass-through.
- **Interactive prompting for variables** — rejected (confirmed): flag-only.
- A built-in lowercase `{version}` placeholder in commands (per-installer
  version injection) — separate idea, take it up later.
- `**` recursive globs, brace expansion, extglobs in check paths (POSIX sh glob only).
- `--var-file`, environment-variable fallback (`ITA_VERSION`), or
  config-file default values with an override order — the `--var` flag is
  the single input mechanism.
- Host-side glob expansion of check paths (files live in the container).

## 12. Resolved decision — variable scope: patterns only, literal elsewhere

**Confirmed: variables are supported in installer `pattern` strings only
("for now"). Anywhere else, an `{UPPERCASE}` token is ordinary literal text —
not substituted, no error, no warning.** The check simply behaves as if the
path does not exist, because the tool does not support variables in any
other place:

```yaml
installers:
  - pattern: "bin/jobarranger-server-{VERSION}*el8*.rpm"   # substituted
    install_checks:
      must_exist:
        - /usr/share/doc/jobarranger-server-{VERSION}/database   # NOT substituted:
      # probed literally -> acts like a folder that does not exist -> FAIL
      commands:
        - command: "jobarranger-server --test {VERSION}"          # NOT substituted:
      # the command receives the literal {VERSION} argument
```

Concrete consequences, documented and accepted:

| `{VAR}` left in… | Behavior |
|---|---|
| a `must_exist` path | probed literally -> "file does not exist" -> **check FAILS** — visible in the report, so the mistake surfaces (just late: after install, not at load) |
| a `must_not_exist` path | probed literally -> never exists -> **check PASSES** — the mistake does **not** surface; accepted sharp edge |
| a `command` | the command gets the literal `{VERSION}` argument — usually fails loudly (unknown option/argument) |
| an exact path | `test -e '/…/{VERSION}/…'` -> does not exist |
| a glob path | `ls -d -- /…/{VERSION}*` -> braces are literal in POSIX `sh` (no brace expansion) -> matches nothing (charset allows braces as literal text, §2) |
| a `pattern` itself (`{installer}` built-in, or any lowercase brace) | never substituted — literal text in the glob, matches no files -> the existing no-match load error (same natural path as a `{VRSION}` typo) |

This keeps the implementation minimal (zero guard code) and leaves room for
a future "substitute everywhere" extension that would simply start
substituting in those places — configs that today rely on literal `{VAR}`
text elsewhere would be the only breakage, and such reliance is unlikely
(almost always a mistake today). Until then: **patterns only, for now.**
