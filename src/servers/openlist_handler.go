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
	Mode               string                 `json:"mode"`
	Endpoint           string                 `json:"endpoint,omitempty"`
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

// SetOpenListManager 设置全局 OpenList 管理器（保留兼容性，实际设置到 openlist 包）
func SetOpenListManager(m *openlist.Manager) {
	openlist.SetGlobalManager(m)
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
	mgr := openlist.GetGlobalManager()
	if mgr == nil {
		if config.OnRecordFinished.CloudUpload.Enable {
			response.Errors = append(response.Errors, "OpenList 管理器未初始化")
		}
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	// 检查 OpenList 是否运行
	response.OpenListRunning = mgr.IsRunning()
	response.Endpoint = mgr.GetAPIEndpoint()
	if mgr.IsExternal() {
		response.Mode = "external"
	} else {
		response.Mode = "local"
	}

	if !response.OpenListRunning {
		response.Errors = append(response.Errors, "OpenList 服务未运行")
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	// 使用凭据创建客户端
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	client, err := mgr.GetClient(ctx, config.OpenList.Token, config.OpenList.Username, config.OpenList.Password)
	if err != nil {
		response.Errors = append(response.Errors, "创建 OpenList 客户端失败: "+err.Error())
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

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

	mgr := openlist.GetGlobalManager()
	if mgr == nil || !mgr.IsRunning() {
		response.Message = "OpenList 服务未运行"
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	config := configs.GetCurrentConfig()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	client, err := mgr.GetClient(ctx, config.OpenList.Token, config.OpenList.Username, config.OpenList.Password)
	if err != nil {
		response.Message = "创建 OpenList 客户端失败: " + err.Error()
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	if err := client.CheckStorageHealth(ctx, storageName); err != nil {
		response.Message = err.Error()
	} else {
		response.Healthy = true
	}

	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(response)
}
