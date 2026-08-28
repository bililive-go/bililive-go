// Package sentry 提供 Sentry 错误监控的封装
// 用于收集程序崩溃日志，同时保护用户隐私
package sentry

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/sirupsen/logrus"
)

var (
	// initialized 标记 Sentry 是否已初始化
	initialized bool
	// initMu 保护初始化状态
	initMu sync.RWMutex
)

// 敏感关键字列表，用于过滤敏感数据
var sensitiveKeywords = []string{
	"cookie", "token", "password", "passwd", "secret", "key", "auth",
	"credential", "api_key", "apikey", "access_token", "refresh_token",
	"bot_token", "bottoken", "chat_id", "chatid", "sender_password",
}

// 敏感 URL 参数正则表达式
var sensitiveURLPattern = regexp.MustCompile(`[?&](token|key|secret|password|auth|access_token|session)[=][^&]*`)

// 完整 URL 往往包含直播间 ID、用户配置或临时鉴权参数。错误遥测不需要这些信息，
// 因此统一移除，既保护隐私，也避免相同错误按不同房间 URL 分裂成大量 issue。
var fullURLPattern = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+`)

// Init 初始化 Sentry SDK
// dsn 为 Sentry DSN，留空则禁用
// environment 为环境标识（development/production）
// release 为版本号
func Init(dsn, environment, release string) error {
	if dsn == "" {
		return nil // DSN 为空时不初始化
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      environment,
		Release:          release,
		AttachStacktrace: true,
		BeforeSend:       beforeSendHook,
		// 采样率：100% 发送所有错误
		SampleRate: 1.0,
	})

	if err != nil {
		return err
	}

	// 设置匿名用户标识
	deviceID := GetAnonymousDeviceID()
	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetUser(sentry.User{
			ID: deviceID,
		})
	})

	initMu.Lock()
	initialized = true
	initMu.Unlock()

	return nil
}

// IsInitialized 返回 Sentry 是否已初始化
func IsInitialized() bool {
	initMu.RLock()
	defer initMu.RUnlock()
	return initialized
}

// Flush 刷新所有待发送事件（程序退出前调用）
func Flush(timeout time.Duration) {
	if !IsInitialized() {
		return
	}
	sentry.Flush(timeout)
}

// RecoverWithContext 用于 goroutine 的 panic 恢复
// 应在 goroutine 开始时使用 defer 调用
// 注意：必须先调用 recover()，再检查 Sentry 状态，否则 panic 不会被捕获
func RecoverWithContext(ctx context.Context) {
	err := recover()
	if err == nil {
		return
	}
	logRecoveredPanic(err)

	// 尝试上报给 Sentry，但即使失败也不应该再次 panic
	if IsInitialized() {
		hub := sentry.GetHubFromContext(ctx)
		if hub == nil {
			hub = sentry.CurrentHub()
		}
		if hub != nil {
			hub.RecoverWithContext(ctx, err)
		}
	}
	// 不重新 panic；本地日志已经保留脱敏后的错误信息。
}

// Recover 用于 goroutine 的 panic 恢复（无 context 版本）
// 应在 goroutine 开始时使用 defer 调用
// 注意：必须先调用 recover()，再检查 Sentry 状态，否则 panic 不会被捕获
func Recover() {
	err := recover()
	if err == nil {
		return
	}
	logRecoveredPanic(err)

	// 尝试上报给 Sentry，但即使失败也不应该再次 panic
	if IsInitialized() {
		hub := sentry.CurrentHub()
		if hub != nil {
			hub.Recover(err)
		}
	}
	// 不重新 panic；本地日志已经保留脱敏后的错误信息。
}

func logRecoveredPanic(value interface{}) {
	logrus.WithField("panic", sanitizeString(fmt.Sprint(value))).Error("goroutine panic recovered")
}

// CaptureException 捕获异常
func CaptureException(err error) {
	if !IsInitialized() || err == nil {
		return
	}
	sentry.CaptureException(err)
}

// CaptureMessage 捕获消息
func CaptureMessage(msg string) {
	if !IsInitialized() {
		return
	}
	sentry.CaptureMessage(msg)
}

// CaptureTestMessage 发送一条测试消息
func CaptureTestMessage() string {
	if !IsInitialized() {
		return "Sentry not initialized"
	}
	eventID := sentry.CaptureMessage("This is a test message from bililive-go Sentry integration")
	if eventID != nil {
		return string(*eventID)
	}
	return "failed to capture message"
}

// Go 启动一个新的 goroutine 并自动添加 panic 恢复
func Go(f func()) {
	go func() {
		defer Recover()
		f()
	}()
}

// GoWithContext 启动一个新的 goroutine 并自动添加 panic 恢复（带 Context）
// 传入的函数 f 会接收到传入的 ctx
func GoWithContext(ctx context.Context, f func(context.Context)) {
	go func() {
		defer RecoverWithContext(ctx)
		f(ctx)
	}()
}

// beforeSendHook 在发送事件前清理敏感数据
func beforeSendHook(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
	// 主机名和 SDK 自动补充的用户字段会识别具体部署，只保留匿名设备 ID。
	event.ServerName = ""
	event.User = sentry.User{ID: event.User.ID}

	// 清理异常消息中的敏感数据
	if event.Message != "" {
		event.Message = sanitizeString(event.Message)
	}

	// 清理异常信息
	for i := range event.Exception {
		if event.Exception[i].Value != "" {
			event.Exception[i].Value = sanitizeString(event.Exception[i].Value)
		}
	}

	// 清理堆栈帧中的敏感数据
	for i := range event.Exception {
		if event.Exception[i].Stacktrace != nil {
			for j := range event.Exception[i].Stacktrace.Frames {
				frame := &event.Exception[i].Stacktrace.Frames[j]
				// 清理可能包含敏感信息的变量
				frame.Vars = sanitizeVars(frame.Vars)
			}
		}
	}

	// 清理 Extra 数据
	event.Extra = sanitizeMap(event.Extra)

	// 清理 Breadcrumb 中可能出现的直播间 URL 和敏感字段。
	for _, breadcrumb := range event.Breadcrumbs {
		if breadcrumb == nil {
			continue
		}
		breadcrumb.Message = sanitizeString(breadcrumb.Message)
		breadcrumb.Data = sanitizeMap(breadcrumb.Data)
	}

	// 清理 Contexts 数据
	for key, ctxData := range event.Contexts {
		sanitizedCtx := make(map[string]interface{})
		for k, v := range ctxData {
			if isSensitiveKey(k) {
				sanitizedCtx[k] = "[REDACTED]"
			} else if strVal, ok := v.(string); ok {
				sanitizedCtx[k] = sanitizeString(strVal)
			} else {
				sanitizedCtx[k] = v
			}
		}
		event.Contexts[key] = sanitizedCtx
	}

	// 清理 Tags 中可能的敏感数据
	event.Tags = sanitizeTags(event.Tags)
	delete(event.Tags, "server_name")

	// 清理请求数据
	if event.Request != nil {
		event.Request = sanitizeRequest(event.Request)
	}

	// sentry-go 默认把 Recover 捕获的 panic 标记为 fatal/unhandled，但这些包装器
	// 已经阻止 panic 逃出 goroutine，应按已处理错误上报，避免误报整个进程崩溃。
	if hint != nil && hint.RecoveredException != nil {
		event.Level = sentry.LevelError
		if event.Tags == nil {
			event.Tags = make(map[string]string)
		}
		event.Tags["panic_handled"] = "true"
		for i := range event.Exception {
			if event.Exception[i].Mechanism == nil {
				event.Exception[i].Mechanism = &sentry.Mechanism{Type: "generic"}
			}
			event.Exception[i].Mechanism.Handled = sentry.Pointer(true)
		}
	}

	return event
}

// sanitizeString 清理字符串中的敏感数据
func sanitizeString(s string) string {
	result := s

	// 先移除完整 URL，防止直播间地址和查询参数进入遥测。
	result = fullURLPattern.ReplaceAllString(result, "[REDACTED_URL]")

	// 清理 URL 中的敏感参数
	result = sensitiveURLPattern.ReplaceAllString(result, "$1=[REDACTED]")

	// 清理可能的敏感键值对
	for _, keyword := range sensitiveKeywords {
		// 匹配 keyword=value 或 keyword: value 格式
		pattern := regexp.MustCompile(`(?i)(` + regexp.QuoteMeta(keyword) + `)\s*[=:]\s*[^\s,}"\]]+`)
		result = pattern.ReplaceAllString(result, "$1=[REDACTED]")
	}

	return result
}

