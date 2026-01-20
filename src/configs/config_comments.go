package configs

import "gopkg.in/yaml.v3"

// DecorateConfigNode 将硬编码的中文注释注入到配置节点树中。
func DecorateConfigNode(node *yaml.Node) {
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return
	}

	root.HeadComment = `# 这个配置文件内的注释是自动生成的，请不要手动修改。
# 需要修改注释时，请在 src/configs/config_comments.go 文件内修改。`

	setFieldLineComment(root, "ffmpeg_path", "# 如果此项为空，就自动在环境变量里寻找")

	setFieldComment(root, "out_put_tmpl",
		`# '{{ .Live.GetPlatformCNName }}/{{ .HostName | filenameFilter }}/[{{ now | date "2006-01-02 15-04-05"}}][{{ .HostName | filenameFilter }}][{{ .RoomName | filenameFilter }}].flv'
# ./平台名称/主播名字/[时间戳][主播名字][房间名字].flv
# https://github.com/bililive-go/bililive-go/wiki/More-Tips`, "")

	splitNode := findNode(root, "video_split_strategies")
	if splitNode != nil {
		setFieldComment(splitNode, "max_file_size",
			`# 仅在使用 ffmpeg 或 bililive-recorder 下载器时生效
# 单位为字节 (byte)
# 有效值为正数，默认值 0 为无效
# 负数为非法值，程序会输出 log 提醒，并无视所设定的数值`, "")
	}

	finishNode := findNode(root, "on_record_finished")
	if finishNode != nil {
		setFieldComment(finishNode, "custom_commandline",
			`#  当 custom_commandline 的值 不为空时，convert_to_mp4 的值会被无视，
#  而是在录制结束后直接执行 custom_commandline 中的命令。
#  在 custom_commandline 执行结束后，程序还会继续查看 delete_flv_after_convert 的值，
#  来判断是否需要删除原始 flv 文件。
#  以下是一个在录制结束后将 flv 视频转换为同名 mp4 视频的示例：
#  custom_commandline: '{{ .Ffmpeg }} -hide_banner -i "{{ .FileName }}" -c copy "{{ .FileName | trimSuffix (.FileName | ext)}}.mp4"'`, "")
	}

	setFieldHeadComment(root, "notify", "# 通知服务配置")
	notifyNode := findNode(root, "notify")
	if notifyNode != nil {
		telegram := findNode(notifyNode, "telegram")
		if telegram != nil {
			setFieldComment(telegram, "enable", "# 是否开启Telegram通知", "")
			setFieldComment(telegram, "withNotification", "# 是否启用声音通知", "")
			setFieldComment(telegram, "botToken", "# Telegram机器人Token", "")
			setFieldComment(telegram, "chatID", "# Telegram聊天ID", "")
		}
		email := findNode(notifyNode, "email")
		if email != nil {
			setFieldComment(email, "enable", "# 是否开启Email通知", "")
			setFieldComment(email, "smtpHost", "# SMTP服务器地址 (例如: smtp.gmail.com, smtp.qq.com等)", "")
			setFieldComment(email, "smtpPort", "# SMTP服务器端口 (常用端口: 25, 465, 587)", "")
			setFieldComment(email, "senderEmail", "# 发送者邮箱地址", "")
			setFieldComment(email, "senderPassword", "# 发送者邮箱授权码或应用专用密码", "")
			setFieldComment(email, "recipientEmail", "# 接收者邮箱地址 ", "")
		}
	}

	// 特殊处理 live_rooms
	// 注释需要出现在 live_rooms 列表的第一个元素上方
	liveRoomsNode := findNode(root, "live_rooms")
	if liveRoomsNode != nil && liveRoomsNode.Kind == yaml.SequenceNode && len(liveRoomsNode.Content) > 0 {
		firstItem := liveRoomsNode.Content[0]
		firstItem.HeadComment = `# quality参数目前仅B站启用，默认为0
# (B站)0代表原画PRO(HEVC)优先, 其他数值为原画(AVC)
# 原画PRO会保存为.ts文件, 原画为.flv
# HEVC相比AVC体积更小, 减少35%体积, 画质相当, 但是B站转码有时候会崩`
	}

	// Sentry 配置注释
	setFieldHeadComment(root, "sentry", "# Sentry 错误监控配置（用于收集崩溃日志）")
	sentryNode := findNode(root, "sentry")
	if sentryNode != nil {
		setFieldComment(sentryNode, "enable", "# 是否启用 Sentry 错误监控", "")
		setFieldComment(sentryNode, "dsn", "# Sentry DSN，留空则禁用。申请地址：https://sentry.io/", "")
		setFieldComment(sentryNode, "environment", "# 环境标识：production 或 development", "")
	}

	// Proxy 代理配置注释
	setFieldHeadComment(root, "proxy", "# 代理配置（支持 HTTP 和 SOCKS5 代理）")
	proxyNode := findNode(root, "proxy")
	if proxyNode != nil {
		setFieldComment(proxyNode, "enable",
			`# 是否启用配置的代理
# false: 使用系统环境变量 (HTTP_PROXY, HTTPS_PROXY, ALL_PROXY)
# true: 使用下方配置的代理地址`, "")
		setFieldComment(proxyNode, "url",
			`# 代理地址，支持以下格式：
# HTTP 代理: http://host:port 或 http://user:pass@host:port
# SOCKS5 代理: socks5://host:port 或 socks5://user:pass@host:port
# 示例: socks5://127.0.0.1:1080 (翻墙软件常用端口)`, "")
	}

	// Feature 功能配置注释
	featureNode := findNode(root, "feature")
	if featureNode != nil {
		setFieldComment(featureNode, "downloader_type",
			`# 下载器类型：ffmpeg（默认）、native（内置 FLV 解析器）、bililive-recorder
# ffmpeg: 使用 FFmpeg 录制，支持所有流格式，需要安装 FFmpeg
# native: 使用内置 FLV 解析器，仅支持 FLV 流，无需额外依赖
# bililive-recorder: 使用 BililiveRecorder CLI，仅支持 FLV 流`, "")
		setFieldComment(featureNode, "enable_flv_proxy_segment",
			`# FLV 代理分段功能（仅对 FFmpeg 下载器生效）
# 当检测到视频编码参数变化（新的 SPS/PPS）时，会主动断开连接触发 FFmpeg 分段
# 这可以避免因编码参数变化导致的花屏问题
# 注意：启用后会在本地启动一个 FLV 代理服务器，FFmpeg 从代理读取流`, "")
	}
}

func findNode(mapNode *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(mapNode.Content); i += 2 {
		if mapNode.Content[i].Value == key {
			return mapNode.Content[i+1]
		}
	}
	return nil
}

func setFieldComment(mapNode *yaml.Node, key, headComment, lineComment string) {
	for i := 0; i < len(mapNode.Content); i += 2 {
		k := mapNode.Content[i]
		if k.Value == key {
			if headComment != "" {
				k.HeadComment = headComment
			}
			if lineComment != "" {
				k.LineComment = lineComment
			}
			return
		}
	}
}

func setFieldLineComment(mapNode *yaml.Node, key, lineComment string) {
	for i := 0; i < len(mapNode.Content); i += 2 {
		k := mapNode.Content[i]
		if k.Value == key {
			k.LineComment = lineComment
			return
		}
	}
}

func setFieldHeadComment(mapNode *yaml.Node, key, headComment string) {
	for i := 0; i < len(mapNode.Content); i += 2 {
		k := mapNode.Content[i]
		if k.Value == key {
			k.HeadComment = headComment
			return
		}
	}
}
