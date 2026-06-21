package servers

import (
	"encoding/json"
	"net/http"

	"github.com/bililive-go/bililive-go/src/tools"
)

// getFFmpegStatusHandler 返回当前 FFmpeg 就绪状态
// GET /api/ffmpeg/status
func getFFmpegStatusHandler(w http.ResponseWriter, r *http.Request) {
	status := tools.GetFFmpegStatus()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
