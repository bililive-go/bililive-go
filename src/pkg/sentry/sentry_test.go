package sentry

import (
	"testing"

	sentrysdk "github.com/getsentry/sentry-go"
)

func TestBeforeSendHookRemovesDeploymentAndRoomPII(t *testing.T) {
	event := &sentrysdk.Event{
		Message:    "failed room https://live.douyin.com/123456?token=secret",
		ServerName: "users-private-hostname",
		User: sentrysdk.User{
			ID:        "anonymous-device-id",
			Email:     "person@example.com",
			IPAddress: "192.0.2.1",
			Username:  "local-user",
			Name:      "Local User",
			Data:      map[string]string{"home": "/home/local-user"},
		},
		Tags: map[string]string{
			"server_name": "users-private-hostname",
			"room":        "https://live.douyin.com/123456",
		},
		Exception: []sentrysdk.Exception{{
			Value: "request https://www.douyu.com/987654 failed",
		}},
		Breadcrumbs: []*sentrysdk.Breadcrumb{{
			Message: "opening https://example.com/private-room",
			Data:    map[string]interface{}{"url": "https://example.com/private-room"},
		}},
		Request: &sentrysdk.Request{
			URL:         "https://localhost/api/room/123?unknown_private_value=yes",
			QueryString: "unknown_private_value=yes",
			Headers: map[string]string{
				"AUTHORIZATION": "Bearer secret",
				"X-Real-IP":     "192.0.2.1",
			},
		},
	}

	result := beforeSendHook(event, nil)
	if result.ServerName != "" {
		t.Errorf("server name 未清除: %q", result.ServerName)
	}
	if result.User.ID != "anonymous-device-id" || result.User.Email != "" || result.User.IPAddress != "" ||
		result.User.Username != "" || result.User.Name != "" || result.User.Data != nil {
		t.Errorf("用户字段未按预期仅保留匿名 ID: %#v", result.User)
	}
	if _, ok := result.Tags["server_name"]; ok {
		t.Error("server_name tag 未清除")
	}
	assertDoesNotContainURL(t, result.Message)
	assertDoesNotContainURL(t, result.Exception[0].Value)
	assertDoesNotContainURL(t, result.Tags["room"])
	assertDoesNotContainURL(t, result.Breadcrumbs[0].Message)
	assertDoesNotContainURL(t, result.Breadcrumbs[0].Data["url"].(string))
	assertDoesNotContainURL(t, result.Request.URL)
	if result.Request.QueryString != "[REDACTED]" {
		t.Errorf("查询字符串未整体清理: %q", result.Request.QueryString)
	}
	if result.Request.Headers["AUTHORIZATION"] != "[REDACTED]" || result.Request.Headers["X-Real-IP"] != "[REDACTED]" {
		t.Errorf("大小写不规则的敏感请求头未清理: %#v", result.Request.Headers)
	}
}

func TestBeforeSendHookMarksRecoveredPanicHandled(t *testing.T) {
	unhandled := false
	event := &sentrysdk.Event{
		Level: sentrysdk.LevelFatal,
		Exception: []sentrysdk.Exception{{
			Mechanism: &sentrysdk.Mechanism{Type: "generic", Handled: &unhandled},
		}},
	}

	result := beforeSendHook(event, &sentrysdk.EventHint{RecoveredException: "panic"})
	if result.Level != sentrysdk.LevelError {
		t.Errorf("已恢复 panic 应标记为 error，实际 %q", result.Level)
	}
	if result.Tags["panic_handled"] != "true" {
		t.Errorf("缺少 panic_handled tag: %#v", result.Tags)
	}
	if result.Exception[0].Mechanism.Handled == nil || !*result.Exception[0].Mechanism.Handled {
		t.Error("已恢复 panic 的 mechanism 应标记为 handled")
	}
}

func assertDoesNotContainURL(t *testing.T, value string) {
	t.Helper()
	if fullURLPattern.MatchString(value) {
		t.Errorf("字符串仍包含完整 URL: %q", value)
	}
}
