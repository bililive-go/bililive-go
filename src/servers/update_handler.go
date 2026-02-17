package servers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/pkg/launcher"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/update"
	"github.com/bililive-go/bililive-go/src/recorders"
)

// 全局更新管理器
var (
	updateManager     *update.Manager
	autoUpdater       *update.AutoUpdater
	updateManagerOnce sync.Once

	// 优雅更新相关状态
	gracefulUpdateMu      sync.RWMutex
	gracefulUpdatePending bool   // 是否有等待中的优雅更新
	gracefulUpdateVersion string // 等待更新的版本号

	// 重启关闭回调（由主程序注册）
	shutdownFunc func()

	// 待执行的 launcher 模式切换标志
	pendingLauncherTransition bool
)

// getUpdateManager 获取或初始化更新管理器
func getUpdateManager() *update.Manager {
	updateManagerOnce.Do(func() {
		cfg := configs.GetCurrentConfig()

		updateManager = update.NewManager(update.ManagerConfig{
			CurrentVersion: consts.AppVersion,
			AppDataPath:    cfg.AppDataPath,
			InstanceID:     "",
		})

		// 初始化自动更新器
		autoUpdater = update.NewAutoUpdater(updateManager)
		setupAutoUpdaterCallbacks(autoUpdater)
	})
	return updateManager
}

// GetAutoUpdater 获取自动更新器实例
func GetAutoUpdater() *update.AutoUpdater {
	getUpdateManager() // 确保已初始化
	return autoUpdater
}

// StartAutoUpdater 启动自动更新器
func StartAutoUpdater(ctx context.Context) {
	updater := GetAutoUpdater()
	if updater != nil {
		updater.Start(ctx)
	}
}

// StopAutoUpdater 停止自动更新器
func StopAutoUpdater() {
	if autoUpdater != nil {
		autoUpdater.Stop()
	}
}

// setupAutoUpdaterCallbacks 设置自动更新器的回调函数
func setupAutoUpdaterCallbacks(updater *update.AutoUpdater) {
	hub := GetSSEHub()

	// 检测到更新时广播
	updater.SetOnUpdateAvailable(func(info *update.ReleaseInfo) {
		hub.BroadcastUpdateAvailable(map[string]interface{}{
			"version":      info.Version,
			"release_date": info.ReleaseDate,
			"changelog":    info.Changelog,
			"prerelease":   info.Prerelease,
			"asset_name":   info.AssetName,
			"asset_size":   info.AssetSize,
		})
	})

	// 下载进度广播
	updater.SetOnDownloadProgress(func(progress update.DownloadProgress) {
		hub.BroadcastUpdateDownloading(map[string]interface{}{
			"downloaded_bytes": progress.DownloadedBytes,
			"total_bytes":      progress.TotalBytes,
			"speed":            progress.Speed,
			"percentage":       progress.Percentage,
		})
	})

	// 更新准备就绪时广播
	updater.SetOnUpdateReady(func(info *update.ReleaseInfo) {
		activeRecordings := 0
		cfg := configs.GetCurrentConfig()
		if cfg != nil {
			// 简单估算活跃录制数量（实际应从 recorder manager 获取）
			activeRecordings = getActiveRecordingsCount(context.Background())
		}
		hub.BroadcastUpdateReady(map[string]interface{}{
			"version":           info.Version,
			"can_apply_now":     activeRecordings == 0,
			"active_recordings": activeRecordings,
		})
	})

	// 错误时广播
	updater.SetOnError(func(err error) {
		hub.BroadcastUpdateError(err)
	})
}

// UpdateCheckResponse 更新检查响应
type UpdateCheckResponse struct {
	Available  bool                `json:"available"`
	CurrentVer string              `json:"current_version"`
	LatestInfo *update.ReleaseInfo `json:"latest_info,omitempty"`
	IsDocker   bool                `json:"is_docker"`
	Error      string              `json:"error,omitempty"`
}

// UpdateStatusResponse 更新状态响应
type UpdateStatusResponse struct {
	State                 update.UpdateState      `json:"state"`
	Progress              update.DownloadProgress `json:"progress,omitempty"`
	Error                 string                  `json:"error,omitempty"`
	GracefulUpdatePending bool                    `json:"graceful_update_pending"`
	GracefulUpdateVersion string                  `json:"graceful_update_version,omitempty"`
	ActiveRecordingsCount int                     `json:"active_recordings_count"`
	CanApplyNow           bool                    `json:"can_apply_now"`
	AvailableInfo         *update.ReleaseInfo     `json:"available_info,omitempty"`
}

