package bilibili

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

var reUUID = regexp.MustCompile(`_uuid=(.+?);`)

// reCMD 从 JSON 中快速提取 cmd 字段，支持带冒号后缀的变体（如 "DANMU_MSG:some_param"）
var reCMD = regexp.MustCompile(`"cmd"\s*:\s*"([^"]+)"`)

// refreshThreshold 连续失败多少次后刷新 danmuInfo（token + host_list）
const refreshThreshold = 5

// Client B站弹幕 WebSocket 客户端
type Client struct {
	roomID  int
	cookies string
	logger  *logrus.Entry

	conn      *websocket.Conn
	mu        sync.Mutex
	running   bool
	done      chan struct{}
	closeOnce sync.Once
	workers   sync.WaitGroup
	// heartbeatCh 用于认证成功后立即触发第一次心跳
	heartbeatCh chan struct{}

	onDanmaku   func(DanmakuMsg)
	onGift      func(GiftMsg)
	onSuperChat func(SuperChatMsg)
	onGuardBuy  func(GuardBuyMsg)
}

// NewClient 创建新的 B站弹幕客户端
func NewClient(roomID int, cookies string, logger *logrus.Entry) *Client {
	return &Client{
		roomID:      roomID,
		cookies:     cookies,
		logger:      logger,
		done:        make(chan struct{}),
		heartbeatCh: make(chan struct{}, 1),
	}
}

// OnDanmaku 注册弹幕回调
func (c *Client) OnDanmaku(fn func(DanmakuMsg)) {
	c.onDanmaku = fn
}

// OnGift 注册礼物回调
func (c *Client) OnGift(fn func(GiftMsg)) {
	c.onGift = fn
}

// OnSuperChat 注册醒目留言回调
func (c *Client) OnSuperChat(fn func(SuperChatMsg)) {
	c.onSuperChat = fn
}

// OnGuardBuy 注册舰长购买回调
func (c *Client) OnGuardBuy(fn func(GuardBuyMsg)) {
	c.onGuardBuy = fn
}

// Start 启动连接
func (c *Client) Start() error {
	// 1. 解析真实 room_id
	realRoomID, err := GetRoomInit(c.roomID)
	if err != nil {
		c.logger.WithError(err).Warnf("room_init 失败，使用原始 roomID: %d", c.roomID)
		realRoomID = c.roomID
	}

	// 2. 获取 UID（可选）
	uid, _ := GetUID(c.cookies)

	// 3. 提取 buvid
	buvid := ""
	if match := reUUID.FindStringSubmatch(c.cookies); len(match) > 1 {
		buvid = match[1]
	}

	// 4. 获取弹幕服务器信息
	danmuInfo, err := GetDanmuInfo(realRoomID, c.cookies)
	if err != nil {
		return fmt.Errorf("getDanmuInfo: %w", err)
	}
	if len(danmuInfo.Data.HostList) == 0 {
		return fmt.Errorf("getDanmuInfo returned empty host list")
	}

	// 5. 连接 WebSocket（不发送 Cookie，认证通过 auth 包的 token 完成）
	header := http.Header{}
	header.Set("User-Agent", userAgent)

	var conn *websocket.Conn
	for _, host := range danmuInfo.Data.HostList {
		wsURL := fmt.Sprintf("wss://%s/sub", host.Host)
		conn, _, err = websocket.DefaultDialer.Dial(wsURL, header)
		if err == nil {
			break
		}
		c.logger.WithError(err).Warnf("连接 %s 失败，尝试下一个", wsURL)
	}
	if conn == nil {
		// 回退到默认地址
		conn, _, err = websocket.DefaultDialer.Dial("wss://broadcastlv.chat.bilibili.com/sub", header)
		if err != nil {
			return fmt.Errorf("所有弹幕服务器连接失败: %w", err)
		}
	}

	// 6. 发送认证包
	if err := c.sendAuth(conn, uid, buvid, realRoomID, danmuInfo.Data.Token); err != nil {
		conn.Close()
		return fmt.Errorf("发送认证包失败: %w", err)
	}

	c.conn = conn
	c.running = true
	c.logger.Infof("B站弹幕连接成功: roomID=%d (real=%d)", c.roomID, realRoomID)

	// 7. 启动消息循环和心跳
	c.workers.Add(2)
	go func() {
		defer c.workers.Done()
		c.readLoopWithReconnect(realRoomID, uid, buvid, danmuInfo)
	}()
	go func() {
		defer c.workers.Done()
		c.heartbeatLoop()
	}()

	return nil
}

// sendAuth 构建并发送认证包
func (c *Client) sendAuth(conn *websocket.Conn, uid int, buvid string, realRoomID int, token string) error {
	authBody, _ := json.Marshal(map[string]interface{}{
		"uid":      uid,
		"buvid":    buvid,
		"roomid":   realRoomID,
		"protover": 3,
		"platform": "danmuji",
		"type":     2,
		"key":      token,
	})
	authPkt := Packet{ProtocolVersion: ProtoPopularity, Operation: OpAuth, Body: authBody}
	return conn.WriteMessage(websocket.BinaryMessage, authPkt.Build())
}

// sendHeartbeat 发送一次心跳
func (c *Client) sendHeartbeat(conn *websocket.Conn) error {
	hbPkt := Packet{ProtocolVersion: ProtoPopularity, Operation: OpHeartBeat}
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(websocket.BinaryMessage, hbPkt.Build())
}

