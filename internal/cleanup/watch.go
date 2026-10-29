package cleanup

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WatchCommand is the hidden subcommand the watcher process runs.
const WatchCommand = "__watch"

// Watcher is the runner's handle on its watcher process: the write end of
// the pipe connected to the watcher's stdin. The runner never writes to it;
// when the runner exits for any reason the OS closes it and the watcher sees
// EOF.
type Watcher struct {
	w *os.File
}

// alive keeps the pipe ends referenced for the whole process lifetime (an
// unreferenced *os.File would be closed by its finalizer and wake the
// watcher early).
var (
	aliveMu sync.Mutex
	alive   []*os.File
)

// Spawn starts the detached watcher process for one run: this executable
// with `__watch --run <id> --temp <tempDir>`.
func Spawn(runID, tempDir string) (*Watcher, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("start cleanup watcher: %w", err)
	}
	return spawn(func() *exec.Cmd {
		return exec.Command(exe, WatchCommand, "--run", runID, "--temp", tempDir)
	})
}

func spawn(newCmd func() *exec.Cmd) (*Watcher, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("start cleanup watcher: %w", err)
	}
	cmd, err := startDetached(func() *exec.Cmd {
		c := newCmd()
		c.Stdin = r // stdout/stderr stay unset (null device): NDJSON output stays clean
		return c
	})
	r.Close() // the child has its own copy
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("start cleanup watcher: %w", err)
	}
	_ = cmd.Process.Release()
	aliveMu.Lock()
	alive = append(alive, w)
	aliveMu.Unlock()
	return &Watcher{w: w}, nil
}

// Close tells the watcher the runner is done (same effect as the runner
// exiting). Optional — process exit does the same.
func (w *Watcher) Close() error { return w.w.Close() }

// Watch-loop tuning (variables for tests).
var (
	settleDelay  = 2 * time.Second // let in-flight container creates land before cleaning
	retryDelay   = 5 * time.Second
	cleanRetries = 3
)

// Watch blocks until stdin reaches EOF (the runner exited), then cleans the
// run. Nothing is left on success; on failure the log is written to
// <run dir>/watcher.log so the user can see why.
func Watch(stdin io.Reader, o Options) error {
	_, _ = io.Copy(io.Discard, stdin)
	time.Sleep(settleDelay)

	var log strings.Builder
	var err error
	for i := 0; i < cleanRetries; i++ {
		if i > 0 {
			time.Sleep(retryDelay)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		o.Out = &log
		err = Clean(ctx, o)
		cancel()
		if err == nil {
			return nil
		}
		fmt.Fprintf(&log, "attempt %d: %v\n", i+1, err)
	}
	if o.TempDir == "" {
		o.TempDir = os.TempDir()
	}
	dir := RunDir(o.TempDir, o.RunID)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr == nil {
		_ = os.WriteFile(filepath.Join(dir, "watcher.log"), []byte(log.String()), 0o644)
	}
	return err
}
