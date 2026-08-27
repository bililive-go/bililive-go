package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBToolsCommandEnvInheritsParentEnvironment(t *testing.T) {
	t.Setenv("HOME", "/tmp/bililive-home")
	t.Setenv("HTTPS_PROXY", "http://proxy.example.com")
	t.Setenv("PATH", "/usr/bin")

	nodeFolder := filepath.Join("opt", "bililive", "tools", "node")
	env := btoolsCommandEnv(nodeFolder)

	if got := lastEnvironmentValue(env, "HOME"); got != "/tmp/bililive-home" {
		t.Fatalf("HOME 未被继承，实际值为 %q", got)
	}
	if got := lastEnvironmentValue(env, "HTTPS_PROXY"); got != "http://proxy.example.com" {
		t.Fatalf("HTTPS_PROXY 未被继承，实际值为 %q", got)
	}
	wantPath := nodeFolder + string(os.PathListSeparator) + "/usr/bin"
	if got := lastEnvironmentValue(env, "PATH"); got != wantPath {
		t.Fatalf("PATH = %q，期望 %q", got, wantPath)
	}
}

func lastEnvironmentValue(env []string, key string) string {
	var value string
	for _, entry := range env {
		name, current, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, key) {
			value = current
		}
	}
	return value
}
