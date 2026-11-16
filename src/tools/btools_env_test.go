package tools

import (
	"os"
	"slices"
	"testing"
)

func TestBuildBToolsEnv(t *testing.T) {
	sep := string(os.PathListSeparator)
	tests := []struct {
		name string
		base []string
		goos string
		want []string
	}{
		{
			name: "继承 HOME 等变量并前置 node 目录",
			base: []string{"HOME=/srv/bililive", "PATH=/usr/bin", "TZ=Asia/Shanghai"},
			goos: "linux",
			want: []string{"HOME=/srv/bililive", "TZ=Asia/Shanghai", "PATH=/opt/node/bin" + sep + "/usr/bin"},
		},
		{
			name: "缺少 PATH 时不产生空路径段",
			base: []string{"HOME=/srv/bililive"},
			goos: "linux",
			want: []string{"HOME=/srv/bililive", "PATH=/opt/node/bin"},
		},
		{
			name: "PATH 为空时不产生空路径段",
			base: []string{"PATH="},
			goos: "linux",
			want: []string{"PATH=/opt/node/bin"},
		},
		{
			name: "过滤 NODE_* 与 BILILIVE_TOOLS_* 变量",
			base: []string{
				"HOME=/srv/bililive",
				"NODE_OPTIONS=--require /tmp/x.js",
				"NODE_PATH=/tmp",
				"BILILIVE_TOOLS_PASSKEY=secret",
				"BILILIVE_TOOLS_BILIKEY=key",
				"PATH=/usr/bin",
			},
			goos: "linux",
			want: []string{"HOME=/srv/bililive", "PATH=/opt/node/bin" + sep + "/usr/bin"},
		},
		{
			name: "Unix 上 PATH 区分大小写",
			base: []string{"path=/custom", "PATH=/usr/bin"},
			goos: "linux",
			want: []string{"path=/custom", "PATH=/opt/node/bin" + sep + "/usr/bin"},
		},
		{
			name: "Windows 上 Path 不区分大小写",
			base: []string{"Path=C:\\Windows", "USERPROFILE=C:\\Users\\kira"},
			goos: "windows",
			want: []string{"USERPROFILE=C:\\Users\\kira", "PATH=/opt/node/bin" + sep + "C:\\Windows"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildBToolsEnv(tt.base, "/opt/node/bin", tt.goos)
			if !slices.Equal(got, tt.want) {
				t.Errorf("buildBToolsEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}
