// Package cleanup guarantees that a run leaves no containers or host files
// behind (docs/windows-defaults-cleanup-watcher-plan.md §3): every run has an
// id; its containers carry the label ita.run=<id> and its host files live in
// one run directory <TEMP>/ita-run-<id>. Clean removes them — called by the
// watcher process when the runner exits, and by the `cleanup` command.
// Containers kept with --keep are listed in <run dir>/kept and are only
// removed when IncludeKept is set.
package cleanup

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

const (
	// LabelManaged marks every container the tool creates.
	LabelManaged = "ita"
	// LabelRun carries the run id.
	LabelRun = "ita.run"

	runDirPrefix  = "ita-run-"
	keptFile      = "kept"
	legacyStageGl = "ita-stage-*" // v4 staging folders directly under TEMP
)

// NewRunID returns a unique, sortable run id (e.g. 20261029T124003-1a2b3c).
func NewRunID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b)
}

// RunDir is the host directory holding every file of one run.
func RunDir(tempDir, runID string) string {
	return filepath.Join(tempDir, runDirPrefix+runID)
}

// KeptEntry is one container kept with --keep (one JSON line in <run dir>/kept).
type KeptEntry struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Paths []string `json:"paths,omitempty"` // host files belonging to it (e.g. the Windows staging folder)
}

var keptMu sync.Mutex

// RecordKept appends a kept container to the run's kept list. Called by the
// runner before it reports the case as kept.
func RecordKept(runDir string, e KeptEntry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	keptMu.Lock()
	defer keptMu.Unlock()
	f, err := os.OpenFile(filepath.Join(runDir, keptFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readKept(runDir string) ([]KeptEntry, error) {
	f, err := os.Open(filepath.Join(runDir, keptFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []KeptEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e KeptEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// DockerAPI is the subset of the Docker client Clean uses.
type DockerAPI interface {
	ContainerList(ctx context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerRemove(ctx context.Context, id string, opts client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
}

// NewDockerClient connects to the local engine (DOCKER_HOST or the platform
// default), like the environments do.
func NewDockerClient() (DockerAPI, error) {
	c, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return c, nil
}

// Options selects what Clean removes.
type Options struct {
	// RunID limits cleanup to one run; "" = every run (and leftovers of
	// older tool versions: ita-* containers, <TEMP>/ita-stage-* folders).
	RunID string
	// IncludeKept also removes containers kept with --keep.
	IncludeKept bool
	// TempDir is where run directories live (default os.TempDir()).
	TempDir string
	// Docker is the client; nil = NewDockerClient.
	Docker DockerAPI
	// Out receives one line per removed / kept item (nil = discard).
	Out io.Writer
}

// Clean removes the selected run(s): containers first, then host files.
// It is idempotent. When the container step fails (e.g. daemon down) the
// host files are left alone, so the run can be cleaned again later.
func Clean(ctx context.Context, o Options) error {
	if o.TempDir == "" {
		o.TempDir = os.TempDir()
	}
	out := o.Out
	if out == nil {
		out = io.Discard
	}

	runDirs, err := selectRunDirs(o.TempDir, o.RunID)
	if err != nil {
		return err
	}
	kept := map[string]KeptEntry{}
	for _, d := range runDirs {
		entries, err := readKept(d)
		if err != nil {
			return fmt.Errorf("read kept list in %s: %w", d, err)
		}
		for _, e := range entries {
			kept[e.ID] = e
		}
	}

	if err := cleanContainers(ctx, o, kept, out); err != nil {
		return err
	}

	var errs []error
	for _, d := range runDirs {
		if err := cleanRunDir(d, o.IncludeKept, out); err != nil {
			errs = append(errs, err)
		}
	}
	if o.RunID == "" {
		legacy, _ := filepath.Glob(filepath.Join(o.TempDir, legacyStageGl))
		for _, d := range legacy {
			if err := os.RemoveAll(d); err != nil {
				errs = append(errs, err)
				continue
			}
			fmt.Fprintf(out, "removed folder %s\n", d)
		}
	}
	return errors.Join(errs...)
}

func selectRunDirs(tempDir, runID string) ([]string, error) {
	if runID != "" {
		d := RunDir(tempDir, runID)
		if _, err := os.Stat(d); err != nil {
			return nil, nil // nothing on disk for this run
		}
		return []string{d}, nil
	}
	dirs, err := filepath.Glob(filepath.Join(tempDir, runDirPrefix+"*"))
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

func cleanContainers(ctx context.Context, o Options, kept map[string]KeptEntry, out io.Writer) error {
	api := o.Docker
	if api == nil {
		var err error
		if api, err = NewDockerClient(); err != nil {
			return err
		}
	}
	filters := client.Filters{}
	if o.RunID != "" {
		filters.Add("label", LabelRun+"="+o.RunID)
	}
	list, err := api.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	var errs []error
	for _, c := range list.Items {
		name := containerName(c.Names)
		if o.RunID == "" && c.Labels[LabelManaged] != "true" && c.Labels[LabelRun] == "" &&
			!strings.HasPrefix(name, "ita-") {
			continue // not ours
		}
		if _, isKept := kept[c.ID]; isKept && !o.IncludeKept {
			fmt.Fprintf(out, "kept (--keep): container %s %s\n", name, shortID(c.ID))
			continue
		}
		_, err := api.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
		if err != nil && !cerrdefs.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("remove container %s: %w", name, err))
			continue
		}
		fmt.Fprintf(out, "removed container %s %s\n", name, shortID(c.ID))
	}
	return errors.Join(errs...)
}

// cleanRunDir deletes a run directory. Without includeKept, the kept list
// and the paths of kept containers stay (and so does the directory).
func cleanRunDir(dir string, includeKept bool, out io.Writer) error {
	entries, err := readKept(dir)
	if err != nil {
		return err
	}
	if includeKept || len(entries) == 0 {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
		fmt.Fprintf(out, "removed folder %s\n", dir)
		return nil
	}
	protect := map[string]bool{filepath.Join(dir, keptFile): true}
	for _, e := range entries {
		for _, p := range e.Paths {
			protect[filepath.Clean(p)] = true
		}
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, it := range items {
		p := filepath.Join(dir, it.Name())
		if protect[p] {
			continue
		}
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, err)
		}
	}
	fmt.Fprintf(out, "kept (--keep): folder %s (%d kept container(s))\n", dir, len(entries))
	return errors.Join(errs...)
}

func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
