// Package sentry 提供 Sentry 错误监控的封装
// 用于收集程序崩溃日志，同时保护用户隐私
package sentry

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
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
	// 不重新 panic，让 goroutine 优雅退出
}

// Recover 用于 goroutine 的 panic 恢复（无 context 版本）
// 应在 goroutine 开始时使用 defer 调用
// 注意：必须先调用 recover()，再检查 Sentry 状态，否则 panic 不会被捕获
func Recover() {
	err := recover()
	if err == nil {
		return
	}

	// 尝试上报给 Sentry，但即使失败也不应该再次 panic
	if IsInitialized() {
		hub := sentry.CurrentHub()
		if hub != nil {
			hub.Recover(err)
		}
	}
	// 不重新 panic，让 goroutine 优雅退出
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

	// 清理请求数据
	if event.Request != nil {
		event.Request = sanitizeRequest(event.Request)
	}

	return event
}

// sanitizeString 清理字符串中的敏感数据
func sanitizeString(s string) string {
	result := s

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
		req.URL = sensitiveURLPattern.ReplaceAllString(req.URL, "$1=[REDACTED]")
	}

	// 清理查询字符串
	if req.QueryString != "" {
		req.QueryString = sensitizeQueryString(req.QueryString)
	}

	// 清理敏感请求头
	if req.Headers != nil {
		sensitiveHeaders := []string{"authorization", "cookie", "x-api-key", "x-auth-token"}
		for _, header := range sensitiveHeaders {
			if _, exists := req.Headers[header]; exists {
				req.Headers[header] = "[REDACTED]"
			}
			// 也检查首字母大写版本（如 Authorization, Cookie 等）
			if len(header) > 0 {
				headerCapitalized := strings.ToUpper(header[:1]) + header[1:]
				if _, exists := req.Headers[headerCapitalized]; exists {
					req.Headers[headerCapitalized] = "[REDACTED]"
				}
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

// sensitizeQueryString 清理查询字符串中的敏感参数
func sensitizeQueryString(qs string) string {
	result := qs
	for _, keyword := range sensitiveKeywords {
		pattern := regexp.MustCompile(`(?i)(` + regexp.QuoteMeta(keyword) + `)=([^&]*)`)
		result = pattern.ReplaceAllString(result, "$1=[REDACTED]")
	}
	return result
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
