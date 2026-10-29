# Windows environments

`os: windows` configs run the same tasks, checks and report as Linux — in
**Windows Server Core containers** on a plain **Docker Engine** (no Docker
Desktop). The tool runs **directly on the Windows host**.

Not supported: installers that need a reboot, drivers, GUI-only installers,
`nanoserver` images (no `msiexec`), Docker Desktop / WSL, ARM64.

## Host setup

Run every command in an **administrator PowerShell**. Paste only the command
itself — never the `PS C:\...>` prompt in front of it.

**1. Check the host**

```powershell
cmd /c ver                                        # build, e.g. 10.0.18363 / 10.0.20348
(Get-CimInstance Win32_OperatingSystem).Caption   # edition
```

Windows 10/11 **Home** cannot run Windows containers — use Pro / Enterprise
or Windows Server.

**2. Pick the image and isolation from the host build**

The image build must be **equal to or older than** the host build: equal →
`process` isolation (fast); older → `hyperv` only.

| Host | `image` | `isolation` |
|---|---|---|
| Windows Server 2022 (20348) | `mcr.microsoft.com/windows/servercore:ltsc2022` | `process` |
| Windows Server 2019 (17763) | `mcr.microsoft.com/windows/servercore:ltsc2019` | `process` |
| Windows 11 / Server 2025 | `servercore:ltsc2022` | `hyperv` |
| Windows 10 (e.g. 1909 = 18363) | `servercore:ltsc2019` | `hyperv` |

The image is ≈ 2.3 GB to download (≈ 5 GB on disk), pulled once and shared
by all containers. Microsoft republishes it monthly; pin a dated tag
(`ltsc2022-amd64-YYYYMMDD`) for reproducible runs.

**3. Enable the features** (reboot afterwards)

```powershell
# Windows Server
Install-WindowsFeature -Name Containers
# Windows 10 / 11
Enable-WindowsOptionalFeature -Online -FeatureName Containers -All
# needed for isolation: hyperv
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All
Restart-Computer
```

For `hyperv`, the hypervisor must start at boot and virtualization must be
available:

```powershell
bcdedit /enum | Select-String hypervisorlaunchtype   # must not be Off (fix: bcdedit /set hypervisorlaunchtype auto)
systeminfo | Select-String "Hyper-V|Virtualization"
```

On a physical machine, VT-x / AMD-V must be on in the BIOS/UEFI. If the host
is a VM, enable **nested virtualization** on the outer host (Hyper-V:
`Set-VMProcessor -VMName <vm> -ExposeVirtualizationExtensions $true` with the
VM off; VMware: "Virtualize Intel VT-x/EPT or AMD-V/RVI").

**4. Install Docker Engine**

Download the newest `docker-<version>.zip` (highest version, no `rc`) from
`https://download.docker.com/win/static/stable/x86_64/`:

```powershell
Expand-Archive .\docker-<version>.zip -DestinationPath "C:\Program Files"   # -> C:\Program Files\docker
```

**5. Add Docker to PATH — read first, then write**

```powershell
$old = [Environment]::GetEnvironmentVariable("Path", "Machine")
[Environment]::SetEnvironmentVariable("Path", "$old;C:\Program Files\docker", "Machine")
$env:Path += ";C:\Program Files\docker"   # current window
where.exe docker                           # -> C:\Program Files\docker\docker.exe
```

Writing before reading replaces the whole system PATH. If that happened, run
`[Environment]::SetEnvironmentVariable("Path", $env:Path, "Machine")` in the
**same** window to put it back.

**6. Register and start the engine**

```powershell
dockerd --register-service
Start-Service docker
docker version     # the Server section must show OS/Arch: windows/amd64
```

**7. Smoke test** (image and isolation from step 2)

```powershell
docker run --rm --isolation hyperv mcr.microsoft.com/windows/servercore:ltsc2019 cmd /c ver
```

**8. Install the tool**

Cross-compile in the devcontainer:

```sh
GOOS=windows GOARCH=amd64 go build -mod=vendor -o installer-test-exec.exe ./cmd/installer-test
```

Copy into **one folder** on the host and always run the tool from there
(patterns like `bin/*.msi` are relative):

```
C:\ita-test\
  installer-test-exec.exe
  configs\        <- your os: windows configs (+ presets)
  bin\            <- the installer files
```

**9. First run**

```powershell
cd C:\ita-test
.\installer-test-exec.exe list   --config "configs/jobarg-windows-*.yaml"
.\installer-test-exec.exe run    --config "configs/jobarg-windows-*.yaml" --task fresh-install --parallel 1 > run.jsonl
.\installer-test-exec.exe report --details --from run.jsonl
```

Windows configs always need `--config`. Hyper-V containers take tens of
seconds to start — keep `--parallel` low.

## Troubleshooting

