package utils

import (
	"strings"
	"testing"
)

func TestValidateWindowsCompatibleAbsoluteFilePath(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		errorSubstr string
	}{
		{
			name: "普通路径",
			path: "/recordings/平台/主播/2026-08-29 03-29-00.flv",
		},
		{
			name: "单个名称恰好达到上限",
			path: "/" + strings.Repeat("a", windowsMaxComponentLength),
		},
		{
			name: "完整文件路径恰好达到上限",
			path: "/" + strings.Repeat("a/", 120) + strings.Repeat("b", 18),
		},
		{
			name: "文件夹路径恰好达到上限",
			path: "/" + strings.Repeat("a/", 122) + "bb/x.flv",
		},
		{
			name:        "单个名称超过上限",
			path:        "/recordings/" + strings.Repeat("a", windowsMaxComponentLength+1) + ".flv",
			errorSubstr: "单个名称过长",
		},
		{
			name:        "完整文件路径超过上限",
			path:        "/" + strings.Repeat("a/", 120) + strings.Repeat("b", 19),
			errorSubstr: "输出文件完整路径过长",
		},
		{
			name:        "文件夹路径超过上限",
			path:        "/" + strings.Repeat("a/", 124) + "x.flv",
			errorSubstr: "输出文件夹路径过长",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWindowsCompatibleAbsoluteFilePath(tt.path)
			if tt.errorSubstr == "" {
				if err != nil {
					t.Fatalf("普通路径不应校验失败: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.errorSubstr) {
				t.Fatalf("期望错误包含 %q，实际为 %v", tt.errorSubstr, err)
			}
		})
	}
}

func TestWindowsUTF16Length(t *testing.T) {
	if got := windowsUTF16Length("中文😀"); got != 4 {
		t.Fatalf("UTF-16 长度期望为 4，实际为 %d", got)
	}

	// utf16.RuneLen 对代理项返回 -1，长度统计必须进行下限保护。
	if got := windowsUTF16RuneLength(rune(0xd800)); got != 1 {
		t.Fatalf("非法代理项应按一个 UTF-16 码元计数，实际为 %d", got)
	}
}

func TestValidateOutputFilePathOnCurrentPlatform(t *testing.T) {
	err := ValidateOutputFilePath("recording.flv")
	if err != nil {
		t.Fatalf("普通输出路径不应校验失败: %v", err)
	}
}
