package tools

import (
	"os"
	"strings"
	"testing"
)

func TestBuildBToolsEnv(t *testing.T) {
	sep := string(os.PathListSeparator)
	tests := []struct {
		name     string
		base     []string
		wantPath string
		want     map[string]string
	}{
		{
			name:     "继承 HOME 等变量并前置 node 目录",
			base:     []string{"HOME=/srv/bililive", "PATH=/usr/bin", "TZ=Asia/Shanghai"},
			wantPath: "/opt/node/bin" + sep + "/usr/bin",
			want:     map[string]string{"HOME": "/srv/bililive", "TZ": "Asia/Shanghai"},
		},
		{
			name:     "Windows 风格的 Path 也只保留一个",
			base:     []string{"Path=C:\\Windows", "USERPROFILE=C:\\Users\\kira"},
			wantPath: "/opt/node/bin" + sep + "C:\\Windows",
			want:     map[string]string{"USERPROFILE": "C:\\Users\\kira"},
		},
		{
			name:     "缺少 PATH 时不产生空路径段",
			base:     []string{"HOME=/srv/bililive"},
			wantPath: "/opt/node/bin",
			want:     map[string]string{"HOME": "/srv/bililive"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := buildBToolsEnv(tt.base, "/opt/node/bin")

			got := map[string]string{}
			var paths []string
			for _, kv := range env {
				key, value, _ := strings.Cut(kv, "=")
				if strings.EqualFold(key, "PATH") {
					paths = append(paths, kv)
					continue
				}
				got[key] = value
			}
			if len(paths) != 1 || paths[0] != "PATH="+tt.wantPath {
				t.Fatalf("PATH = %v, want [PATH=%s]", paths, tt.wantPath)
			}
			for key, value := range tt.want {
				if got[key] != value {
					t.Errorf("%s = %q, want %q", key, got[key], value)
				}
			}
		})
	}
}
