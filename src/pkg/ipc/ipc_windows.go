//go:build windows

// Package ipc 提供启动器与主程序之间的进程间通信功能（Windows 实现）
package ipc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
)

// WindowsServer 实现基于 Named Pipe 的 IPC 服务器
type WindowsServer struct {
	pipeName     string
	listener     net.Listener
	connections  map[*connWrapper]struct{}
	connMu       sync.RWMutex
	onConnect    func(conn Conn)
	onMessage    func(conn Conn, msg *Message)
	onDisconnect func(conn Conn, err error)
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// NewServer 创建新的 IPC 服务器（Windows 实现）
func NewServer(instanceID string) Server {
	return &WindowsServer{
		pipeName:    GetPipeName(instanceID),
		connections: make(map[*connWrapper]struct{}),
	}
}

// Start 启动服务器
func (s *WindowsServer) Start(ctx context.Context) error {
	config := &winio.PipeConfig{
		SecurityDescriptor: "", // 默认安全描述符
		MessageMode:        false,
		InputBufferSize:    65536,
		OutputBufferSize:   65536,
	}

	listener, err := winio.ListenPipe(s.pipeName, config)
	if err != nil {
		return fmt.Errorf("无法创建 Named Pipe: %w", err)
	}

	s.listener = listener
	s.ctx, s.cancel = context.WithCancel(ctx)

	// 启动接受连接的 goroutine
	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

// acceptLoop 接受新连接
func (s *WindowsServer) acceptLoop() {
	defer bilisentry.Recover()
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				// 非正常错误，继续接受连接
				continue
			}
		}

		wrapper := newConnWrapper(conn)
		s.connMu.Lock()
		s.connections[wrapper] = struct{}{}
		s.connMu.Unlock()

		if s.onConnect != nil {
			s.onConnect(wrapper)
		}

		// 启动消息处理 goroutine
		s.wg.Add(1)
		go s.handleConnection(wrapper)
	}
}

// handleConnection 处理单个连接的消息
func (s *WindowsServer) handleConnection(conn *connWrapper) {
	defer bilisentry.Recover()
	defer s.wg.Done()
	defer func() {
		s.connMu.Lock()
		delete(s.connections, conn)
		s.connMu.Unlock()
		conn.Close()
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		msg, err := conn.Receive()
		if err != nil {
			if s.onDisconnect != nil {
				s.onDisconnect(conn, err)
			}
			return
		}

		if s.onMessage != nil {
			s.onMessage(conn, msg)
		}
	}
}

// Stop 停止服务器
func (s *WindowsServer) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.listener != nil {
		s.listener.Close()
	}

	// 关闭所有连接
	s.connMu.Lock()
	for conn := range s.connections {
		conn.Close()
	}
	s.connMu.Unlock()

	s.wg.Wait()
	return nil
}

// OnConnect 设置连接回调
func (s *WindowsServer) OnConnect(handler func(conn Conn)) {
	s.onConnect = handler
}

// OnMessage 设置消息回调
func (s *WindowsServer) OnMessage(handler func(conn Conn, msg *Message)) {
	s.onMessage = handler
}

// OnDisconnect 设置断开连接回调
func (s *WindowsServer) OnDisconnect(handler func(conn Conn, err error)) {
	s.onDisconnect = handler
}

// Broadcast 广播消息到所有连接
func (s *WindowsServer) Broadcast(msg *Message) error {
	s.connMu.RLock()
	defer s.connMu.RUnlock()

	var lastErr error
	for conn := range s.connections {
		if err := conn.Send(msg); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// WindowsClient 实现基于 Named Pipe 的 IPC 客户端
type WindowsClient struct {
	pipeName     string
	conn         *connWrapper
	mu           sync.Mutex
	onMessage    func(msg *Message)
	onDisconnect func(err error)
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// NewClient 创建新的 IPC 客户端（Windows 实现）
func NewClient(instanceID string) Client {
	return &WindowsClient{
		pipeName: GetPipeName(instanceID),
	}
}

// Connect 连接到服务器
func (c *WindowsClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return ErrAlreadyConnected
	}

	// 使用 winio.DialPipe 连接到 Named Pipe
	conn, err := winio.DialPipeContext(ctx, c.pipeName)
	if err != nil {
		return fmt.Errorf("连接到 IPC 服务器失败: %w", err)
	}

	c.conn = newConnWrapper(conn)
	c.ctx, c.cancel = context.WithCancel(ctx)

	// 启动消息接收 goroutine
	c.wg.Add(1)
	go c.receiveLoop()

	return nil
}

// receiveLoop 接收消息循环
func (c *WindowsClient) receiveLoop() {
	defer bilisentry.Recover()
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		msg, err := c.conn.Receive()
		if err != nil {
			if c.onDisconnect != nil {
				c.onDisconnect(err)
			}
			return
		}

		if c.onMessage != nil {
			c.onMessage(msg)
		}
	}
}

// Disconnect 断开连接
func (c *WindowsClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	c.wg.Wait()
	return nil
}

// Send 发送消息
func (c *WindowsClient) Send(msg *Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return ErrNotConnected
	}
	return c.conn.Send(msg)
}

// Receive 接收消息（阻塞）
func (c *WindowsClient) Receive() (*Message, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return nil, ErrNotConnected
	}
	return conn.Receive()
}

// IsConnected 检查是否已连接
func (c *WindowsClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// OnMessage 设置消息回调
func (c *WindowsClient) OnMessage(handler func(msg *Message)) {
	c.onMessage = handler
}

// OnDisconnect 设置断开连接回调
func (c *WindowsClient) OnDisconnect(handler func(err error)) {
	c.onDisconnect = handler
}

// TryConnectWithRetry 尝试连接到服务器，失败时重试
func (c *WindowsClient) TryConnectWithRetry(ctx context.Context, maxRetries int, retryInterval time.Duration) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if err := c.Connect(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
		}
	}
	return lastErr
}
