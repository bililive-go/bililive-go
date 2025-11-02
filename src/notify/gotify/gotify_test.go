package gotify

import (
	"strings"
	"testing"
)

// TestGotifyMessageStructure 测试Gotify消息结构体
func TestGotifyMessageStructure(t *testing.T) {
	msg := GotifyMessage{
		Title:    "测试标题",
		Message:  "测试内容",
		Priority: 5,
	}

	if msg.Title != "测试标题" {
		t.Errorf("Expected title '测试标题', got '%s'", msg.Title)
	}
	if msg.Message != "测试内容" {
		t.Errorf("Expected message '测试内容', got '%s'", msg.Message)
	}
	if msg.Priority != 5 {
		t.Errorf("Expected priority 5, got %d", msg.Priority)
	}
}

// TestSendMessageInvalidURL 测试发送消息到无效URL
func TestSendMessageInvalidURL(t *testing.T) {
	// 测试发送消息到无效URL应该返回错误
	err := SendMessage("http://invalid-url-that-does-not-exist-12345.com", "test-token", "测试标题", "测试内容", 5)
	if err == nil {
		t.Error("Expected error when sending to invalid URL, got nil")
	}
}

// TestSendMessageEmptyServerURL 测试空服务器URL
func TestSendMessageEmptyServerURL(t *testing.T) {
	// 测试空服务器URL应该返回错误
	err := SendMessage("", "test-token", "测试标题", "测试内容", 5)
	if err == nil {
		t.Error("Expected error when server URL is empty, got nil")
	}
}

// TestSendMessageEmptyToken 测试空Token
func TestSendMessageEmptyToken(t *testing.T) {
	// 测试空Token，虽然会发送请求，但服务器应该拒绝
	err := SendMessage("http://localhost:8080", "", "测试标题", "测试内容", 5)
	// 这里不检查err是否为nil，因为可能连接失败或服务器拒绝
	// 主要确保函数不会panic
	_ = err
}

// TestSendMessageInvalidScheme 测试无效的URL协议
func TestSendMessageInvalidScheme(t *testing.T) {
	// 测试使用非HTTP/HTTPS协议应该返回错误
	err := SendMessage("ftp://example.com", "test-token", "测试标题", "测试内容", 5)
	if err == nil {
		t.Error("Expected error when using invalid URL scheme, got nil")
	}
	if !strings.Contains(err.Error(), "invalid URL scheme") {
		t.Errorf("Expected 'invalid URL scheme' error, got: %v", err)
	}
}

// TestSendMessageMalformedURL 测试格式错误的URL
func TestSendMessageMalformedURL(t *testing.T) {
	// 测试格式错误的URL应该返回错误
	err := SendMessage("://invalid", "test-token", "测试标题", "测试内容", 5)
	if err == nil {
		t.Error("Expected error when using malformed URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid server URL") {
		t.Errorf("Expected 'invalid server URL' error, got: %v", err)
	}
}
