package env

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"iter"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"bufio"
	cerrdefs "github.com/containerd/errdefs"
	stdcopy "github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/zukigit/installer-testing-auto/internal/shell"
)

type fakeExec struct {
	stdout, stderr string
	code           int
	runningPolls   int  // ExecInspect reports Running this many times first
	hang           bool // output stream never ends (until the connection is closed)
}

type fakeDocker struct {
	imageMissing bool
	pulled       []string
	created      []client.ContainerCreateOptions
	started      []string
	startErr     error
	removed      []string
	removeOpts   []client.ContainerRemoveOptions
	onExec       func(cmd []string) // called at every ExecCreate
	execCmds     [][]string
	execs        []fakeExec // scripted results, in order; default exit 0
	cur          *fakeExec
}

func (f *fakeDocker) ImageInspect(ctx context.Context, image string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	if f.imageMissing {
		return client.ImageInspectResult{}, cerrdefs.ErrNotFound
	}
	return client.ImageInspectResult{}, nil
}

type fakePull struct{ io.Reader }

func (fakePull) Close() error { return nil }
func (fakePull) JSONMessages(context.Context) iter.Seq2[jsonstream.Message, error] {
	return func(func(jsonstream.Message, error) bool) {}
}
func (fakePull) Wait(context.Context) error { return nil }

func (f *fakeDocker) ImagePull(ctx context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
	f.pulled = append(f.pulled, ref)
	return fakePull{strings.NewReader("")}, nil
}

func (f *fakeDocker) ContainerCreate(ctx context.Context, opts client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	f.created = append(f.created, opts)
	return client.ContainerCreateResult{ID: "ctr123"}, nil
}

func (f *fakeDocker) ContainerStart(ctx context.Context, id string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
	f.started = append(f.started, id)
	return client.ContainerStartResult{}, f.startErr
}

func (f *fakeDocker) ExecCreate(ctx context.Context, id string, opts client.ExecCreateOptions) (client.ExecCreateResult, error) {
	f.execCmds = append(f.execCmds, opts.Cmd)
	if f.onExec != nil {
		f.onExec(opts.Cmd)
	}
	f.cur = &fakeExec{}
	if len(f.execs) > 0 {
		f.cur = &f.execs[0]
		f.execs = f.execs[1:]
	}
	return client.ExecCreateResult{ID: "exec1"}, nil
}

func (f *fakeDocker) ExecAttach(ctx context.Context, execID string, _ client.ExecAttachOptions) (client.ExecAttachResult, error) {
	if f.cur.hang {
		c1, _ := net.Pipe() // reads block until c1 is closed
		return client.ExecAttachResult{HijackedResponse: client.HijackedResponse{Conn: c1, Reader: bufio.NewReader(c1)}}, nil
	}
	var buf bytes.Buffer
	writeFrame(&buf, stdcopy.Stdout, f.cur.stdout)
	writeFrame(&buf, stdcopy.Stderr, f.cur.stderr)
	c1, _ := net.Pipe()
	return client.ExecAttachResult{HijackedResponse: client.HijackedResponse{Conn: c1, Reader: bufio.NewReader(&buf)}}, nil
}

// writeFrame writes one docker multiplexed-stream frame (8-byte header).
func writeFrame(w *bytes.Buffer, stream stdcopy.StdType, data string) {
	if data == "" {
		return
	}
	hdr := make([]byte, 8)
	hdr[0] = byte(stream)
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(data)))
	w.Write(hdr)
	w.WriteString(data)
}

func (f *fakeDocker) ExecInspect(ctx context.Context, execID string, _ client.ExecInspectOptions) (client.ExecInspectResult, error) {
	if f.cur.runningPolls > 0 {
		f.cur.runningPolls--
		return client.ExecInspectResult{Running: true}, nil
	}
	return client.ExecInspectResult{ExitCode: f.cur.code}, nil
}

func (f *fakeDocker) ContainerRemove(ctx context.Context, id string, opts client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	f.removed = append(f.removed, id)
	f.removeOpts = append(f.removeOpts, opts)
	return client.ContainerRemoveResult{}, nil
}

func newFakeWin(f *fakeDocker, opts Options) *WindowsDockerEnvironment {
	return newWindowsDocker(opts, func() (dockerAPI, error) { return f, nil })
}

