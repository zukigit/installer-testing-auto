# Windows Defaults, Host Setup Docs and Cleanup Watcher Plan (v4.1)

> Status: **REVIEWED — open questions answered (§6).** Plan only; nothing is
> implemented.
> Baseline: v4 (`docs/windows-environment-plan.md`), as fixed after the first
> Windows host run (staging-folder copy, direct msiexec start, 30-minute exec
> limit, Ctrl+C-aware exec).
> Scope: three changes —
> (1) Windows configs leave the **default** config set,
> (2) the README gets the Windows host preparation steps learned on the host,
> (3) a **watcher process** guarantees no leftover containers or files, however
> the run ends.
> Linux run output (NDJSON events, report) stays byte-identical, apart from
> the cases (1) explicitly removes from default runs.

## 1. Default config set excludes Windows

### 1.1 Today

`--config` unset → `config.Discover("")` → `configs/*.yaml` → every config,
Linux **and** Windows (`runner.Run`, `runner.List`). A default run on a Linux
host therefore starts Windows cases that can only fail at `Start`, and on a
Windows host the Linux cases fail the same way.

### 1.2 Change

When `--config` is **not given** (`run` and `list`), configs with
`environment.os: windows` are **left out**. Windows configs run only when
`--config` is given explicitly, e.g.:

```powershell
.\installer-test-exec.exe run --config "configs/jobarg-windows-*.yaml"
```

- **Explicit `--config` is never filtered**, even a broad glob like
  `configs/*.yaml` — the user asked for those files.
- **How the OS is detected**: before the full load, each discovered file gets
  a light YAML peek that reads only `environment.os`. Windows files are
  dropped before validation, so a broken Windows config can't add a config
  error to a default Linux run. A file the peek cannot parse stays in the set
  (the full load reports the error as today).
- **Same rule on every host** — the default set is "all Linux configs" on a
  Linux and a Windows host alike (no host-OS detection; §6 Q3). On a Windows
  host a default run therefore starts Linux cases, which fail at `Start` with
  Docker's own error (v4 §6.5) — Windows users always pass `--config`.
- **Visibility**:
  - `list` (default set) prints one line per left-out file:
    `SKIPPED config configs/jobarg-windows-2019.yaml: os windows is not in the default set (use --config)`.
  - `run` emits nothing new for left-out files (they are not part of the run;
    Linux logs stay identical). If **only** Windows configs exist, the
    existing "no cases to run" error gets a hint:
    `... (os: windows configs run only with an explicit --config)`.
- **Help text** of `--config` (main.go) says: default `configs/*.yaml`,
  excluding `os: windows` configs.

### 1.3 Code impact

| File | Change |
|---|---|
| `internal/config/config.go` | `PeekOS(path) (string, error)` (decodes only `environment.os`, non-strict) and `FilterDefault(paths) (kept, windows []string)` |
| `internal/runner/runner.go` | `Run` / `List`: when `ConfigPattern == ""`, filter before loading; `List` prints the SKIPPED lines; "no cases" hint |
| `cmd/installer-test/main.go` | `--config` help text |
| tests | default set drops windows files; explicit glob keeps them; unparsable file kept; only-windows hint |

## 2. README: Windows host preparation

Replace the current short **Windows host setup** section with a step-by-step
guide built from the first host run (Windows 10 1909 host, ltsc2019 image,
Hyper-V isolation). Content:

1. **Check the host first** — `cmd /c ver` (build number) and
   `(Get-CimInstance Win32_OperatingSystem).Caption` (edition; Home has no
   Hyper-V → no Windows containers).
2. **Pick image + isolation from the host build** — table:

   | Host | Image | `isolation` |
   |---|---|---|
   | Windows Server 2022 (20348) | `servercore:ltsc2022` | `process` (fast) |
   | Windows Server 2019 (17763) | `servercore:ltsc2019` | `process` |
   | Windows 11 / Server 2025 (newer than the image) | `ltsc2022` or `ltsc2019` | `hyperv` |
   | Windows 10 (e.g. 1909 = 18363) | `servercore:ltsc2019` | `hyperv` |

   Rule: the image build must be **≤ the host build**; equal → `process`
   possible, older → `hyperv` only.
3. **Enable features** (admin PowerShell, then reboot): `Containers`;
   `Microsoft-Hyper-V` for `hyperv`; check `bcdedit` `hypervisorlaunchtype`;
   on a VM host, nested virtualization (Hyper-V / VMware settings).
4. **Install Docker Engine (no Docker Desktop)** — which file to download
   from `download.docker.com/win/static/stable/x86_64/` (highest
   `docker-<version>.zip`, no `rc`), extract to `C:\Program Files\docker`.
5. **Add to PATH safely** — the two-line form with a warning: reading the old
   value **before** writing (the one-liner order mistake wipes the system
   PATH), plus how to restore it from `$env:Path` in the same window or the
   Windows defaults.
6. **Register and start the engine** — `dockerd --register-service`,
   `Start-Service docker`, `docker version` must show `Server ... windows/amd64`.
