# Windows Environment Plan (v4) — Windows Server Core containers via Docker Engine

> Status: **CONFIRMED — all decisions approved (§4 config keys, §4.5/§7
> checks, §5.2 shell, §3.1/§6 Docker client, §6.5 no preflight, §8.1 defaults
> incl. MSI 3010/1641 and `.ps1`, §8.2 `version_format` upgrade-only, §9
> output, §16 scope); open questions answered (§13). Remaining gate: the
> spike in §3 must pass (results in §14) before Phase 1.** Implementation scope is the **core system only**:
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
    version_format: "{package}-{version}-x64.msi"   # NEW: only needed for the upgrade task
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
| testcontainers-go (used by `DockerEnvironment`) | Ryuk reaper (`testcontainers/ryuk`, a **Linux** image) is started before the first container; `CopyFileToContainer` builds a tar whose entry name is the **full container path** and extracts it at `/` (`vendor/.../testcontainers-go/docker.go` `copyToContainer`, `file.go` `tarFile`) — `C:\ita\x.msi` as a tar entry name is not a valid Windows extraction target. → Windows environments don't use it (§3.1) |

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
| S3 | Copy into a **running** container | `docker cp x.msi <ctr>:C:\ita\x.msi` (same API call §6.2 uses) | §6.2 |
| S4 | The installer installs silently in Server Core | `Start-Process msiexec -ArgumentList '/i C:\ita\x.msi /qn /norestart /l*v C:\ita\x.log' -Wait -PassThru` → `ExitCode` 0 / 3010 | that option A is viable at all |
| S5 | Exit-code propagation through `docker exec <ctr> powershell -EncodedCommand <base64>` (the form §5.2 uses) | `exit 7`, a failing native exe, a failing cmdlet | §5.2 wrapper |
| S6 | Native stderr under `docker exec` (PS 5.1) is passed through, not turned into error records | `cmd /c "echo err 1>&2"` | §5.2 wrapper |
| S7 | Output line endings / encoding | non-ASCII path in `Get-Item` output | CRLF + UTF-8 handling, §6.3 |
| S8 | Hidden files and registry paths with `Get-Item -Force` / `Test-Path` | `Test-Path 'HKLM:\SOFTWARE\Microsoft'` | §7 |
| S9 | Container stop/remove time | `docker rm -f` timing | expectations only |

If S4 fails, stop and revisit (the installer needs the full `windows/server`
image — a config-only change — or a VM environment, out of scope).

### 3.1 Why Windows does not use testcontainers-go

testcontainers-go **does not officially support Windows containers** (its own
source: "This function does not consider Windows containers at the moment",
`testcontainers.go`, `internal/core/docker_host.go`). Making it work would
need three workarounds, each a source of surprises:

- **Ryuk**: the reaper is a Linux image, started *before* the first
  container; on a Windows engine it can never run, so every case would fail
  unless `TESTCONTAINERS_RYUK_DISABLED=true` is set — and testcontainers reads
  that setting once per process, so it must be set before any other
  testcontainers call.
- **Copy**: `CopyFileToContainer` tars the file under the full container path
  and extracts at `/`, so `C:\ita\x.msi` must be rewritten to `/ita/x.msi`,
  limiting the workdir to drive `C:`.
- **Unknowns**: anything else in the library that assumes Linux.

The tool only needs five operations (create+start, copy, exec, remove,
container ID), which the plain Docker client
(`github.com/moby/moby/client`, already vendored as a testcontainers
dependency) provides directly. **Decision: Windows environments use the
Docker client directly** (§6); Linux environments keep testcontainers-go
unchanged. No Ryuk switch, no path rewriting, no library behavior to work
around — and the `Environment` interface keeps the choice inside
`internal/env`.

## 4. Configuration changes

### 4.1 `environment.os` (new, optional)