func TestWindowsStartCreateArgs(t *testing.T) {
	f := &fakeDocker{imageMissing: true}
	e := newFakeWin(f, Options{Name: "win", Image: "servercore:ltsc2022", ContainerName: "ita-win-a-msi-fresh-install", Isolation: "process"})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.pulled, []string{"servercore:ltsc2022"}) {
		t.Errorf("pulled = %v", f.pulled)
	}
	c := f.created[0]
	if c.Name != "ita-win-a-msi-fresh-install" || c.Config.Image != "servercore:ltsc2022" ||
		string(c.HostConfig.Isolation) != "process" || c.Config.Labels["ita"] != "true" {
		t.Errorf("create opts = %+v / %+v / %+v", c, c.Config, c.HostConfig)
	}
	wantCmd := []string{"powershell.exe", "-NoProfile", "-Command", "while ($true) { Start-Sleep -Seconds 3600 }"}
	if !reflect.DeepEqual([]string(c.Config.Cmd), wantCmd) {
		t.Errorf("keep-alive = %q", c.Config.Cmd)
	}
	if e.ContainerID() != "ctr123" || e.Workdir() != `C:\ita` || e.Shell() != shell.PowerShell {
		t.Errorf("id=%q workdir=%q shell=%v", e.ContainerID(), e.Workdir(), e.Shell().Name())
	}
	// the host staging directory is bind-mounted read-only at C:\ita-stage
	m := c.HostConfig.Mounts
	if len(m) != 1 || m[0].Type != mount.TypeBind || m[0].Source != e.stageDir ||
		m[0].Target != `C:\ita-stage` || !m[0].ReadOnly {
		t.Errorf("mounts = %+v (stageDir %q)", m, e.stageDir)
	}
	if fi, err := os.Stat(e.stageDir); err != nil || !fi.IsDir() {
		t.Errorf("staging dir missing: %v", err)
	}
	_ = e.Stop(context.Background())
}

func TestWindowsStartFailureRemovesContainer(t *testing.T) {
	f := &fakeDocker{startErr: errors.New("hcs: the container operating system does not match the host")}
	e := newFakeWin(f, Options{Name: "win", Image: "x"})
	err := e.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not match the host") {
		t.Fatalf("err = %v (Docker's error must be kept)", err)
	}
	if !reflect.DeepEqual(f.removed, []string{"ctr123"}) || e.ContainerID() != "" || len(f.pulled) != 0 {
		t.Errorf("removed=%v id=%q pulled=%v", f.removed, e.ContainerID(), f.pulled)
	}
	stage := f.created[0].HostConfig.Mounts[0].Source
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Errorf("staging dir %s must be removed on start failure (err %v)", stage, err)
	}
}

func TestWindowsCopy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "app-1.0.0.msi")
	if err := os.WriteFile(src, []byte("MSI"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeDocker{}
	e := newFakeWin(f, Options{Name: "win", Image: "x", Workdir: `D:\work`})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background())
	var stagedAtExec string
	f.onExec = func([]string) {
		b, _ := os.ReadFile(filepath.Join(e.stageDir, "app-1.0.0.msi"))
		stagedAtExec = string(b)
	}
	if err := e.Copy(context.Background(), src, `D:\work\app-1.0.0.msi`); err != nil {
		t.Fatal(err)
	}
	if len(f.execCmds) != 1 {
		t.Fatalf("want one exec (mkdir + Copy-Item), got %d", len(f.execCmds))
	}
	want := `New-Item -ItemType Directory -Force -Path 'D:\work' | Out-Null` + "\n" +
		`Copy-Item -LiteralPath 'C:\ita-stage\app-1.0.0.msi' -Destination 'D:\work\app-1.0.0.msi' -Force -ErrorAction Stop`
	if s, _ := shell.ScriptOf(f.execCmds[0]); s != want {
		t.Errorf("copy script =\n%s\nwant\n%s", s, want)
	}
	if stagedAtExec != "MSI" {
		t.Errorf("file not staged when Copy-Item ran: %q", stagedAtExec)
	}
	if _, err := os.Stat(filepath.Join(e.stageDir, "app-1.0.0.msi")); !os.IsNotExist(err) {
		t.Errorf("staged file must be removed after the copy (err %v)", err)
	}
}

