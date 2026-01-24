// Package main 提供 bililive-go 启动器
// 启动器负责管理主程序的生命周期，包括启动、更新和回滚
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bililive-go/bililive-go/src/pkg/ipc"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
)

var (
	configPath  = flag.String("config", "", "启动器配置文件路径")
	instanceID  = flag.String("instance", "default", "实例 ID")
	mainProgram = flag.String("main", "", "主程序路径（覆盖配置）")
	verbose     = flag.Bool("verbose", false, "显示详细日志")
)

func main() {
	flag.Parse()

	launcher, err := NewLauncher(*instanceID, *configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化启动器失败: %v\n", err)
		os.Exit(1)
	}

	// 覆盖主程序路径
	if *mainProgram != "" {
		launcher.config.CurrentBinaryPath = *mainProgram
	}

	launcher.verbose = *verbose

	// 设置信号处理
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		defer bilisentry.Recover()
		<-sigChan
		launcher.log("收到退出信号，正在关闭...")
		cancel()
	}()

	// 运行启动器
	if err := launcher.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "启动器错误: %v\n", err)
		os.Exit(1)
	}
}

// Launcher 启动器
type Launcher struct {
	config      *LauncherConfig
	configPath  string
	instanceID  string
	server      ipc.Server
	mainProcess *exec.Cmd
	mainPID     int
	verbose     bool
	updateReq   *ipc.UpdateRequestPayload
	startupOK   bool
}

// NewLauncher 创建新的启动器
func NewLauncher(instanceID, configPath string) (*Launcher, error) {
	if configPath == "" {
		exePath, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("获取执行路径失败: %w", err)
		}
		configPath = filepath.Join(filepath.Dir(exePath), "launcher-config.json")
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		// 如果配置不存在，创建默认配置
		if os.IsNotExist(err) {
			config = DefaultConfig()
		} else {
			return nil, fmt.Errorf("加载配置失败: %w", err)
		}
	}

	return &Launcher{
		config:     config,
		configPath: configPath,
		instanceID: instanceID,
	}, nil
}

// Run 运行启动器
func (l *Launcher) Run(ctx context.Context) error {
	// 启动 IPC 服务器
	l.server = ipc.NewServer(l.instanceID)
	l.server.OnMessage(l.handleMessage)
	l.server.OnConnect(func(conn ipc.Conn) {
		l.log("主程序已连接")
	})
	l.server.OnDisconnect(func(conn ipc.Conn, err error) {
		l.log("主程序断开连接: %v", err)
	})

	if err := l.server.Start(ctx); err != nil {
		return fmt.Errorf("启动 IPC 服务器失败: %w", err)
	}
	defer l.server.Stop()

	l.log("IPC 服务器已启动")

	// 启动主程序
	for {
		select {
		case <-ctx.Done():
			l.log("启动器退出")
			return nil
		default:
		}

		// 检查是否有待处理的更新
		if l.updateReq != nil {
			if err := l.performUpdate(); err != nil {
				l.log("更新失败: %v", err)
				// 更新失败，继续使用当前版本
			}
			l.updateReq = nil
		}

		// 启动主程序
		if err := l.startMainProgram(ctx); err != nil {
			l.log("启动主程序失败: %v", err)

			// 如果有备份版本，尝试回滚
			if l.config.BackupBinaryPath != "" {
				l.log("尝试回滚到备份版本...")
				if err := l.rollback(); err != nil {
					l.log("回滚失败: %v", err)
					return fmt.Errorf("主程序启动失败且无法回滚: %w", err)
				}
				continue // 回滚后重新启动
			}
			return err
		}

		// 等待主程序确认启动或退出
		startupTimer := time.NewTimer(time.Duration(l.config.StartupTimeout) * time.Second)

		select {
		case <-ctx.Done():
			l.stopMainProgram()
			return nil
		case <-startupTimer.C:
			if !l.startupOK {
				l.log("主程序启动超时")
				l.stopMainProgram()

				// 尝试回滚
				if l.config.BackupBinaryPath != "" {
					l.log("尝试回滚到备份版本...")
					if err := l.rollback(); err != nil {
						l.log("回滚失败: %v", err)
					}
				}
				continue
			}
		}

		startupTimer.Stop()

		// 等待主程序退出
		l.waitForMainProgram()

		// 如果是正常更新退出，继续循环
		if l.updateReq != nil {
			continue
		}

		// 主程序退出，也退出启动器
		break
	}

	return nil
}