| Value | Meaning |
|---|---|
| `linux` (default) | today's behavior — POSIX shell dialect, `/tmp` workdir |
| `windows` | PowerShell dialect, `C:\ita` workdir, MSI defaults, Docker-client environment (§6) |

Unknown values are a load error. `type` stays `docker`: the OS is a property
of the **environment**, the type is the **provisioning mechanism** (a future
`winrm`/VM type would also be `os: windows` and reuse the same dialect).

### 4.2 `environment.workdir`

Default becomes OS-dependent: `/tmp` (linux) / `C:\ita` (windows). For
`os: windows` the workdir must be an absolute Windows path (`^[A-Za-z]:\\`,
any drive the image has — §6.2). Forward slashes in a Windows workdir are normalized to `\`.

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

No schema change beyond the new installer key `version_format` (§4.7), which presets can carry like any other installer key. Windows presets live next to the Linux ones (or in their
own file, e.g. `configs/shared/presets-windows.yaml`, selected with the
config's `presets:` key). Presets are OS-agnostic data — a Linux preset used
by a Windows env simply fails its checks, which is the user's mistake to fix.

### 4.7 `installers[].version_format` (new, optional, any OS, upgrade task only)

User-defined file-name template used **only by the `upgrade` task** to pair
old and new installers — e.g. `"{package}-{version}-x64.msi"`. Not needed
for `fresh-install` / `uninstall`, and it never changes command
placeholders. Full rules in §8.2. Unset: built-in `.deb`/`.rpm` rules on
linux (unchanged); on windows the upgrade task skips the entry as
`no_version_format`.

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

The Linux `DockerEnvironment` returns `shell.POSIX`; the Windows Docker
environment (§6) returns `shell.PowerShell`. A future WinRM/VM environment returns `shell.PowerShell`
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

## 6. Windows Docker environment (`internal/env/docker_windows.go`)

A new implementation of the existing `Environment` interface, built on the
plain Docker client (§3.1). The file name describes its role (no build
tag) — it compiles and unit-tests on Linux. The env factory selects it when
`environment.os: windows` (`type: docker` stays the config value);
`os: linux` keeps today's testcontainers-based `DockerEnvironment`, whose
only change is `Shell()` returning `shell.POSIX` (§5.3).

| `Environment` method | Docker client call(s) |
|---|---|
| `Start` | `ImagePull` when the image is missing → `ContainerCreate` (image, keep-alive cmd §6.1, `HostConfig.Isolation` §4.3, deterministic name, `ita` labels) → `ContainerStart` |
| `Copy` | `MkdirAll` via `Exec` (§6.2) → `CopyToContainer` |
| `Exec` | `ExecCreate` (stdout+stderr attached) → `ExecAttach` → `stdcopy.StdCopy` → `ExecInspect` for the exit code (§6.3) |
| `Stop` | `ContainerRemove` with `Force: true` |
| `Shell` | `shell.PowerShell` |
| `ContainerID` | the created container's ID (for `case_start` / `--keep`) |

The client is created once per run (`client.FromEnv`, API version
negotiation) and shared by every Windows case; it talks to the local engine
over its default `npipe:////./pipe/docker_engine` (or `DOCKER_HOST`).
The struct depends on a small interface covering just the calls above, so
unit tests use a fake client; integration testing happens on the Windows
host only. ~150 lines.

### 6.1 Keep-alive

`Cmd`: `powershell.exe -NoProfile -Command "while ($true) { Start-Sleep -Seconds 3600 }"`
(spike S2). Linux keeps `sleep infinity`.

### 6.2 Copy

1. `MkdirAll(dir)` via the PowerShell dialect.
2. `CopyToContainer` with `DestinationPath: <workdir>` (e.g. `C:\ita`) and a
   tar holding **one entry named by the base name** — the same mechanism as
   `docker cp` (spike S3). The target directory is passed as a Windows path,
   so any drive works (no `C:`-only restriction).
3. File mode is irrelevant on Windows.

### 6.3 Exec output

