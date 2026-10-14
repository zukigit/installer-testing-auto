# Pattern Variables Unset No-Match Plan (v3.3) — remove the implicit all-versions default

> Status: **CONFIRMED — ready to implement, not yet implemented** (user direction:
> "remove default version"; "user not input version with flag → jobarg-redhat-9.yaml
> is accepting → just act like there is no installer file").
> Baseline: the implemented v3.2 (`docs/wildcard-checks-pattern-vars-plan.md`).
> Scope: **one behavior change** — what an unset (or empty) pattern variable
> substitutes. No new keys, no CLI changes, no event/report/check changes.

## 1. What happened (verified root cause)

```sh
sudo ./installer-test-exec run --config ./configs/jobarg-redhat-9.yaml | tee run.jsonl
```

`configs/jobarg-redhat-9.yaml` asks for a version:

```yaml
installers:
  - pattern: "bin/jobarranger-server-{VERSION}*el9*.rpm"
```

No `--var VERSION=…` was given, yet the tool ran cases against **7.2.x**
installers. Verified against the actual `bin/` contents (el9 files present:
server/agentd/manager at 7.2.1 **and** 7.2.2, utils at 7.2.2):

- v3.2 decision 10: an unset variable substitutes as the **empty string** —
  so `{VERSION}` becomes `""` and the pattern silently turns into
  `bin/jobarranger-server-*el9*.rpm`.
- Because a `*` sits **right after the token**, the missing version is
  absorbed by that wildcard: `jobarranger-server-` + `*` matches
  `7.2.1-1.el9.x86_64` and `7.2.2-1.el9.x86_64` alike.
- Result: with no `--var` at all, the tool resolved **7 installers** (every
  el9 version of every package) and ran them — behaving as if "all versions /
  latest present" were a **default version**. Exactly what the
  `list` output shows today:

```text
$ ./installer-test-exec list --config configs/jobarg-redhat-9.yaml
  installer bin/jobarranger-server-7.2.1-1.el9.x86_64.rpm     <-- no --var given!
  installer bin/jobarranger-server-7.2.2-1.el9.x86_64.rpm
  installer bin/jobarranger-agentd-7.2.1-1.el9.x86_64.rpm
  ... (7 installers, both versions)
```

This is the v3.2 §4 case row "{VERSION} empty but the pattern still matches
files (e.g. a trailing `*` absorbs it)" — documented, but in practice it
defeats the point of the variable: an unset version should mean **no
version**, not **every version**.

## 2. The change

**Remove the empty-string default. An unset (or explicitly empty) variable
substitutes a never-matching marker, so the pattern matches nothing — and the
existing "pattern matched no files" load error rejects the config: "just act
like there is no installer file."**

| Run | v3.2 (today) | v3.3 (this plan) |
|---|---|---|
| `run --var VERSION=7.2.2` | selects the 7.2.2 files | **unchanged** |
| `run` (no `--var`) | `""` → `server-*el9*.rpm` → matches **all** el9 versions (the implicit default version) | **no match** → load error: `installers[0].pattern "bin/jobarranger-server-{UNSET:VERSION}*el9*.rpm" matched no files` → config rejected, **nothing runs** |
| `run --var VERSION=` (explicitly empty) | same as no `--var` (match-all) | same as no `--var` (**no match**) |
| config typo `{VRSION}`, no `--var VRSION` | `""` → possible silent match-all (dangerous) | **no match**; the error shows `{UNSET:VRSION}` — the typo is visible |

For the motivating case:

```text
$ ./installer-test-exec run --config ./configs/jobarg-redhat-9.yaml
{"event":"error","data":{"message":"config configs/jobarg-redhat-9.yaml: installers[0].pattern \"bin/jobarranger-server-{UNSET:VERSION}*el9*.rpm\" matched no files"}}
{"event":"run_end","data":{"failed":0,"passed":0,"config_errors":1}}
# exit code 1 — no containers started, exactly like any pattern with no files
```

- Per-config, per-entry: **every** entry whose pattern references an unset
  variable fails to match; since `Resolve` treats a no-match pattern as a
  load error for the whole config, the config is rejected (existing
  behavior, reused verbatim).
- No new error message, no new error path — the substituted pattern in the
  existing message already explains itself (`{UNSET:VERSION}` reads as
  "VERSION was not set").
- `list` behaves identically (prints the same `ERROR   config …` line it
  prints for any no-match pattern today).

## 3. The marker: `{UNSET:NAME}`

An unset `{NAME}` substitutes the literal `{UNSET:NAME}` (e.g.
`{UNSET:VERSION}`), chosen so the guarantee "unset ⇒ no match" cannot leak:

| Property | Why it holds |
|---|---|
| never appears in real installer file names | file names are package-version-release strings; no `{UNSET:…}` text exists in `bin/` — so the glob always fails |
| contains no glob metachars (`* ? [ ] \`) | safe through the host-side `filepath.Glob` unchanged |
| `:` is not allowed in variable names (`[A-Z][A-Z0-9_]*`) | the marker is **not re-matched** by the substitution regex — no self-reference, no double substitution |
| reads as "the variable NAME was not set" | the reused load-error message stays self-explanatory |

Rejected alternatives:

- **Keep `""` and error only when a pattern references an unset variable**
  (dedicated "variable not set" message): a second, special error path —
  rejected; the user asked for the no-installer-file behavior, which the
  marker reaches through the existing path.
- **Fail the whole run pre-flight when a variable is missing**: v3.2
  decision 11 already rejected the pre-flight usage error; per-config load
  errors compose better across `--config` selections.
- **Warn and match all (today's behavior)**: rejected — that is the bug
  being fixed.

## 4. What does not change (v3.2 decisions that survive)

- `--var NAME=VALUE` flag only, on `run` and `list`; repeatable; last wins;
  unused names ignored; **no prompting** (v3.2 §5).
- Substitution in installer **patterns only** — `{VAR}` anywhere else stays
  literal text; no scope guard (v3.2 §12).
- Lowercase built-in placeholders (`{installer}`, `{package}`, `{basename}`)
  are never substituted (v3.2 §4).
- Provided values substitute literally and may contain anything, including
  glob text (v3.2 §4).
- `run_start` carries the **provided** `vars` (nothing to record for unset
  ones); report/`list` output shapes unchanged (v3.2 decisions 15–17).
- Wildcard file checks (feature A) are untouched.

## 5. Code impact

| File | Change |
|------|--------|
| `internal/config/vars.go` | `Substitute`: a name with **no value or an empty value** substitutes `{UNSET:NAME}` instead of `""` (provided non-empty values unchanged) — ~3 lines |
| `internal/config/vars_test.go` | update unset/empty/typo expectations; add the **regression test for the motivating case**: `bin/pkg-{VERSION}*el9*.rpm` with both 7.2.1/7.2.2-style files present and no `--var` must produce the matched-no-files error naming `{UNSET:VERSION}` (v3.2 matched both files) |
| `internal/checks/`, `internal/config/config.go`, `resolve.go`, `runner`, `logging`, `report`, CLI | **unchanged** |
| `README.md` | §6 |
| `docs/wildcard-checks-pattern-vars-plan.md` | optional amendment note: decision 10 and the §4 "trailing `*` absorbs it" row are superseded by this plan |

## 6. README.md updates

1. **"Pattern variables" section**: replace the "empty value still matches
   all versions" bullet with: *a variable not provided (or provided empty,
   `--var VERSION=`) substitutes a never-matching marker — the pattern
   matches no files and the regular "pattern matched no files" load error
   fires. An unset variable never silently widens to all versions.*
2. No other sections change (usage text, tables, wildcard section stay as
   documented in v3.2).

## 7. Decisions

| # | Question | Decision |
|---|----------|----------|
| 1 | Unset variable substitutes | `{UNSET:NAME}` — a literal that matches no real file — **not** `""` (confirmed: "remove default version") |
| 2 | Resulting behavior | pattern matches nothing → the **existing** `pattern … matched no files` load error → config rejected ("just act like there is no installer file") |
| 3 | Explicitly empty `--var VERSION=` | treated exactly like unset ("no value = no version") |
| 4 | Typo'd variable name (`{VRSION}`) | same path: `{UNSET:VRSION}` in the error — the typo becomes visible (v3.2 could silently match all) |
| 5 | Marker syntax | `{UNSET:NAME}` — never in real filenames, no glob metachars, not re-matched by the substitution regex, self-explanatory in the reused error message |
| 6 | Dedicated "variable not set" error message | **rejected** — reuse the existing no-match path (user direction); zero new error strings |
| 7 | Pre-flight run-level failure | stays rejected (v3.2 decision 11) — per-config load errors compose across `--config` selections |
| 8 | Scope | patterns only, as v3.2 — the marker never appears anywhere else (unset variables outside patterns are already literal text) |

## 8. Non-goals

- Optional variables with wildcard fallback ("match all when unset") — that
  is the removed behavior; `--installer` remains the explicit filter for
  that intent.
- Config-file default values or override orders for variables
  (`--var` stays the single source).
- Any change to feature A (wildcard checks), events, report rendering,
  `list` output, or prompting (still rejected).