// startMainProgram 启动主程序
func (l *Launcher) startMainProgram(ctx context.Context) error {
	binaryPath := l.config.CurrentBinaryPath
	if binaryPath == "" {
		// 默认查找同目录下的 bililive-go
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("获取执行路径失败: %w", err)
		}
		binaryPath = filepath.Join(filepath.Dir(exePath), "bililive-go")
		if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
			binaryPath += ".exe"
		}
	}

	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return fmt.Errorf("主程序不存在: %s", binaryPath)
	}

	l.log("启动主程序: %s", binaryPath)

	l.mainProcess = exec.CommandContext(ctx, binaryPath, os.Args[1:]...)
	l.mainProcess.Stdout = os.Stdout
	l.mainProcess.Stderr = os.Stderr
	l.mainProcess.Stdin = os.Stdin

	// 设置环境变量告知主程序由启动器启动
	l.mainProcess.Env = append(os.Environ(),
		"BILILIVE_LAUNCHER=1",
		fmt.Sprintf("BILILIVE_INSTANCE_ID=%s", l.instanceID),
	)

	if err := l.mainProcess.Start(); err != nil {
		return fmt.Errorf("启动进程失败: %w", err)
	}

	l.mainPID = l.mainProcess.Process.Pid
	l.startupOK = false
	l.log("主程序已启动，PID: %d", l.mainPID)

	return nil
}

// stopMainProgram 停止主程序
func (l *Launcher) stopMainProgram() {
	if l.mainProcess == nil || l.mainProcess.Process == nil {
		return
	}

	// 发送关闭请求
	msg, _ := ipc.NewMessage(ipc.MsgTypeShutdown, ipc.ShutdownPayload{
		Reason:      "launcher_shutdown",
		GracePeriod: 30,
	})
	l.server.Broadcast(msg)

	// 等待进程退出
	done := make(chan struct{})
	go func() {
		defer bilisentry.Recover()
		l.mainProcess.Wait()
		close(done)
	}()

	select {
	case <-done:
		l.log("主程序已正常退出")
	case <-time.After(35 * time.Second):
		l.log("主程序未响应，强制终止")
		l.mainProcess.Process.Kill()
	}
}

// waitForMainProgram 等待主程序退出
func (l *Launcher) waitForMainProgram() {
	if l.mainProcess == nil {
		return
	}
	l.mainProcess.Wait()
	l.log("主程序已退出")
}

// handleMessage 处理来自主程序的 IPC 消息
func (l *Launcher) handleMessage(conn ipc.Conn, msg *ipc.Message) {
	l.log("收到消息: %s", msg.Type)

	switch msg.Type {
	case ipc.MsgTypeStartupSuccess:
		var payload ipc.StartupSuccessPayload
		if err := msg.ParsePayload(&payload); err == nil {
			l.log("主程序启动成功: 版本 %s, PID %d", payload.Version, payload.PID)
			l.startupOK = true

			// 更新配置为当前版本
			l.config.CurrentVersion = payload.Version
			l.config.LastUpdateTime = time.Now().Unix()
			l.saveConfig()
		}

	case ipc.MsgTypeStartupFailed:
		var payload ipc.StartupFailedPayload
		if err := msg.ParsePayload(&payload); err == nil {
			l.log("主程序启动失败: %s", payload.Error)
			l.startupOK = false
		}

	case ipc.MsgTypeUpdateRequest:
		var payload ipc.UpdateRequestPayload
		if err := msg.ParsePayload(&payload); err == nil {
			l.log("收到更新请求: 版本 %s", payload.NewVersion)
			l.updateReq = &payload

			// 发送关闭信号
			shutdownMsg, _ := ipc.NewMessage(ipc.MsgTypeShutdown, ipc.ShutdownPayload{
				Reason:      "update",
				GracePeriod: 30,
			})
			conn.Send(shutdownMsg)
		}

	case ipc.MsgTypeShutdownAck:
		l.log("主程序确认关闭")

	case ipc.MsgTypeHeartbeat:
		ackMsg, _ := ipc.NewMessage(ipc.MsgTypeHeartbeatAck, nil)
		conn.Send(ackMsg)
	}
}

