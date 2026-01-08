package notify

import (
	"fmt"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/notify/email"
	"github.com/bililive-go/bililive-go/src/notify/ntfy"
	"github.com/bililive-go/bililive-go/src/notify/telegram"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
)

// SendNotification 发送统一通知函数
// 检测用户是否开启了telegram和email通知服务，然后分别发送通知
// 参数: logger(LiveLogger), hostName(主播姓名), platform(直播平台), liveURL(直播地址), status(直播状态: consts.LiveStatusStart/consts.LiveStatusStop)
func SendNotification(logger *livelogger.LiveLogger, hostName, platform, liveURL, status string) error {
	// 获取当前配置
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		return fmt.Errorf("configuration is nil")
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
			logger.WithError(err).Error("Failed to send Telegram message")
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
			logger.WithError(err).Error("Failed to send email")
		}
	}

	// 检查是否开启了Ntfy通知服务
	if cfg.Notify.Ntfy.Enable {
		// 根据不同的状态发送不同的ntfy消息
		var err error
		switch status {
		case consts.LiveStatusStart:
			// 从配置中获取scheme URL
			var schemeUrl string
			// 根据liveURL查找对应的LiveRoom配置
			if liveRoom, lookupErr := cfg.GetLiveRoomByUrl(liveURL); lookupErr == nil {
				schemeUrl = liveRoom.SchemeUrl
			}

			// 发送Ntfy开始录制通知
			err = ntfy.SendMessage(
				cfg.Notify.Ntfy.URL,
				cfg.Notify.Ntfy.Token,
				cfg.Notify.Ntfy.Tag,
				hostName,
				platform,
				liveURL,
				schemeUrl,
			)
		case consts.LiveStatusStop:
			// 发送Ntfy停止录制通知
			err = ntfy.SendStopMessage(
				cfg.Notify.Ntfy.URL,
				cfg.Notify.Ntfy.Token,
				cfg.Notify.Ntfy.Tag,
				hostName,
				platform,
				liveURL,
			)
		}

		if err != nil {
			// 使用项目原来的日志打印方式打印错误
			if logger != nil && logger.Logger != nil {
				logger.Logger.WithError(err).Error("Failed to send Ntfy message")
			} else {
				fmt.Printf("[ERROR] Failed to send Ntfy message: %v\n", err)
			}
		}
	}

	return nil
}

// SendTestNotification 发送测试通知
func SendTestNotification(logger *livelogger.LiveLogger) {
	// 测试开始直播通知
	err := SendNotification(logger, "测试主播", "测试平台", "https://example.com/live", consts.LiveStatusStart)
	if err != nil {
		logger.WithError(err).Error("Failed to send start live test notification")
	}

	// 测试结束直播通知
	err = SendNotification(logger, "测试主播", "测试平台", "https://example.com/live", consts.LiveStatusStop)
	if err != nil {
		logger.WithError(err).Error("Failed to send stop live test notification")
	}
}
