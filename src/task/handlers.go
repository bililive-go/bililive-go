package task

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

// RegisterHandlers 注册任务队列相关的 HTTP 处理器
// 注意：r 已经是 /api 前缀的子路由器，所以这里不需要再加 /api 前缀
func RegisterHandlers(r *mux.Router, qm *QueueManager) {
	// 获取任务列表
	r.HandleFunc("/tasks", makeListTasksHandler(qm)).Methods("GET")

	// 获取队列统计
	r.HandleFunc("/tasks/stats", makeGetStatsHandler(qm)).Methods("GET")

	// 清除已完成的任务
	r.HandleFunc("/tasks/clear-completed", makeClearCompletedHandler(qm)).Methods("POST")

	// 获取单个任务
	r.HandleFunc("/tasks/{id}", makeGetTaskHandler(qm)).Methods("GET")

	// 取消任务
	r.HandleFunc("/tasks/{id}/cancel", makeCancelTaskHandler(qm)).Methods("POST")

	// 重新排队
	r.HandleFunc("/tasks/{id}/requeue", makeRequeueTaskHandler(qm)).Methods("POST")

	// 更新优先级
	r.HandleFunc("/tasks/{id}/priority", makeUpdatePriorityHandler(qm)).Methods("PUT")

	// 删除任务
	r.HandleFunc("/tasks/{id}", makeDeleteTaskHandler(qm)).Methods("DELETE")
}

// makeListTasksHandler 列出任务
func makeListTasksHandler(qm *QueueManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := TaskFilter{}

		// 解析查询参数
		if status := r.URL.Query().Get("status"); status != "" {
			s := TaskStatus(status)
			filter.Status = &s
		}
		if taskType := r.URL.Query().Get("type"); taskType != "" {
			t := TaskType(taskType)
			filter.Type = &t
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

		tasks, err := qm.ListTasks(filter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// 确保返回空数组而不是 null
		if tasks == nil {
			tasks = []*Task{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tasks)
	}
}

// makeGetStatsHandler 获取队列统计
func makeGetStatsHandler(qm *QueueManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := qm.GetStats()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

// makeClearCompletedHandler 清除已完成的任务
func makeClearCompletedHandler(qm *QueueManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count, err := qm.ClearCompletedTasks()
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

// makeGetTaskHandler 获取单个任务
func makeGetTaskHandler(qm *QueueManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "invalid task id", http.StatusBadRequest)
			return
		}

		task, err := qm.GetTask(id)
		if err != nil {
			if err == ErrTaskNotFound {
				http.Error(w, "task not found", http.StatusNotFound)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)
	}
}

// makeCancelTaskHandler 取消任务
func makeCancelTaskHandler(qm *QueueManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "invalid task id", http.StatusBadRequest)
			return
		}

		if err := qm.CancelTask(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	}
}

// makeRequeueTaskHandler 重新排队
func makeRequeueTaskHandler(qm *QueueManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "invalid task id", http.StatusBadRequest)
			return
		}

		if err := qm.RequeueTask(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "requeued"})
	}
}

// makeUpdatePriorityHandler 更新优先级
func makeUpdatePriorityHandler(qm *QueueManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "invalid task id", http.StatusBadRequest)
			return
		}

		var req struct {
			Priority int `json:"priority"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if err := qm.UpdatePriority(id, req.Priority); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	}
}

// makeDeleteTaskHandler 删除任务
func makeDeleteTaskHandler(qm *QueueManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "invalid task id", http.StatusBadRequest)
			return
		}

		if err := qm.DeleteTask(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	}
}