// checkUpdate 检查是否有新版本
// GET /api/update/check?prerelease=false
func checkUpdate(w http.ResponseWriter, r *http.Request) {
	includePrerelease := r.URL.Query().Get("prerelease") == "true"

	manager := getUpdateManager()
	info, err := manager.CheckForUpdate(r.Context(), includePrerelease)

	resp := UpdateCheckResponse{
		CurrentVer: consts.AppVersion,
		IsDocker:   os.Getenv("IS_DOCKER") != "",
	}

	if err != nil {
		resp.Error = err.Error()
		writeJSON(w, resp)
		return
	}

	if info != nil {
		resp.Available = true
		resp.LatestInfo = info
	}

	writeJSON(w, resp)
}

// downloadUpdate 下载更新
// POST /api/update/download
func downloadUpdate(w http.ResponseWriter, r *http.Request) {
	manager := getUpdateManager()

	// 检查是否有可用更新
	if manager.GetAvailableInfo() == nil {
		writeJsonWithStatusCode(w, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "没有可用的更新，请先检查更新",
		})
		return
	}

	hub := GetSSEHub()

	// 在后台启动下载，设置进度回调并广播完成/失败事件
	bilisentry.Go(func() {
		ctx := context.Background() // 使用独立的 context，不受请求取消影响

		// 设置进度回调，与 AutoUpdater.doDownload 保持一致
		progressCh := make(chan update.DownloadProgress, 10)
		manager.SetProgressCallback(progressCh)

		// 启动进度监听并广播
		bilisentry.Go(func() {
			for progress := range progressCh {
				hub.BroadcastUpdateDownloading(map[string]interface{}{
					"downloaded_bytes": progress.DownloadedBytes,
					"total_bytes":      progress.TotalBytes,
					"speed":            progress.Speed,
					"percentage":       progress.Percentage,
				})
			}
		})

		err := manager.DownloadUpdate(ctx)
		close(progressCh)

		if err != nil {
			hub.BroadcastUpdateError(err)
			return
		}

		// 下载完成，广播 update_ready 事件
		info := manager.GetAvailableInfo()
		if info != nil {
			activeRecordings := getActiveRecordingsCount(context.Background())
			hub.BroadcastUpdateReady(map[string]interface{}{
				"version":           info.Version,
				"can_apply_now":     activeRecordings == 0,
				"active_recordings": activeRecordings,
			})
		}
	})

	writeJSON(w, commonResp{
		Data: "下载已开始",
	})
}

// getUpdateStatus 获取更新状态和下载进度
// GET /api/update/status
func getUpdateStatus(w http.ResponseWriter, r *http.Request) {
	manager := getUpdateManager()

	// 获取当前录制数量
	activeRecordings := getActiveRecordingsCount(r.Context())

	gracefulUpdateMu.RLock()
	pending := gracefulUpdatePending
	version := gracefulUpdateVersion
	gracefulUpdateMu.RUnlock()

	resp := UpdateStatusResponse{
		State:                 manager.GetState(),
		Progress:              manager.GetDownloadProgress(),
		Error:                 manager.GetLastError(),
		GracefulUpdatePending: pending,
		GracefulUpdateVersion: version,
		ActiveRecordingsCount: activeRecordings,
		CanApplyNow:           manager.GetState() == update.UpdateStateReady && activeRecordings == 0,
		AvailableInfo:         manager.GetAvailableInfo(),
	}

	writeJSON(w, resp)
}

// getActiveRecordingsCount 获取当前正在录制的直播间数量
func getActiveRecordingsCount(ctx context.Context) int {
	inst := instance.GetInstance(ctx)
	if inst == nil {
		return 0
	}

	rm, ok := inst.RecorderManager.(recorders.Manager)
	if !ok {
		return 0
	}

	return rm.GetActiveRecordingsCount()
}

// ApplyUpdateRequest 应用更新请求
type ApplyUpdateRequest struct {
	// GracefulWait 是否等待所有录制结束后再升级
	GracefulWait bool `json:"graceful_wait"`
	// ForceNow 是否立即强制升级（会中断录制）
	ForceNow bool `json:"force_now"`
}