// sanitizeVars 清理变量中的敏感数据
func sanitizeVars(vars map[string]interface{}) map[string]interface{} {
	if vars == nil {
		return nil
	}

	result := make(map[string]interface{})
	for key, value := range vars {
		if isSensitiveKey(key) {
			result[key] = "[REDACTED]"
		} else if strVal, ok := value.(string); ok {
			result[key] = sanitizeString(strVal)
		} else {
			result[key] = value
		}
	}
	return result
}

// sanitizeMap 清理 map 中的敏感数据
func sanitizeMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}

	result := make(map[string]interface{})
	for key, value := range m {
		if isSensitiveKey(key) {
			result[key] = "[REDACTED]"
		} else if strVal, ok := value.(string); ok {
			result[key] = sanitizeString(strVal)
		} else if mapVal, ok := value.(map[string]interface{}); ok {
			result[key] = sanitizeMap(mapVal)
		} else {
			result[key] = value
		}
	}
	return result
}

// sanitizeTags 清理 tags 中的敏感数据
func sanitizeTags(tags map[string]string) map[string]string {
	if tags == nil {
		return nil
	}

	result := make(map[string]string)
	for key, value := range tags {
		if isSensitiveKey(key) {
			result[key] = "[REDACTED]"
		} else {
			result[key] = sanitizeString(value)
		}
	}
	return result
}

// sanitizeRequest 清理 HTTP 请求中的敏感数据
func sanitizeRequest(req *sentry.Request) *sentry.Request {
	if req == nil {
		return nil
	}

	// 清理 URL
	if req.URL != "" {
		req.URL = sanitizeString(req.URL)
	}

	// URL 已整体移除，单独的查询字符串也没有排障价值，避免遗漏未知敏感参数。
	if req.QueryString != "" {
		req.QueryString = "[REDACTED]"
	}

	// 清理敏感请求头
	if req.Headers != nil {
		for header := range req.Headers {
			switch strings.ToLower(header) {
			case "authorization", "proxy-authorization", "cookie", "x-api-key", "x-auth-token", "x-forwarded-for", "x-real-ip":
				req.Headers[header] = "[REDACTED]"
			}
		}
	}

	// 清理 Cookies
	if req.Cookies != "" {
		req.Cookies = "[REDACTED]"
	}

	// 清理请求体中可能的敏感数据
	if req.Data != "" {
		req.Data = sanitizeString(req.Data)
	}

	return req
}

// isSensitiveKey 检查键名是否为敏感键
func isSensitiveKey(key string) bool {
	keyLower := strings.ToLower(key)
	for _, keyword := range sensitiveKeywords {
		if strings.Contains(keyLower, keyword) {
			return true
		}
	}
	return false
}
