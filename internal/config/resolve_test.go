package config

import "testing"

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
