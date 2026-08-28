package utils

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

const (
	// Windows 传统 MAX_PATH 为 260，其中包含结尾的 NUL。
	windowsMaxFilePathLength = 259
	// 创建目录时需要为 8.3 文件名预留 12 个字符。
	windowsMaxDirPathLength   = 247
	windowsMaxComponentLength = 255
)

// ValidateOutputFilePath 在 Windows 上检查输出文件路径是否能被资源管理器等
// 仍受传统 MAX_PATH 限制的程序可靠处理。其他平台不施加 Windows 路径限制。
func ValidateOutputFilePath(filePath string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	return validateWindowsCompatibleFilePath(filePath)
}

func validateWindowsCompatibleFilePath(filePath string) error {
	absolutePath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("无法解析输出文件绝对路径: %w", err)
	}
	return validateWindowsCompatibleAbsoluteFilePath(absolutePath)
}

func validateWindowsCompatibleAbsoluteFilePath(filePath string) error {
	cleanPath := filepath.Clean(filePath)

	for _, component := range strings.FieldsFunc(cleanPath, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if length := windowsUTF16Length(component); length > windowsMaxComponentLength {
			return fmt.Errorf(
				"输出路径中的单个名称过长（%d 个字符，Windows 兼容上限为 %d），请缩短输出模板",
				length,
				windowsMaxComponentLength,
			)
		}
	}

	if length := windowsUTF16Length(cleanPath); length > windowsMaxFilePathLength {
		return fmt.Errorf(
			"输出文件完整路径过长（%d 个字符，Windows 兼容上限为 %d），请缩短输出目录或输出模板",
			length,
			windowsMaxFilePathLength,
		)
	}

	directoryPath := filepath.Dir(cleanPath)
	if length := windowsUTF16Length(directoryPath); length > windowsMaxDirPathLength {
		return fmt.Errorf(
			"输出文件夹路径过长（%d 个字符，Windows 兼容上限为 %d），请缩短输出目录或输出模板",
			length,
			windowsMaxDirPathLength,
		)
	}

	return nil
}

func windowsUTF16Length(value string) int {
	length := 0
	for _, r := range value {
		length += utf16.RuneLen(r)
	}
	return length
}