Windows output uses CRLF. `Exec` normalizes `\r\n` → `\n` (so glob match
splitting, `expected_stdout_contains` and the report's stdout/stderr
rendering behave as on Linux).

### 6.4 Ryuk

Not involved for Windows: Windows cases never touch testcontainers-go, so
the reaper is never started for them. Linux environments keep
testcontainers-go (and Ryuk) exactly as today, on whatever daemon they run.
Without a reaper, only a hard kill of the tool can leak Windows containers;
they are named `ita-*` and labelled, so
`docker ps -aq --filter name=ita- | % { docker rm -f $_ }` cleans up
(README).

### 6.5 Daemon / environment OS mismatch — Docker decides

No preflight. Linux containers can run on a Windows machine too (e.g. a
Linux-mode engine on the same host), so the tool does not second-guess which
images a daemon can run. Every case simply starts; when the daemon cannot run
an image (e.g. a Linux image on a Windows-containers engine, or a Windows
image on a Linux engine), Docker's own error fails that case at `Start`
(`case_end` FAIL with the `start docker environment: ...` error event), and
all other cases keep running. The error text is Docker's, unmodified.

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

### 8.2 Upgrade pairing: `version_format` (user-defined, upgrade task only)

Version formats differ per vendor, so the tool does **not** guess them for
Windows. The user describes the file-name format per installer entry (or in
its preset) with a new optional key, **`version_format`**.

**Scope: the `upgrade` task only.** The version and package identity of a
file are only ever used to pair old → new installers (and to name upgrade
containers `...-from-<old-version>-upgrade`). `fresh-install` and
`uninstall` never read them, so `version_format`:

- is **not needed** for `fresh-install` / `uninstall` — those run for every
  file the `pattern` matches, with or without `version_format`;
- does **not** change command placeholders — `{package}` in
  `install_command` / `uninstall_command` / `upgrade_command` keeps the
  built-in derivation (§ "Command placeholders" in the README);
- is **only evaluated when the `upgrade` task is selected** (`--task`
  default or explicit). A run with `--task fresh-install,uninstall` ignores
  it entirely (beyond the load-time syntax check below).

```yaml
installers:
  - pattern: "bin/jobarranger-agentd-{VERSION}*.msi"
    version_format: "{package}-{version}-x64.msi"
  - pattern: "bin/MyAppSetup_*.exe"
    version_format: "MyAppSetup_{version}.exe"     # no {package}: see below
```

Template syntax — matched against the file's **base name**, whole-name,
case-insensitive on windows envs:

| Token | Matches | Required |
|---|---|---|
| `{version}` | `MAJOR.MINOR.PATCH` plus an optional suffix introduced by `-` or `.` (`7.2.2`, `7.2.2-1`, `7.2.2.1234`, `2.0.1-beta`) | **exactly once** |
| `{package}` | any non-empty text (shortest match) | at most once |
| `*` | any text (incl. empty) | any number |
| anything else | itself, literally | |

Rules:

- **Version**: the captured text becomes the existing `MAJOR.MINOR.PATCH[-Suffix]`
  form — a `.`-introduced suffix is normalized to `-` (`7.2.2.1234` →
  `7.2.2-1234`), so 4-part vendor versions compare numerically on their
  build number. Ordering, pairing and the upgrade pool
  (`CompareInstallerVersions`) are reused **unchanged**.
- **Upgrade identity (package)**: the `{package}` capture; without
  `{package}`, the literal text before `{version}` with trailing `-`, `_`,
  `.`, spaces trimmed (`MyAppSetup_{version}.exe` → `MyAppSetup`) — every
  file of that entry is then one package. Two files pair only when their
  upgrade identities are equal. This identity is internal to pairing; it is
  **not** the `{package}` command placeholder.
