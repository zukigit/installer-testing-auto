# installer-testing-auto

Automation tool to test installer files on target environments using
[testcontainers-go](https://golang.testcontainers.org/) (Windows via WinRM is a future environment type).

## How it works

1. Drop installer files into `bin/` (never committed).
3. Describe each target environment in `configs/<env>.yaml` — one file per environment:
   docker image, installer file patterns (**wildcards allowed**, with
   `{VAR}` variables set by `--var NAME=VALUE`), and optional
   per-installer checks (`must_exist`, `must_not_exist`, shell `commands`).
3. Run the tool: every **(installer file × task) pair** becomes an independent test
   case with **its own container**. Cases run in parallel through a **global pool** —
   `--parallel N` caps the total number of containers running at once (default 5).
   - `fresh-install` case: copy installer → install → install checks.
   - `uninstall` case (standalone): copy installer → install (setup, unchecked) →
     uninstall → uninstall checks.
   - `upgrade` case (standalone, requires versions like `MAJOR.MINOR.PATCH-suffix`
     in file names): copy old installer → install old (setup) → copy new installer →
     upgrade → `upgrade_checks`. Installers without older versions (or whose file
     names carry no version) are **skipped** and shown as such in the report.
     See `docs/upgrade-task-plan.md` for the pairing rules.
4. Output is **live JSON logs** (NDJSON, one event per line) — no summary at the end;
   the exit code is `0` only when every case passed.
5. Build a report afterwards from the captured logs with the `report` subcommand.

See `docs/installer-testing-tool-plan.md` (v1 baseline),
`docs/container-per-task-plan.md` (v2 design),
`docs/upgrade-task-plan.md` (v3 upgrade task),
`docs/shared-checks-presets-plan.md` (v3.1 shared presets) and
`docs/wildcard-checks-pattern-vars-plan.md` (v3.2 wildcard checks + pattern
variables)
for the full design.

## Usage

```sh
# build
go build -mod=vendor -o installer-test-exec github.com/zukigit/installer-testing-auto/cmd/installer-test

# dry-run: show envs, matched installers, resolved commands
./installer-test-exec list

# run all tasks on all environments (live JSON logs), max 5 containers at once
./installer-test-exec run | tee run.jsonl

# install cases only, 8 containers max
./installer-test-exec run --task fresh-install --parallel 8 | tee run.jsonl

# pattern variables: substitute {VERSION} into installer patterns
./installer-test-exec run --var VERSION=7.2.2 | tee run.jsonl

# one environment + installer filter
./installer-test-exec run --config configs/rocky-9.yaml --installer "bin/*.rpm" | tee run.jsonl

# keep containers of failed cases for debugging (container IDs are always logged)
./installer-test-exec run --keep | tee run.jsonl

# generate a text report from the captured logs
./installer-test-exec report --from run.jsonl
./installer-test-exec report --details --from run.jsonl   # steps + install_checks/uninstall_checks/upgrade_checks sections for every case
./installer-test-exec report --from - < run.jsonl      # from stdin

```

## Example reports

A report generated from `run.jsonl` with `./installer-test-exec report --from run.jsonl`
(compact view, grouped by installer — PASS/FAIL with check counts and durations,
failure details inline):

![Report summary](screenshots/report_summary.png)

The same logs with `./installer-test-exec report --details --from run.jsonl`
additionally shows, for every case, the container, the executed steps and the
`install_checks:` / `uninstall_checks:` (and `upgrade_checks:`) sections with
what was checked — paths, `must exist`/`must not exist` expectations, command
exit codes and stdout/stderr:

![Report details](screenshots/report_detail.png)

> Running `run` requires a Docker daemon; the devcontainer has none, so use a
> Docker-enabled host or mount the Docker socket.

## Example config (`configs/ubuntu-24.04.yaml`)

```yaml
environment:
  name: ubuntu-24.04
  type: docker
  image: ubuntu:24.04

installers:
  - pattern: "bin/*.deb"          # wildcards allowed; {VAR} set via --var (see below)
    install_checks:
      commands:
        - command: "dpkg -s {package}"
```
    uninstall_checks:
      commands:
        - command: "dpkg -s {package}"
          expected_exit_code: 1
```

## Configuration reference

One config file = one target environment. An installer **pattern** resolves to
the **package list**: `install_command`, `uninstall_command` and `upgrade_command`
run once per matched file.

### `environment` (required)

| Key      | Required? | Description |
|----------|-----------|-------------|
| `image`  | **required** | Docker image name used for the container (e.g. `ubuntu:24.04`). |
| `type`   | optional | Environment type. Default: `docker`. (`winrm` is a future type.) |
| `name`   | optional | Environment name shown in reports. Default: config file basename (e.g. `configs/ubuntu-24.04.yaml` → `ubuntu-24.04`). |
| `workdir`| optional | In-container directory where installer files are copied. Default: `/tmp`. |

### `installers` (required, at least one entry)

| Key                | Required? | Description |
|--------------------|-----------|-------------|
| `pattern`          | **required** | Glob relative to the repo root (e.g. `bin/*.deb`). Must match at least one file, or the run fails. May contain `{VAR}` variables set via `--var NAME=VALUE` (see [Pattern variables](#pattern-variables-var-in-installer-patterns)). |
| `use_preset`       | optional | Name of a preset defined in the config's presets file (see [Shared presets](#shared-presets-configssharedpresetsyaml)). A preset may carry any installer key except `pattern`; a locally defined key beats the preset. |
| `install_command`  | optional | Install command template. Defaults by file extension (table below). |
| `uninstall_command`| optional | Uninstall command template. Defaults by file extension — **`.sh` has no default, so it must be set explicitly for scripts**. |
| `upgrade_command`  | optional | Upgrade command template for the `upgrade` task, defaults by file extension (table below) — **`.sh` has no default**. Used to upgrade from an **older version of the same package** (pairing rules below). |
| `install_checks`   | optional | Check block run after each installer's install command. All sub-keys optional. |
| `uninstall_checks` | optional | Check block run after each installer's uninstall command. All sub-keys optional. |
| `upgrade_checks`   | optional | Check block run after the upgrade command in `upgrade` cases. Same shape as the other check blocks. |

### Upgrade pairing

The `upgrade` task needs installer file names carrying versions:
**`MAJOR.MINOR.PATCH[-suffix]`** (`.deb`: `name_VERSION_arch.deb`,
`.rpm`: `name-VERSION-release[.arch].rpm`).

- For each installer, the tool finds every **older** version of the same
  package from the config's full resolved list and creates one upgrade case
  per pair: install the old version (setup) → run `upgrade_command` →
  run `upgrade_checks`.
- Version ordering: MAJOR.MINOR.PATCH compared numerically; then the suffix —
  numeric suffixes compared numerically (`0.1.1-1` < `0.1.1-2`), non-numeric
  suffixes (`-beta`, `-1.el9`) compared by the files' modification dates.
- Installers **without an older version** or with a **non-parsable version**
  are `⏸ SKIPPED` in the report (never failures).
- Full pairing rules: `docs/upgrade-task-plan.md`.

### `install_checks` / `uninstall_checks` / `upgrade_checks` (all sub-keys optional)

| Key              | Required? | Description |
|------------------|-----------|-------------|
| `must_exist`     | optional  | List of file paths that must exist after the phase (e.g. `/usr/bin/myapp`). Entries containing `*`, `?` or `[` are **glob patterns** expanded by the container's shell (see [Wildcard file checks](#wildcard-file-checks)). |
| `must_not_exist` | optional  | List of file paths that must NOT exist after the phase. Same glob support. |
| `commands`       | optional  | List of shell command checks (see below). |

### Wildcard file checks

A `must_exist` / `must_not_exist` entry containing any of `*`, `?`, `[` is a
glob pattern, expanded by the **container's own `/bin/sh`** (POSIX glob:
`*`, `?`, `[...]`; `**` is not recursive). Files and directories both count:

- `must_exist` glob: PASS iff the pattern expands to **at least 1** name
  (and the check event records every matched name, capped at 20).
- `must_not_exist` glob: PASS iff the pattern expands to **0** names.

```yaml
install_checks:
  must_exist:
    - /usr/share/man/man8/jobarg_server.8.*   # any compression/layout variation
uninstall_checks:
  must_not_exist:
    - /etc/jobarranger/jobarg_server.conf*    # incl. *.rpmnew leftovers
```

Exact paths (no glob characters) keep the literal `test -e` probe — existing
configs behave identically. Glob paths are passed **unquoted** to the shell,
so they are validated at load time: they may only contain letters, digits,
`/ . - _ + { }` and the glob metachars `* ? [ ]`. Anything else (whitespace,
`$`, `;`, quotes, ...) is a load error — use a `commands` check for such
paths.

### `commands` entries

| Key                        | Required? | Description |
|----------------------------|-----------|-------------|
| `command`                  | **required** | Shell command run inside the environment (via `sh -c`). |
| `expected_exit_code`       | optional  | Expected exit code. Default: `0`. |
| `expected_stdout_contains` | optional  | Substring the command's output must contain. |

### Shared presets (`configs/shared/presets.yaml`)

The 13 environment configs share their per-package checks and commands.
A **preset** = the same keys as an installer entry minus `pattern`:
`install_command`, `uninstall_command`, `upgrade_command`, `install_checks`,
`uninstall_checks`, `upgrade_checks` — each optional, any combination.

```yaml
# configs/shared/presets.yaml
presets:
  server-rpm:
    install_checks:
      must_exist: ["/usr/share/doc/jobarranger-server/database"]
    uninstall_checks:
      must_not_exist: ["/var/log/jobarranger", "/etc/jobarranger/jobarg_server.conf"]
    upgrade_command: "echo new >> /etc/jobarranger/jobarg_server.conf && rpm -Uvh {installer}"
    upgrade_checks:
      must_exist: ["/etc/jobarranger/jobarg_server.conf.rpmnew"]
```

Each environment config names **one** presets file and references presets per
installer entry (paths are cwd-relative, like installer patterns):

```yaml
presets: configs/shared/presets.yaml   # optional; default: configs/shared/presets.yaml

installers:
  - pattern: "bin/jobarranger-server*el8*.rpm"
    use_preset: server-rpm
```

Merge rules — **per key, local wins**:

- A locally defined command/check block replaces the preset's entirely
  (no list unioning — no silent duplicates).
- Commands fall back: entry > preset > extension defaults table below.
- Exactly one preset per entry; no chaining or multiple presets.
- Unknown preset names, missing presets files (when used, or explicitly
  named but unused) and malformed presets files are config-load errors.

Presets files live under `configs/shared/`, which is **never treated as
environment configs** — even custom `--config` globs (e.g.
`configs/**/*.yaml`) skip that directory, so you can keep several presets
files side by side (e.g. per product).

### Pattern variables ({VAR} in installer patterns)

A pattern can carry `{NAME}` variables (uppercase names, `NAME` matching
`[A-Z][A-Z0-9_]*`) that are substituted at load time from **`--var NAME=VALUE`**
command-line flags (repeatable, on `run` and `list`; last value wins, unused
names ignored):

```yaml
installers:
  - pattern: "bin/jobarranger-server-{VERSION}*el8*.rpm"
    use_preset: server-rpm
```

```sh
# the version under test is a run input, not a config edit:
./installer-test-exec run --var VERSION=7.2.2 | tee run.jsonl
./installer-test-exec list --config configs/rocky-9.yaml --var VERSION=7.2.2
```

- A variable **not provided** (or provided empty, `--var VERSION=`)
  substitutes as the empty string — the resulting pattern matches no files
  and the regular "pattern matched no files" load error fires. No prompting.
- A typical subrange pattern (`...-{VERSION}*el8*.rpm`) with an empty value
  still matches *all* versions: the glob decides, there is no special case.
- Variables substitute **in installer patterns only**. A `{VAR}` left in a
  check path or command is literal text (a `must_exist` path with a literal
  `{VAR}` acts like a path that does not exist).
- Lowercase braces are the built-in command placeholders below
  (`{installer}`, `{package}`, `{basename}`) — never substituted here.

### Command placeholders

| Placeholder   | Meaning                                                        | Example (file `myapp_1.2.3_amd64.deb`) |
|---------------|----------------------------------------------------------------|----------------------------------------|
| `{installer}` | In-container path of the matched installer file                | `/tmp/myapp_1.2.3_amd64.deb`           |
| `{basename}`  | File name without directories                                  | `myapp_1.2.3_amd64.deb`                |
| `{package}`   | Package name derived from the file name                        | `myapp`                                |

### Default commands by extension

| Extension | `install_command` default | `uninstall_command` default | `upgrade_command` default |
|-----------|---------------------------|-----------------------------|---------------------------|
| `.deb`    | `dpkg -i {installer}`     | `dpkg -r {package}`         | `dpkg -i {installer}`     |
| `.rpm`    | `rpm -ivh {installer}`    | `rpm -e {package}`          | `rpm -Uvh {installer}`    |
| `.sh`     | `sh {installer}`          | *(none — must be set in config)* | *(none — must be set in config)* |
| other     | *(none — must be set in config)* | *(none — must be set in config)* | *(none — must be set in config)* |

`{package}` derivation: `.deb` strips the `_version_arch.deb` suffix
(`myapp_1.2.3_amd64.deb` → `myapp`); `.rpm` strips version, release and arch
(`nginx-1.24.0-2.el9.noarch.rpm` → `nginx`); anything else is the basename
without extension.
