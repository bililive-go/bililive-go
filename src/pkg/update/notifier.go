package update

import (
	"context"
	"sync"

	"github.com/bililive-go/bililive-go/src/pkg/ipc"
)

// Notifier 负责与启动器通信，发送更新请求
type Notifier struct {
	client    ipc.Client
	connected bool
	mu        sync.RWMutex
}

// NewNotifier 创建新的通知器
func NewNotifier(instanceID string) *Notifier {
	return &Notifier{
		client: ipc.NewClient(instanceID),
	}
}

// Connect 连接到启动器
func (n *Notifier) Connect(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.connected {
		return nil
	}

	if err := n.client.Connect(ctx); err != nil {
		return err
	}

	n.connected = true
	return nil
}

// Disconnect 断开与启动器的连接
func (n *Notifier) Disconnect() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.connected {
		return nil
	}

	if err := n.client.Disconnect(); err != nil {
		return err
	}

	n.connected = false
	return nil
}

// IsConnected 检查是否已连接到启动器
func (n *Notifier) IsConnected() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.connected
}

// NotifyStartupSuccess 通知启动器主程序启动成功
func (n *Notifier) NotifyStartupSuccess(version string, pid int) error {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if !n.connected {
		return ipc.ErrNotConnected
	}

	payload := ipc.StartupSuccessPayload{
		Version:   version,
		PID:       pid,
		StartTime: 0, // TODO: 从启动时间计算
	}

	msg, err := ipc.NewMessage(ipc.MsgTypeStartupSuccess, payload)
	if err != nil {
		return err
	}

	return n.client.Send(msg)
}

// NotifyStartupFailed 通知启动器主程序启动失败
func (n *Notifier) NotifyStartupFailed(version string, pid int, errMsg string) error {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if !n.connected {
		return ipc.ErrNotConnected
	}

	payload := ipc.StartupFailedPayload{
		Version: version,
		PID:     pid,
		Error:   errMsg,
	}

	msg, err := ipc.NewMessage(ipc.MsgTypeStartupFailed, payload)
	if err != nil {
		return err
	}

	return n.client.Send(msg)
}

// RequestUpdate 向启动器发送更新请求
func (n *Notifier) RequestUpdate(newVersion, downloadPath, sha256Checksum string) error {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if !n.connected {
		return ipc.ErrNotConnected
	}

	payload := ipc.UpdateRequestPayload{
		NewVersion:     newVersion,
		DownloadPath:   downloadPath,
		SHA256Checksum: sha256Checksum,
	}

	msg, err := ipc.NewMessage(ipc.MsgTypeUpdateRequest, payload)
	if err != nil {
		return err
	}

	return n.client.Send(msg)
}

// SendShutdownAck 发送关闭确认
func (n *Notifier) SendShutdownAck() error {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if !n.connected {
		return ipc.ErrNotConnected
	}

	msg, err := ipc.NewMessage(ipc.MsgTypeShutdownAck, nil)
	if err != nil {
		return err
	}

	return n.client.Send(msg)
}

// OnMessage 设置消息处理回调
func (n *Notifier) OnMessage(handler func(msg *ipc.Message)) {
	n.client.OnMessage(handler)
}

// OnDisconnect 设置断开连接回调
func (n *Notifier) OnDisconnect(handler func(err error)) {
	n.client.OnDisconnect(handler)
}
