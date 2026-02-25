package notify

import (
	"fmt"
	"strings"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/notify/bark"
	"github.com/bililive-go/bililive-go/src/notify/email"
	"github.com/bililive-go/bililive-go/src/notify/ntfy"
	"github.com/bililive-go/bililive-go/src/notify/telegram"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
)

// RecordingFileDetail 录制文件详情
type RecordingFileDetail struct {
	Name string // 文件名（不含路径）
	Size int64  // 文件大小（字节）
}

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
			logger.WithError(err).Error("Failed to send Ntfy message")
		}
	}

	// 检查是否开启了 Bark 通知服务
	if cfg.Notify.Bark.Enable {
		var err error
		switch status {
		case consts.LiveStatusStart:
			err = bark.SendMessage(
				cfg.Notify.Bark.ServerURL,
				cfg.Notify.Bark.DeviceKey,
				cfg.Notify.Bark.Sound,
				cfg.Notify.Bark.Group,
				cfg.Notify.Bark.Icon,
				cfg.Notify.Bark.Level,
				hostName,
				platform,
				liveURL,
			)
		case consts.LiveStatusStop:
			err = bark.SendStopMessage(
				cfg.Notify.Bark.ServerURL,
				cfg.Notify.Bark.DeviceKey,
				cfg.Notify.Bark.Sound,
				cfg.Notify.Bark.Group,
				cfg.Notify.Bark.Icon,
				cfg.Notify.Bark.Level,
				hostName,
				platform,
				liveURL,
			)
		}
		if err != nil {
			logger.WithError(err).Error("Failed to send Bark message")
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

// formatFileSize 将字节数格式化为可读的文件大小
func formatFileSize(size int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case size >= gb:
		return fmt.Sprintf("%.2f GB", float64(size)/float64(gb))
	case size >= mb:
		return fmt.Sprintf("%.2f MB", float64(size)/float64(mb))
	case size >= kb:
		return fmt.Sprintf("%.2f KB", float64(size)/float64(kb))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// buildRecordingSummaryMessage 构造录制摘要消息内容
// 当文件数量过多时，截断文件列表以避免超出消息平台字符限制（如 Telegram 4096 字符）
// outputPath 用于获取剩余磁盘空间，为空则不显示
func buildRecordingSummaryMessage(hostName, platform string, files []RecordingFileDetail, outputPath string) (title, body string) {
	const maxDisplayFiles = 30 // 最多显示的文件数量

	title = fmt.Sprintf("%s 录制完成", hostName)

	var sb strings.Builder
	fmt.Fprintf(&sb, "平台：%s\n", platform)
	fmt.Fprintf(&sb, "录制文件：%d 个\n", len(files))
	var totalSize int64
	for i, f := range files {
		totalSize += f.Size
		if i < maxDisplayFiles {
			fmt.Fprintf(&sb, "  %d. %s (%s)\n", i+1, f.Name, formatFileSize(f.Size))
		}
	}
	if len(files) > maxDisplayFiles {
		fmt.Fprintf(&sb, "  ... 还有 %d 个文件未显示\n", len(files)-maxDisplayFiles)
	}
	fmt.Fprintf(&sb, "总大小：%s", formatFileSize(totalSize))
	// 显示剩余磁盘空间
	if outputPath != "" {
		if free, err := getDiskFreeSpace(outputPath); err == nil {
			fmt.Fprintf(&sb, "\n剩余磁盘空间：%s", formatFileSize(int64(free)))
		}
	}
	body = sb.String()
	return
}

// SendRecordingSummary 录制结束后发送录制文件摘要通知
// outputPath 为录制输出路径，用于获取剩余磁盘空间
func SendRecordingSummary(logger *livelogger.LiveLogger, hostName, platform string, files []RecordingFileDetail, outputPath string) {
	cfg := configs.GetCurrentConfig()
	if cfg == nil || !cfg.Notify.SendRecordingSummary {
		return
	}
	if len(files) == 0 {
		return
	}

	title, body := buildRecordingSummaryMessage(hostName, platform, files, outputPath)

	// Telegram
	if cfg.Notify.Telegram.Enable {
		msg := fmt.Sprintf("%s\n%s", title, body)
		if err := telegram.SendMessage(
			cfg.Notify.Telegram.BotToken,
			cfg.Notify.Telegram.ChatID,
			msg,
			cfg.Notify.Telegram.WithNotification,
		); err != nil {
			logger.WithError(err).Error("Failed to send recording summary via Telegram")
		}
	}

	// Email
	if cfg.Notify.Email.Enable {
		if err := email.SendEmail(title, body); err != nil {
			logger.WithError(err).Error("Failed to send recording summary via Email")
		}
	}

	// Bark
	if cfg.Notify.Bark.Enable {
		if err := bark.SendSummaryMessage(
			cfg.Notify.Bark.ServerURL,
			cfg.Notify.Bark.DeviceKey,
			cfg.Notify.Bark.Sound,
			cfg.Notify.Bark.Group,
			cfg.Notify.Bark.Icon,
			cfg.Notify.Bark.Level,
			title,
			body,
		); err != nil {
			logger.WithError(err).Error("Failed to send recording summary via Bark")
		}
	}
}
