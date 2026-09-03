package live

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/types"
)

type infoMarshalLive struct{ Live }

func (l *infoMarshalLive) GetLiveId() types.LiveID     { return "marshal-test" }
func (l *infoMarshalLive) GetRawUrl() string           { return "https://example.com/room" }
func (l *infoMarshalLive) GetPlatformCNName() string   { return "测试平台" }
func (l *infoMarshalLive) GetLastStartTime() time.Time { return time.Time{} }
func (l *infoMarshalLive) GetOptions() *Options        { return &Options{} }

func TestInfoMarshalJSONIncludesRecordingError(t *testing.T) {
	data, err := json.Marshal(&Info{
		Live:           &infoMarshalLive{},
		RecordingError: "输出文件完整路径过长",
	})
	if err != nil {
		t.Fatalf("序列化直播信息失败: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("解析直播信息失败: %v", err)
	}
	if response["recording_error"] != "输出文件完整路径过长" {
		t.Fatalf("响应未包含录制错误: %#v", response)
	}
}
