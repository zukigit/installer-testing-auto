package config

import (
	"strings"
	"testing"
)

func TestDerivePackage(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"myapp_1.2.3_amd64.deb", "myapp"},
		{"MyTool_0.1_all.deb", "MyTool"},
		{"zabbix-agent2-7.2.4-release1.el9.x86_64.rpm", "zabbix-agent2"},
		{"nginx-1.24.0-2.el9.noarch.rpm", "nginx"},
		{"install.sh", "install"},
		{"setup.run", "setup"},
	}
	for _, c := range cases {
		if got := derivePackage(c.file); got != c.want {
			t.Errorf("derivePackage(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}

func TestExpand(t *testing.T) {
	ri := &ResolvedInstaller{
		ContainerPath: "/tmp/demoapp_1.2.3_amd64.deb",
		Basename:      "demoapp_1.2.3_amd64.deb",
		PackageName:   "demoapp",
	}
	got := ri.Expand("dpkg -i {installer} && echo {basename} {package}")
	want := "dpkg -i /tmp/demoapp_1.2.3_amd64.deb && echo demoapp_1.2.3_amd64.deb demoapp"
	if got != want {
		t.Errorf("Expand = %q, want %q", got, want)
	}
}

func TestResolveNoMatch(t *testing.T) {
	cfg := &Config{
		Environment: EnvironmentSpec{Image: "ubuntu:24.04"},
		Installers:  []InstallerSpec{{Pattern: "bin/*.deb"}},
	}
	if _, err := cfg.Resolve("x.yaml"); err == nil {
		t.Fatal("expected error for pattern matching no files")
	}
}

// v3.4: the upgrade pool reaches past variable pinning — with VERSION=7.2.2
// the resolved list holds only the 7.2.2 file, but the pool also holds the
// older 7.2.1 file matched by the widened pattern. Extras are resolved from
// the same installer entry (they inherit its commands and checks).
func TestResolveUpgradePoolWidensPastPinning(t *testing.T) {
	writeTestFiles(t, map[string]string{
		"bin/pkg-7.2.1-1.el8.x86_64.rpm": "",
		"bin/pkg-7.2.2-1.el8.x86_64.rpm": "",
		"configs/test.yaml": `
environment:
  image: test:latest
installers:
  - pattern: "bin/pkg-{VERSION}*el8*.rpm"
`,
	})
	b, err := LoadBundle("configs/test.yaml", map[string]string{"VERSION": "7.2.2"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(b.Installers) != 1 || !strings.HasSuffix(b.Installers[0].LocalPath, "pkg-7.2.2-1.el8.x86_64.rpm") {
		t.Fatalf("pinned list must hold only the 7.2.2 file, got %+v", b.Installers)
	}
	if len(b.UpgradePool) != 2 {
		t.Fatalf("pool must hold both versions, got %d: %+v", len(b.UpgradePool), b.UpgradePool)
	}
	if b.UpgradePool[0] != b.Installers[0] {
		t.Error("pool must start with the pinned installers as the same pointers")
	}
	from := b.UpgradePool[1]
	if !strings.HasSuffix(from.LocalPath, "pkg-7.2.1-1.el8.x86_64.rpm") || from.PackageName != "pkg" || from.Version == "" {
		t.Errorf("pool extra must be the older pkg file with parsed version, got %+v", from)
	}
	if from.InstallCmd != "rpm -ivh {installer}" {
		t.Errorf("extras are resolved from the same entry spec (rpm install default), got %q", from.InstallCmd)
	}
}

// Without variables the widened pattern equals the pinned pattern: the pool
// is the resolved list itself (byte-identical to pre-v3.4 behavior).
func TestResolveUpgradePoolWithoutVariables(t *testing.T) {
	writeTestFiles(t, map[string]string{
		"bin/pkg-7.2.1-1.el8.x86_64.rpm": "",
		"bin/pkg-7.2.2-1.el8.x86_64.rpm": "",
		"configs/test.yaml": `
environment:
  image: test:latest
installers:
  - pattern: "bin/pkg-*el8*.rpm"
`,
	})
	b, err := LoadBundle("configs/test.yaml", nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(b.Installers) != 2 || len(b.UpgradePool) != 2 {
		t.Fatalf("expected 2 installers and 2 pool entries, got %d and %d", len(b.Installers), len(b.UpgradePool))
	}
	for i := range b.Installers {
		if b.UpgradePool[i] != b.Installers[i] {
			t.Errorf("pool[%d] must be the same pointer as installers[%d]", i, i)
		}
	}
}

// A file reachable by two entries (pinned by one, widened by another) appears
// exactly once in the pool, resolved from its own entry when pinned.
func TestResolveUpgradePoolDeduplicatesAcrossEntries(t *testing.T) {
	writeTestFiles(t, map[string]string{
		"bin/pkg-7.2.1-1.el8.x86_64.rpm": "",
		"bin/pkg-7.2.2-1.el8.x86_64.rpm": "",
		"configs/test.yaml": `
environment:
  image: test:latest
installers:
  - pattern: "bin/pkg-{VERSION}*el8*.rpm"
  - pattern: "bin/pkg-7.2.1-1.el8*.rpm"
`,
	})
	b, err := LoadBundle("configs/test.yaml", map[string]string{"VERSION": "7.2.2"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(b.Installers) != 2 {
		t.Fatalf("entry 1 is unfiltered; want 2 pinned installers, got %d", len(b.Installers))
	}
	if len(b.UpgradePool) != 2 {
		t.Errorf("the 7.2.1 file must appear exactly once in the pool, got %d: %+v", len(b.UpgradePool), b.UpgradePool)
	}
}
