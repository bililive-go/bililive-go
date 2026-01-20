package servers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/bililive-go/bililive-go/src/pipeline"
)

// RegisterPipelineHandlers 注册 Pipeline 任务管理相关的 HTTP 处理器
// 注意：r 已经是 /api 前缀的子路由器
func RegisterPipelineHandlers(r *mux.Router, pm *pipeline.Manager) {
	if pm == nil {
		return
	}

	// 获取 Pipeline 任务列表
	r.HandleFunc("/pipeline/tasks", makePipelineListTasksHandler(pm)).Methods("GET")

	// 获取队列统计
	r.HandleFunc("/pipeline/tasks/stats", makePipelineGetStatsHandler(pm)).Methods("GET")

	// 清除已完成的任务
	r.HandleFunc("/pipeline/tasks/clear-completed", makePipelineClearCompletedHandler(pm)).Methods("POST")

	// 获取单个任务
	r.HandleFunc("/pipeline/tasks/{id}", makePipelineGetTaskHandler(pm)).Methods("GET")

	// 取消任务
	r.HandleFunc("/pipeline/tasks/{id}/cancel", makePipelineCancelTaskHandler(pm)).Methods("POST")

	// 重试任务
	r.HandleFunc("/pipeline/tasks/{id}/retry", makePipelineRetryTaskHandler(pm)).Methods("POST")

	// 删除任务
	r.HandleFunc("/pipeline/tasks/{id}", makePipelineDeleteTaskHandler(pm)).Methods("DELETE")
}

// makePipelineListTasksHandler 列出 Pipeline 任务
func makePipelineListTasksHandler(pm *pipeline.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := pipeline.TaskFilter{}

		// 解析查询参数
		if status := r.URL.Query().Get("status"); status != "" {
			s := pipeline.PipelineStatus(status)
			filter.Status = &s
		}
		if liveID := r.URL.Query().Get("live_id"); liveID != "" {
			filter.LiveID = &liveID
		}
		if limit := r.URL.Query().Get("limit"); limit != "" {
			if l, err := strconv.Atoi(limit); err == nil {
				filter.Limit = l
			}
		}
		if offset := r.URL.Query().Get("offset"); offset != "" {
			if o, err := strconv.Atoi(offset); err == nil {
				filter.Offset = o
			}
		}

		tasks, err := pm.ListTasks(filter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// 确保返回空数组而不是 null
		if tasks == nil {
			tasks = []*pipeline.PipelineTask{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tasks)
	}
}

// makePipelineGetStatsHandler 获取队列统计
func makePipelineGetStatsHandler(pm *pipeline.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := pm.GetStats()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

// makePipelineClearCompletedHandler 清除已完成的任务
func makePipelineClearCompletedHandler(pm *pipeline.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count, err := pm.ClearCompletedTasks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"deleted": count,
		})
	}
}

// makePipelineGetTaskHandler 获取单个任务
func makePipelineGetTaskHandler(pm *pipeline.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "invalid task id", http.StatusBadRequest)
			return
		}

		task, err := pm.GetTask(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if task == nil {
			http.Error(w, "task not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)
	}
}

// makePipelineCancelTaskHandler 取消任务
func makePipelineCancelTaskHandler(pm *pipeline.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "invalid task id", http.StatusBadRequest)
			return
		}

		if err := pm.CancelTask(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	}
}

// makePipelineRetryTaskHandler 重试任务
func makePipelineRetryTaskHandler(pm *pipeline.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "invalid task id", http.StatusBadRequest)
			return
		}

		if err := pm.RetryTask(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "retried"})
	}
}

// makePipelineDeleteTaskHandler 删除任务
func makePipelineDeleteTaskHandler(pm *pipeline.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "invalid task id", http.StatusBadRequest)
			return
		}

		if err := pm.DeleteTask(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	}
}
