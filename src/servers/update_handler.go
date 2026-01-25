package servers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/update"
	"github.com/bililive-go/bililive-go/src/recorders"
)

// 全局更新管理器
var (
	updateManager     *update.Manager
	updateManagerOnce sync.Once

	// 优雅更新相关状态
	gracefulUpdateMu      sync.RWMutex
	gracefulUpdatePending bool   // 是否有等待中的优雅更新
	gracefulUpdateVersion string // 等待更新的版本号
)

// getUpdateManager 获取或初始化更新管理器
func getUpdateManager() *update.Manager {
	updateManagerOnce.Do(func() {
		cfg := configs.GetCurrentConfig()
		downloadDir := filepath.Join(cfg.AppDataPath, "updates")

		updateManager = update.NewManager(update.ManagerConfig{
			CurrentVersion: consts.AppVersion,
			DownloadDir:    downloadDir,
			InstanceID:     "",
		})
	})
	return updateManager
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

	// 在后台启动下载（错误会存储在 manager 中，可通过 status API 获取）
	bilisentry.Go(func() {
		ctx := context.Background() // 使用独立的 context，不受请求取消影响
		_ = manager.DownloadUpdate(ctx)
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

	count := 0
	for _, l := range inst.Lives {
		if rm.HasRecorder(ctx, l.GetLiveId()) {
			count++
		}
	}
	return count
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

	// 如果连接到启动器，通过启动器升级
	if manager.IsLauncherConnected() {
		return manager.ApplyUpdate(ctx)
	}

	// 对于 Docker 或直接运行的情况，程序需要退出让外部重启
	// 这里只是发出信号，由主程序处理退出逻辑
	// TODO: 实现进程自我重启逻辑
	return manager.ApplyUpdate(ctx)
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

	resp := map[string]interface{}{
		"connected":               manager.IsLauncherConnected(),
		"launched_by":             os.Getenv("BILILIVE_LAUNCHER"),
		"is_docker":               os.Getenv("IS_DOCKER") != "",
		"update_available":        manager.GetAvailableInfo() != nil,
		"current_version":         consts.AppVersion,
		"graceful_update_pending": pending,
		"graceful_update_version": pendingVersion,
		"active_recordings":       getActiveRecordingsCount(r.Context()),
	}

	if info := manager.GetAvailableInfo(); info != nil {
		resp["available_version"] = info.Version
	}

	writeJSON(w, resp)
}
