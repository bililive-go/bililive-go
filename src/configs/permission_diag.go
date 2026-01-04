//go:build !windows

package configs

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// PermissionDiagnostics 包含权限诊断信息
type PermissionDiagnostics struct {
	FilePath    string
	FileExists  bool
	CanRead     bool
	CanWrite    bool
	FileMode    os.FileMode
	OwnerUID    uint32
	OwnerGID    uint32
	CurrentUID  int
	CurrentGID  int
	PUID        string
	PGID        string
	IsDocker    bool
	Suggestions []string
}

// DiagnoseFilePermission 诊断文件权限问题
func DiagnoseFilePermission(filePath string) *PermissionDiagnostics {
	diag := &PermissionDiagnostics{
		FilePath: filePath,
		PUID:     os.Getenv("PUID"),
		PGID:     os.Getenv("PGID"),
		IsDocker: isInContainer(),
	}

	// 获取当前进程的 UID 和 GID
	diag.CurrentUID = os.Getuid()
	diag.CurrentGID = os.Getgid()

	// 检查文件是否存在
	fileInfo, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		diag.FileExists = false
		diag.Suggestions = append(diag.Suggestions,
			fmt.Sprintf("文件 %s 不存在，请检查配置文件路径是否正确", filePath))
		return diag
	}
	if err != nil {
		diag.FileExists = false
		diag.Suggestions = append(diag.Suggestions,
			fmt.Sprintf("无法获取文件信息: %v", err))
		return diag
	}

	diag.FileExists = true
	diag.FileMode = fileInfo.Mode()

	// 获取文件所有者信息（仅 Unix 系统）
	if stat, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
		diag.OwnerUID = stat.Uid
		diag.OwnerGID = stat.Gid
	}

	// 尝试读取文件
	file, err := os.OpenFile(filePath, os.O_RDONLY, 0)
	if err != nil {
		diag.CanRead = false
	} else {
		diag.CanRead = true
		file.Close()
	}

	// 尝试写入文件
	file, err = os.OpenFile(filePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		diag.CanWrite = false
	} else {
		diag.CanWrite = true
		file.Close()
	}

	// 生成建议
	diag.generateSuggestions()

	return diag
}

// generateSuggestions 根据诊断结果生成建议
func (d *PermissionDiagnostics) generateSuggestions() {
	if d.CanRead && d.CanWrite {
		return // 没有权限问题
	}

	if d.IsDocker {
		// Docker 环境特定建议
		if d.PUID != "" && d.PUID != "0" {
			d.Suggestions = append(d.Suggestions,
				fmt.Sprintf("检测到 Docker 环境中 PUID=%s（非 root）", d.PUID))
		}
		if d.PGID != "" && d.PGID != "0" {
			d.Suggestions = append(d.Suggestions,
				fmt.Sprintf("检测到 Docker 环境中 PGID=%s（非 root）", d.PGID))
		}

		if !d.CanRead {
			d.Suggestions = append(d.Suggestions,
				fmt.Sprintf("文件 %s 无法读取。文件所有者 UID:GID = %d:%d，当前进程 UID:GID = %d:%d",
					d.FilePath, d.OwnerUID, d.OwnerGID, d.CurrentUID, d.CurrentGID))

			// 如果文件属于 root，但进程以非 root 运行
			if d.OwnerUID == 0 && d.CurrentUID != 0 {
				d.Suggestions = append(d.Suggestions,
					"文件属于 root 用户，但您的容器以非 root 用户运行")
				d.Suggestions = append(d.Suggestions,
					"请尝试以下解决方案之一:")
				d.Suggestions = append(d.Suggestions,
					"  1. 设置环境变量 PUID=0 PGID=0 以 root 用户运行")
				d.Suggestions = append(d.Suggestions,
					"  2. 手动进入容器并执行: chown -R ${PUID}:${PGID} /etc/bililive-go")
			}
		}

		if !d.CanWrite {
			d.Suggestions = append(d.Suggestions,
				fmt.Sprintf("文件 %s 无法写入，配置变更将无法保存", d.FilePath))
		}
	} else {
		// 非 Docker 环境
		if !d.CanRead {
			d.Suggestions = append(d.Suggestions,
				fmt.Sprintf("无法读取文件 %s，请检查文件权限。当前权限: %v",
					d.FilePath, d.FileMode))
		}
		if !d.CanWrite {
			d.Suggestions = append(d.Suggestions,
				fmt.Sprintf("无法写入文件 %s，配置变更将无法保存。当前权限: %v",
					d.FilePath, d.FileMode))
		}
	}
}

// FormatError 格式化权限诊断为用户友好的错误信息
func (d *PermissionDiagnostics) FormatError() string {
	if len(d.Suggestions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n========== 权限诊断信息 ==========\n")

	for _, suggestion := range d.Suggestions {
		sb.WriteString(suggestion)
		sb.WriteString("\n")
	}

	sb.WriteString("===================================\n")
	return sb.String()
}
