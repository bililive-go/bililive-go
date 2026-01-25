package servers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/pkg/openlist"
)

// OpenListStatusResponse OpenList 状态响应
type OpenListStatusResponse struct {
	OpenListRunning    bool                   `json:"openlist_running"`
	WebUIPath          string                 `json:"web_ui_path"`
	Storages           []openlist.StorageInfo `json:"storages"`
	Errors             []string               `json:"errors"`
	CloudUploadEnabled bool                   `json:"cloud_upload_enabled"`
}

// OpenListStorageHealthResponse 存储健康检查响应
type OpenListStorageHealthResponse struct {
	Healthy bool   `json:"healthy"`
	Message string `json:"message,omitempty"`
}

// 全局 OpenList 管理器引用（由 main 设置）
var globalOpenListManager *openlist.Manager

// SetOpenListManager 设置全局 OpenList 管理器
func SetOpenListManager(m *openlist.Manager) {
	globalOpenListManager = m
}

// getOpenListStatus 获取 OpenList 状态
func getOpenListStatus(writer http.ResponseWriter, r *http.Request) {
	config := configs.GetCurrentConfig()

	response := OpenListStatusResponse{
		CloudUploadEnabled: config.OnRecordFinished.CloudUpload.Enable,
		WebUIPath:          "/remotetools/tool/openlist/",
		Storages:           []openlist.StorageInfo{},
		Errors:             []string{},
	}

	// 检查 OpenList 管理器是否存在
	if globalOpenListManager == nil {
		if config.OnRecordFinished.CloudUpload.Enable {
			response.Errors = append(response.Errors, "OpenList 管理器未初始化")
		}
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	// 检查 OpenList 是否运行
	response.OpenListRunning = globalOpenListManager.IsRunning()

	if !response.OpenListRunning {
		response.Errors = append(response.Errors, "OpenList 服务未运行")
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	// 尝试获取存储列表
	client := openlist.NewClient(globalOpenListManager.GetAPIEndpoint(), "")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	storages, err := client.ListStorages(ctx)
	if err != nil {
		response.Errors = append(response.Errors, "无法获取存储列表: "+err.Error())
	} else {
		response.Storages = storages
		if len(storages) == 0 {
			response.Errors = append(response.Errors, "未配置任何存储，请在 OpenList 中添加网盘")
		}
	}

	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(response)
}

// checkOpenListStorageHealth 检查存储健康状态
func checkOpenListStorageHealth(writer http.ResponseWriter, r *http.Request) {
	storageName := r.URL.Query().Get("name")
	if storageName == "" {
		http.Error(writer, "缺少 name 参数", http.StatusBadRequest)
		return
	}

	response := OpenListStorageHealthResponse{
		Healthy: false,
	}

	if globalOpenListManager == nil || !globalOpenListManager.IsRunning() {
		response.Message = "OpenList 服务未运行"
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	client := openlist.NewClient(globalOpenListManager.GetAPIEndpoint(), "")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := client.CheckStorageHealth(ctx, storageName); err != nil {
		response.Message = err.Error()
	} else {
		response.Healthy = true
	}

	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(response)
}
