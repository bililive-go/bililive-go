//go:build !windows

package tools

import (
	"os"
	"path/filepath"
	"syscall"

	blog "github.com/bililive-go/bililive-go/src/log"
)

// logDirectoryPermissionDiagnostics 输出目录权限诊断信息
func logDirectoryPermissionDiagnostics(dirPath string) {
	logger := blog.GetLogger()

	puid := os.Getenv("PUID")
	pgid := os.Getenv("PGID")
	isDocker := os.Getenv("IS_DOCKER") == "true"

	currentUID := os.Getuid()
	currentGID := os.Getgid()

	logger.Warnf("========== 目录权限诊断 ==========")
	logger.Warnf("目标目录: %s", dirPath)
	logger.Warnf("当前进程 UID:GID = %d:%d", currentUID, currentGID)

	if isDocker {
		logger.Warnf("检测到 Docker 环境 (IS_DOCKER=true)")
		if puid != "" {
			logger.Warnf("环境变量 PUID=%s", puid)
		}
		if pgid != "" {
			logger.Warnf("环境变量 PGID=%s", pgid)
		}
	}

	// 检查上级目录的权限
	parentDir := filepath.Dir(dirPath)
	for parentDir != "/" && parentDir != "." {
		if info, err := os.Stat(parentDir); err == nil {
			var ownerUID, ownerGID uint32
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				ownerUID = stat.Uid
				ownerGID = stat.Gid
			}

			canWrite := false
			testFile := filepath.Join(parentDir, ".bililive_permission_test")
			if f, err := os.Create(testFile); err == nil {
				f.Close()
				os.Remove(testFile)
				canWrite = true
			}

			logger.Warnf("  目录 %s: 所有者 UID:GID=%d:%d, 权限=%v, 可写=%v",
				parentDir, ownerUID, ownerGID, info.Mode().Perm(), canWrite)

			if ownerUID == 0 && currentUID != 0 && !canWrite {
				logger.Warnf("  ↳ 该目录属于 root，当前进程以非 root 用户运行且无写入权限")
			}
		} else if os.IsNotExist(err) {
			logger.Warnf("  目录 %s: 不存在", parentDir)
		} else {
			logger.Warnf("  目录 %s: 无法获取信息 (%v)", parentDir, err)
		}
		parentDir = filepath.Dir(parentDir)
	}

	logger.Warnf("===================================")

	if isDocker && currentUID != 0 {
		logger.Warnf("建议的解决方案:")
		logger.Warnf("  1. 设置环境变量 PUID=0 PGID=0 以 root 用户运行")
		logger.Warnf("  2. 手动进入容器执行: chown -R ${PUID}:${PGID} /opt/bililive")
		logger.Warnf("  3. 更新 Docker 镜像到最新版本（已修复此权限问题）")
	} else {
		logger.Warnf("请检查目录权限，确保当前用户对目录有读写权限")
		logger.Warnf("可尝试: sudo chown -R %d:%d %s", currentUID, currentGID, filepath.Dir(dirPath))
	}
}
