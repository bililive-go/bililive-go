package ntfy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 创建一个共享的HTTP客户端，设置合理的超时时间
var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// NtfyAction 定义ntfy的Action结构
type NtfyAction struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url"`
}

// sendNtfyRequest 发送ntfy请求的通用函数
func sendNtfyRequest(url, token, tag, hostname, message, liveURL string) error {
	// 创建HTTP请求
	req, err := http.NewRequest("POST", url, strings.NewReader(message))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// 设置必要的请求头
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Title", hostname)
	req.Header.Set("Tags", tag)

	// 设置Actions头，用于打开直播间
	if liveURL != "" {
		// 确保liveURL有https://前缀
		fullURL := liveURL
		if !strings.HasPrefix(liveURL, "http://") && !strings.HasPrefix(liveURL, "https://") {
			fullURL = "https://" + liveURL
		}

		// 使用结构体和json.Marshal安全地构造JSON
		actions := []NtfyAction{
			{
				Action: "view",
				Label:  "打开直播间",
				URL:    fullURL,
			},
		}

		actionsJSON, err := json.Marshal(actions)
		if err != nil {
			return fmt.Errorf("failed to marshal actions: %w", err)
		}

		req.Header.Set("Actions", string(actionsJSON))
	}

	// 如果提供了token，则添加Authorization头
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// 发送请求
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// SendMessage 发送ntfy消息
func SendMessage(url, token, tag, hostname, message, liveURL string) error {
	return sendNtfyRequest(url, token, tag, hostname, message, liveURL)
}
