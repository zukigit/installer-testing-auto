# installer-testing-auto

Tests installer files (`.deb`, `.rpm`, `.msi`, `.exe`, scripts, …) in fresh
containers: install, uninstall and upgrade, each followed by your checks.
Linux environments run in Linux containers; Windows environments in Windows
Server Core containers ([Windows guide](docs/windows.md)).

## How it works

1. Put installer files into `bin/` (never committed).
2. Describe each target environment in `configs/<env>.yaml`: the image, which
   installer files to test (wildcards and `{VAR}` variables allowed) and
   optional checks.
3. `run` turns every **(installer file × task)** pair into a case with **its
   own container**; cases run in parallel (`--parallel`, default 5):
   - `fresh-install`: copy → install → `install_checks`
   - `uninstall`: copy → install → uninstall → `uninstall_checks`
   - `upgrade`: install an older installer → upgrade to the new one →
     `upgrade_checks` (see [Upgrade task](#upgrade-task))
4. `run` prints live JSON log lines; `report` turns them into a readable
   report. The exit code is `0` only when every case passed.

Design history: `docs/`.

## Quick start

```sh
# build
go build -mod=vendor -o installer-test-exec ./cmd/installer-test

# dry run: environments, matched installers, resolved commands, upgrade pairs
./installer-test-exec list

# run everything and keep the log
./installer-test-exec run | tee run.jsonl

# report (add --details for every step and check)
./installer-test-exec report --from run.jsonl
```

More `run` options:

```sh
./installer-test-exec run --config configs/rocky-9.yaml      # one environment
./installer-test-exec run --task fresh-install --parallel 8  # one task, 8 containers
./installer-test-exec run --installer "bin/*.rpm"            # filter installer files
./installer-test-exec run --var VERSION=7.2.2                # fill {VERSION} in patterns
./installer-test-exec run --keep                             # keep containers of failed cases
```

- Without `--config`, all `configs/*.yaml` run **except Windows configs** —
  those always need `--config`.
- `run` needs a Docker engine (Linux containers for Linux configs, Windows
  containers for Windows configs).

## Example report

![Report summary](screenshots/report_summary.png)

`report --details` also shows each case's container, steps, and every check
with its result and output:

![Report details](screenshots/report_detail.png)

## Example config

```yaml
# configs/ubuntu-24.04.yaml
environment:
  image: ubuntu:24.04

installers:
  - pattern: "bin/*.deb"
    install_checks:
      must_exist: ["/usr/bin/myapp"]
      commands:
        - command: "dpkg -s {package}"
    uninstall_checks:
      must_not_exist: ["/usr/bin/myapp"]
```

The real configs are `configs/jobarg-*.yaml`.

## Configuration reference

### `environment`

| Key | Description |
|---|---|
| `image` | **Required.** Container image, e.g. `ubuntu:24.04`. |
| `name` | Name in reports. Default: config file name (`configs/ubuntu-24.04.yaml` → `ubuntu-24.04`). |
| `os` | `linux` (default) or `windows` ([Windows guide](docs/windows.md)). |
| `isolation` | Windows only: `process` or `hyperv`. |
| `workdir` | Where installer files are copied in the container. Default: `/tmp` (Linux), `C:\ita` (Windows). |
| `type` | `docker` (default and only value). |

### `installers`

Each entry selects installer files with `pattern`; every matched file is
tested.

| Key | Description |
|---|---|
| `pattern` | **Required.** Glob from the repo root, e.g. `bin/*.deb`. Must match at least one file. May contain [`{VAR}` variables](#pattern-variables). |
| `use_preset` | Take defaults from a [preset](#presets). |
| `install_command` / `uninstall_command` / `upgrade_command` | Command templates. Defaults depend on the file extension ([table](#default-commands)). |
| `upgrade_from` | The exact old installer(s) to upgrade from ([Upgrade task](#upgrade-task)). |
| `version_format` | File-name template for version-based upgrade pairing ([Upgrade task](#upgrade-task)). |
| `install_checks` / `uninstall_checks` / `upgrade_checks` | Checks after install / uninstall / upgrade (below). |

### Checks

All keys are optional:

| Key | Passes when |
|---|---|
| `must_exist` | every listed path exists |
| `must_not_exist` | no listed path exists |
| `commands` | each command exits with `expected_exit_code` (default `0`) and, if set, its output contains `expected_stdout_contains` |

```yaml
install_checks:
  must_exist:
    - /usr/share/man/man8/jobarg_server.8.*   # glob: at least one match
  commands:
    - command: "systemctl is-enabled jobarg-server"
      expected_stdout_contains: enabled
uninstall_checks:
  must_not_exist:
    - /etc/jobarranger/jobarg_server.conf*    # glob: no match
```

- Paths with `*`, `?` or `[` are globs (files and directories, not
  recursive). On Linux they may only contain letters, digits, `/ . - _ + { }`
  and `* ? [ ]`; use a `commands` check for anything else.
- Commands run with `sh -c` on Linux and PowerShell on Windows.

### Command placeholders

| Placeholder | Meaning | Example (`myapp_1.2.3_amd64.deb`) |
|---|---|---|
| `{installer}` | path of the file in the container | `/tmp/myapp_1.2.3_amd64.deb` |
| `{basename}` | file name | `myapp_1.2.3_amd64.deb` |
| `{package}` | package name from the file name | `myapp` |

`{package}`: `.deb` drops `_version_arch.deb`, `.rpm` drops
version-release.arch, anything else drops the extension.

### Default commands

| Extension | install | uninstall | upgrade |
|---|---|---|---|
| `.deb` | `dpkg -i {installer}` | `dpkg -r {package}` | `dpkg -i {installer}` |
| `.rpm` | `rpm -ivh {installer}` | `rpm -e {package}` | `rpm -Uvh {installer}` |
| `.sh` | `sh {installer}` | — | — |
| `.msi` | `msiexec /i` (silent) | `msiexec /x` (silent) | `msiexec /i` (silent) |
| `.ps1` | `& '{installer}'` | — | — |
| other | — | — | — |

"—" means: set the command in the config, otherwise the case fails.

### Presets

Configs can share installer settings through a presets file (default
`configs/shared/presets.yaml`, or the config's top-level `presets:` key). A
preset may set every installer key except `pattern`; a key set on the entry
wins.

```yaml
# configs/shared/presets.yaml
presets:
  server-rpm:
    install_checks:
      must_exist: ["/usr/share/doc/jobarranger-server/database"]
    uninstall_checks:
      must_not_exist: ["/var/log/jobarranger"]
```

```yaml
# a config
installers:
  - pattern: "bin/jobarranger-server*el8*.rpm"
    use_preset: server-rpm
```

A key set on the entry replaces the preset's value completely (lists are not
merged). Files under `configs/shared/` are never treated as environments.

### Pattern variables

`{NAME}` (uppercase) in `pattern` and `upgrade_from` is filled from
`--var NAME=VALUE`, so the version under test is a run input:

```yaml
installers:
  - pattern: "bin/jobarranger-server-{VERSION}*el8*.rpm"
```

```sh
./installer-test-exec run --var VERSION=7.2.2
```

A variable that is not given matches **nothing** (the pattern then fails
with "matched no files") — it never silently selects all versions.

## Upgrade task

An upgrade case installs an **old** installer, upgrades to the **new** one
with `upgrade_command`, then runs `upgrade_checks`. How the old installer is
chosen:

| How | Key | Use when |
|---|---|---|
| exactly the file(s) you name | `upgrade_from` | you know the old installer |
| older versions, read from `.deb` / `.rpm` file names | — | Linux packages (default) |
| older versions, read with your file-name template | `version_format` | other file names, Windows |

**`upgrade_from`** — the old file can live anywhere, no versions needed:

```yaml
installers:
  - pattern: "bin/app-*.rpm"                 # app-0.1.1.rpm and app-0.1.2.rpm
    upgrade_from: "bin/app-0.1.1.rpm"        # -> one case: 0.1.1 -> 0.1.2
```

A file is never upgraded onto itself (same file name = same installer). A
glob or `{VAR}` works too: `upgrade_from: "bin/old/app-{OLD_VERSION}.rpm"`.

**By version** — every installer is paired with every older version of the
same package in `bin/`, also versions outside a `--var` pin
(`--var VERSION=0.2.0` still upgrades from `0.1.0`).

**`version_format`** — describes file names the built-in rules don't know:

```yaml
installers:
  - pattern: "bin/jobarranger-agentd-*.msi"
    version_format: "{package}-{version}-x64.msi"
```

`{version}` (required, e.g. `7.2.2`, `7.2.2-1`, `7.2.2.1234`) and
`{package}` (optional) are captured; `*` matches anything; everything else
must match literally.

**Skipped** installers show as `⏸ SKIPPED` in the report (never failures):

| Reason | Meaning |
|---|---|
| `no_older_version` | no older version of the same package |
| `nonstandard_version` | no version found in the file name |
| `no_version_format` | Windows installer without `version_format` or `upgrade_from` |
| `no_upgrade_from_match` | `upgrade_from` matched no file |
| `is_upgrade_from` | this file is the `upgrade_from` installer itself |

`list` shows each installer's pairing (`pairing:` line) before anything runs.

## Windows

Windows configs set `os: windows` and run in Windows Server Core
containers on a Windows host. Commands and checks use PowerShell, `.msi`
files need no commands, and registry paths work in checks. Run them with an
explicit `--config`:

```powershell
.\installer-test-exec.exe run --config "configs/jobarg-windows-*.yaml" > run.jsonl
```

Host setup, an example config, PowerShell tips and troubleshooting:
**[docs/windows.md](docs/windows.md)**.

## Cleanup

Containers are removed when their case ends, and also when the tool is
stopped or killed. `--keep` containers of failed cases stay up while the
tool runs; afterwards Linux ones are removed by testcontainers, Windows ones
stay until you remove them. For leftovers after a reboot or crash:

```sh
./installer-test-exec cleanup                  # --keep containers stay
./installer-test-exec cleanup --include-kept   # --keep containers too
```