// performUpdate 执行更新
func (l *Launcher) performUpdate() error {
	if l.updateReq == nil {
		return fmt.Errorf("没有待处理的更新请求")
	}

	req := l.updateReq
	l.log("开始更新到版本 %s", req.NewVersion)

	// 验证下载文件
	if _, err := os.Stat(req.DownloadPath); os.IsNotExist(err) {
		return fmt.Errorf("更新文件不存在: %s", req.DownloadPath)
	}

	// 验证 SHA256
	if req.SHA256Checksum != "" {
		l.log("验证文件完整性...")
		actualChecksum, err := calculateSHA256(req.DownloadPath)
		if err != nil {
			return fmt.Errorf("计算文件校验和失败: %w", err)
		}
		if actualChecksum != req.SHA256Checksum {
			return fmt.Errorf("文件校验失败: 期望 %s, 实际 %s", req.SHA256Checksum, actualChecksum)
		}
		l.log("文件校验通过")
	}

	// 备份当前版本可执行文件
	if l.config.CurrentBinaryPath != "" {
		backupPath := l.config.CurrentBinaryPath + ".backup"
		if err := copyFile(l.config.CurrentBinaryPath, backupPath); err != nil {
			l.log("备份当前版本失败: %v", err)
		} else {
			l.config.BackupBinaryPath = backupPath
			l.config.BackupVersion = l.config.CurrentVersion
			l.log("已备份当前版本到: %s", backupPath)
		}
	}

	// 备份数据库文件
	if err := l.backupDatabases(); err != nil {
		l.log("备份数据库失败: %v", err)
		// 数据库备份失败不阻止更新，但记录警告
	}

	// 替换可执行文件
	targetPath := l.config.CurrentBinaryPath
	if targetPath == "" {
		exePath, _ := os.Executable()
		targetPath = filepath.Join(filepath.Dir(exePath), "bililive-go")
	}

	// 解压或复制新版本
	if err := l.extractUpdate(req.DownloadPath, targetPath); err != nil {
		return fmt.Errorf("替换可执行文件失败: %w", err)
	}

	l.config.CurrentVersion = req.NewVersion
	l.saveConfig()

	l.log("更新完成，新版本: %s", req.NewVersion)
	return nil
}

// extractUpdate 解压或复制更新文件
// 支持：直接可执行文件、.tar.gz、.tgz、.zip 格式
func (l *Launcher) extractUpdate(srcPath, dstPath string) error {
	ext := strings.ToLower(filepath.Ext(srcPath))

	switch ext {
	case ".gz":
		// 可能是 .tar.gz
		if strings.HasSuffix(strings.ToLower(srcPath), ".tar.gz") {
			return l.extractTarGz(srcPath, dstPath)
		}
		// 单独的 .gz 文件
		return l.extractGzip(srcPath, dstPath)
	case ".tgz":
		return l.extractTarGz(srcPath, dstPath)
	case ".zip":
		return l.extractZip(srcPath, dstPath)
	default:
		// 直接复制可执行文件
		return copyFile(srcPath, dstPath)
	}
}