- **Skips** (upgrade task only, `case_skipped` events, never failures):

  | Situation | Skip reason |
  |---|---|
  | windows env, entry has no `version_format` | `no_version_format` (**new**) |
  | file matched by `pattern` but not by `version_format` | `nonstandard_version` |
  | no older version of the same package in the upgrade pool | `no_older_version` |

  The report renders `no_version_format` as e.g.
  `⏸ SKIPPED  no version_format configured` (`skipReasonText`).
- **Without `version_format`**: Linux keeps today's built-in `.deb` / `.rpm`
  rules exactly (byte-identical); on windows envs there is **no** built-in
  rule → `no_version_format` skip. Upgrade testing on Windows therefore always
  means writing a `version_format`.
- **Linux can use it too**: when set, it overrides the built-in rule for that
  entry's upgrade pairing on any OS (e.g. a versioned `.sh` installer).
- **Validation (load error, always)**: `{version}` missing or repeated,
  `{package}` repeated, unknown `{lowercase}` token — a broken template is a
  config typo, reported even on runs without the upgrade task. `{VAR}`
  pattern variables are **not** substituted in `version_format` (it
  describes the shape, not the pinned value).
- **Preset-able**: like every installer key except `pattern`; entry wins
  over preset (v3.1 merge rule).
- `list` prints an `upgrade:` detail per installer — the parsed
  `version` / identity, or why it would be skipped — so a `version_format`
  can be checked with a dry run before any container starts.

Implementation shape: `ResolvedInstaller` keeps `PackageName` (placeholder,
unchanged) and gains `UpgradeKey` (pairing identity) next to the existing
`Version`; without `version_format`, `UpgradeKey = PackageName`, so Linux
pairing is unchanged. `buildUpgradeCases` compares `UpgradeKey` instead of
`PackageName`.

Unit tests pin the template compiler (escaping of `.`, `+`, `(`, `[` …),
the normalization, the identity fallback, the three skip reasons, and that
`fresh-install` / `uninstall` cases and `{package}` expansion are identical
with and without `version_format`.

## 9. Runner, logging, list, report

- `runner`: no preflight — OS mismatches fail at `Start` with Docker's error (§6.5); workdir fallback in
  `envTracker` becomes OS-aware. Case matrix, pool, naming unchanged
  (container names are already sanitized to `[a-z0-9-]`).
- `logging`: `env_start` gains `"os"` (omitted for linux → old logs and Linux
  logs unchanged).
- `list`: prints `os:` (and `isolation:` when set) under `type:`.
- `list`: an `upgrade:` line per installer (parsed version, or the skip
  reason it would get — §8.2).
- `report`: one new skip reason text, `no_version_format` (§8.2); otherwise
  no rendering changes — paths and commands are opaque strings. Optionally show `(windows)` next to the env header when `os` is
  present.
- CLI: no new flags.

## 10. Building and running on Windows

- Cross-compile from the devcontainer:
  `GOOS=windows GOARCH=amd64 go build -mod=vendor -o installer-test-exec.exe ./cmd/installer-test`.
