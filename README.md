# installer-testing-auto

Automation tool to test installer files on target environments using
[testcontainers-go](https://golang.testcontainers.org/) (Windows via WinRM is a future environment type).

## How it works

1. Drop installer files into `bin/` (never committed).
2. Describe each target environment in `configs/<env>.yaml` — one file per environment:
   docker image, installer file patterns (**wildcards allowed**), and optional
   per-installer checks (`must_exist`, `must_not_exist`, shell `commands`).
3. Run the tool: it spins up **one container per environment**, copies the
   installer files in, **installs** them, runs the checks, **uninstalls** them
   (uninstall always runs install first) and runs the uninstall checks.

See `docs/installer-testing-tool-plan.md` for the full design.

## Usage

```sh
# build (binary is a build artifact — do not commit it)
go build -o installer-test ./cmd/installer-test

# dry-run: show envs, matched installers, resolved commands
./installer-test list

# install + uninstall on all environments in configs/*.yaml
./installer-test run

# install phase only
./installer-test run --task fresh-install

# one environment, keep the container for debugging
./installer-test run --config configs/ubuntu-24.04.yaml --keep

# remove the binary when done (build artifact hygiene)
rm installer-test
```

Exit code `0` means all checks passed (CI friendly).

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
