// Package telemetry 提供匿名版本统计功能
// 仅发送程序版本号，用于了解用户更新程序的整体趋势
// 不收集平台、架构或任何其他个人数据
package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// PingEndpoint bililive-go.com 的统计端点
	PingEndpoint = "https://bililive-go.com/api/ping"
	// 请求超时
	pingTimeout = 10 * time.Second
)

// EventType 事件类型
type EventType string

const (
	// EventStartup 程序启动事件
	EventStartup EventType = "startup"
	// EventUpdate 更新事件
	EventUpdate EventType = "update"
)

// Telemetry 统计客户端
type Telemetry struct {
	httpClient *http.Client
	version    string
	enabled    bool
	mu         sync.RWMutex
}

var (
	instance *Telemetry
	once     sync.Once
)

// Init 初始化统计客户端
// 如果 enabled 为 false，则所有统计操作都不会执行
func Init(version string, enabled bool) *Telemetry {
	once.Do(func() {
		instance = &Telemetry{
			httpClient: &http.Client{
				Timeout: pingTimeout,
			},
			version: version,
			enabled: enabled,
		}
	})
	return instance
}

// GetInstance 获取统计客户端实例
func GetInstance() *Telemetry {
	return instance
}

// SetEnabled 设置是否启用统计
func (t *Telemetry) SetEnabled(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = enabled
}

// IsEnabled 检查统计是否启用
func (t *Telemetry) IsEnabled() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.enabled
}

// SendEvent 发送统计事件
// 这是一个非阻塞操作，在后台异步执行
func (t *Telemetry) SendEvent(ctx context.Context, event EventType) {
	if t == nil || !t.IsEnabled() {
		return
	}

	// 在后台执行，不阻塞主程序
	go func() {
		if err := t.sendEventSync(ctx, event); err != nil {
			// 静默失败，不影响主程序
			logrus.WithError(err).Debug("发送统计事件失败")
		}
	}()
}

// sendEventSync 同步发送统计事件
// 仅发送版本号和事件类型
func (t *Telemetry) sendEventSync(ctx context.Context, event EventType) error {
	// 构建 URL
	pingURL, err := url.Parse(PingEndpoint)
	if err != nil {
		return fmt.Errorf("解析端点 URL 失败: %w", err)
	}

	// 仅发送版本号和事件类型
	query := pingURL.Query()
	query.Set("v", t.version)
	query.Set("e", string(event))
	pingURL.RawQuery = query.Encode()

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pingURL.String(), nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	// 使用简单的 User-Agent，不包含系统信息
	req.Header.Set("User-Agent", fmt.Sprintf("bililive-go/%s", t.version))

	// 发送请求
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("服务器返回错误状态码: %d", resp.StatusCode)
	}

	logrus.WithFields(logrus.Fields{
		"event":   event,
		"version": t.version,
	}).Debug("版本统计已发送")

	return nil
}

// SendStartup 发送启动事件的便捷方法
func (t *Telemetry) SendStartup(ctx context.Context) {
	t.SendEvent(ctx, EventStartup)
}

// SendUpdate 发送更新事件的便捷方法
func (t *Telemetry) SendUpdate(ctx context.Context) {
	t.SendEvent(ctx, EventUpdate)
}
