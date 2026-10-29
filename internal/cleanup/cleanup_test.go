package cleanup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

type fakeDocker struct {
	items   []container.Summary
	listErr error
	filters []client.Filters
	removed []string
}

func (f *fakeDocker) ContainerList(ctx context.Context, o client.ContainerListOptions) (client.ContainerListResult, error) {
	f.filters = append(f.filters, o.Filters)
	if f.listErr != nil {
		return client.ContainerListResult{}, f.listErr
	}
	var out []container.Summary
	for _, c := range f.items {
		if want, ok := o.Filters["label"]; ok {
			match := false
			for kv := range want {
				k, v, _ := strings.Cut(kv, "=")
				if c.Labels[k] == v {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, c)
	}
	return client.ContainerListResult{Items: out}, nil
}

func (f *fakeDocker) ContainerRemove(ctx context.Context, id string, o client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	f.removed = append(f.removed, id)
	return client.ContainerRemoveResult{}, nil
}

func ctr(id, name string, labels map[string]string) container.Summary {
	return container.Summary{ID: id, Names: []string{"/" + name}, Labels: labels}
}

// scenario: run A has a removable container and a kept one (with its staging
// folder); run B has one container; plus an unrelated user container.
func scenario(t *testing.T) (tmp string, f *fakeDocker) {
	t.Helper()
	tmp = t.TempDir()
	runA, runB := RunDir(tmp, "A"), RunDir(tmp, "B")
	for _, d := range []string{filepath.Join(runA, "stage-1"), filepath.Join(runA, "stage-kept"), runB, filepath.Join(tmp, "ita-stage-legacy")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := RecordKept(runA, KeptEntry{ID: "keptA", Name: "ita-a-kept", Paths: []string{filepath.Join(runA, "stage-kept")}}); err != nil {
		t.Fatal(err)
	}
	f = &fakeDocker{items: []container.Summary{
		ctr("a1", "ita-a-1", map[string]string{LabelManaged: "true", LabelRun: "A"}),
		ctr("keptA", "ita-a-kept", map[string]string{LabelManaged: "true", LabelRun: "A"}),
		ctr("b1", "ita-b-1", map[string]string{LabelManaged: "true", LabelRun: "B"}),
		ctr("old", "ita-old-linux", nil), // older version: name only
		ctr("user", "my-database", nil),  // not ours
	}}
	return tmp, f
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestCleanOneRunKeepsKept(t *testing.T) {
	tmp, f := scenario(t)
	var out strings.Builder
	if err := Clean(context.Background(), Options{RunID: "A", TempDir: tmp, Docker: f, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.removed, []string{"a1"}) {
		t.Errorf("removed = %v, want only a1", f.removed)
	}
	if f.filters[0]["label"][LabelRun+"=A"] != true {
		t.Errorf("list must filter on the run label: %v", f.filters[0])
	}
	runA := RunDir(tmp, "A")
	if exists(filepath.Join(runA, "stage-1")) {
		t.Error("stage-1 must be removed")
	}
	if !exists(filepath.Join(runA, "stage-kept")) || !exists(filepath.Join(runA, keptFile)) {
		t.Error("kept container's folder and the kept list must stay")
	}
	if !exists(RunDir(tmp, "B")) || !exists(filepath.Join(tmp, "ita-stage-legacy")) {
		t.Error("other runs and legacy folders must not be touched by a one-run cleanup")
	}
	if !strings.Contains(out.String(), "kept (--keep): container ita-a-kept") {
		t.Errorf("output must say what was kept:\n%s", out.String())
	}
}

func TestCleanAllRuns(t *testing.T) {
	tmp, f := scenario(t)
	if err := Clean(context.Background(), Options{TempDir: tmp, Docker: f}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(f.removed)
	if !reflect.DeepEqual(f.removed, []string{"a1", "b1", "old"}) {
		t.Errorf("removed = %v (kept and user containers must stay)", f.removed)
	}
	if exists(RunDir(tmp, "B")) || exists(filepath.Join(tmp, "ita-stage-legacy")) {
		t.Error("run B and legacy staging folders must be removed")
	}
	if !exists(filepath.Join(RunDir(tmp, "A"), "stage-kept")) {
		t.Error("kept folder must stay without --include-kept")
	}
}

func TestCleanIncludeKept(t *testing.T) {
	tmp, f := scenario(t)
	if err := Clean(context.Background(), Options{TempDir: tmp, Docker: f, IncludeKept: true}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(f.removed)
	if !reflect.DeepEqual(f.removed, []string{"a1", "b1", "keptA", "old"}) {
		t.Errorf("removed = %v", f.removed)
	}
	if exists(RunDir(tmp, "A")) {
		t.Error("run A must be gone completely")
	}
}

func TestCleanDaemonDownLeavesFiles(t *testing.T) {
	tmp, f := scenario(t)
	f.listErr = errors.New("daemon unreachable")
	if err := Clean(context.Background(), Options{RunID: "A", TempDir: tmp, Docker: f}); err == nil {
		t.Fatal("want error")
	}
	if !exists(filepath.Join(RunDir(tmp, "A"), "stage-1")) {
		t.Error("files must stay when containers could not be cleaned")
	}
}

func TestWatchCleansOnEOF(t *testing.T) {
	settleDelay, retryDelay = 0, 0
	tmp, f := scenario(t)
	if err := Watch(strings.NewReader(""), Options{RunID: "B", TempDir: tmp, Docker: f}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.removed, []string{"b1"}) || exists(RunDir(tmp, "B")) {
		t.Errorf("removed=%v, run B dir exists=%v", f.removed, exists(RunDir(tmp, "B")))
	}

	// failure: log stays in the run dir
	f.listErr = errors.New("daemon unreachable")
	if err := Watch(strings.NewReader(""), Options{RunID: "A", TempDir: tmp, Docker: f}); err == nil {
		t.Fatal("want error")
	}
	b, _ := os.ReadFile(filepath.Join(RunDir(tmp, "A"), "watcher.log"))
	if !strings.Contains(string(b), "attempt 3: list containers: daemon unreachable") {
		t.Errorf("watcher.log = %q", b)
	}
}

// TestSpawnDetectsParentExit runs this test binary as a detached watcher
// child (helper mode): closing the pipe's write end — what the OS does when
// the runner dies — must wake it up.
func TestSpawnDetectsParentExit(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "woke")
	w, err := spawn(func() *exec.Cmd {
		c := exec.Command(os.Args[0], "-test.run=TestHelperWatcher")
		c.Env = append(os.Environ(), "ITA_HELPER_MARKER="+marker)
		return c
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if exists(marker) {
		t.Fatal("watcher woke up before the runner exited")
	}
	w.Close() // the runner "exits"
	deadline := time.Now().Add(10 * time.Second)
	for !exists(marker) {
		if time.Now().After(deadline) {
			t.Fatal("watcher did not notice the runner exit")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestHelperWatcher(t *testing.T) {
	marker := os.Getenv("ITA_HELPER_MARKER")
	if marker == "" {
		t.Skip("helper process only")
	}
	buf := make([]byte, 1)
	for {
		if _, err := os.Stdin.Read(buf); err != nil {
			break
		}
	}
	_ = os.WriteFile(marker, []byte("ok"), 0o644)
}
