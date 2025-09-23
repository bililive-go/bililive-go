package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type TelegramMessage struct {
	ChatID              string `json:"chat_id"`
	Text                string `json:"text"`
	DisableNotification bool   `json:"disable_notification,omitempty"`
}

// SendMessage 发送Telegram消息
// withNotification参数控制是否发送带通知的消息
// true表示发送带提醒的消息，false表示发送静默消息
func SendMessage(token, chatID, message string, withNotification bool) error {
	// 确保token不包含"bot"前缀，因为URL中已经添加了
	token = strings.TrimPrefix(token, "bot")

	// 打印完整URL（仅用于调试，生产环境应移除）
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)

	msg := TelegramMessage{
		ChatID:              chatID,
		Text:                message,
		DisableNotification: !withNotification, // 取反：true表示带通知，false表示静默
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 读取响应体以获取更多错误信息
		var respBody bytes.Buffer
		_, err := respBody.ReadFrom(resp.Body)
		if err != nil {
			return fmt.Errorf("unexpected status code: %d, failed to read response body: %v", resp.StatusCode, err)
		}
		return fmt.Errorf("unexpected status code: %d, response: %s", resp.StatusCode, respBody.String())
	}

	return nil
}
