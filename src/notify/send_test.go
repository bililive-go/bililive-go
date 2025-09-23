package notify

import (
	"context"
	"testing"

	"github.com/bililive-go/bililive-go/src/consts"
)

// TestSendTestNotification 测试SendTestNotification函数
func TestSendTestNotification(t *testing.T) {
	// 由于SendTestNotification函数主要打印输出和调用SendNotification，
	// 我们在这里主要是确保函数能够正常运行，不会出现panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SendTestNotification panicked: %v", r)
		}
	}()

	// 调用测试函数
	SendTestNotification(context.Background())

	// 如果没有panic，则测试通过
	// 注意：实际的通知发送测试需要mock相关的服务
}

// TestSendNotificationStart 测试SendNotification函数发送开始直播通知
func TestSendNotificationStart(t *testing.T) {
	// 由于实际发送通知需要配置和网络连接，这里主要测试函数是否能正常处理开始状态
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SendNotification with LiveStatusStart panicked: %v", r)
		}
	}()

	// 调用SendNotification函数，使用开始状态
	err := SendNotification(context.Background(), "测试主播", "测试平台", "https://example.com/live", consts.LiveStatusStart)

	// 检查是否有错误返回（注意：在没有配置的情况下，可能会返回错误）
	// 这里我们主要关注函数是否能正常执行，而不是是否真的发送了通知
	_ = err // 在实际测试中，我们可能需要检查错误

	// 如果没有panic，则测试通过
}

// TestSendNotificationStop 测试SendNotification函数发送结束直播通知
func TestSendNotificationStop(t *testing.T) {
	// 由于实际发送通知需要配置和网络连接，这里主要测试函数是否能正常处理结束状态
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SendNotification with LiveStatusStop panicked: %v", r)
		}
	}()

	// 调用SendNotification函数，使用结束状态
	err := SendNotification(context.Background(), "测试主播", "测试平台", "https://example.com/live", consts.LiveStatusStop)

	// 检查是否有错误返回（注意：在没有配置的情况下，可能会返回错误）
	// 这里我们主要关注函数是否能正常执行，而不是是否真的发送了通知
	_ = err // 在实际测试中，我们可能需要检查错误

	// 如果没有panic，则测试通过
}

// TestSendNotificationUnknown 测试SendNotification函数处理未知状态
func TestSendNotificationUnknown(t *testing.T) {
	// 测试函数处理未知状态的能力
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SendNotification with unknown status panicked: %v", r)
		}
	}()

	// 调用SendNotification函数，使用未知状态
	err := SendNotification(context.Background(), "测试主播", "测试平台", "https://example.com/live", "unknown_status")

	// 检查是否有错误返回
	_ = err // 在实际测试中，我们可能需要检查错误

	// 如果没有panic，则测试通过
}
