package tools

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestEnvironmentHelpersUsePlatformKeyCaseSensitivity(t *testing.T) {
	env := []string{
		"https_proxy=http://lowercase-proxy.invalid",
		"HTTPS_PROXY=http://uppercase-proxy.invalid",
	}
	wantValue := "http://uppercase-proxy.invalid"
	wantCount := 1
	if runtime.GOOS == "windows" {
		wantValue = "http://lowercase-proxy.invalid"
		wantCount = 2
	}

	if got := firstEnvironmentValue(env, "HTTPS_PROXY"); got != wantValue {
		t.Fatalf("HTTPS_PROXY = %q，期望 %q", got, wantValue)
	}
	if got := environmentKeyCount(env, "HTTPS_PROXY"); got != wantCount {
		t.Fatalf("HTTPS_PROXY 计数为 %d，期望 %d", got, wantCount)
	}
}

func firstEnvironmentValue(env []string, key string) string {
	for _, entry := range env {
		_, current, ok := strings.Cut(entry, "=")
		if ok && environmentEntryHasKey(entry, key) {
			return current
		}
	}
	return ""
}

func environmentKeyCount(env []string, key string) int {
	count := 0
	for _, entry := range env {
		if environmentEntryHasKey(entry, key) {
			count++
		}
	}
	return count
}
