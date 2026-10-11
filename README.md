# installer-testing-auto

Automation tool to test installer files on target environments using
[testcontainers-go](https://golang.testcontainers.org/) (Windows via WinRM is a future environment type).

## How it works

1. Drop installer files into `bin/` (never committed).
2. Describe each target environment in `configs/<env>.yaml` — one file per environment:
   docker image, installer file patterns (**wildcards allowed**), and optional
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
`docs/container-per-task-plan.md` (v2 design) and `docs/upgrade-task-plan.md`
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
  - pattern: "bin/*.deb"
    install_checks:
      commands:
        - command: "dpkg -s {package}"
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
| `pattern`          | **required** | Glob relative to the repo root (e.g. `bin/*.deb`). Must match at least one file, or the run fails. |
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
| `must_exist`     | optional  | List of file paths that must exist after the phase (e.g. `/usr/bin/myapp`). |
| `must_not_exist` | optional  | List of file paths that must NOT exist after the phase. |
| `commands`       | optional  | List of shell command checks (see below). |

### `commands` entries

| Key                        | Required? | Description |
|----------------------------|-----------|-------------|
| `command`                  | **required** | Shell command run inside the environment (via `sh -c`). |
| `expected_exit_code`       | optional  | Expected exit code. Default: `0`. |
| `expected_stdout_contains` | optional  | Substring the command's output must contain. |

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