- The binary runs on the Windows host; the Docker client finds the local
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
| `internal/env/docker.go` | `Shell()` returns `shell.POSIX`; otherwise unchanged (Linux, testcontainers-go) |
| `internal/env/docker_windows.go` (new) | Docker-client Windows environment (§6) + factory selection by `os` |
| `internal/env/docker_windows_test.go` (new) | fake-client tests: create args (isolation, keep-alive, name), copy tar entry + destination, exec exit code + CRLF, remove |
| `go.mod` | `github.com/moby/moby/client` moves from indirect to direct — already vendored, no new download |
| `internal/task/install.go`, `uninstall.go`, `upgrade.go` | `e.Shell().Command(...)` |
| `internal/checks/checks.go` | probes via dialect; drop local `quote` |
| `internal/config/config.go` | `EnvironmentSpec.OS`, `.Isolation`; validation; OS-aware workdir default; glob charset only for linux |
| `internal/config/config.go` (again) | `InstallerSpec.VersionFormat`; template validation (§8.2); preset merge of the new key |
| `internal/config/resolve.go` | container-OS path join; `.msi`/`.ps1` defaults; `ResolvedInstaller.UpgradeKey`; `Version`/`UpgradeKey` from `version_format` when set, else built-in `.deb`/`.rpm` rules (linux) / none (windows); `PackageName` unchanged |
| `internal/runner/runner.go` (upgrade) | `buildUpgradeCases` pairs on `UpgradeKey`; new skip reason `no_version_format` |
| `internal/report/text.go` | `skipReasonText` for `no_version_format` |
| `internal/config/versionformat.go` (new) + test | `version_format` template compiler and matcher (§8.2) |
| `internal/report/report.go` (again) | `list` prints parsed `package` / `version` per installer |
| `internal/runner/runner.go` | pass OS/isolation to `env.Options`; env factory picks the Windows environment for `os: windows` |
| `internal/logging/logging.go` | `env_start.os` |
| `internal/report/report.go` | `list` prints `os` / `isolation` |
| test fakes (`task_test.go`, `checks_test.go`, `runner_test.go`) | fake env implements `Shell()`; new PowerShell-dialect cases |
| `README.md` | changed + new sections, listed in §11.1 |
| `configs/**` | **untouched** — Windows env configs and presets are user-written (§13) |

### 11.1 README.md updates

`README.md` is updated in **Phase 5** (after the code works on the Windows
host, so every example is verified). Section by section, against the
current README:

**Existing sections — changed**

| README section | Change |
|---|---|
| Intro (top) | "using testcontainers-go (Windows via WinRM is a future environment type)" → Linux environments via testcontainers-go, **Windows Server Core containers via the Docker Engine API** (v4); WinRM/VM stays future. |
| How it works | Fix the numbering (`1, 3, 3, 4, 5` → `1–5`). Step 2: configs describe Linux **or Windows** environments (`os:`). Upgrade bullet: versions come from built-in `.deb`/`.rpm` rules or a `version_format` (Windows: always `version_format`); add the `no_version_format` skip. |
| Design-docs list ("See docs/…") | Complete it: add v3.3 `pattern-vars-unset-no-match-plan.md`, v3.4 `upgrade-pool-pattern-vars-plan.md`, v3.5 `list-upgrade-checks-plan.md`, **v4 `windows-environment-plan.md`**. |
| Usage | Add a Windows block: cross-compile `GOOS=windows GOARCH=amd64 go build -mod=vendor -o installer-test-exec.exe ./cmd/installer-test`, and `list` / `run` / `report` with `--config "configs/<windows-glob>.yaml"`, PowerShell redirection (`> run.jsonl`). |
| Docker-daemon note (under Example reports) | Linux configs need a Linux-containers engine, Windows configs a Windows-containers engine; a mismatch fails that case at start with Docker's own error (no preflight). |
| Example config | Keep the Linux example; fix its title (`configs/ubuntu-24.04.yaml` → an illustrative name, the real files are `configs/jobarg-*.yaml`). Windows example goes in the new section below. |
| `environment` table | Add `os` (`linux` default / `windows`), `isolation` (windows only: `process` / `hyperv`), and the OS-dependent `workdir` default (`/tmp` / `C:\ita`). `type` row: drop "(`winrm` is a future type)" wording in favour of "`docker` (Linux and Windows)". |
| `installers` table | Add `version_format` (optional, **upgrade task only**, link to its section). `uninstall_command` / `upgrade_command` rows: "`.sh` has no default" → "`.sh`, `.ps1` and non-MSI Windows types have no default". |
| Upgrade pairing | Version sources: built-in `.deb`/`.rpm` rules, or `version_format` (any OS, required on Windows). Three skip reasons: `no_version_format`, `nonstandard_version`, `no_older_version`. |
| Check blocks table | `must_exist` / `must_not_exist`: on Windows, paths are probed with `Test-Path` / `Get-Item`; registry paths (`HKLM:\...`) allowed. `commands`: run via `sh -c` (Linux) or PowerShell (Windows). |
| Wildcard file checks | Scope the current text to Linux; add the Windows rules: PowerShell wildcards, passed **quoted**, no charset restriction (spaces fine), hidden items included, case-insensitive. |
| `commands` entries | `command` row: "via `sh -c`" → "via `sh -c` (Linux) / Windows PowerShell 5.1 (Windows)". |
| Shared presets | Key list gains `version_format`; drop the hard-coded "13 environment configs" count. Note that Windows presets can live in their own file (`presets:` key). |
| Command placeholders | Add a Windows example column/row (`{installer}` → `C:\ita\myapp-1.2.3-x64.msi`). State that `{package}` is **not** affected by `version_format`. |
| Default commands by extension | Add rows `.msi` (install/uninstall/upgrade via `msiexec`, shown in full) and `.ps1` (`& '{installer}'`, no uninstall/upgrade default); "other" row covers `.exe`, `.msix`, `.zip`, … |

