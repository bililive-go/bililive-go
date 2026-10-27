package build

import (
	"strings"
	"testing"
)

func TestBuildVersionPreservesMetadata(t *testing.T) {
	t.Setenv("APP_VERSION", "v0.8.3+audio412")
	flags := GetBuildFlags(false)
	if !strings.Contains(flags.LdFlags, ".AppVersion=v0.8.3+audio412 ") {
		t.Fatalf("构建版本元数据被转义: %s", flags.LdFlags)
	}
}
