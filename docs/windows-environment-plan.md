# Windows Environment Plan (v4) — Windows Server Core containers via Docker Engine

> Status: **PROPOSED — open questions answered (§13); the spike in §3 must
> pass before Phase 1.** Implementation scope is the **core system only**:
> `configs/` stays untouched — environment configs, installer patterns and
> Windows presets (paths, services, registry keys, upgrade commands, image
> versions) are written by the user afterwards.
> Baseline: the implemented v3.5 (`docs/list-upgrade-checks-plan.md`) on top
> of the v2 container-per-task design (`docs/container-per-task-plan.md`) and
> the v3 upgrade task (`docs/upgrade-task-plan.md`).
> Scope: test **any Windows installer file type** (`.msi` with built-in
> defaults; `.exe`, `.ps1`, `.msix`, … with commands from the config) in
> **Windows containers** (`servercore` image) run by a
> **plain Docker Engine** (no Docker Desktop) on a Windows host — the same
> three tasks, the same check blocks, the same NDJSON events and report.
> Linux behavior must stay **byte-identical**.

## 1. Goal

"Option A" from the design discussion: Windows is just another **container
environment**. Everything the Linux side guarantees carries over unchanged:

- every **(installer file × task)** pair is a case with **its own container**
  (fresh state per case, `--parallel` pool, `--keep` for failed cases);
- tasks `fresh-install`, `uninstall`, `upgrade` with `install_checks`,
  `uninstall_checks`, `upgrade_checks` (`must_exist`, `must_not_exist`,
  `commands`);
- shared presets, `{VAR}` pattern variables, the widened upgrade pool;
- live NDJSON logs → `report --from run.jsonl`.

What differs is **how commands are spoken inside the environment** (PowerShell
instead of `/bin/sh`), **paths** (`C:\ita\x.msi` instead of `/tmp/x.deb`) and
**installer conventions** (`msiexec`, MSI exit codes, MSI file names).

Illustrative only — the core system makes this config possible; writing it
(and its preset) is the user's job, not part of this plan's implementation:

```yaml
# configs/jobarg-windows-2022.yaml (user-written)
presets: configs/shared/presets.yaml

environment:
  name: windows-2022
  type: docker
  os: windows                                            # NEW (default: linux)
  image: mcr.microsoft.com/windows/servercore:ltsc2022
  isolation: process                                     # NEW, windows only (optional)
  # workdir defaults to C:\ita for os: windows

installers:
  - pattern: "bin/jobarranger-agentd-{VERSION}*.msi"
    use_preset: agentd-msi
```

```sh
# on the Windows host (Docker Engine + Containers feature installed)
installer-test-exec.exe list --config "configs/jobarg-windows-*.yaml" --var VERSION=7.2.2
installer-test-exec.exe run  --config "configs/jobarg-windows-*.yaml" --var VERSION=7.2.2 > run.jsonl
installer-test-exec.exe report --from run.jsonl
```

## 2. Where Linux assumptions live today (verified)

The `Environment` interface (`internal/env/env.go`) is already OS-neutral:
`Start / Copy / Exec(argv) / Stop`. The Linux assumptions sit **above and
below** it:

| Location | Linux assumption |
|---|---|
| `internal/task/install.go` `runPhase` | `[]string{"/bin/sh", "-c", cmd}` |
| `internal/task/uninstall.go` | `/bin/sh -c` for `setup-install` |
| `internal/task/upgrade.go` | `/bin/sh -c` for `setup-install` and `upgrade` |
| `internal/checks/checks.go` `probeFile` | `/bin/sh -c "test -e '<path>'"`, POSIX single-quote `quote()` |
| `internal/checks/checks.go` `probeGlob` | `/bin/sh -c "ls -d -- <pattern>"` (unquoted, shell-expanded) |
| `internal/checks/checks.go` command checks | `/bin/sh -c <command>` |
| `internal/env/docker.go` `Start` | keep-alive `Cmd: sleep infinity` |
| `internal/env/docker.go` `Copy` | `mkdir -p '<dir>'` via `sh`; `filepath.Dir` on the container path |
| `internal/env/docker.go` factory | default workdir `/tmp` |
| `internal/config/resolve.go` `resolveInstaller` | `ContainerPath = filepath.ToSlash(filepath.Join(workdir, base))` — **host**-OS path joining for a **container** path |
| `internal/config/config.go` `DefaultWorkdir` | `/tmp`; also the fallback in `runner.envTracker.caseStarted` |
| `internal/config/config.go` `validateGlobPaths` | POSIX-shell-safe charset (because globs are passed **unquoted** to `sh`) |
| `internal/config/resolve.go` `default*Cmd`, `derivePackage` | `.deb` / `.rpm` / `.sh` only |
| `internal/config/version.go` `ExtractVersionFromFilename` | `.deb` / `.rpm` only → every `.msi` would be `nonstandard_version` (upgrade always skipped) |
| testcontainers-go | Ryuk reaper (`testcontainers/ryuk`, a **Linux** image) is started by default; `CopyFileToContainer` builds a tar whose entry name is the **full container path** and extracts it at `/` (`vendor/.../testcontainers-go/docker.go` `copyToContainer`, `file.go` `tarFile`) — `C:\ita\x.msi` as a tar entry name is not a valid Windows extraction target |

Nothing in `runner`, `logging`, `report` (rendering) or the presets/vars code
depends on the container OS — those stay as they are apart from small
additions (§9).

## 3. Phase 0 — spike on the Windows host (before any code)

Manual, ~1 hour, on the target Windows host. Each item decides an
implementation detail below; record the results in this doc (§14).

| # | Verify | Command (sketch) | Decides |
|---|---|---|---|
| S1 | Engine + image work, process isolation | `docker run --rm --isolation process mcr.microsoft.com/windows/servercore:ltsc2022 cmd /c ver` | host/image pairing, §4.3 |
| S2 | Keep-alive command | `docker run -d ... powershell -NoProfile -Command "while ($true) { Start-Sleep 3600 }"` then `docker exec` | §6.1 |
| S3 | Copy into a **running** container | `docker cp x.msi <ctr>:C:\ita\x.msi`, and via the API path testcontainers uses (tar entry `ita/x.msi`, destination `/`) | §6.2 |
| S4 | The installer installs silently in Server Core | `Start-Process msiexec -ArgumentList '/i C:\ita\x.msi /qn /norestart /l*v C:\ita\x.log' -Wait -PassThru` → `ExitCode` 0 / 3010 | that option A is viable at all |
| S5 | Exit-code propagation through `docker exec powershell -Command` | `exit 7`, a failing native exe, a failing cmdlet | §5.2 wrapper |
| S6 | Native stderr under `docker exec` (PS 5.1) is passed through, not turned into error records | `cmd /c "echo err 1>&2"` | §5.2 wrapper |
| S7 | Output line endings / encoding | non-ASCII path in `Get-Item` output | CRLF + UTF-8 handling, §6.3 |
| S8 | Hidden files and registry paths with `Get-Item -Force` / `Test-Path` | `Test-Path 'HKLM:\SOFTWARE\Microsoft'` | §7 |
| S9 | Container stop/remove time | `docker rm -f` timing | expectations only |

If S3 or S4 fails, stop and revisit (S3 → copy via a bind-mounted folder
instead; S4 → the installer needs the full `windows/server` image — a
config-only change, or a VM environment — out of scope).

## 4. Configuration changes

### 4.1 `environment.os` (new, optional)

| Value | Meaning |
|---|---|
| `linux` (default) | today's behavior — POSIX shell dialect, `/tmp` workdir |
| `windows` | PowerShell dialect, `C:\ita` workdir, MSI defaults, Ryuk off (§6.4) |

Unknown values are a load error. `type` stays `docker`: the OS is a property
of the **environment**, the type is the **provisioning mechanism** (a future
`winrm`/VM type would also be `os: windows` and reuse the same dialect).

### 4.2 `environment.workdir`