// extractTarGz 解压 .tar.gz 文件
func (l *Launcher) extractTarGz(srcPath, dstPath string) error {
	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("打开压缩包失败: %w", err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("创建 gzip reader 失败: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	// 查找可执行文件
	dstDir := filepath.Dir(dstPath)
	dstBase := filepath.Base(dstPath)
	var extractedPath string

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 tar 头失败: %w", err)
		}

		// 跳过目录
		if header.Typeflag == tar.TypeDir {
			continue
		}

		// 查找可执行文件（通常是与目标文件名相同或类似的文件）
		name := filepath.Base(header.Name)
		if name == dstBase || name == dstBase+".exe" ||
			strings.HasPrefix(name, "bililive-go") {
			extractedPath = filepath.Join(dstDir, name)
			outFile, err := os.OpenFile(extractedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("创建目标文件失败: %w", err)
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return fmt.Errorf("解压文件失败: %w", err)
			}
			outFile.Close()
			l.log("已解压: %s", name)
		}
	}

	if extractedPath == "" {
		return fmt.Errorf("压缩包中未找到可执行文件")
	}

	// 如果解压的文件名与目标不同，重命名
	if extractedPath != dstPath {
		if err := os.Rename(extractedPath, dstPath); err != nil {
			return fmt.Errorf("重命名文件失败: %w", err)
		}
	}

	return nil
}

// extractGzip 解压单独的 .gz 文件
func (l *Launcher) extractGzip(srcPath, dstPath string) error {
	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("创建 gzip reader 失败: %w", err)
	}
	defer gzReader.Close()

	outFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, gzReader); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}

	// 设置执行权限
	return os.Chmod(dstPath, 0755)
}

// extractZip 解压 .zip 文件
func (l *Launcher) extractZip(srcPath, dstPath string) error {
	zipReader, err := zip.OpenReader(srcPath)
	if err != nil {
		return fmt.Errorf("打开 zip 文件失败: %w", err)
	}
	defer zipReader.Close()

	dstDir := filepath.Dir(dstPath)
	dstBase := filepath.Base(dstPath)
	var extractedPath string

	for _, file := range zipReader.File {
		// 跳过目录
		if file.FileInfo().IsDir() {
			continue
		}

		// 查找可执行文件
		name := filepath.Base(file.Name)
		if name == dstBase || name == dstBase+".exe" ||
			strings.HasPrefix(name, "bililive-go") {
			rc, err := file.Open()
			if err != nil {
				return fmt.Errorf("打开压缩文件失败: %w", err)
			}

			extractedPath = filepath.Join(dstDir, name)
			outFile, err := os.OpenFile(extractedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
			if err != nil {
				rc.Close()
				return fmt.Errorf("创建目标文件失败: %w", err)
			}

			if _, err := io.Copy(outFile, rc); err != nil {
				outFile.Close()
				rc.Close()
				return fmt.Errorf("解压文件失败: %w", err)
			}
			outFile.Close()
			rc.Close()
			l.log("已解压: %s", name)
		}
	}

	if extractedPath == "" {
		return fmt.Errorf("压缩包中未找到可执行文件")
	}

	// 如果解压的文件名与目标不同，重命名
	if extractedPath != dstPath {
		if err := os.Rename(extractedPath, dstPath); err != nil {
			return fmt.Errorf("重命名文件失败: %w", err)
		}
	}

	return nil
}

// rollback 回滚到备份版本
func (l *Launcher) rollback() error {
	if l.config.BackupBinaryPath == "" {
		return fmt.Errorf("没有可用的备份版本")
	}

	if _, err := os.Stat(l.config.BackupBinaryPath); os.IsNotExist(err) {
		return fmt.Errorf("备份文件不存在: %s", l.config.BackupBinaryPath)
	}

	l.log("从备份恢复: %s", l.config.BackupBinaryPath)

	// 恢复可执行文件
	if err := copyFile(l.config.BackupBinaryPath, l.config.CurrentBinaryPath); err != nil {
		return fmt.Errorf("恢复备份失败: %w", err)
	}

	// 恢复数据库备份
	if err := l.restoreDatabases(); err != nil {
		l.log("恢复数据库备份失败: %v", err)
		// 继续回滚流程，不阻断
	}

	l.config.CurrentVersion = l.config.BackupVersion
	l.saveConfig()

	// 发送更新失败通知
	resultMsg, _ := ipc.NewMessage(ipc.MsgTypeUpdateResult, ipc.UpdateResultPayload{
		Success:    false,
		Version:    l.config.CurrentVersion,
		Error:      "启动失败，已回滚到旧版本",
		RolledBack: true,
	})
	l.server.Broadcast(resultMsg)

	l.log("已回滚到版本: %s", l.config.CurrentVersion)
	return nil
}

// backupDatabases 备份数据库文件
func (l *Launcher) backupDatabases() error {
	// 确定应用数据目录
	appDataPath := l.config.AppDataPath
	if appDataPath == "" {
		// 尝试从默认位置查找
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("获取执行路径失败: %w", err)
		}
		appDataPath = filepath.Join(filepath.Dir(exePath), ".appdata")
	}

	dbPath := filepath.Join(appDataPath, "db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		l.log("数据库目录不存在，跳过备份: %s", dbPath)
		return nil
	}

	// 创建备份目录
	backupDir := filepath.Join(appDataPath, "db_backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("创建备份目录失败: %w", err)
	}

	// 备份所有 .db 文件
	entries, err := os.ReadDir(dbPath)
	if err != nil {
		return fmt.Errorf("读取数据库目录失败: %w", err)
	}

	backupCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 备份 .db 和 .db-wal .db-shm 文件
		if filepath.Ext(name) == ".db" ||
			filepath.Ext(name) == ".db-wal" ||
			filepath.Ext(name) == ".db-shm" {
			srcPath := filepath.Join(dbPath, name)
			dstPath := filepath.Join(backupDir, name)
			if err := copyFile(srcPath, dstPath); err != nil {
				l.log("备份文件 %s 失败: %v", name, err)
			} else {
				backupCount++
			}
		}
	}

	l.config.BackupDbPath = backupDir
	l.log("已备份 %d 个数据库文件到: %s", backupCount, backupDir)
	return nil
}