7. **Smoke test** — `docker run --rm --isolation <process|hyperv> <image> cmd /c ver`.
8. **Install the tool** — cross-compile, copy `installer-test-exec.exe`,
   `configs\`, `bin\` into one folder, and **run from that folder** (not from
   `C:\Windows\system32`).
9. **First run** — `list`, then one `--task fresh-install --parallel 1` case,
   then `report --details`; Hyper-V containers start slowly, keep
   `--parallel` low.
10. **Troubleshooting table** — the errors met on the host and their fix:

    | Error | Meaning / fix |
    |---|---|
    | `failed to connect to the docker API at npipe:////./pipe/docker_engine` | engine not running → step 6 |
    | `no matching manifest for windows(10.0.18363)/amd64` | image newer than the host → older image (step 2) |
    | `The container operating system does not match the host operating system` | `process` isolation with a different build → `hyperv` or matching image |
    | `hcs::CreateComputeSystem ...: The request is not supported` | Hyper-V not available → step 3 (edition, feature, BIOS VT-x, nested virtualization) |
    | `positional parameter cannot be found that accepts argument 'exec'` | the `PS C:\...>` prompt was pasted with the command |
    | a step ends with `[installer-test] command timed out after 30m0s` | the installer hung; check the log tail in `report --details` |

The existing sections (example config, PowerShell semantics, MSI defaults,
images, Cleanup) stay; **Cleanup** is rewritten for §3 (watcher, manual
`cleanup` command).

## 3. Cleanup watcher — no leftovers, however the run ends

### 3.1 Problem

Today cleanup lives in the run process: each case removes its container in
`runCase`. It is skipped when the process dies abruptly (closed console
window, `kill -9` / Task Manager, crash, a second Ctrl+C during teardown).
Leftovers:

- **Windows**: `ita-*` containers and `%TEMP%\ita-stage-*` folders (no reaper).
- **Linux**: testcontainers' Ryuk removes containers ~10 s after the process
  dies, but only while Ryuk itself works.

### 3.2 Design: two processes

`run` starts a **watcher** process before the first container, then does the
work as today:

```
installer-test-exec run ...            (process 1: runner)
  └─ spawns: installer-test-exec __watch --run-id <id>   (process 2: watcher, detached)
```

- **Run id**: random per run (e.g. `20261029T124003-1a2b3c`). Every container
  the run creates is labelled `ita.run=<id>` (Linux: testcontainers request
  labels; Windows: the existing `ita` labels). Every host file the run
  creates lives under one **run directory** `<TEMP>/ita-run-<id>/`
  (Windows staging folders move there: `<run dir>/stage-<container>`).
  So the watcher removes **exactly this run's** resources — parallel runs
  and user containers are never touched.
- **How the watcher notices the exit**: the runner keeps the **write end of a
  pipe** connected to the watcher's stdin and never writes to it. When the
  runner exits **for any reason** — normal end, Ctrl+C, crash, `kill -9`,
  closed window — the OS closes the pipe and the watcher's read returns EOF.
  This is the same on Linux and Windows, needs no PID polling, and is immune
  to PID reuse.