func TestWindowsCopyFailure(t *testing.T) {
	src := filepath.Join(t.TempDir(), "a.msi")
	_ = os.WriteFile(src, []byte("x"), 0o644)
	f := &fakeDocker{execs: []fakeExec{{stderr: "Copy-Item : Access is denied", code: 1}}}
	e := newFakeWin(f, Options{Name: "win", Image: "x"})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background())
	err := e.Copy(context.Background(), src, `C:\ita\a.msi`)
	if err == nil || !strings.Contains(err.Error(), "Access is denied") || !strings.Contains(err.Error(), "exit 1") {
		t.Errorf("err = %v", err)
	}
}

func TestWindowsExec(t *testing.T) {
	f := &fakeDocker{execs: []fakeExec{{stdout: "\ufeffline1\r\nline2\r\n", stderr: "warn\r\n", code: 3010, runningPolls: 2}}}
	execPollInterval = 0
	e := newFakeWin(f, Options{Name: "win", Image: "x"})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	out, errOut, code, err := e.Exec(context.Background(), shell.PowerShell.Command("x"))
	if err != nil || out != "line1\nline2\n" || errOut != "warn\n" || code != 3010 {
		t.Errorf("Exec = %q, %q, %d, %v", out, errOut, code, err)
	}
}

func TestWindowsStop(t *testing.T) {
	f := &fakeDocker{}
	e := newFakeWin(f, Options{Name: "win", Image: "x"})
	if err := e.Stop(context.Background()); err != nil || len(f.removed) != 0 {
		t.Fatalf("Stop before Start: err=%v removed=%v", err, f.removed)
	}
	_ = e.Start(context.Background())
	if err := e.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.removed, []string{"ctr123"}) || !f.removeOpts[0].Force {
		t.Errorf("removed=%v opts=%+v", f.removed, f.removeOpts)
	}
	if _, err := os.Stat(e.stageDir); !os.IsNotExist(err) {
		t.Errorf("staging dir must be removed on Stop (err %v)", err)
	}
}

func TestFactorySelectsByOS(t *testing.T) {
	w, err := New(TypeDocker, Options{Name: "w", Image: "x", OS: OSWindows})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.(*WindowsDockerEnvironment); !ok || w.Shell() != shell.PowerShell {
		t.Errorf("os windows -> %T", w)
	}
	for _, osName := range []string{"", OSLinux} {
		l, err := New(TypeDocker, Options{Name: "l", Image: "x", OS: osName})
		if err != nil {
			t.Fatal(err)
		}
		d, ok := l.(*DockerEnvironment)
		if !ok || d.Shell() != shell.POSIX || d.Workdir() != "/tmp" {
			t.Errorf("os %q -> %T", osName, l)
		}
	}
}

func TestSplitWindowsPath(t *testing.T) {
	for in, want := range map[string][2]string{
		`C:\ita\x.msi`: {`C:\ita`, "x.msi"},
		`C:\x.msi`:     {`C:\`, "x.msi"},
		`C:/ita/x.msi`: {`C:/ita`, "x.msi"},
	} {
		d, b := splitWindowsPath(in)
		if d != want[0] || b != want[1] {
			t.Errorf("split(%q) = %q, %q", in, d, b)
		}
	}
}

func TestWindowsExecTimeoutAndCancel(t *testing.T) {
	old := execTimeout
	defer func() { execTimeout = old }()

	execTimeout = 50 * time.Millisecond
	f := &fakeDocker{execs: []fakeExec{{hang: true}}}
	e := newFakeWin(f, Options{Name: "win", Image: "x"})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background())
	_, stderr, code, err := e.Exec(context.Background(), shell.PowerShell.Command("hang"))
	if err == nil || code != -1 || !strings.Contains(stderr, "timed out after 50ms") {
		t.Errorf("timeout: code=%d stderr=%q err=%v", code, stderr, err)
	}

	execTimeout = time.Hour
	f.execs = []fakeExec{{hang: true}}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel) // Ctrl+C
	_, stderr, code, err = e.Exec(ctx, shell.PowerShell.Command("hang"))
	if err == nil || code != -1 || !strings.Contains(stderr, "interrupted") {
		t.Errorf("cancel: code=%d stderr=%q err=%v", code, stderr, err)
	}
}
