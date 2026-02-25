package bark

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSendRequest_Success(t *testing.T) {
	var received BarkMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/push", r.URL.Path)
		assert.Equal(t, "application/json; charset=utf-8", r.Header.Get("Content-Type"))
		json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(BarkResponse{Code: 200, Message: "success"})
	}))
	defer server.Close()

	err := SendMessage(server.URL, "test_key", "alarm", "test-group", "", "", "主播A", "bilibili", "https://live.bilibili.com/123")
	assert.NoError(t, err)
	assert.Equal(t, "test_key", received.DeviceKey)
	assert.Equal(t, "主播A 开始直播", received.Title)
	assert.Equal(t, "平台：bilibili\n正在录制中", received.Body)
	assert.Equal(t, "alarm", received.Sound)
	assert.Equal(t, "test-group", received.Group)
	assert.Equal(t, "https://live.bilibili.com/123", received.URL)
	assert.Equal(t, 1, received.IsArchive)
}

func TestSendStopMessage_Format(t *testing.T) {
	var received BarkMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		json.NewEncoder(w).Encode(BarkResponse{Code: 200, Message: "success"})
	}))
	defer server.Close()

	err := SendStopMessage(server.URL, "test_key", "", "", "", "", "主播B", "douyin", "https://live.douyin.com/789")
	assert.NoError(t, err)
	assert.Equal(t, "主播B 直播结束", received.Title)
	assert.Equal(t, "平台：douyin\n录制已停止", received.Body)
}

func TestSendRequest_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(BarkResponse{Code: 400, Message: "bad request"})
	}))
	defer server.Close()

	err := SendMessage(server.URL, "key", "", "", "", "", "host", "platform", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bark push failed")
	assert.Contains(t, err.Error(), "code=400")
}

func TestSendRequest_NetworkError(t *testing.T) {
	err := SendMessage("http://127.0.0.1:1", "key", "", "", "", "", "host", "platform", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send bark request")
}

func TestSendRequest_URLNormalization(t *testing.T) {
	var requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		json.NewEncoder(w).Encode(BarkResponse{Code: 200, Message: "success"})
	}))
	defer server.Close()

	// serverURL 末尾带斜杠
	err := SendMessage(server.URL+"/", "key", "", "", "", "", "host", "platform", "")
	assert.NoError(t, err)
	assert.Equal(t, "/push", requestPath)
}

func TestSendRequest_LiveURLPrefix(t *testing.T) {
	var received BarkMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		json.NewEncoder(w).Encode(BarkResponse{Code: 200, Message: "success"})
	}))
	defer server.Close()

	// 无 http 前缀的 liveURL 应自动补全 https://
	err := SendMessage(server.URL, "key", "", "", "", "", "host", "platform", "live.bilibili.com/123")
	assert.NoError(t, err)
	assert.Equal(t, "https://live.bilibili.com/123", received.URL)

	// 已有 http:// 前缀不应重复添加
	err = SendMessage(server.URL, "key", "", "", "", "", "host", "platform", "http://live.bilibili.com/123")
	assert.NoError(t, err)
	assert.Equal(t, "http://live.bilibili.com/123", received.URL)
}

func TestSendRequest_OptionalFieldsOmitted(t *testing.T) {
	var rawJSON map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&rawJSON)
		json.NewEncoder(w).Encode(BarkResponse{Code: 200, Message: "success"})
	}))
	defer server.Close()

	// sound/icon/group/level/url 全部为空
	err := SendMessage(server.URL, "key", "", "", "", "", "host", "platform", "")
	assert.NoError(t, err)
	// omitempty 字段不应出现在 JSON 中
	_, hasSound := rawJSON["sound"]
	_, hasIcon := rawJSON["icon"]
	_, hasGroup := rawJSON["group"]
	_, hasLevel := rawJSON["level"]
	_, hasURL := rawJSON["url"]
	assert.False(t, hasSound, "sound should be omitted when empty")
	assert.False(t, hasIcon, "icon should be omitted when empty")
	assert.False(t, hasGroup, "group should be omitted when empty")
	assert.False(t, hasLevel, "level should be omitted when empty")
	assert.False(t, hasURL, "url should be omitted when empty")
}
