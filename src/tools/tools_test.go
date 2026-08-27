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

	if got := firstEnvironmentValue(env, "HOME"); got != "/tmp/bililive-home" {
		t.Fatalf("HOME 未被继承，实际值为 %q", got)
	}
	if got := firstEnvironmentValue(env, "HTTPS_PROXY"); got != "http://proxy.example.com" {
		t.Fatalf("HTTPS_PROXY 未被继承，实际值为 %q", got)
	}
	wantPath := nodeFolder + string(os.PathListSeparator) + "/usr/bin"
	if got := firstEnvironmentValue(env, "PATH"); got != wantPath {
		t.Fatalf("PATH = %q，期望 %q", got, wantPath)
	}
	if count := environmentKeyCount(env, "PATH"); count != 1 {
		t.Fatalf("PATH 出现了 %d 次，期望仅出现一次", count)
	}
}

func firstEnvironmentValue(env []string, key string) string {
	for _, entry := range env {
		name, current, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, key) {
			return current
		}
	}
	return ""
}

func environmentKeyCount(env []string, key string) int {
	count := 0
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, key) {
			count++
		}
	}
	return count
}
