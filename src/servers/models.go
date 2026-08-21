package servers

import (
	"time"

	"github.com/bililive-go/bililive-go/src/live"
)

type commonResp struct {
	ErrNo  int    `json:"err_no"`
	ErrMsg string `json:"err_msg"`
	Data   any    `json:"data"`
}

type liveSlice []*live.Info

func (c liveSlice) Len() int {
	return len(c)
}
func (c liveSlice) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}
func (c liveSlice) Less(i, j int) bool {
	if c[i].Pinned != c[j].Pinned {
		return c[i].Pinned
	}
	return c[i].Live.GetLiveId() < c[j].Live.GetLiveId()
}

// batchAddRequest 批量添加直播间请求
type batchAddRequest struct {
	URLs       []string `json:"urls"`
	Listen     bool     `json:"listen"`
	NotifyOnly bool     `json:"notify_only,omitempty"`
	BatchID    string   `json:"batch_id,omitempty"`
}

// batchAddResponse 批量添加直播间响应（立即返回）
type batchAddResponse struct {
	BatchID string `json:"batch_id"`
	Total   int    `json:"total"`
}

// batchProgressEvent SSE 进度事件数据
type batchProgressEvent struct {
	Index   int        `json:"index"`
	Total   int        `json:"total"`
	URL     string     `json:"url"`
	Success bool       `json:"success"`
	Error   string     `json:"error,omitempty"`
	Info    *live.Info `json:"live_info,omitempty"`
}

// batchCompleteEvent SSE 完成事件数据
type batchCompleteEvent struct {
	Total        int       `json:"total"`
	SuccessCount int       `json:"success_count"`
	FailCount    int       `json:"fail_count"`
	PersistError string    `json:"persist_error,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}
