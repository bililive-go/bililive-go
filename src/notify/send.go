package notify

import (
	"context"
	"fmt"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/notify/email"
	"github.com/bililive-go/bililive-go/src/notify/telegram"
)

// SendNotification 发送统一通知函数
// 检测用户是否开启了telegram和email通知服务，然后分别发送通知
// 参数: ctx(context上下文), hostName(主播姓名), platform(直播平台), liveURL(直播地址), status(直播状态: consts.LiveStatusStart/consts.LiveStatusStop)
func SendNotification(ctx context.Context, hostName, platform, liveURL, status string) error {
	// 获取当前配置
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		return fmt.Errorf("configuration is nil")
	}

	// 获取logger实例
	var logger *instance.Instance
	if ctx != nil {
		logger = instance.GetInstance(ctx)
	}

	// 根据状态设置消息内容
	var messageStatus string
	switch status {
	case consts.LiveStatusStart:
		messageStatus = "已开始直播,正在录制中"
	case consts.LiveStatusStop:
		messageStatus = "已结束直播,录制已停止"
	default:
		messageStatus = "直播状态未知"
	}

	// 统一主播信息格式
	hostInfo := fmt.Sprintf("%s,%s", hostName, messageStatus)

	// 构造Telegram消息内容 (包含所有信息)
	telegramMessage := fmt.Sprintf("主播：%s\n平台：%s\n直播地址：%s", hostInfo, platform, liveURL)

	// 检查是否开启了Telegram通知服务
	if cfg.Notify.Telegram.Enable {
		// 发送Telegram通知
		err := telegram.SendMessage(
			cfg.Notify.Telegram.BotToken,
			cfg.Notify.Telegram.ChatID,
			telegramMessage,
			cfg.Notify.Telegram.WithNotification, // 发送带提醒的消息
		)
		if err != nil {
			// 使用项目原来的日志打印方式打印错误
			if logger != nil && logger.Logger != nil {
				logger.Logger.WithError(err).Error("Failed to send Telegram message")
				// 保留了 fallback 机制
			} else {
				fmt.Printf("[ERROR] Failed to send Telegram message: %v\n", err)
			}
			// 注意：即使Telegram发送失败，我们仍然继续尝试发送邮件
		}
	}

	// 构造邮件主题和内容
	emailSubject := fmt.Sprintf("%s - %s", hostInfo, platform)
	emailBody := fmt.Sprintf("主播：%s\n平台：%s\n直播地址：%s", hostInfo, platform, liveURL)

	// 检查是否开启了Email通知服务
	if cfg.Notify.Email.Enable {
		// 发送Email通知
		err := email.SendEmail(emailSubject, emailBody)
		if err != nil {
			// 使用项目原来的日志打印方式打印错误
			if logger != nil && logger.Logger != nil {
				logger.Logger.WithError(err).Error("Failed to send email")
			} else {
				fmt.Printf("[ERROR] Failed to send email: %v\n", err)
			}
		}
	}

	return nil
}

// SendTestNotification 发送测试通知
func SendTestNotification(ctx context.Context) {
	// 测试开始直播通知
	err := SendNotification(ctx, "测试主播", "测试平台", "https://example.com/live", consts.LiveStatusStart)
	if err != nil {
		// 获取logger实例
		var logger *instance.Instance
		if ctx != nil {
			logger = instance.GetInstance(ctx)
		}
		if logger != nil && logger.Logger != nil {
			logger.Logger.WithError(err).Error("Failed to send start live test notification")
		} else {
			fmt.Printf("[ERROR] Failed to send start live test notification: %v\n", err)
		}
	}

	// 测试结束直播通知
	err = SendNotification(ctx, "测试主播", "测试平台", "https://example.com/live", consts.LiveStatusStop)
	if err != nil {
		// 获取logger实例
		var logger *instance.Instance
		if ctx != nil {
			logger = instance.GetInstance(ctx)
		}
		if logger != nil && logger.Logger != nil {
			logger.Logger.WithError(err).Error("Failed to send stop live test notification")
		} else {
			fmt.Printf("[ERROR] Failed to send stop live test notification: %v\n", err)
		}
	}
}