// applyUpdate 应用更新
// POST /api/update/apply
// 请求体: {"graceful_wait": true} - 等待所有录制结束后升级
//
//	{"force_now": true} - 立即升级（中断录制）
func applyUpdate(w http.ResponseWriter, r *http.Request) {
	manager := getUpdateManager()

	// 检查更新是否已准备好
	if manager.GetState() != update.UpdateStateReady {
		writeJsonWithStatusCode(w, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "更新尚未准备好，请先下载更新",
		})
		return
	}

	// 解析请求参数
	var req ApplyUpdateRequest
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			json.Unmarshal(body, &req)
		}
	}

	activeRecordings := getActiveRecordingsCount(r.Context())
	info := manager.GetAvailableInfo()

	// 如果有正在录制的直播间
	if activeRecordings > 0 {
		if req.GracefulWait {
			// 设置优雅升级等待标志
			gracefulUpdateMu.Lock()
			gracefulUpdatePending = true
			if info != nil {
				gracefulUpdateVersion = info.Version
			}
			gracefulUpdateMu.Unlock()

			writeJSON(w, map[string]interface{}{
				"status":            "waiting",
				"message":           "已启用优雅升级模式，将在所有录制结束后自动升级",
				"active_recordings": activeRecordings,
				"pending_version":   gracefulUpdateVersion,
			})
			return
		}

		if !req.ForceNow {
			// 既不等待也不强制，返回提示
			writeJsonWithStatusCode(w, http.StatusConflict, map[string]interface{}{
				"err_no":            http.StatusConflict,
				"err_msg":           "有正在录制的直播间，请选择操作方式",
				"active_recordings": activeRecordings,
				"options": map[string]string{
					"graceful_wait": "等待所有录制结束后自动升级",
					"force_now":     "立即升级（会中断当前录制）",
				},
			})
			return
		}
		// force_now = true，继续执行升级
	}

	// 执行升级
	if err := doApplyUpdate(r.Context(), manager); err != nil {
		writeJsonWithStatusCode(w, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}

	writeJSON(w, commonResp{
		Data: "更新已应用，程序即将重启",
	})
}

// doApplyUpdate 执行更新应用
func doApplyUpdate(ctx context.Context, manager *update.Manager) error {
	// 清除优雅更新标志
	gracefulUpdateMu.Lock()
	gracefulUpdatePending = false
	gracefulUpdateVersion = ""
	gracefulUpdateMu.Unlock()

	// 如果连接到外部启动器，通过启动器升级（兼容旧模式）
	if manager.IsLauncherConnected() {
		return manager.ApplyUpdate(ctx)
	}

	// 使用自托管 launcher 模式：将更新解压到版本目录并更新状态文件
	if err := manager.ApplyUpdateSelfHosted(ctx); err != nil {
		return err
	}

	// 进程内热切换到 launcher 模式（所有环境通用，包括 Docker）
	// ApplyUpdateSelfHosted 已将新版本解压到 versions/ 目录并写入 launcher-state.json
	// 现在需要关闭所有 bgo 服务，然后在同一进程内进入 launcher 模式来运行新版本
	// 这样进程不会退出，Docker 容器也不会重启
	hub := GetSSEHub()
	hub.BroadcastUpdateReady(map[string]interface{}{
		"status":          "transitioning",
		"message":         "更新已应用，正在切换到新版本...",
		"current_version": consts.AppVersion,
	})

	// 设置待切换标志，然后触发服务关闭
	// main() 中 WaitGroup.Wait() 返回后会检查此标志，
	// 如果为 true 则在同一进程内进入 launcher 模式
	pendingLauncherTransition = true
	fmt.Fprintf(os.Stderr, "[doApplyUpdate] pendingLauncherTransition=true, 即将触发关闭\n")

	// 延迟触发关闭，确保 HTTP 响应先发送给前端
	bilisentry.Go(func() {
		time.Sleep(500 * time.Millisecond)
		if shutdownFunc != nil {
			fmt.Fprintf(os.Stderr, "[doApplyUpdate] 调用 shutdownFunc()\n")
			shutdownFunc()
		} else {
			fmt.Fprintf(os.Stderr, "[doApplyUpdate] shutdownFunc 为 nil！\n")
		}
	})

	return nil
}

// SetShutdownFunc 注册关闭回调函数
// 由主程序初始化时调用，传入触发优雅关闭的函数
func SetShutdownFunc(fn func()) {
	shutdownFunc = fn
}

