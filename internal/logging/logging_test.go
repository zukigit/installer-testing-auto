package logging

import (
	"strings"
	"testing"
)

func TestEnvStartOS(t *testing.T) {
	for _, c := range []struct {
		os   string
		want bool
	}{{"", false}, {"linux", false}, {"windows", true}} {
		var b strings.Builder
		New(&b).EnvStart("e", "img", "/tmp", c.os)
		if got := strings.Contains(b.String(), `"os":"windows"`); got != c.want {
			t.Errorf("os %q: line %s", c.os, b.String())
		}
		if !c.want && strings.Contains(b.String(), `"os"`) {
			t.Errorf("os %q must be omitted: %s", c.os, b.String())
		}
	}
}
