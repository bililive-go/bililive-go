package douyu

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	douyuDanmuAddr = "danmuproxy.douyu.com:8601"

	msgTypeClient = 689
	msgTypeServer = 690

	heartbeatInterval = 45 * time.Second
	readTimeout       = 60 * time.Second
)

type DouyuClient struct {
	roomID     string
	cookies    string
	conn       net.Conn
	onDanmaku  func(username, content string, color int)
	onGift     func(username, giftName string, num int)
	done       chan struct{}
	closeOnce  sync.Once
	workers    sync.WaitGroup
	logger     *logrus.Entry
	mu         sync.Mutex
	running    bool
	cachedAddr string
}

func NewDouyuClient(roomID, cookies string, onDanmaku func(username, content string, color int), onGift func(username, giftName string, num int), logger *logrus.Entry) *DouyuClient {
	return &DouyuClient{
		roomID:    roomID,
		cookies:   cookies,
		onDanmaku: onDanmaku,
		onGift:    onGift,
		done:      make(chan struct{}),
		logger:    logger,
	}
}

func (c *DouyuClient) Start(ctx context.Context) error {
	addr := c.resolveServerAddr()

	c.logger.Infof("连接斗鱼弹幕服务器: roomID=%s addr=%s", c.roomID, addr)

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("TCP 连接失败: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.running = true
	c.mu.Unlock()

	if err := c.sendLogin(); err != nil {
		conn.Close()
		c.mu.Lock()
		c.running = false
		c.conn = nil
		c.mu.Unlock()
		return fmt.Errorf("发送登录消息失败: %w", err)
	}

	resp, err := c.readOneFrame()
	if err != nil {
		conn.Close()
		c.mu.Lock()
		c.running = false
		c.conn = nil
		c.mu.Unlock()
		return fmt.Errorf("读取登录响应失败: %w", err)
	}
	fields := parseSTT(resp)
	if fields["type"] != "loginres" {
		conn.Close()
		c.mu.Lock()
		c.running = false
		c.conn = nil
		c.mu.Unlock()
		return fmt.Errorf("登录失败: %s", string(resp))
	}
	c.logger.Debug("斗鱼弹幕登录成功")

	if err := c.sendJoinGroup(); err != nil {
		conn.Close()
		c.mu.Lock()
		c.running = false
		c.conn = nil
		c.mu.Unlock()
		return fmt.Errorf("加入房间失败: %w", err)
	}

	c.workers.Add(2)
	go func() {
		defer c.workers.Done()
		c.readLoopWithReconnect(ctx)
	}()
	go func() {
		defer c.workers.Done()
		c.heartbeatLoop(ctx)
	}()

	c.logger.Info("斗鱼弹幕连接成功")
	return nil
}

func (c *DouyuClient) Stop() {
	c.mu.Lock()
	c.running = false
	c.closeOnce.Do(func() { close(c.done) })
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	c.workers.Wait()
}

func (c *DouyuClient) resolveServerAddr() string {
	if c.cachedAddr != "" {
		return c.cachedAddr
	}

	type serverInfo struct {
		IP   string `json:"ip"`
		Port string `json:"port"`
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", "https://www.douyu.com/betard/"+c.roomID, nil)
	if err != nil {
		return douyuDanmuAddr
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	if c.cookies != "" {
		req.Header.Set("Cookie", c.cookies)
	}

	resp, err := client.Do(req)
	if err != nil {
		c.logger.WithError(err).Debug("获取弹幕服务器列表失败，使用默认地址")
		return douyuDanmuAddr
	}
	defer resp.Body.Close()

	var result struct {
		Room struct {
			ServerConfig string `json:"server_config"`
		} `json:"room"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return douyuDanmuAddr
	}
	if result.Room.ServerConfig == "" {
		return douyuDanmuAddr
	}

	var servers []serverInfo
	if err := json.Unmarshal([]byte(result.Room.ServerConfig), &servers); err != nil {
		return douyuDanmuAddr
	}
	if len(servers) == 0 {
		return douyuDanmuAddr
	}

	s := servers[0]
	addr := net.JoinHostPort(s.IP, s.Port)
	c.cachedAddr = addr
	c.logger.Debugf("从 API 获取弹幕服务器: %s", addr)
	return addr
}

func (c *DouyuClient) invalidateAddrCache() {
	c.cachedAddr = ""
}

// getConn 安全获取当前连接引用，不持锁执行 I/O。
// 当客户端已停止时返回 nil，避免读取已关闭的连接触发无意义的重连。
func (c *DouyuClient) getConn() net.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil
	}
	return c.conn
}

func (c *DouyuClient) readLoopWithReconnect(ctx context.Context) {
	defer func() {
		c.mu.Lock()
		c.running = false
		c.closeOnce.Do(func() { close(c.done) })
		if c.conn != nil {
			c.conn.Close()
		}
		c.mu.Unlock()
	}()

	reconnectCount := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		default:
		}

		err := c.readLoop(ctx)
		if err == nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		default:
		}

		reconnectCount++

		// 线性退避，上限 60 秒
		delay := 3 * reconnectCount
		if delay > 60 {
			delay = 60
		}
		c.logger.Warnf("连接断开，%d秒后第 %d 次重连...", delay, reconnectCount)

		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-time.After(time.Duration(delay) * time.Second):
		}

		c.invalidateAddrCache()
		newAddr := c.resolveServerAddr()
		conn, dialErr := net.DialTimeout("tcp", newAddr, 10*time.Second)
		if dialErr != nil {
			c.logger.WithError(dialErr).Warn("重连失败")
			continue
		}

		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
		}
		c.conn = conn
		c.mu.Unlock()

		if loginErr := c.sendLogin(); loginErr != nil {
			c.logger.WithError(loginErr).Warn("重连后登录失败")
			c.mu.Lock()
			if c.conn != nil {
				c.conn.Close()
				c.conn = nil
			}
			c.mu.Unlock()
			continue
		}
		resp, readErr := c.readOneFrame()
		if readErr != nil {
			c.logger.WithError(readErr).Warn("重连后读取登录响应失败")
			c.mu.Lock()
			if c.conn != nil {
				c.conn.Close()
				c.conn = nil
			}
			c.mu.Unlock()
			continue
		}
		respFields := parseSTT(resp)
		if respFields["type"] != "loginres" {
			c.logger.Warnf("重连后登录失败: %s", string(resp))
			c.mu.Lock()
			if c.conn != nil {
				c.conn.Close()
				c.conn = nil
			}
			c.mu.Unlock()
			continue
		}
		if joinErr := c.sendJoinGroup(); joinErr != nil {
			c.logger.WithError(joinErr).Warn("重连后加入房间失败")
			c.mu.Lock()
			if c.conn != nil {
				c.conn.Close()
				c.conn = nil
			}
			c.mu.Unlock()
			continue
		}

		reconnectCount = 0
		c.logger.Info("重连成功")
	}
}

func (c *DouyuClient) readLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.done:
			return nil
		default:
		}

		conn := c.getConn()
		if conn == nil {
			return fmt.Errorf("连接不可用")
		}
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		frameType, body, err := c.readFrame(conn)

		if err != nil {
			return fmt.Errorf("读取帧失败: %w", err)
		}

		if frameType != msgTypeServer {
			continue
		}

		fields := parseSTT(body)
		msgType := fields["type"]
		if msgType == "" {
			continue
		}

		switch msgType {
		case "chatmsg":
			nn := fields["nn"]
			txt := fields["txt"]
			if nn != "" && txt != "" && c.onDanmaku != nil {
				col := parseDouyuColor(fields["col"])
				c.handleDanmakuSafe(nn, txt, col)
			}
		case "dgb":
			if c.onGift != nil {
				nn := fields["nn"]
				gfn := fields["gfn"]
				gfcnt := fields["gfcnt"]
				if nn != "" && gfn != "" {
					num := 1
					if n, err := strconv.Atoi(gfcnt); err == nil && n > 0 {
						num = n
					}
					c.handleGiftSafe(nn, gfn, num)
				}
			}
		case "pingreq":
			c.sendKeepAlive()
		case "kickmsg":
			c.logger.Warn("被服务器踢出")
			return fmt.Errorf("kicked by server")
		case "error":
			c.logger.Warnf("服务器错误: %s", string(body))
			return fmt.Errorf("server error: %s", string(body))
		}
	}
}

func (c *DouyuClient) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-ticker.C:
			c.sendKeepAlive()
		}
	}
}

func (c *DouyuClient) sendKeepAlive() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running || c.conn == nil {
		return
	}
	c.sendFrame(encodeSTT("type", "keeplive", "tick", fmt.Sprintf("%d", time.Now().UnixMilli())))
}

func (c *DouyuClient) sendLogin() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running || c.conn == nil {
		return fmt.Errorf("client not running")
	}
	return c.sendFrame(encodeSTT("type", "loginreq", "roomid", c.roomID))
}

func (c *DouyuClient) sendJoinGroup() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running || c.conn == nil {
		return fmt.Errorf("client not running")
	}
	return c.sendFrame(encodeSTT("type", "joingroup", "rid", c.roomID, "gid", "-9999"))
}

func (c *DouyuClient) sendFrame(data []byte) error {
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.conn.Write(data)
	return err
}

func (c *DouyuClient) readOneFrame() ([]byte, error) {
	conn := c.getConn()
	if conn == nil {
		return nil, fmt.Errorf("client not running")
	}
	conn.SetReadDeadline(time.Now().Add(readTimeout))
	_, body, err := c.readFrame(conn)
	return body, err
}

// readFrame reads a single Douyu frame from conn.
// The caller must have set the read deadline on conn.
func (c *DouyuClient) readFrame(conn net.Conn) (int, []byte, error) {
	header := make([]byte, 12)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, nil, err
	}
	length := binary.LittleEndian.Uint32(header[0:4])
	msgType := int(binary.LittleEndian.Uint32(header[8:12]))
	bodyLen := int(length) - 8
	if bodyLen < 0 || bodyLen > 100000 {
		return msgType, nil, fmt.Errorf("invalid body length: %d", bodyLen)
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		return msgType, nil, err
	}
	if len(body) > 0 && body[len(body)-1] == 0 {
		body = body[:len(body)-1]
	}
	return msgType, body, nil
}

// encodeSTT encodes key-value pairs into Douyu STT binary frame.
// Each pair is "key@=value/", the whole message ends with "\x00".
// The frame is wrapped with a 12-byte header: length(4) + length(4) + type(4).
func encodeSTT(pairs ...string) []byte {
	if len(pairs)%2 != 0 {
		pairs = pairs[:len(pairs)-1]
	}
	var sb strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		sb.WriteString(pairs[i])
		sb.WriteString("@=")
		sb.WriteString(escapeSTTValue(pairs[i+1]))
		sb.WriteByte('/')
	}
	sb.WriteByte(0)
	body := []byte(sb.String())
	length := uint32(len(body) + 8)
	buf := make([]byte, 12+len(body))
	binary.LittleEndian.PutUint32(buf[0:4], length)
	binary.LittleEndian.PutUint32(buf[4:8], length)
	binary.LittleEndian.PutUint32(buf[8:12], msgTypeClient)
	copy(buf[12:], body)
	return buf
}

// parseSTT parses Douyu STT-encoded body into a map.
// Format: "key1@=value1/key2@=value2/"
func parseSTT(body []byte) map[string]string {
	result := make(map[string]string)
	s := string(body)
	if s == "" {
		return result
	}
	segments := strings.Split(s, "/")
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		idx := strings.Index(seg, "@=")
		if idx < 0 {
			continue
		}
		key := seg[:idx]
		val := unescapeSTTValue(seg[idx+2:])
		if key != "" {
			result[key] = val
		}
	}
	return result
}

func escapeSTTValue(s string) string {
	s = strings.ReplaceAll(s, "@", "@A")
	s = strings.ReplaceAll(s, "/", "@S")
	return s
}

func unescapeSTTValue(s string) string {
	s = strings.ReplaceAll(s, "@@", "\x00") // 先处理 @@，避免 @A 被错误匹配
	s = strings.ReplaceAll(s, "@A", "@")
	s = strings.ReplaceAll(s, "@S", "/")
	s = strings.ReplaceAll(s, "\x00", "@")
	return s
}

// parseDouyuColor maps Douyu color index to RGB color.
// Douyu uses small integers as color indices. Values are the softer colors
// actually used by the Douyu client, not fully saturated primaries.
func parseDouyuColor(col string) int {
	switch col {
	case "1": // red    #FF6B6B
		return 0xFF6B6B
	case "2": // orange #FFA940
		return 0xFFA940
	case "3": // green  #6BD66B
		return 0x6BD66B
	case "4": // yellow #FFD666
		return 0xFFD666
	case "5": // purple #CC77FF
		return 0xCC77FF
	case "6": // cyan   #66DFFF
		return 0x66DFFF
	default: // white  #FFFFFF
		return 16777215
	}
}

// handleDanmakuSafe 带 panic 恢复的弹幕处理
func (c *DouyuClient) handleDanmakuSafe(username, content string, color int) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Errorf("弹幕处理 panic: %v", r)
		}
	}()
	c.onDanmaku(username, content, color)
}

// handleGiftSafe 带 panic 恢复的礼物处理
func (c *DouyuClient) handleGiftSafe(username, giftName string, num int) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Errorf("礼物处理 panic: %v", r)
		}
	}()
	c.onGift(username, giftName, num)
}
