//go:build dev

package servers

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/bililive-go/bililive-go/src/tools"
)

func registerDevDebugRoutes(apiRoute *mux.Router) {
	// POST /api/debug/ffmpeg/force-state — 强制设置 FFmpeg 状态，仅用于 e2e 测试
	apiRoute.HandleFunc("/debug/ffmpeg/force-state", forceFFmpegStateHandler).Methods("POST")
}

func forceFFmpegStateHandler(w http.ResponseWriter, r *http.Request) {
	var req tools.FFmpegStatus
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	tools.ForceFFmpegStatus(req)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true}`))
}
