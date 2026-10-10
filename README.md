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
4. Output is **live JSON logs** (NDJSON, one event per line) — no summary at the end;
   the exit code is `0` only when every case passed.
5. Build a report afterwards from the captured logs with the `report` subcommand.

See `docs/installer-testing-tool-plan.md` (v1 baseline) and
`docs/container-per-task-plan.md` (v2 design) for the full design.

## Usage

```sh
# build (binary is a build artifact — do not commit it)
go build -o installer-test ./cmd/installer-test

# dry-run: show envs, matched installers, resolved commands
./installer-test list

# run all tasks on all environments (live JSON logs), max 5 containers at once
./installer-test run | tee run.jsonl

# install cases only, 8 containers max
./installer-test run --task fresh-install --parallel 8 | tee run.jsonl

# one environment + installer filter
./installer-test run --config configs/rocky-9.yaml --installer "bin/*.rpm" | tee run.jsonl

# keep containers of failed cases for debugging (container IDs are always logged)
./installer-test run --keep | tee run.jsonl

# generate a text report from the captured logs
./installer-test report --from run.jsonl
./installer-test report --from - < run.jsonl      # from stdin

# remove the binary when done (build artifact hygiene)
rm installer-test
```

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
the **package list**: both `install_command` and `uninstall_command` run once
per matched file.

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
| `install_checks`   | optional | Check block run after each installer's install command. All sub-keys optional. |
| `uninstall_checks` | optional | Check block run after each installer's uninstall command. All sub-keys optional. |

### `install_checks` / `uninstall_checks` (all sub-keys optional)

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

| Extension | `install_command` default | `uninstall_command` default |
|-----------|---------------------------|-----------------------------|
| `.deb`    | `dpkg -i {installer}`     | `dpkg -r {package}`         |
| `.rpm`    | `rpm -ivh {installer}`    | `rpm -e {package}`          |
| `.sh`     | `sh {installer}`          | *(none — must be set in config)* |
| other     | *(none — must be set in config)* | *(none — must be set in config)* |

`{package}` derivation: `.deb` strips the `_version_arch.deb` suffix
(`myapp_1.2.3_amd64.deb` → `myapp`); `.rpm` strips version, release and arch
(`nginx-1.24.0-2.el9.noarch.rpm` → `nginx`); anything else is the basename
without extension.
