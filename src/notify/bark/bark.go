package bark

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// BarkMessage Bark 推送消息体
type BarkMessage struct {
	DeviceKey string `json:"device_key"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Sound     string `json:"sound,omitempty"`
	Icon      string `json:"icon,omitempty"`
	Group     string `json:"group,omitempty"`
	URL       string `json:"url,omitempty"`
	Level     string `json:"level,omitempty"`
	IsArchive int    `json:"isArchive,omitempty"`
}

// BarkResponse Bark API 响应
type BarkResponse struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// SendMessage 发送 Bark 开始直播通知
func SendMessage(serverURL, deviceKey, sound, group, icon, level, hostName, platform, liveURL string) error {
	title := fmt.Sprintf("%s 开始直播", hostName)
	body := fmt.Sprintf("平台：%s\n正在录制中", platform)
	return sendRequest(serverURL, deviceKey, title, body, sound, group, icon, level, liveURL)
}

// SendStopMessage 发送 Bark 停止直播通知
func SendStopMessage(serverURL, deviceKey, sound, group, icon, level, hostName, platform, liveURL string) error {
	title := fmt.Sprintf("%s 直播结束", hostName)
	body := fmt.Sprintf("平台：%s\n录制已停止", platform)
	return sendRequest(serverURL, deviceKey, title, body, sound, group, icon, level, liveURL)
}

// SendSummaryMessage 发送 Bark 录制摘要通知
func SendSummaryMessage(serverURL, deviceKey, sound, group, icon, level, title, body string) error {
	return sendRequest(serverURL, deviceKey, title, body, sound, group, icon, level, "")
}

// sendRequest 发送 Bark HTTP 请求
func sendRequest(serverURL, deviceKey, title, body, sound, group, icon, level, liveURL string) error {
	serverURL = strings.TrimRight(serverURL, "/")

	msg := BarkMessage{
		DeviceKey: deviceKey,
		Title:     title,
		Body:      body,
		Sound:     sound,
		Icon:      icon,
		Group:     group,
		Level:     level,
		IsArchive: 1,
	}

	if liveURL != "" {
		fullURL := liveURL
		if !strings.HasPrefix(liveURL, "http://") && !strings.HasPrefix(liveURL, "https://") {
			fullURL = "https://" + liveURL
		}
		msg.URL = fullURL
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal bark message: %w", err)
	}

	url := serverURL + "/push"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send bark request: %w", err)
	}
	defer resp.Body.Close()

	var barkResp BarkResponse
	if err := json.NewDecoder(resp.Body).Decode(&barkResp); err != nil {
		return fmt.Errorf("failed to decode bark response: %w", err)
	}

	if barkResp.Code != 200 {
		return fmt.Errorf("bark push failed: code=%d, message=%s", barkResp.Code, barkResp.Message)
	}

	return nil
}
