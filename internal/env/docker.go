package env

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// DockerEnvironment runs the tests inside a container managed by
// testcontainers-go. It is the first implementation of Environment; future
// implementations (e.g. winrm) follow the same interface.
type DockerEnvironment struct {
	name    string
	image   string
	workdir string
	ctr     testcontainers.Container
}

func init() {
	Register(TypeDocker, func(name, image, workdir string) (Environment, error) {
		if image == "" {
			return nil, fmt.Errorf("docker environment requires an image")
		}
		if workdir == "" {
			workdir = "/tmp"
		}
		return &DockerEnvironment{name: name, image: image, workdir: workdir}, nil
	})
}

// NewDocker is a direct constructor for tests.
func NewDocker(name, image, workdir string) (*DockerEnvironment, error) {
	e, err := New(TypeDocker, name, image, workdir)
	if err != nil {
		return nil, err
	}
	return e.(*DockerEnvironment), nil
}

func (e *DockerEnvironment) Name() string    { return e.name }
func (e *DockerEnvironment) Workdir() string { return e.workdir }

// ContainerID returns the docker container id, or "" before Start.
func (e *DockerEnvironment) ContainerID() string {
	if e.ctr == nil {
		return ""
	}
	return e.ctr.GetContainerID()
}

// Start provisions the container. The default image entrypoint is replaced
// with `sleep infinity` so the container stays alive for the whole run; the
// image must therefore contain a `sleep` binary (true for virtually all
// base images).
func (e *DockerEnvironment) Start(ctx context.Context) error {
	req := testcontainers.ContainerRequest{
		Image: e.image,
		Cmd:   []string{"sleep", "infinity"},
	}
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return fmt.Errorf("start container (image %q): %w (is the docker daemon reachable?)", e.image, err)
	}
	e.ctr = ctr
	return nil
}

// Copy uploads one file, creating the destination directory if needed.
func (e *DockerEnvironment) Copy(ctx context.Context, src, dest string) error {
	if e.ctr == nil {
		return fmt.Errorf("environment %s not started", e.name)
	}
	dest = filepath.ToSlash(dest)
	dir := filepath.ToSlash(filepath.Dir(dest))
	if out, code, err := e.sh(ctx, "mkdir -p "+quote(dir)); err != nil || code != 0 {
		return fmt.Errorf("mkdir %s: %s (exit %d, err %v)", dir, out, code, err)
	}
	if err := e.ctr.CopyFileToContainer(ctx, src, dest, 0o755); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dest, err)
	}
	return nil
}

// Exec runs a command inside the container. Note: docker exec merges stdout
// and stderr, so both returned strings carry the combined output.
func (e *DockerEnvironment) Exec(ctx context.Context, cmd []string) (stdout, stderr string, exitCode int, err error) {
	if e.ctr == nil {
		return "", "", -1, fmt.Errorf("environment %s not started", e.name)
	}
	code, reader, err := e.ctr.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		return "", "", -1, fmt.Errorf("exec %v: %w", cmd, err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		return "", "", code, fmt.Errorf("read exec output: %w", err)
	}
	out := buf.String()
	return out, out, code, nil
}

// Stop terminates the container (testcontainers also cleans up the network).
func (e *DockerEnvironment) Stop(ctx context.Context) error {
	if e.ctr == nil {
		return nil
	}
	return e.ctr.Terminate(ctx)
}

// sh is a convenience wrapper running one shell line inside the container.
func (e *DockerEnvironment) sh(ctx context.Context, cmd string) (string, int, error) {
	stdout, _, code, err := e.Exec(ctx, []string{"/bin/sh", "-c", cmd})
	return stdout, code, err
}

func quote(s string) string {
	return "'" + s + "'"
}
