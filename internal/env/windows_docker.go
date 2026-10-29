package env

// Windows containers are driven with the plain Docker client instead of
// testcontainers-go, which does not support Windows containers (Ryuk is a
// Linux image; CopyFileToContainer extracts full-path tar entries at "/").
// See docs/windows-environment-plan.md §3.1 and §6. No build tag: this file
// compiles and unit-tests on Linux (hence not named *_windows.go, which Go
// would only build for GOOS=windows).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	stdcopy "github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/zukigit/installer-testing-auto/internal/shell"
)

// DefaultWindowsWorkdir is the in-container directory installers are copied
// to in Windows environments when environment.workdir is not set.
const DefaultWindowsWorkdir = `C:\ita`

// stageMount is where the host staging directory is mounted (read-only)
// inside the container. Copy goes through it because the Docker API cannot
// copy files into a running Hyper-V isolated container.
const stageMount = `C:\ita-stage`

// windowsKeepAlive keeps the container running for the whole case (the
// Linux side uses `sleep infinity`).
var windowsKeepAlive = []string{"powershell.exe", "-NoProfile", "-Command", "while ($true) { Start-Sleep -Seconds 3600 }"}

// dockerAPI is the subset of *client.Client the Windows environment uses, so
// unit tests can substitute a fake.
type dockerAPI interface {
	ImageInspect(ctx context.Context, image string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImagePull(ctx context.Context, ref string, opts client.ImagePullOptions) (client.ImagePullResponse, error)
	ContainerCreate(ctx context.Context, opts client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerStart(ctx context.Context, id string, opts client.ContainerStartOptions) (client.ContainerStartResult, error)
	ExecCreate(ctx context.Context, id string, opts client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(ctx context.Context, execID string, opts client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(ctx context.Context, execID string, opts client.ExecInspectOptions) (client.ExecInspectResult, error)
	ContainerRemove(ctx context.Context, id string, opts client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
}

// clientProvider returns the Docker client; errors surface at Start.
type clientProvider func() (dockerAPI, error)

// sharedDockerClient creates one client per process (on first use) and shares
// it between all Windows cases. It talks to the local engine
// (npipe:////./pipe/docker_engine on Windows) or DOCKER_HOST; the API version
// is negotiated on the first request.
var sharedDockerClient = func() clientProvider {
	var (
		once sync.Once
		c    *client.Client
		err  error
	)
	return func() (dockerAPI, error) {
		once.Do(func() { c, err = client.New(client.FromEnv) })
		if err != nil {
			return nil, fmt.Errorf("create docker client: %w", err)
		}
		return c, nil
	}
}()

// WindowsDockerEnvironment runs the tests inside a Windows container
// (e.g. mcr.microsoft.com/windows/servercore) on a Windows Docker Engine.
type WindowsDockerEnvironment struct {
	name, image, workdir, containerName string
	isolation                           string
	clients                             clientProvider
	api                                 dockerAPI
	id                                  string
	stageDir                            string // host staging directory, mounted at stageMount
	runID, runDir                       string
}

func newWindowsDocker(opts Options, clients clientProvider) *WindowsDockerEnvironment {
	if opts.Workdir == "" {
		opts.Workdir = DefaultWindowsWorkdir
	}
	return &WindowsDockerEnvironment{
		name: opts.Name, image: opts.Image, workdir: opts.Workdir,
		containerName: opts.ContainerName, isolation: opts.Isolation,
		clients: clients, runID: opts.RunID, runDir: opts.RunDir,
	}
}

func (e *WindowsDockerEnvironment) Name() string         { return e.name }
func (e *WindowsDockerEnvironment) Workdir() string      { return e.workdir }
func (e *WindowsDockerEnvironment) Shell() shell.Dialect { return shell.PowerShell }

// ContainerID returns the docker container id, or "" before Start.
func (e *WindowsDockerEnvironment) ContainerID() string { return e.id }

// HostPaths returns the host files belonging to this container (its staging
// folder), recorded for --keep so cleanup leaves them with the container.
func (e *WindowsDockerEnvironment) HostPaths() []string {
	if e.stageDir == "" {
		return nil
	}
	return []string{e.stageDir}
}

// Start pulls the image when it is missing, then creates and starts the
// container with a PowerShell keep-alive command.
func (e *WindowsDockerEnvironment) Start(ctx context.Context) error {
	api, err := e.clients()
	if err != nil {
		return err
	}
	e.api = api
	if err := e.ensureImage(ctx); err != nil {
		return err
	}
	// staging folder: inside the run directory, so the cleanup watcher
	// removes it with the run; without a run dir directly under TEMP
	parent, prefix := e.runDir, "stage-"
	if parent == "" {
		parent, prefix = os.TempDir(), "ita-stage-"
	}
	stageDir, err := os.MkdirTemp(parent, prefix)
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	if stageDir, err = filepath.Abs(stageDir); err != nil {
		os.RemoveAll(stageDir)
		return fmt.Errorf("create staging directory: %w", err)
	}
	labels := Labels(e.name, e.runID)
	created, err := api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: e.containerName,
		Config: &container.Config{
			Image:  e.image,
			Cmd:    windowsKeepAlive,
			Labels: labels,
		},
		HostConfig: &container.HostConfig{
			Isolation: container.Isolation(e.isolation),
			Mounts: []mount.Mount{{
				Type: mount.TypeBind, Source: stageDir, Target: stageMount, ReadOnly: true,
			}},
		},
	})
	if err != nil {
		os.RemoveAll(stageDir)
		return fmt.Errorf("create container (image %q): %w", e.image, err)
	}
	if _, err := api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		_, _ = api.ContainerRemove(context.WithoutCancel(ctx), created.ID, client.ContainerRemoveOptions{Force: true})
		os.RemoveAll(stageDir)
		return fmt.Errorf("start container (image %q): %w", e.image, err)
	}
	e.id = created.ID
	e.stageDir = stageDir
	return nil
}

func (e *WindowsDockerEnvironment) ensureImage(ctx context.Context) error {
	_, err := e.api.ImageInspect(ctx, e.image)
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspect image %q: %w (is the docker daemon reachable?)", e.image, err)
	}
	resp, err := e.api.ImagePull(ctx, e.image, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %q: %w", e.image, err)
	}
	defer resp.Close()
	if err := resp.Wait(ctx); err != nil {
		return fmt.Errorf("pull image %q: %w", e.image, err)
	}
	return nil
}

// Copy uploads one file. The Docker API cannot copy into a running Hyper-V
// isolated container, so the file is placed in the host staging directory
// (mounted read-only at C:\ita-stage) and copied to dest inside the
// container with Copy-Item. Works the same for process isolation.
func (e *WindowsDockerEnvironment) Copy(ctx context.Context, src, dest string) error {
	if e.id == "" {
		return fmt.Errorf("environment %s not started", e.name)
	}
	dir, base := splitWindowsPath(dest)
	if base == "" {
		return fmt.Errorf("copy %s -> %s: destination has no file name", src, dest)
	}
	staged := filepath.Join(e.stageDir, base)
	if err := copyFile(src, staged); err != nil {
		return fmt.Errorf("copy %s -> %s: stage file: %w", src, dest, err)
	}
	defer os.Remove(staged) // the container has its own copy afterwards
	ps := shell.PowerShell
	script := ps.MkdirAll(dir) + "\n" +
		"Copy-Item -LiteralPath " + ps.Quote(stageMount+`\`+base) +
		" -Destination " + ps.Quote(dest) + " -Force -ErrorAction Stop"
	out, errOut, code, err := e.Exec(ctx, ps.Command(script))
	if err != nil || code != 0 {
		return fmt.Errorf("copy %s -> %s: %s%s (exit %d, err %v)", src, dest,
			strings.TrimSpace(out), strings.TrimSpace(errOut), code, err)
	}
	return nil
}

// copyFile copies a local file (hard link when possible, else a full copy).
func copyFile(src, dst string) error {
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// splitWindowsPath splits a container path at its last separator (\ or /).
func splitWindowsPath(p string) (dir, base string) {
	i := strings.LastIndexAny(p, `\/`)
	if i < 0 {
		return "", p
	}
	dir = p[:i]
	if strings.HasSuffix(dir, ":") {
		dir += `\` // "C:" alone is drive-relative; keep the root
	}
	return dir, p[i+1:]
}

// execPollInterval is how often Exec re-inspects an exec whose output stream
// ended before the daemon marked it finished.
var execPollInterval = 100 * time.Millisecond

// execTimeout caps one command (install step, check, copy). A command that
// never returns (e.g. a hung installer) fails its step instead of blocking
// the run forever.
var execTimeout = 30 * time.Minute

// Exec runs a command inside the container. stdout and stderr are
// demultiplexed; CRLF line endings are normalized to LF. The command is
// abandoned on Ctrl+C (ctx cancelled) or after execTimeout; the container
// (and the hung process in it) is removed by Stop as usual.
func (e *WindowsDockerEnvironment) Exec(ctx context.Context, cmd []string) (stdout, stderr string, exitCode int, err error) {
	if e.id == "" {
		return "", "", -1, fmt.Errorf("environment %s not started", e.name)
	}
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	created, err := e.api.ExecCreate(ctx, e.id, client.ExecCreateOptions{
		Cmd: cmd, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return "", "", -1, fmt.Errorf("exec %v: %w", cmd, err)
	}
	att, err := e.api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", "", -1, fmt.Errorf("exec %v: %w", cmd, err)
	}
	var outBuf, errBuf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(&outBuf, &errBuf, att.Reader)
		done <- err
	}()
	var cerr error
	select {
	case cerr = <-done:
		att.Close()
	case <-ctx.Done():
		att.Close() // unblocks StdCopy
		<-done
		stdout, stderr = normalizeOutput(outBuf.String()), normalizeOutput(errBuf.String())
		reason := "interrupted"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			reason = fmt.Sprintf("timed out after %s", execTimeout)
		}
		// stderr carries the reason because steps log stdout/stderr, not err
		stderr = strings.TrimRight(stderr, "\n")
		if stderr != "" {
			stderr += "\n"
		}
		stderr += "[installer-test] command " + reason + " (did not finish)"
		return stdout, stderr, -1, fmt.Errorf("exec %v: %s", cmd, reason)
	}
	stdout, stderr = normalizeOutput(outBuf.String()), normalizeOutput(errBuf.String())
	if cerr != nil {
		return stdout, stderr, -1, fmt.Errorf("demultiplex exec output: %w", cerr)
	}
	for {
		ins, err := e.api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			return stdout, stderr, -1, fmt.Errorf("inspect exec: %w", err)
		}
		if !ins.Running {
			return stdout, stderr, ins.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return stdout, stderr, -1, ctx.Err()
		case <-time.After(execPollInterval):
		}
	}
}

// normalizeOutput turns Windows output into the Linux shape: CRLF -> LF and
// no leading UTF-8 byte-order mark.
func normalizeOutput(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// Stop force-removes the container and its host staging directory.
func (e *WindowsDockerEnvironment) Stop(ctx context.Context) error {
	if e.id == "" {
		return nil
	}
	if _, err := e.api.ContainerRemove(ctx, e.id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
		return fmt.Errorf("remove container %s: %w", e.id, err)
	}
	if e.stageDir != "" {
		_ = os.RemoveAll(e.stageDir)
	}
	return nil
}
