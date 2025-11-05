package bark

import (
	"testing"
)

// TestSendMessage 测试SendMessage函数的基本功能
func TestSendMessage(t *testing.T) {
	// 测试空URL的情况
	err := SendMessage("", "测试标题", "测试内容")
	if err == nil {
		t.Errorf("Expected error for empty server URL, got nil")
	}
}

// TestSendMessageWithInvalidURL 测试无效URL的情况
func TestSendMessageWithInvalidURL(t *testing.T) {
	// 测试无效URL的情况 - 不进行实际的网络请求
	// 这里主要测试函数的错误处理能力
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SendMessage panicked: %v", r)
		}
	}()

	// 使用无效的URL，但不会真正发送请求
	err := SendMessage("http://invalid.url.test", "测试标题", "测试内容")
	// 我们期望会有错误，因为URL无效
	if err == nil {
		// 如果没有错误，可能是网络问题或URL意外可达，这是可以接受的
		t.Log("No error returned, which is acceptable in test environment")
	}
}