Default becomes OS-dependent: `/tmp` (linux) / `C:\ita` (windows). For
`os: windows` the workdir must match `^[A-Za-z]:\\` and, for v4, must be on
drive `C:` (the copy mechanism extracts relative to the container root —
§6.2). Forward slashes in a Windows workdir are normalized to `\`.

### 4.3 `environment.isolation` (new, optional, windows only)

`process` | `hyperv` | unset (daemon default — `process` on Windows Server,
`hyperv` on Windows 10/11). Passed to the container's `HostConfig.Isolation`.
Setting it with `os: linux` is a load error. Recommendation: `process` on a
Server host whose build matches the image (fast; and `docker cp` into a
running container works — spike S3).

### 4.4 Container paths

`ResolvedInstaller.ContainerPath` is built with **container-OS** joining,
never host joining:

- linux: `path.Join(workdir, base)` → `/tmp/myapp_1.2.3_amd64.deb` (same as today)
- windows: `strings.TrimRight(workdir, `\`) + `\` + base` → `C:\ita\myapp-7.2.2-x64.msi`

`{installer}` expands to that path, so commands and checks see native
Windows paths. `resolveInstaller` gains the OS (or a `JoinPath` func).

### 4.5 Check paths for windows

- Exact paths: any string; probed with `Test-Path -LiteralPath` (quoted, §7).
- Glob detection unchanged (`* ? [`), but globs are passed **quoted** to
  PowerShell (PowerShell expands wildcards in `-Path` itself), so the POSIX
  charset restriction of `validateGlobPaths` **does not apply** to
  `os: windows` — paths like `C:\Program Files\Job Arranger\*.dll` are valid.
  Only control characters (newline, NUL, …) are rejected.
- Registry paths work out of the box: `HKLM:\SOFTWARE\JobArranger` in
  `must_exist` / `must_not_exist` (PowerShell providers).

### 4.6 Presets

No schema change. Windows presets live next to the Linux ones (or in their
own file, e.g. `configs/shared/presets-windows.yaml`, selected with the
config's `presets:` key). Presets are OS-agnostic data — a Linux preset used
by a Windows env simply fails its checks, which is the user's mistake to fix.

## 5. Shell dialect abstraction (the core refactor)

### 5.1 Interface

New package `internal/shell` — pure functions, no I/O, fully unit-testable:

```go
// Dialect builds the argv and probe scripts for one command language.
type Dialect interface {
	Name() string                       // "posix" | "powershell"
	Command(script string) []string     // argv for Environment.Exec
	PathExists(path string) string      // exit 0 = exists, 1 = absent, other = error
	Glob(pattern string) string         // prints one match per line, nothing on no match
	MkdirAll(dir string) string
	Quote(s string) string
}

var POSIX Dialect      // exactly today's strings
var PowerShell Dialect
```

`Environment` gains one method — the only interface change:

```go
type Environment interface {
	...
	// Shell returns the command dialect spoken inside the environment.
	Shell() shell.Dialect
}
```

The Docker environment returns `shell.POSIX` or `shell.PowerShell` from
`env.Options.OS`. A future WinRM/VM environment returns `shell.PowerShell`
and reuses everything in §5–§8.

Call sites (`task/*.go`, `checks/checks.go`, `env/docker.go`) replace every
literal `[]string{"/bin/sh", "-c", x}` with `e.Shell().Command(x)` and every
`test -e` / `ls -d` / `mkdir -p` with the dialect's builders.

### 5.2 Dialects

| | POSIX (unchanged output) | PowerShell |
|---|---|---|
| `Command(s)` | `["/bin/sh", "-c", s]` | `["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", base64(UTF-16LE(prelude + s + epilogue))]` |
| `PathExists(p)` | `test -e 'p'` | `if (Test-Path -LiteralPath 'p') { exit 0 } else { exit 1 }` |
| `Glob(p)` | `ls -d -- p` (unquoted) | `Get-Item -Path 'p' -Force -ErrorAction SilentlyContinue \| ForEach-Object { $_.PSPath -replace '^.*::', '' }` |
| `MkdirAll(d)` | `mkdir -p 'd'` | `New-Item -ItemType Directory -Force -Path 'd' \| Out-Null` |
| `Quote(s)` | `'s'` (as today) | `'` + `s` with `'` doubled + `'` |

Why `-EncodedCommand` instead of `-Command`: Windows has no argv — `docker
exec` joins the arguments into one command line that `powershell.exe`
re-splits, so double quotes and newlines inside a `-Command` script get
mangled. The base64 form is opaque to that re-parsing, so any script
(multi-line, quotes, `$`) arrives intact. The logged step/check `command`
stays the readable script, never the base64 blob. Tests decode the argv
(`shell.ScriptOf`) to assert on the script.

PowerShell **exit-code contract** (must mirror `sh -c` semantics, verified by
spike S5/S6):

1. An explicit `exit N` in the script → `N`.
2. Otherwise the exit code of the **last native command** (`$LASTEXITCODE`).
3. Otherwise `1` if the last statement failed (`$?` false), else `0`.

`prelude` sets `$ProgressPreference='SilentlyContinue'` (progress bars
pollute stdout), `[Console]::OutputEncoding=[Text.Encoding]::UTF8` and
`$global:LASTEXITCODE=0`; the `epilogue` implements rules 2–3. **No**
`$ErrorActionPreference='Stop'`: like `sh -c` without `-e`, a script keeps
going after a failing statement. The exact prelude/epilogue text is fixed by
the spike and pinned by unit tests.

Windows PowerShell 5.1 (`powershell.exe`, present in `servercore`) is the
target; `pwsh` 7 is not in the base image (non-goal).

### 5.3 Linux guarantee

The POSIX dialect returns **exactly** the strings used today. The existing
argv assertions in `internal/checks/checks_test.go`
(`{"/bin/sh", "-c", "test -e '/usr/bin/app'"}`,
`{"/bin/sh", "-c", "ls -d -- /usr/share/man/man8/jobarg_server.8.*"}`)
must pass **unmodified** after the refactor — that is the acceptance test for
Phase 1.

## 6. Docker environment changes (`internal/env/docker.go`)

`env.Options` gains `OS` and `Isolation`.

### 6.1 Keep-alive

| OS | `Cmd` |
|---|---|
| linux | `sleep infinity` (unchanged) |
| windows | `powershell.exe -NoProfile -Command "while ($true) { Start-Sleep -Seconds 3600 }"` (spike S2) |

`isolation` → `ContainerRequest.HostConfigModifier` sets `hc.Isolation`.

### 6.2 Copy

1. `MkdirAll(dir)` via the dialect.
2. testcontainers' `CopyFileToContainer` tars the file under the **full
   container path** and extracts at `/`. For windows, pass the drive-less,
   forward-slash form: `C:\ita\x.msi` → `/ita/x.msi` (extracts to
   `C:\ita\x.msi` because `/` is `C:\` in the container). This is why v4
   restricts the workdir to `C:` (§4.2). Spike S3 confirms; if the API path
   misbehaves, fall back to a `docker cp`-equivalent `CopyToContainer` call
   with `DestinationPath: "C:\\ita"` and a base-name-only tar entry.
3. File mode (`0o755`) is ignored by Windows — harmless.

### 6.3 Exec output

Windows output uses CRLF. `Exec` normalizes `\r\n` → `\n` for `os: windows`
(so glob match splitting, `expected_stdout_contains`, and the report's
stdout/stderr rendering behave as on Linux). Linux output is untouched.

### 6.4 Ryuk reaper

`testcontainers/ryuk` is a Linux image and cannot run on a Windows daemon.
testcontainers-go reads `TESTCONTAINERS_RYUK_DISABLED` **once per process**
(`internal/config` `sync.Once`), so the decision is made **before the first
testcontainers call**: if any selected environment has `os: windows`, the
runner sets `TESTCONTAINERS_RYUK_DISABLED=true` unless the user set the
variable explicitly. The tool already terminates every container itself
(`runCase` → `Stop` with `context.WithoutCancel`); only a hard kill of the
tool can leak containers — they are named `ita-*`, so
`docker ps -aq --filter name=ita- | % { docker rm -f $_ }` cleans up
(documented in the README). `run_start` records `"ryuk": false` when
disabled.

### 6.5 Daemon OS preflight

A Windows daemon cannot run Linux images and vice versa, and the default
`--config configs/*.yaml` selects every environment. Before the queue runs,
the runner asks the daemon for its `OSType` once (testcontainers Docker
client `Info`) and compares it with each environment's `os`:

- mismatch → one `error` event per environment
  (`environment windows-2022 needs a windows Docker daemon; the daemon is linux`),
  its cases are dropped, counted as a config error (exit code 1);
- daemon unreachable → unchanged behavior (each case fails at `Start`).

## 7. Checks on Windows

Unchanged semantics, dialect-built probes:

| Entry | Probe | PASS |
|---|---|---|
| `must_exist` exact | `PathExists` | exit 0 |
| `must_not_exist` exact | `PathExists` | exit 1 |
| `must_exist` glob | `Glob` | ≥ 1 printed match (capped list in the event, as today) |
| `must_not_exist` glob | `Glob` | 0 printed matches |
| `commands` | `Command(cmd)` | exit code = `expected_exit_code`, optional `expected_stdout_contains` |

Notes:

- Glob semantics = PowerShell wildcards (`*`, `?`, `[...]`), non-recursive,
  files **and** directories, hidden items included (`-Force`) — same shape as
  POSIX `ls -d`. Matching is case-insensitive (NTFS).
- Registry: `must_exist: ['HKLM:\SOFTWARE\JobArranger']` and globs like
  `HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*` work through
  the same probes. Matches print as `HKEY_LOCAL_MACHINE\...`.
- Services are checked with `commands`, e.g.
  `command: "(Get-Service jobarg_agentd).Status"` +
  `expected_stdout_contains: Running`. A first-class `services:` key is a
  follow-up (§12).
- Cost: each probe is one `powershell.exe` start (~0.3–1 s vs ~10 ms for
  `sh`). A 60-path check block adds roughly a minute per case. Acceptable for
  v4; batching probes into one script is a follow-up (§12).

## 8. Installers on Windows

### 8.1 Default commands by extension (added rows)

| Extension | `install_command` | `uninstall_command` | `upgrade_command` |
|---|---|---|---|
| `.msi` | `msiexec /i` (below) | `msiexec /x` (below) | `msiexec /i` (below) |
| `.ps1` | `& '{installer}'` | *(none — must be set)* | *(none — must be set)* |
| `.exe`, `.msix`, `.zip`, any other | *(none — must be set)* | *(none — must be set)* | *(none — must be set)* |

**All installer file types are supported**: the pattern decides which files
are tested, and every command can come from the config entry or its preset
(entry > preset > extension default — the existing v3.1 merge rule). An
extension without a default and without a configured command fails the case
with the existing "no install command … set install_command in the config"
step, exactly like `.sh` uninstall on Linux today. Typical user-written
commands (README examples, not code):

| Installer kind | `install_command` example |
|---|---|
| Inno Setup `.exe` | `$p = Start-Process '{installer}' -Wait -PassThru -ArgumentList '/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART'; exit $p.ExitCode` |
| NSIS `.exe` | `$p = Start-Process '{installer}' -Wait -PassThru -ArgumentList '/S'; exit $p.ExitCode` |
| InstallShield `.exe` | `$p = Start-Process '{installer}' -Wait -PassThru -ArgumentList '/s','/v"/qn /norestart"'; exit $p.ExitCode` |
| `.zip` | `Expand-Archive -LiteralPath '{installer}' -DestinationPath 'C:\Program Files\MyApp' -Force` |

The `.msi` default (install shown; `/x` for uninstall, same shape):

```powershell
$p = Start-Process msiexec.exe -Wait -PassThru -ArgumentList '/i "{installer}" /qn /norestart /l*v "{installer}.install.log"'
if ($p.ExitCode -in 0, 3010, 1641) { if ($p.ExitCode) { "msiexec exit $($p.ExitCode) (reboot required) treated as success" }; exit 0 }
Get-Content '{installer}.install.log' -Tail 40; exit $p.ExitCode
```

Why it looks like this:

- **`Start-Process -Wait`**: `msiexec` is a GUI-subsystem program —
  PowerShell (and `cmd`) do **not** wait for it when called directly, the
  step would "pass" before the install finished. This is the #1 pitfall for
  custom commands too; the README must say so.
- **3010 / 1641** (success, reboot required/initiated): containers cannot
  reboot, so these count as success, with a note in the step stdout.
- **Log tail on failure**: MSI errors are only readable from the verbose log;
  the last 40 lines land in the step's stdout → visible in the report.
- Uninstall by **MSI file** (`/x "{installer}"`) — the uninstall task's setup
  already copied the file, so no ProductCode lookup is needed.
- Upgrade by `/i` of the new MSI works for **major upgrades** (MSIs authored
  with `MajorUpgrade`). Minor upgrades / patches need
  `REINSTALL=ALL REINSTALLMODE=vomus` — the user overrides `upgrade_command`
  in the config/preset when that applies (§13 Q4). Same for install and
  uninstall: the defaults are only a fallback, any config value wins.

Defaults stay **extension-based**, not OS-based: `.msi` never appears in a
Linux config, `.deb`/`.rpm` never in a Windows one.

### 8.2 Package name and version from file names

The upgrade task needs a package name and a version per file. Installer
patterns are written by the user per environment, so the parser must work
for **any** extension in a Windows environment, not a fixed list.
`derivePackage` / `ExtractVersionFromFilename` gain one generic rule, applied
to **every extension except `.deb` / `.rpm` when `environment.os: windows`**
(Linux environments keep today's rules exactly — e.g. a versioned `.sh` file
stays `nonstandard_version` there, so no Linux upgrade case appears or
disappears). Both functions therefore take the environment OS:

```
<package>[-_]<MAJOR.MINOR.PATCH>[-<N>][<anything>].<ext>
```

- package = everything before the first `-` / `_` that is followed by
  `MAJOR.MINOR.PATCH`;
- version = `MAJOR.MINOR.PATCH`, plus `-N` as a numeric suffix when the core
  is followed by `-<digits>` and then `-`, `_`, `.` or the extension;
- a 4th dotted component (`7.2.2.0`) is ignored — MSI itself ignores it for
  upgrades.

| File | package | version |
|---|---|---|
| `jobarranger-agentd-7.2.2-1-x64.msi` | `jobarranger-agentd` | `7.2.2-1` |
| `jobarranger-agentd_7.2.2_x64.msi` | `jobarranger-agentd` | `7.2.2` |
| `jaz-agent-7.2.2.0-x64.msi` | `jaz-agent` | `7.2.2` |
| `MyApp_Setup_2.0.1.exe` | `MyApp_Setup` | `2.0.1` |
| `agent-1.4.0-win64.zip` | `agent` | `1.4.0` |
| `setup.msi` | `setup` | *(none → upgrade `nonstandard_version`)* |

Pairing, ordering and skip rules (`CompareInstallerVersions`, upgrade pool,
`case_skipped`) are reused unchanged. File names that don't fit the rule are
still installed/uninstalled normally; only their upgrade case is skipped
(`nonstandard_version`), as on Linux. The user picks file names and patterns
(§13 Q2); the unit tests pin the table above.

## 9. Runner, logging, list, report

- `runner`: Ryuk switch (§6.4), daemon preflight (§6.5), workdir fallback in
  `envTracker` becomes OS-aware. Case matrix, pool, naming unchanged
  (container names are already sanitized to `[a-z0-9-]`).
- `logging`: `env_start` gains `"os"` (omitted for linux → old logs and Linux
  logs unchanged); `run_start` gains `"ryuk": false` only when disabled.
- `list`: prints `os:` (and `isolation:` when set) under `type:`.
- `report`: no rendering changes required — paths and commands are opaque
  strings. Optionally show `(windows)` next to the env header when `os` is
  present.
- CLI: no new flags.

## 10. Building and running on Windows

- Cross-compile from the devcontainer:
  `GOOS=windows GOARCH=amd64 go build -mod=vendor -o installer-test-exec.exe ./cmd/installer-test`.
- The binary runs on the Windows host; testcontainers-go finds the local
  engine via its default `npipe:////./pipe/docker_engine` (or `DOCKER_HOST`).
- Host-side code already uses `filepath` for local paths (`bin/*.msi` globs
  work on Windows), and `syscall.SIGTERM` exists on Windows. `report`'s
  terminal detection works; colors render in Windows Terminal / modern
  conhost.
- CI: `go vet` + `go test` with `GOOS=windows` in the devcontainer
  (compile-check), unit tests run on Linux (dialects are pure functions).

## 11. Code impact

| File | Change |
|---|---|
| `internal/shell/shell.go` (new) | `Dialect`, `POSIX`, `PowerShell` |
| `internal/shell/shell_test.go` (new) | exact strings for both dialects, quoting edge cases (`'`, spaces, `[`) |
| `internal/env/env.go` | `Shell()` on `Environment`; `Options.OS`, `Options.Isolation` |
| `internal/env/docker.go` | dialect per OS, keep-alive, isolation, copy path mapping, CRLF normalization |
| `internal/task/install.go`, `uninstall.go`, `upgrade.go` | `e.Shell().Command(...)` |
| `internal/checks/checks.go` | probes via dialect; drop local `quote` |
| `internal/config/config.go` | `EnvironmentSpec.OS`, `.Isolation`; validation; OS-aware workdir default; glob charset only for linux |
| `internal/config/resolve.go` | container-OS path join; `.msi`/`.ps1` defaults; generic package rule for windows envs (any extension except `.deb`/`.rpm`) |
| `internal/config/version.go` | generic version rule for windows envs; OS parameter on `ExtractVersionFromFilename` |
| `internal/runner/runner.go` | Ryuk switch, daemon preflight, pass OS/isolation to `env.Options` |
| `internal/logging/logging.go` | `env_start.os`, `run_start.ryuk` |
| `internal/report/report.go` | `list` prints `os` / `isolation` |
| test fakes (`task_test.go`, `checks_test.go`, `runner_test.go`) | fake env implements `Shell()`; new PowerShell-dialect cases |
| `README.md` | Windows section (§11.1) |
| `configs/**` | **untouched** — Windows env configs and presets are user-written (§13) |

### 11.1 README additions

Windows host setup (Containers feature, Docker Engine static install,
`dockerd --register-service`), the `os` / `isolation` / workdir keys,
PowerShell semantics of `command` and check paths (quoting, registry paths,
`Start-Process -Wait` pitfall), `.msi` defaults and exit-code mapping, file
naming rule for upgrades, silent-install command examples for `.exe`
kinds (§8.1), a complete example Windows config + preset **as README text
only** (the user copies it into `configs/`), Ryuk-off + manual cleanup command, image size and
monthly-update note (pin dated tags like `ltsc2022-amd64-YYYYMMDD` for
reproducible runs).

## 12. Implementation phases

| Phase | Content | Done when |
|---|---|---|
| 0 | Spike §3 on the Windows host | S1–S9 recorded in §14 |
| 1 | `internal/shell` + `Environment.Shell()`; tasks/checks/docker use the dialect; POSIX only | all existing tests pass **unmodified**; Linux run output identical |
| 2 | Config: `os`, `isolation`, OS-aware workdir & container paths, windows glob validation | unit tests; `list` on a windows config shows `C:\ita\...` paths |
| 3 | Docker env for windows: keep-alive, isolation, copy mapping, CRLF, Ryuk switch, daemon preflight | one `fresh-install` case passes on the Windows host (with a throwaway local config, not committed) |
| 4 | `.msi` / `.ps1` defaults, generic file-name package/version rule | unit tests pin §8.1/§8.2 tables; all three tasks pass for one MSI incl. one upgrade pair on the Windows host |
| 5 | Logging/list tweaks, README, `GOOS=windows` build check | docs reviewed; `configs/` diff is empty; full run + report on the Windows host with the user's configs |

Each phase is independently mergeable; Phase 1 has zero user-visible change.

### Follow-ups (not v4)

- First-class `services:` check key (name → expected status / start type).
- Batch all file probes of a check block into one PowerShell invocation.
- `docker_host` per environment (one Linux-hosted run driving a remote
  Windows daemon) — needs bypassing testcontainers' process-global config.
- VM / WinRM / SSH environment types for installers that need reboots,
  drivers or a desktop (they reuse the PowerShell dialect).

## 13. Open questions — answered

| # | Question | Answer | Effect on the plan |
|---|---|---|---|
| Q1 | Where does the tool run? | **Directly on the Windows host.** | Local Docker Engine via npipe (§10); remote daemon stays a follow-up. |
| Q2 | Real Windows installer file names? | **The user adds the patterns** in `configs/`. | No file names are assumed in code; §8.2 is a generic rule for any extension in windows envs, pinned by unit tests. |
| Q3 | Install paths, services, registry keys? | **User-defined in `configs/`.** | No preset ships with the code; check blocks (paths, registry, service `commands`) are plain config data (§7). |
| Q4 | MSIs authored as major upgrades? | **`upgrade_command` is overridden in `configs/`.** | The `.msi` default is only a fallback (§8.1); entry > preset > default, unchanged merge rule. |
| Q5 | EXE too, or MSI only? | **All installer file types.** | `.msi`/`.ps1` get defaults, every other type runs from configured commands (§8.1); version parsing is extension-agnostic for windows envs (§8.2). |
| Q6 | Server 2022 / 2025? | **User-defined later in `configs/`.** | Image and `isolation` are config values; nothing version-specific in code. |

Overall: **implement the core system only, `configs/` stays untouched.**

## 14. Spike results

*(to be filled in after Phase 0)*

## 15. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Windows containers (`servercore`) on plain Docker Engine | same isolation model as Linux; no Docker Desktop |
| D2 | `os` is a new environment key; `type` stays `docker` | OS ≠ provisioning mechanism; future VM types reuse the dialect |
| D3 | Shell dialect behind `Environment.Shell()` | one seam for every Linux assumption; POSIX output byte-identical |
| D4 | PowerShell 5.1 (`powershell.exe`) | present in `servercore`; `pwsh` is not |
| D5 | `sh -c`-like exit-code contract, no `ErrorAction Stop` | same mental model for `command` entries on both OSes |
| D6 | Globs passed quoted to PowerShell; no charset restriction on windows | PowerShell expands wildcards itself; Windows paths need spaces |
| D7 | `.msi` defaults use `Start-Process -Wait`, map 3010/1641 → 0, print log tail on failure | msiexec doesn't block; containers can't reboot; MSI errors live in the log |
| D8 | Ryuk auto-disabled when any windows env is selected | Ryuk is Linux-only; the tool already cleans up its containers |
| D9 | Daemon/env OS mismatch = config error per env | explicit, never silent; default `configs/*.yaml` on a Windows host otherwise fails confusingly at image pull |
| D10 | Workdir on `C:` only (v4) | testcontainers extracts copies relative to the container root |
| D11 | PowerShell scripts passed via `-EncodedCommand` | Windows command-line re-parsing mangles quotes/newlines in `-Command` |
| D12 | Generic package/version rule for every extension in windows envs; Linux rules untouched | all installer types supported (Q5) without changing Linux upgrade pairing |
| D13 | Core system only; no configs, presets or file names in code | environments, patterns, checks and upgrade commands are user data (Q2–Q4, Q6) |

## 16. Non-goals

- Docker Desktop, WSL, Linux containers on the Windows host.
- `nanoserver` (no `msiexec`), full `windows/server` image specifics (works
  by config change only), Hyper-V isolation tuning beyond the `isolation` key.
- Reboot-requiring, driver, GUI-only installers.
- WinRM / SSH / VM environments, remote daemons, mixed Linux+Windows in one
  process (follow-ups, §12).
- ARM64 Windows.
- Any file under `configs/` (env configs, presets, patterns) — user-written.
- Any change to Linux behavior, events, or report output for Linux runs.