**New sections**

| New README section | Content |
|---|---|
| **Windows environments** (overview) | What runs where (tool on the Windows host, Windows containers, Server Core image), what isn't supported (reboots, drivers, GUI-only installers, nanoserver). |
| **Windows host setup** | Enable the Containers feature (Server / Windows 10–11 commands), install Docker Engine without Docker Desktop (Microsoft script or static zip + `dockerd --register-service`), smoke test `docker run --rm --isolation process mcr.microsoft.com/windows/servercore:ltsc2022 cmd /c ver`. Host/image build matching for process isolation. |
| **Windows example config + preset** | A complete `os: windows` config and a matching preset (file/registry checks, a service check via `commands`, `version_format`) — **as README text only**; the user copies it into `configs/`. |
| **PowerShell semantics** | Commands and checks run in Windows PowerShell 5.1; exit-code contract (explicit `exit N` > last native exit code > failed statement = 1); the script keeps going after a failing statement (like `sh -c`); single-quote quoting; **`Start-Process -Wait` pitfall** for GUI-subsystem programs (`msiexec`, most `setup.exe`). |
| **MSI defaults and exit codes** | The default `msiexec` commands, `/qn /norestart`, verbose log next to the installer, log tail printed on failure, **3010 / 1641 = pass with a note** (why: success codes; containers can't reboot; checks still catch real problems), how to override for strict behaviour. |
| **EXE and other installers** | Silent-install command examples: Inno Setup (`/VERYSILENT /SUPPRESSMSGBOXES /NORESTART`), NSIS (`/S`), InstallShield (`/s /v"/qn /norestart"`), `.zip` via `Expand-Archive`. |
| **`version_format`** | Purpose (upgrade pairing only), token table (`{version}`, `{package}`, `*`), 4-part normalization (`7.2.2.1234` → `7.2.2-1234`), identity fallback without `{package}`, examples for common vendor formats, validation errors, checking with `list`. |
| **Windows images: size and updates** | Download sizes (servercore ≈ 2.2–2.3 GB compressed, ≈ 5 GB on disk; full `server` ≈ 4.4 GB), shared by all parallel containers; monthly Patch-Tuesday re-download; pin dated tags (`ltsc2022-amd64-YYYYMMDD`) for reproducible runs. |
| **Cleanup** | No reaper on Windows: leaked containers after a hard kill are removed with `docker ps -aq --filter name=ita- \| % { docker rm -f $_ }`. |

**Unchanged**: Example reports + screenshots (the report format does not
change), Pattern variables (works identically on Windows).

## 12. Implementation phases

| Phase | Content | Done when |
|---|---|---|
| 0 | Spike §3 on the Windows host | S1–S9 recorded in §14 |
| 1 | `internal/shell` + `Environment.Shell()`; tasks/checks/docker use the dialect; POSIX only | all existing tests pass **unmodified**; Linux run output identical |
| 2 | Config: `os`, `isolation`, OS-aware workdir & container paths, windows glob validation | unit tests; `list` on a windows config shows `C:\ita\...` paths |
| 3 | `docker_windows.go` (§6): create/start with keep-alive + isolation, copy, exec + CRLF, remove | one `fresh-install` case passes on the Windows host (with a throwaway local config, not committed) |
| 4 | `.msi` / `.ps1` defaults, `version_format` key (§8.2) | unit tests pin §8.1 defaults and the §8.2 template rules; all three tasks pass for one MSI incl. one upgrade pair on the Windows host |
| 5 | Logging/list tweaks, README per §11.1, `GOOS=windows` build check | every §11.1 row done and its examples verified on the Windows host; `configs/` diff is empty; full run + report on the Windows host with the user's configs |

Each phase is independently mergeable; Phase 1 has zero user-visible change.

### Follow-ups (not v4)

- First-class `services:` check key (name → expected status / start type).
- Batch all file probes of a check block into one PowerShell invocation.
- `docker_host` per environment (one Linux-hosted run driving a remote
  Windows daemon) — the Windows environment just gets a per-env client;
  Linux envs on a remote host would still need testcontainers' process-global
  host config solved.
- VM / WinRM / SSH environment types for installers that need reboots,
  drivers or a desktop (they reuse the PowerShell dialect).

## 13. Open questions — answered

| # | Question | Answer | Effect on the plan |
|---|---|---|---|
| Q1 | Where does the tool run? | **Directly on the Windows host.** | Local Docker Engine via npipe (§10); remote daemon stays a follow-up. |
| Q2 | Real Windows installer file names? | **The user adds the patterns** in `configs/`. | No file names or version formats are assumed in code; the user describes them per entry/preset with `version_format` (§8.2). |
| Q3 | Install paths, services, registry keys? | **User-defined in `configs/`.** | No preset ships with the code; check blocks (paths, registry, service `commands`) are plain config data (§7). |
| Q4 | MSIs authored as major upgrades? | **`upgrade_command` is overridden in `configs/`.** | The `.msi` default is only a fallback (§8.1); entry > preset > default, unchanged merge rule. |
| Q5 | EXE too, or MSI only? | **All installer file types.** | `.msi`/`.ps1` get defaults, every other type runs from configured commands (§8.1); `version_format` works for any extension (§8.2). |
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
| D8 | Windows environments never touch testcontainers-go, so Ryuk is never started for them | Ryuk is a Linux image — it can never run on the Windows engine; the tool already removes its own containers |
| D9 | No daemon/env OS preflight; Docker's own error fails the case at `Start` | Linux containers can run on Windows machines too; the tool doesn't second-guess the daemon, other cases keep running |
| D10 | Windows workdir: any absolute drive path, default `C:\ita` | copy targets the workdir directly (§6.2), no root-relative extraction |
| D11 | PowerShell scripts passed via `-EncodedCommand` | Windows command-line re-parsing mangles quotes/newlines in `-Command` |
| D12 | Upgrade pairing (version + identity) comes from a user-defined `version_format` per entry/preset, used **only by the upgrade task**; `{package}` placeholder and other tasks unaffected; no built-in Windows rule (skip `no_version_format`); Linux built-ins untouched when unset | version formats vary per vendor — the config, not the code, knows them; only upgrade pairing needs versions |
| D13 | Core system only; no configs, presets or file names in code | environments, patterns, checks and upgrade commands are user data (Q2–Q4, Q6) |
| D14 | Windows environments use the plain Docker client (`moby/moby/client`), not testcontainers-go; Linux keeps testcontainers-go | testcontainers-go doesn't officially support Windows containers and would need a Ryuk switch, copy-path rewriting and a `C:`-only workdir; five Docker calls cover everything (§3.1) |

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