// PendingLauncherTransition 检查是否有待执行的 launcher 模式切换
// main() 在所有服务关闭后调用此函数检查是否需要就地切换到 launcher 模式
func PendingLauncherTransition() bool {
	return pendingLauncherTransition
}

// CheckGracefulUpdate 检查并执行优雅更新（在录制结束时调用）
func CheckGracefulUpdate(ctx context.Context) bool {
	gracefulUpdateMu.RLock()
	pending := gracefulUpdatePending
	gracefulUpdateMu.RUnlock()

	if !pending {
		return false
	}

	// 检查是否还有活跃的录制
	if getActiveRecordingsCount(ctx) > 0 {
		return false
	}

	// 所有录制已结束，执行更新
	manager := getUpdateManager()
	if manager.GetState() == update.UpdateStateReady {
		bilisentry.Go(func() {
			doApplyUpdate(context.Background(), manager)
		})
		return true
	}

	return false
}

// IsGracefulUpdatePending 检查是否有等待中的优雅更新
func IsGracefulUpdatePending() bool {
	gracefulUpdateMu.RLock()
	defer gracefulUpdateMu.RUnlock()
	return gracefulUpdatePending
}

// cancelUpdate 取消下载或取消优雅更新等待
// POST /api/update/cancel
func cancelUpdate(w http.ResponseWriter, r *http.Request) {
	manager := getUpdateManager()
	manager.CancelDownload()

	// 同时取消优雅更新等待
	gracefulUpdateMu.Lock()
	gracefulUpdatePending = false
	gracefulUpdateVersion = ""
	gracefulUpdateMu.Unlock()

	writeJSON(w, commonResp{
		Data: "下载/等待已取消",
	})
}

