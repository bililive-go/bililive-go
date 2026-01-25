// Package ipc 提供启动器与主程序之间的进程间通信功能
package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// 消息类型常量
const (
	// MsgTypeStartupSuccess 主程序启动成功
	MsgTypeStartupSuccess = "startup_success"
	// MsgTypeStartupFailed 主程序启动失败
	MsgTypeStartupFailed = "startup_failed"
	// MsgTypeUpdateRequest 主程序请求更新
	MsgTypeUpdateRequest = "update_request"
	// MsgTypeShutdown 请求关闭
	MsgTypeShutdown = "shutdown"
	// MsgTypeShutdownAck 确认关闭
	MsgTypeShutdownAck = "shutdown_ack"
	// MsgTypeHeartbeat 心跳消息
	MsgTypeHeartbeat = "heartbeat"
	// MsgTypeHeartbeatAck 心跳确认
	MsgTypeHeartbeatAck = "heartbeat_ack"
	// MsgTypeUpdateResult 更新结果
	MsgTypeUpdateResult = "update_result"
)

// IPC 管道/Socket 名称前缀
const (
	// PipeNamePrefix Windows Named Pipe 名称前缀
	PipeNamePrefix = `\\.\pipe\bililive-go-`
	// SocketPathPrefix Unix Domain Socket 路径前缀
	SocketPathPrefix = "/tmp/bililive-go-"
)

// 超时设置
const (
	// DefaultConnectTimeout 默认连接超时
	DefaultConnectTimeout = 5 * time.Second
	// DefaultReadTimeout 默认读取超时
	DefaultReadTimeout = 30 * time.Second
	// DefaultWriteTimeout 默认写入超时
	DefaultWriteTimeout = 10 * time.Second
	// HeartbeatInterval 心跳间隔
	HeartbeatInterval = 10 * time.Second
)

// Message 表示 IPC 消息
type Message struct {
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// NewMessage 创建新消息
func NewMessage(msgType string, payload any) (*Message, error) {
	msg := &Message{
		Type:      msgType,
		Timestamp: time.Now().UnixMilli(),
	}
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("序列化消息负载失败: %w", err)
		}
		msg.Payload = data
	}
	return msg, nil
}

// ParsePayload 解析消息负载到指定类型
func (m *Message) ParsePayload(v any) error {
	if m.Payload == nil {
		return nil
	}
	return json.Unmarshal(m.Payload, v)
}

// StartupSuccessPayload 启动成功消息负载
type StartupSuccessPayload struct {
	Version   string `json:"version"`
	PID       int    `json:"pid"`
	StartTime int64  `json:"start_time"`
}

// StartupFailedPayload 启动失败消息负载
type StartupFailedPayload struct {
	Version string `json:"version"`
	Error   string `json:"error"`
	PID     int    `json:"pid"`
}

// UpdateRequestPayload 更新请求消息负载
type UpdateRequestPayload struct {
	NewVersion     string `json:"new_version"`
	DownloadPath   string `json:"download_path"`
	SHA256Checksum string `json:"sha256_checksum"`
	Changelog      string `json:"changelog,omitempty"`
}

// UpdateResultPayload 更新结果消息负载
type UpdateResultPayload struct {
	Success    bool   `json:"success"`
	Version    string `json:"version"`
	Error      string `json:"error,omitempty"`
	RolledBack bool   `json:"rolled_back,omitempty"`
}

// ShutdownPayload 关闭请求消息负载
type ShutdownPayload struct {
	Reason      string `json:"reason"`
	GracePeriod int    `json:"grace_period_seconds"`
}

// Conn 表示 IPC 连接接口
type Conn interface {
	// Send 发送消息
	Send(msg *Message) error
	// Receive 接收消息，阻塞直到收到消息或超时
	Receive() (*Message, error)
	// Close 关闭连接
	Close() error
	// SetReadDeadline 设置读取超时
	SetReadDeadline(t time.Time) error
	// SetWriteDeadline 设置写入超时
	SetWriteDeadline(t time.Time) error
}

// Server 表示 IPC 服务器接口
type Server interface {
	// Start 启动服务器
	Start(ctx context.Context) error
	// Stop 停止服务器
	Stop() error
	// OnConnect 设置连接回调
	OnConnect(handler func(conn Conn))
	// OnMessage 设置消息回调
	OnMessage(handler func(conn Conn, msg *Message))
	// OnDisconnect 设置断开连接回调
	OnDisconnect(handler func(conn Conn, err error))
	// Broadcast 广播消息到所有连接
	Broadcast(msg *Message) error
}

// Client 表示 IPC 客户端接口
type Client interface {
	// Connect 连接到服务器
	Connect(ctx context.Context) error
	// Disconnect 断开连接
	Disconnect() error
	// Send 发送消息
	Send(msg *Message) error
	// Receive 接收消息
	Receive() (*Message, error)
	// IsConnected 检查是否已连接
	IsConnected() bool
	// OnMessage 设置消息回调
	OnMessage(handler func(msg *Message))
	// OnDisconnect 设置断开连接回调
	OnDisconnect(handler func(err error))
}

// 错误定义
var (
	ErrNotConnected     = errors.New("未连接到 IPC 服务器")
	ErrAlreadyConnected = errors.New("已经连接到 IPC 服务器")
	ErrServerClosed     = errors.New("IPC 服务器已关闭")
	ErrConnectionClosed = errors.New("IPC 连接已关闭")
	ErrTimeout          = errors.New("操作超时")
)

// connWrapper 封装 net.Conn 实现 Conn 接口
type connWrapper struct {
	conn    net.Conn
	encoder *json.Encoder
	decoder *json.Decoder
	mu      sync.Mutex
}

// newConnWrapper 创建连接包装器
func newConnWrapper(conn net.Conn) *connWrapper {
	return &connWrapper{
		conn:    conn,
		encoder: json.NewEncoder(conn),
		decoder: json.NewDecoder(conn),
	}
}

// Send 发送消息
func (c *connWrapper) Send(msg *Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.encoder.Encode(msg)
}

// Receive 接收消息
func (c *connWrapper) Receive() (*Message, error) {
	var msg Message
	if err := c.decoder.Decode(&msg); err != nil {
		if err == io.EOF {
			return nil, ErrConnectionClosed
		}
		return nil, err
	}
	return &msg, nil
}

// Close 关闭连接
func (c *connWrapper) Close() error {
	return c.conn.Close()
}

// SetReadDeadline 设置读取超时
func (c *connWrapper) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline 设置写入超时
func (c *connWrapper) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// GetInstanceID 返回用于 IPC 的实例 ID
// 默认使用固定的 ID "default"，可以通过环境变量 BILILIVE_INSTANCE_ID 覆盖
func GetInstanceID() string {
	// TODO: 支持从环境变量读取
	return "default"
}

// GetPipeName 返回 Windows Named Pipe 名称
func GetPipeName(instanceID string) string {
	return PipeNamePrefix + instanceID
}

// GetSocketPath 返回 Unix Domain Socket 路径
func GetSocketPath(instanceID string) string {
	return SocketPathPrefix + instanceID + ".sock"
}