// Stop 停止客户端
func (c *Client) Stop() {
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

// heartbeatLoop 心跳循环（每 30 秒）
func (c *Client) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-c.heartbeatCh:
			// 认证成功后立即发送第一次心跳
			c.mu.Lock()
			if c.running && c.conn != nil {
				if err := c.sendHeartbeat(c.conn); err != nil {
					c.logger.WithError(err).Debug("心跳发送失败")
				}
			}
			c.mu.Unlock()
		case <-ticker.C:
			c.mu.Lock()
			if !c.running || c.conn == nil {
				c.mu.Unlock()
				return
			}
			err := c.sendHeartbeat(c.conn)
			c.mu.Unlock()
			if err != nil {
				c.logger.WithError(err).Debug("心跳发送失败")
			}
		}
	}
}

// readLoopWithReconnect 带重连的消息读取循环
func (c *Client) readLoopWithReconnect(realRoomID, uid int, buvid string, danmuInfo *DanmuInfoResponse) {
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()

	var reconnectCount int32
	header := http.Header{}
	header.Set("User-Agent", userAgent)
	stableCh := make(chan struct{}, 1)

	for {
		select {
		case <-c.done:
			return
		default:
		}

		err := c.readLoop(stableCh)
		if err == nil {
			return // 正常关闭
		}

		select {
		case <-c.done:
			return
		default:
		}

		// 等待一小段时间检查是否是稳定连接后的断开
		// 如果 readLoop 成功读取过消息（stableCh 有信号），重置计数
		select {
		case <-stableCh:
			atomic.StoreInt32(&reconnectCount, 0)
		default:
		}

		count := atomic.AddInt32(&reconnectCount, 1)

		// 连续失败 N 次后刷新 danmuInfo（token + host_list）
		if int(count)%refreshThreshold == 0 {
			c.logger.Infof("连续失败 %d 次，刷新弹幕服务器信息...", count)
			if newInfo, infoErr := GetDanmuInfo(realRoomID, c.cookies); infoErr == nil && len(newInfo.Data.HostList) > 0 {
				danmuInfo = newInfo
				c.logger.Info("弹幕服务器信息已刷新")
			} else if infoErr != nil {
				c.logger.WithError(infoErr).Warn("刷新弹幕服务器信息失败")
			}
		}

		// 线性退避，上限 60 秒
		delay := 3 * int(count)
		if delay > 60 {
			delay = 60
		}
		c.logger.Warnf("B站弹幕连接断开（%v），%d秒后第 %d 次重连...", err, delay, count)

		select {
		case <-c.done:
			return
		case <-time.After(time.Duration(delay) * time.Second):
		}

		// 重新连接
		var newConn *websocket.Conn
		for _, host := range danmuInfo.Data.HostList {
			wsURL := fmt.Sprintf("wss://%s/sub", host.Host)
			newConn, _, err = websocket.DefaultDialer.Dial(wsURL, header)
			if err == nil {
				break
			}
		}
		if newConn == nil {
			newConn, _, err = websocket.DefaultDialer.Dial("wss://broadcastlv.chat.bilibili.com/sub", header)
			if err != nil {
				c.logger.WithError(err).Warn("重连失败")
				continue
			}
		}

		// 发送认证包
		if err = c.sendAuth(newConn, uid, buvid, realRoomID, danmuInfo.Data.Token); err != nil {
			newConn.Close()
			c.logger.WithError(err).Warn("重连后发送认证包失败")
			continue
		}

		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
		}
		c.conn = newConn
		c.mu.Unlock()

		c.logger.Info("B站弹幕重连成功")
	}
}

// readLoop 消息读取循环
// stableCh 用于通知 readLoopWithReconnect 连接已稳定（成功读取到消息）
func (c *Client) readLoop(stableCh chan struct{}) error {
	firstMessage := true

	for {
		select {
		case <-c.done:
			return nil
		default:
		}

		c.mu.Lock()
		conn := c.conn
		if !c.running || conn == nil {
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()

		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.logger.Info("WebSocket 连接被服务端关闭")
				return fmt.Errorf("remote close: %w", err)
			}
			return fmt.Errorf("读取消息失败: %w", err)
		}

		// 首次成功读取消息，通知 readLoopWithReconnect 重置 reconnectCount
		if firstMessage {
			firstMessage = false
			select {
			case stableCh <- struct{}{}:
			default:
			}
		}

		packets, err := ParsePackets(data)
		if err != nil {
			c.logger.WithError(err).Debug("解析数据包失败")
			continue
		}

		for _, pkt := range packets {
			switch pkt.Operation {
			case OpNotification:
				c.handleMessageSafe(pkt.Body)
			case OpAuthReply:
				// 检查认证回复错误码
				if err := c.checkAuthReply(pkt.Body); err != nil {
					return err
				}
				// 认证成功后立即触发第一次心跳
				select {
				case c.heartbeatCh <- struct{}{}:
				default:
				}
			case OpHeartBeatReply:
				// 提取人气值（在线人数）
				if len(pkt.Body) >= 4 {
					popularity := binary.BigEndian.Uint32(pkt.Body[:4])
					c.logger.Debugf("在线人气: %d", popularity)
				}
			}
		}
	}
}

// checkAuthReply 检查认证回复，返回 error 表示认证失败（应触发重连）
func (c *Client) checkAuthReply(body []byte) error {
	code := gjson.GetBytes(body, "code").Int()
	if code == 0 {
		c.logger.Debug("认证成功")
		return nil
	}
	// code=-101 表示 token 过期，需要重新获取
	return fmt.Errorf("认证失败: code=%d", code)
}

// handleMessageSafe 带 panic 恢复的消息处理
func (c *Client) handleMessageSafe(data []byte) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Errorf("消息处理 panic: %v", r)
		}
	}()
	c.handleMessage(data)
}
