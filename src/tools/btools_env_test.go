package tools

import (
	"os"
	"strings"
	"testing"
)

func TestBuildBToolsEnvInheritsHomeAndPrependsNodePath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	base := []string{"HOME=/srv/bililive", "PATH=/usr/bin", "TZ=Asia/Shanghai"}

	env := buildBToolsEnv(base, "/opt/node/bin")

	want := map[string]string{
		"HOME": "/srv/bililive",
		"TZ":   "Asia/Shanghai",
		"PATH": "/opt/node/bin" + string(os.PathListSeparator) + "/usr/bin",
	}
	pathCount := 0
	got := map[string]string{}
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		if strings.EqualFold(key, "PATH") {
			pathCount++
		}
		got[key] = value
	}
	if pathCount != 1 {
		t.Fatalf("PATH 应只出现一次，实际 %d 次: %v", pathCount, env)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
}