| Error | Fix |
|---|---|
| `failed to connect to the docker API at npipe:////./pipe/docker_engine` | the engine is not running → step 6 |
| `no matching manifest for windows(10.0.18363)/amd64` | image newer than the host → older image (step 2) |
| `The container operating system does not match the host operating system` | `process` isolation with a different build → `isolation: hyperv` or a matching image |
| `hcs::CreateComputeSystem ...: The request is not supported` | Hyper-V not available → step 3 |
| `A positional parameter cannot be found that accepts argument 'exec'` | the `PS C:\...>` prompt was pasted with the command |
| a step's stderr ends with `[installer-test] command timed out after 30m0s` | the installer hung (every command is limited to 30 minutes); the MSI log tail is in `report --details` |

## Example config + preset

```yaml
# configs/jobarg-windows-2022.yaml
presets: configs/shared/presets-windows.yaml

environment:
  name: windows-2022
  os: windows
  image: mcr.microsoft.com/windows/servercore:ltsc2022
  isolation: process
  # workdir defaults to C:\ita

installers:
  - pattern: "bin/jobarranger-agentd-{VERSION}*.msi"
    use_preset: agentd-msi
```

```yaml
# configs/shared/presets-windows.yaml
presets:
  agentd-msi:
    upgrade_from: "bin/old/jobarranger-agentd-{OLD_VERSION}-x64.msi"
    install_checks:
      must_exist:
        - 'C:\Program Files\Job Arranger\Job Arranger Agent\bin\*.exe'
        - 'HKLM:\SOFTWARE\JobArranger'
      commands:
        - command: "(Get-Service jobarg_agentd).Status"
          expected_stdout_contains: Running
    uninstall_checks:
      must_not_exist:
        - 'HKLM:\SOFTWARE\JobArranger'
      commands:
        - command: "Get-Service jobarg_agentd -ErrorAction SilentlyContinue"
          expected_exit_code: 1
```

Paths, service names and registry keys are illustrative — use the ones your
installer creates. Single-quoted YAML keeps backslashes literal.

## Commands and checks in PowerShell

- Commands and checks run in **Windows PowerShell 5.1**. The exit code is an
  explicit `exit N`, else the last native program's exit code, else `1` if
  the last statement failed. The script keeps going after a failing
  statement (like `sh -c`).
- Quote paths with single quotes: `'C:\Program Files\x'` (a `'` inside is
  written `''`).
- `must_exist` / `must_not_exist` accept registry paths
  (`HKLM:\SOFTWARE\...`). Globs are PowerShell wildcards: case-insensitive,
  hidden items included, no character restrictions (spaces are fine).
- Services are checked with a command, e.g.
  `(Get-Service <name>).Status` + `expected_stdout_contains: Running`.

### Waiting for installers

`msiexec` and most `setup.exe` files don't block PowerShell, so a command
must start them and **wait**. Don't use `Start-Process -Wait` — it can hang
forever in Server Core containers. Use this pattern:

```powershell
$si = New-Object System.Diagnostics.ProcessStartInfo -ArgumentList '{installer}', '<silent arguments>'
$si.UseShellExecute = $false
$p = [System.Diagnostics.Process]::Start($si); $p.WaitForExit(); exit $p.ExitCode
```

| Installer kind | Silent arguments |
|---|---|
| Inno Setup `.exe` | `/VERYSILENT /SUPPRESSMSGBOXES /NORESTART` |
| NSIS `.exe` | `/S` |
| InstallShield `.exe` | `/s /v"/qn /norestart"` |

`.zip` example: `Expand-Archive -LiteralPath '{installer}' -DestinationPath 'C:\Program Files\MyApp' -Force`.

### MSI defaults

`.msi` files need no commands: install / upgrade use `msiexec /i`, uninstall
`msiexec /x` — silent (`/qn /norestart`) with the pattern above.

- On failure, the **last 40 lines** of the MSI's verbose log are printed in
  the step output, so the reason shows up in `report --details`.
- Exit codes **3010 / 1641** (reboot required) count as success, with a note
  — containers can't reboot, and the checks still catch a broken install.
- The upgrade default works for **major upgrades**. Minor upgrades need e.g.
  `REINSTALL=ALL REINSTALLMODE=vomus` in your own `upgrade_command`.

The exact default commands are shown by `list`.

## Cleanup

Runs with Windows environments start a small **watcher** process that
removes the run's containers and temporary files when the run ends —
however it ends (normal end, Ctrl+C, closed window, killed in Task Manager).
Containers kept with `--keep` stay until you remove them.

If the watcher couldn't finish (e.g. the machine rebooted mid-run, or the
Docker engine was down), clean up manually:

```powershell
.\installer-test-exec.exe cleanup                  # all runs; --keep containers stay
.\installer-test-exec.exe cleanup --include-kept   # --keep containers too
```