- **Detached**: the watcher must survive what kills the runner:
  - Windows: `CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS` (Ctrl+C and
    closing the console window don't reach it).
  - Linux: `Setsid` (new session; terminal SIGINT/SIGHUP don't reach it).
  - Its stdout/stderr are not the runner's (the NDJSON output stays clean);
    it writes to `<run dir>/watcher.log`.
- **Cleanup on EOF** (idempotent; normally the runner already removed
  everything and this finds nothing):
  1. list containers with label `ita.run=<id>` (Docker client, both OSes);
  2. force-remove all of them **except kept ones** (§3.3);
  3. delete the run directory, except folders of kept containers;
  4. exit. On success nothing is left — not even `watcher.log`. On failure
     (e.g. daemon unreachable) it retries for a short time (e.g. 3 × 5 s),
     then leaves `watcher.log` in the run directory so the user can see why.
- **Runner side unchanged otherwise**: cases still remove their own
  containers as they finish (fast, visible in the logs). The watcher is the
  safety net for everything the runner could not finish.

### 3.3 `--keep`

Kept containers (failed cases with `--keep`) must survive the watcher. The
runner appends each kept container ID to `<run dir>/kept` **before** emitting
`case_end ... kept`. The watcher skips those IDs (and their staging folders)
and leaves `kept` + them in place. **A kept container is never removed
automatically** — not by the watcher and not by a plain `cleanup` (§3.4).
It goes away only when the user asks for it explicitly
(`cleanup --include-kept` or `docker rm -f`).

### 3.4 Manual `cleanup` command (fallback)

New subcommand for leftovers the watcher could not handle (machine
rebooted, daemon was down, kept containers no longer needed):

```sh
installer-test-exec cleanup                       # all runs, kept containers stay
installer-test-exec cleanup --run <id>            # one run only, kept containers stay
installer-test-exec cleanup --include-kept        # all runs, kept containers too
installer-test-exec cleanup --run <id> --include-kept
```

- **Kept containers are protected by default**: `cleanup` reads every
  `<TEMP>/ita-run-*/kept` list and skips those container IDs and their
  staging folders; a run directory that still holds a `kept` list stays.
- `--include-kept` removes them too, then deletes the run directories
  completely.
- `cleanup` prints one line per removed / kept container and folder, so the
  user sees what stayed and why (`kept (--keep): <name>`).

It is the watcher's cleanup function with a wider selector and the
`--include-kept` switch — one implementation, two entry points.

### 3.5 Failure modes

| Situation | Result |
|---|---|
| Normal end, Ctrl+C, crash, `kill -9`, closed window | watcher cleans up (EOF on the pipe) |
| Watcher fails to start | `run` aborts before any container (`error` event, exit 2) — no run without the safety net |
| Watcher killed by the user | runner still cleans up normally; only an additional runner crash would leave leftovers → `cleanup` |
| Machine power-off / reboot | nothing can run; `cleanup` afterwards |
| Docker daemon down at cleanup time | retries, then `watcher.log` stays; `cleanup` later |
| Ryuk (Linux) also removes containers | harmless — removal is idempotent ("not found" = done) |

### 3.6 Code impact

| File | Change |
|---|---|
| `internal/cleanup/cleanup.go` (new) | `Clean(ctx, selector, keep)` — list by label, force-remove, delete run dirs; used by the watcher and `cleanup` |
| `internal/cleanup/watch.go` (new) | `Spawn(runID) (*Watcher, error)` (pipe + detached child), `Watch(runID)` (block on stdin EOF, then `Clean`) |
| `internal/cleanup/spawn_windows.go`, `spawn_unix.go` (new, build-tagged) | `SysProcAttr`: `CREATE_NEW_PROCESS_GROUP\|DETACHED_PROCESS` / `Setsid` |
| `cmd/installer-test/main.go` | hidden `__watch` subcommand; `cleanup` subcommand; `run` spawns the watcher first |
| `internal/runner/runner.go` | run id + run dir; pass to `env.Options`; append kept IDs to `<run dir>/kept` |
| `internal/env/env.go` | `Options.RunID`, `Options.RunDir` |
| `internal/env/docker.go` | `ita.run` (+ `ita`) labels on Linux containers |
| `internal/env/windows_docker.go` | `ita.run` label; staging folder under the run dir |
| tests | label selection, keep-list skipping (watcher and plain `cleanup`), `--include-kept` removal, run-dir deletion (fake Docker client); a test that spawns the real binary, kills the parent, and asserts the watcher ran `Clean` (fake selector, no Docker) |
| `README.md` | Cleanup section rewritten; `cleanup` command in Usage |

## 4. Phases

| Phase | Content | Done when |
|---|---|---|
| 1 | §1 default set excludes Windows | unit tests; `list` without `--config` shows the SKIPPED lines; Linux run output unchanged |
| 2 | §2 README host preparation | every step and error row checked against the host run |
| 3 | §3.4 `cleanup` + labels + run dir | `cleanup` removes a leftover container + staging folder on the Windows host |
| 4 | §3.2–3.3 watcher | on the Windows host: Ctrl+C, closing the window and Task Manager kill during an install each leave **no** `ita-*` container and no `ita-run-*` folder; `--keep` containers survive the watcher and a plain `cleanup`, and go away with `cleanup --include-kept` |

## 5. Decisions

| # | Decision | Rationale |
|---|---|---|
| E1 | Default set drops `os: windows`; explicit `--config` never filtered | Windows runs are deliberate; a default run should not start cases that cannot work on the host |
| E2 | OS read by a light peek before the full load | a broken Windows config must not add errors to default Linux runs |
| E3 | Watcher detects the exit via stdin-pipe EOF | works for every way a process can die, on both OSes, no PID reuse issue |
| E4 | Watcher detached from console/session | Ctrl+C and closing the window must not kill the safety net |
| E5 | Resources scoped by run id (label + run dir) | concurrent runs and unrelated containers are never touched |
| E6 | Runner keeps its own per-case cleanup; watcher is the safety net | fast cleanup and visible teardown errors stay in the logs |
| E7 | No watcher → no run | the guarantee "no leftovers" must not silently degrade |
| E8 | `--keep` containers are removed only on explicit request (`cleanup --include-kept`) | the user asked to keep them; no automatic path may undo that (§6 Q1) |
| E9 | Default set is the same on every host — no Windows default on Windows hosts | one predictable rule; Windows runs are always explicit (§6 Q3) |

## 6. Open questions — answered

| # | Question | Answer | Effect |
|---|---|---|---|
| Q1 | Should `cleanup` (no options) also remove `--keep` containers? | **No** — if the user wants to keep them, don't remove them. | Plain `cleanup` skips kept containers; new `--include-kept` switch (§3.4, E8) |
| Q2 | Watcher for `list` / `report`? | No — they start no containers. | unchanged |
| Q3 | Should the default set on a **Windows host** be the Windows configs instead? | **No.** | Same default set everywhere; Windows needs `--config` (§1.2, E9) |

## 7. Non-goals

- Cleaning resources created by older tool versions (no `ita.run` label) —
  covered only by `cleanup` (selects on the existing `ita` label / name).
- Surviving power loss (only `cleanup` afterwards).
- Changes to `configs/`.