// getLatestRelease 直接获取最新版本信息（不比较版本）
// GET /api/update/latest?prerelease=false
func getLatestRelease(w http.ResponseWriter, r *http.Request) {
	includePrerelease := r.URL.Query().Get("prerelease") == "true"

	checker := update.NewChecker(consts.AppVersion)
	info, err := checker.GetLatestRelease(includePrerelease)

	if err != nil {
		writeJsonWithStatusCode(w, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}

	writeJSON(w, info)
}

// setUpdateChannel 设置更新通道（稳定版/预发布版）
// PUT /api/update/channel
func setUpdateChannel(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(w, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var req struct {
		Channel string `json:"channel"` // "stable" or "prerelease"
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJsonWithStatusCode(w, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	// TODO: 存储到配置中
	writeJSON(w, commonResp{
		Data: "更新通道已设置为: " + req.Channel,
	})
}

// getLauncherStatus 获取启动器连接状态和更新环境信息
// GET /api/update/launcher
func getLauncherStatus(w http.ResponseWriter, r *http.Request) {
	manager := getUpdateManager()

	gracefulUpdateMu.RLock()
	pending := gracefulUpdatePending
	pendingVersion := gracefulUpdateVersion
	gracefulUpdateMu.RUnlock()

	appInfo := consts.GetAppInfo()

	resp := map[string]interface{}{
		"connected":               manager.IsLauncherConnected(),
		"launched_by":             os.Getenv("BILILIVE_LAUNCHER"),
		"is_docker":               os.Getenv("IS_DOCKER") != "",
		"update_available":        manager.GetAvailableInfo() != nil,
		"current_version":         consts.AppVersion,
		"graceful_update_pending": pending,
		"graceful_update_version": pendingVersion,
		"active_recordings":       getActiveRecordingsCount(r.Context()),
		// 启动器和 bgo 进程信息
		"is_launcher_managed": appInfo.IsLauncherManaged,
		"launcher_pid":        appInfo.LauncherPID,
		"launcher_exe_path":   appInfo.LauncherExePath,
		"bgo_pid":             appInfo.Pid,
		"bgo_exe_path":        appInfo.BgoExePath,
	}

	if info := manager.GetAvailableInfo(); info != nil {
		resp["available_version"] = info.Version
	}

	writeJSON(w, resp)
}

// getRollbackInfo 获取回滚信息
// GET /api/update/rollback
func getRollbackInfo(w http.ResponseWriter, r *http.Request) {
	cfg := configs.GetCurrentConfig()
	statePath := filepath.Join(cfg.AppDataPath, "launcher-state.json")

	resp := map[string]interface{}{
		"available":           false,
		"reason":              "",
		"current_version":     consts.AppVersion,
		"prefer_entry_binary": false,
	}

	// 读取 launcher-state.json
	state, err := launcher.LoadState(statePath)
	if err != nil {
		resp["reason"] = "无启动器状态文件，没有可回滚的版本"
		writeJSON(w, resp)
		return
	}

	resp["prefer_entry_binary"] = state.PreferEntryBinary

	// 检查是否有备份版本
	if state.BackupVersion == "" || state.BackupBinaryPath == "" {
		resp["reason"] = "没有备份版本可供回滚"
		writeJSON(w, resp)
		return
	}

	// 检查备份二进制文件是否存在
	backupPath := state.BackupBinaryPath
	if !filepath.IsAbs(backupPath) {
		backupPath = filepath.Join(cfg.AppDataPath, backupPath)
	}
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		resp["reason"] = "备份文件不存在: " + backupPath
		writeJSON(w, resp)
		return
	}

	resp["available"] = true
	resp["backup_version"] = state.BackupVersion
	resp["backup_binary_path"] = backupPath
	resp["active_version"] = state.ActiveVersion
	writeJSON(w, resp)
}

// doRollback 执行版本回滚（支持所有环境，包括 Docker）
// POST /api/update/rollback
func doRollback(w http.ResponseWriter, r *http.Request) {
	// 检查更新状态，防止在更新过程中并发执行回滚
	manager := getUpdateManager()
	if manager != nil {
		state := manager.GetState()
		if state == update.UpdateStateChecking || state == update.UpdateStateDownloading || state == update.UpdateStateApplying {
			writeJsonWithStatusCode(w, http.StatusConflict, commonResp{
				ErrNo:  http.StatusConflict,
				ErrMsg: fmt.Sprintf("无法在更新过程中执行回滚，当前状态: %s", state),
			})
			return
		}
	}

	cfg := configs.GetCurrentConfig()
	statePath := filepath.Join(cfg.AppDataPath, "launcher-state.json")

	// 读取当前状态
	state, err := launcher.LoadState(statePath)
	if err != nil {
		writeJsonWithStatusCode(w, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "读取启动器状态失败: " + err.Error(),
		})
		return
	}

	if state.BackupVersion == "" || state.BackupBinaryPath == "" {
		writeJsonWithStatusCode(w, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "没有备份版本可供回滚",
		})
		return
	}

	// 验证备份文件存在
	backupPath := state.BackupBinaryPath
	if !filepath.IsAbs(backupPath) {
		backupPath = filepath.Join(cfg.AppDataPath, backupPath)
	}
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		writeJsonWithStatusCode(w, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "备份文件不存在: " + backupPath,
		})
		return
	}

	// 交换活跃版本和备份版本
	oldActive := state.ActiveVersion
	oldActivePath := state.ActiveBinaryPath

	state.ActiveVersion = state.BackupVersion
	state.ActiveBinaryPath = state.BackupBinaryPath
	state.BackupVersion = oldActive
	state.BackupBinaryPath = oldActivePath
	state.PreferEntryBinary = false // 回滚到备份版本时清除入口二进制偏好
	state.LastUpdateTime = time.Now().Unix()
	state.FailureCount = 0

	// 保存新状态
	if err := state.Save(statePath); err != nil {
		writeJsonWithStatusCode(w, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "保存启动器状态失败: " + err.Error(),
		})
		return
	}

	fmt.Fprintf(os.Stderr, "[doRollback] 版本回滚: %s -> %s\n", oldActive, state.ActiveVersion)

	// 通知前端
	hub := GetSSEHub()
	hub.BroadcastUpdateReady(map[string]interface{}{
		"status":          "transitioning",
		"message":         fmt.Sprintf("正在切换到版本 %s...", state.ActiveVersion),
		"current_version": consts.AppVersion,
		"target_version":  state.ActiveVersion,
	})

	// 先返回成功响应
	writeJSON(w, map[string]interface{}{
		"status":         "switching",
		"message":        fmt.Sprintf("正在切换到版本 %s", state.ActiveVersion),
		"target_version": state.ActiveVersion,
	})

	// 设置待切换标志并延迟触发关闭（不停机切换）
	pendingLauncherTransition = true
	bilisentry.Go(func() {
		time.Sleep(500 * time.Millisecond)
		if shutdownFunc != nil {
			fmt.Fprintf(os.Stderr, "[doRollback] 触发不停机版本切换\n")
			shutdownFunc()
		}
	})
}