// restoreDatabases 恢复数据库备份
func (l *Launcher) restoreDatabases() error {
	if l.config.BackupDbPath == "" {
		l.log("没有数据库备份，跳过恢复")
		return nil
	}

	if _, err := os.Stat(l.config.BackupDbPath); os.IsNotExist(err) {
		return fmt.Errorf("数据库备份目录不存在: %s", l.config.BackupDbPath)
	}

	// 确定目标数据库目录
	appDataPath := l.config.AppDataPath
	if appDataPath == "" {
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("获取执行路径失败: %w", err)
		}
		appDataPath = filepath.Join(filepath.Dir(exePath), ".appdata")
	}
	dbPath := filepath.Join(appDataPath, "db")

	// 恢复所有备份的数据库文件
	entries, err := os.ReadDir(l.config.BackupDbPath)
	if err != nil {
		return fmt.Errorf("读取备份目录失败: %w", err)
	}

	restoredCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		srcPath := filepath.Join(l.config.BackupDbPath, name)
		dstPath := filepath.Join(dbPath, name)
		if err := copyFile(srcPath, dstPath); err != nil {
			l.log("恢复文件 %s 失败: %v", name, err)
		} else {
			restoredCount++
		}
	}

	l.log("已恢复 %d 个数据库文件", restoredCount)
	return nil
}

// saveConfig 保存配置
func (l *Launcher) saveConfig() error {
	data, err := json.MarshalIndent(l.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(l.configPath, data, 0644)
}

// log 输出日志
func (l *Launcher) log(format string, args ...any) {
	if l.verbose {
		fmt.Printf("[Launcher] "+format+"\n", args...)
	}
}

// copyFile 复制文件
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := destFile.ReadFrom(sourceFile); err != nil {
		return err
	}

	// 复制权限
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode())
}

// calculateSHA256 计算文件的 SHA256 校验和
func calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
