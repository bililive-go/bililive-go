package bark

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

type BarkMessage struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body"`
}

// SendMessage 发送Bark消息
// serverURL: Bark服务器地址，例如 "https://api.day.app/your_device_key"
// title: 通知标题
// body: 通知内容
func SendMessage(serverURL, title, body string) error {
	if serverURL == "" {
		return fmt.Errorf("bark server URL is empty")
	}

	msg := BarkMessage{
		Title: title,
		Body:  body,
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}

	req, err := http.NewRequest("POST", serverURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var respBody bytes.Buffer
		_, err := respBody.ReadFrom(resp.Body)
		if err != nil {
			return fmt.Errorf("unexpected status code: %d, failed to read response body: %v", resp.StatusCode, err)
		}
		return fmt.Errorf("unexpected status code: %d, response: %s", resp.StatusCode, respBody.String())
	}

	return nil
}
