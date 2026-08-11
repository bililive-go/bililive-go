package douyin

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

//go:embed sign.js
var signJSCode string

var (
	reRoomID  = regexp.MustCompile(`"room_id"\s*:\s*(\d+)`)
	reRoomID2 = regexp.MustCompile(`roomId\\?":\\?"(\d+)`)
)

// signJSProgram 预编译的 sign.js 字节码，避免每次签名都重新解析 485KB 脚本
var signJSProgram *goja.Program
var signJSOnce sync.Once
var signJSErr error

func getCompiledSignJS() (*goja.Program, error) {
	signJSOnce.Do(func() {
		if signJSCode == "" {
			signJSErr = fmt.Errorf("sign.js 未加载")
			return
		}
		signJSProgram, signJSErr = goja.Compile("sign.js", signJSCode, true)
	})
	return signJSProgram, signJSErr
}

// DouyinClient 抖音弹幕 WebSocket 客户端
type DouyinClient struct {
	roomID    string
	cookies   string
	conn      *websocket.Conn
	onDanmaku func(username, content string)
	onGift    func(username, giftName string, num int)
	done      chan struct{}
	closeOnce sync.Once
	workers   sync.WaitGroup
	logger    *logrus.Entry
	mu        sync.Mutex
	running   bool
	seqId     uint64
}

// NewDouyinClient 创建新的抖音弹幕客户端
func NewDouyinClient(roomID, cookies string, onDanmaku func(username, content string), onGift func(username, giftName string, num int), logger *logrus.Entry) *DouyinClient {
	return &DouyinClient{
		roomID:    roomID,
		cookies:   cookies,
		onDanmaku: onDanmaku,
		onGift:    onGift,
		done:      make(chan struct{}),
		logger:    logger,
	}
}

// Start 启动 WebSocket 连接和消息循环
func (c *DouyinClient) Start(ctx context.Context) error {
	// 获取 ttwid
	ttwid := getTtwidFromCookies(c.cookies)
	if ttwid == "" {
		var err error
		ttwid, err = fetchTtwid(c.logger)
		if err != nil {
			return fmt.Errorf("获取 ttwid 失败: %w", err)
		}
	}

	// 从页面获取真实 roomId
	realRoomID, err := fetchRealRoomID(c.roomID, c.cookies, c.logger)
	if err != nil {
		c.logger.WithError(err).Warn("获取真实 roomId 失败，使用原始 roomID")
		realRoomID = c.roomID
	}

	// 生成 user_unique_id
	userUniqueID := generateUserUniqueID()

	// 构建 WebSocket URL
	wsURL := buildWSURL(realRoomID, ttwid, userUniqueID)

	// 生成 signature
	signature, err := generateSignature(wsURL, c.logger)
	if err != nil {
		c.logger.WithError(err).Warn("生成 signature 失败")
		return fmt.Errorf("生成 signature 失败: %w", err)
	}
	wsURL += "&signature=" + signature

	c.logger.Infof("连接抖音弹幕服务器: roomID=%s (real=%s)", c.roomID, realRoomID)

	// 构建请求头
	header := http.Header{}
	header.Set("User-Agent", userAgent)
	header.Set("Origin", "https://live.douyin.com")
	cookieVal := "ttwid=" + ttwid
	if c.cookies != "" {
		cookieVal = c.cookies
		if !strings.Contains(cookieVal, "ttwid=") {
			cookieVal += "; ttwid=" + ttwid
		}
	}
	header.Set("Cookie", cookieVal)

	// 连接 WebSocket
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil {
			c.logger.Errorf("WebSocket 连接被拒绝: status=%d, handshake-msg=%s",
				resp.StatusCode, resp.Header.Get("Handshake-Msg"))
		}
		return fmt.Errorf("WebSocket 连接失败: %w", err)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	c.conn = conn
	c.running = true
	c.logger.Info("WebSocket 连接成功")

	// 启动消息读取循环（带重连）
	c.workers.Add(1)
	go func() {
		defer c.workers.Done()
		c.readLoopWithReconnect(ctx, realRoomID, ttwid, userUniqueID)
	}()

	// 启动心跳
	c.workers.Add(1)
	go func() {
		defer c.workers.Done()
		c.heartbeatLoop(ctx)
	}()

	return nil
}

// Stop 停止客户端
func (c *DouyinClient) Stop() {
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

// readLoopWithReconnect 带重连的消息读取循环
func (c *DouyinClient) readLoopWithReconnect(ctx context.Context, realRoomID, ttwid, userUniqueID string) {
	defer func() {
		c.mu.Lock()
		c.running = false
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

		// 重新构建 URL 并连接
		wsURL := buildWSURL(realRoomID, ttwid, userUniqueID)
		signature, signErr := generateSignature(wsURL, c.logger)
		if signErr != nil {
			c.logger.WithError(signErr).Warn("重连时生成 signature 失败")
			continue
		}
		wsURL += "&signature=" + signature

		header := http.Header{}
		header.Set("User-Agent", userAgent)
		header.Set("Origin", "https://live.douyin.com")
		cookieVal := "ttwid=" + ttwid
		if c.cookies != "" {
			cookieVal = c.cookies
			if !strings.Contains(cookieVal, "ttwid=") {
				cookieVal += "; ttwid=" + ttwid
			}
		}
		header.Set("Cookie", cookieVal)

		conn, resp, dialErr := websocket.DefaultDialer.Dial(wsURL, header)
		if dialErr != nil {
			c.logger.WithError(dialErr).Warn("重连失败")
			continue
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}

		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
		}
		c.conn = conn
		c.mu.Unlock()

		reconnectCount = 0
		c.logger.Info("重连成功")
	}
}

// getConn 安全获取当前连接引用，不持锁执行 I/O。
// 当客户端已停止时返回 nil，避免读取已关闭的连接触发无意义的重连。
func (c *DouyinClient) getConn() *websocket.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil
	}
	return c.conn
}

// readLoop 消息读取循环
func (c *DouyinClient) readLoop(ctx context.Context) error {
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
			return nil
		}

		conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.logger.Info("WebSocket 连接被服务端关闭")
				return fmt.Errorf("remote close: %w", err)
			}
			return fmt.Errorf("读取消息失败: %w", err)
		}

		// 解析 PushFrame
		frame := &PushFrame{}
		if err := frame.Unmarshal(message); err != nil {
			c.logger.WithError(err).Debug("解析 PushFrame 失败")
			continue
		}

		if len(frame.Payload) == 0 {
			continue
		}

		// GZIP 解压
		decompressed, err := gzipDecompress(frame.Payload)
		if err != nil {
			c.logger.WithError(err).Debug("GZIP 解压失败")
			continue
		}

		// 解析 Response
		resp := &Response{}
		if err := resp.Unmarshal(decompressed); err != nil {
			c.logger.WithError(err).Debug("解析 Response 失败")
			continue
		}

		// ACK 确认
		if resp.NeedAck {
			c.sendACK(frame, resp.InternalExt)
		}

		// 遍历消息
		for _, msg := range resp.MessagesList {
			c.handleMessageSafe(msg)
		}
	}
}

// handleMessageSafe 带 panic 恢复的消息处理
func (c *DouyinClient) handleMessageSafe(msg *Message) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Errorf("消息处理 panic: %v", r)
		}
	}()
	c.handleMessage(msg)
}

// handleMessage 处理单条消息
func (c *DouyinClient) handleMessage(msg *Message) {
	switch msg.Method {
	case "WebcastChatMessage":
		c.handleChatMessage(msg.Payload)
	case "WebcastGiftMessage":
		c.handleGiftMessage(msg.Payload)
	}
}

func (c *DouyinClient) handleChatMessage(payload []byte) {
	chatMsg := &ChatMessage{}
	if err := chatMsg.Unmarshal(payload); err != nil {
		c.logger.WithError(err).Debug("解析 ChatMessage 失败")
		return
	}

	username := ""
	content := chatMsg.Content

	if chatMsg.User != nil {
		username = chatMsg.User.Nickname
	}

	if username == "" {
		username = "未知用户"
	}

	if content != "" && c.onDanmaku != nil {
		c.onDanmaku(username, content)
	}
}

func (c *DouyinClient) handleGiftMessage(payload []byte) {
	giftMsg := &GiftMessage{}
	if err := giftMsg.Unmarshal(payload); err != nil {
		c.logger.WithError(err).Debug("解析 GiftMessage 失败")
		return
	}

	if c.onGift == nil {
		return
	}

	username := ""
	if giftMsg.User != nil {
		username = giftMsg.User.Nickname
	}
	if username == "" {
		username = "未知用户"
	}

	giftName := ""
	if giftMsg.Gift != nil {
		giftName = giftMsg.Gift.Name
	}
	if giftName == "" {
		giftName = "礼物"
	}

	num := int(giftMsg.RepeatCount)
	if num < 1 {
		num = 1
	}

	c.onGift(username, giftName, num)
}

// heartbeatLoop 心跳循环（每 5 秒发送一次）
func (c *DouyinClient) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-ticker.C:
			c.sendHeartbeat()
		}
	}
}

// sendHeartbeat 发送心跳帧（BinaryMessage，payloadType='hb'）
func (c *DouyinClient) sendHeartbeat() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running || c.conn == nil {
		return
	}

	hbFrame := &PushFrame{
		SeqId:       c.nextSeqId(),
		LogId:       uint64(time.Now().UnixMilli()),
		PayloadType: "hb",
	}

	data, err := hbFrame.Marshal()
	if err != nil {
		return
	}

	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	c.conn.WriteMessage(websocket.BinaryMessage, data)
}

// sendACK 发送 ACK 确认帧
func (c *DouyinClient) sendACK(frame *PushFrame, internalExt string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running || c.conn == nil {
		return
	}

	ackFrame := &PushFrame{
		LogId:       frame.LogId,
		PayloadType: "ack",
	}
	if internalExt != "" {
		ackFrame.Payload = []byte(internalExt)
	}

	data, err := ackFrame.Marshal()
	if err != nil {
		return
	}

	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	c.conn.WriteMessage(websocket.BinaryMessage, data)
}

// nextSeqId 生成下一个序列号
func (c *DouyinClient) nextSeqId() uint64 {
	c.seqId++
	return c.seqId
}

// gzipDecompress GZIP 解压
func gzipDecompress(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("创建 gzip reader: %w", err)
	}
	defer reader.Close()

	result, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("读取解压数据: %w", err)
	}

	return result, nil
}

// fetchRealRoomID 从直播页面获取真实的 roomId
func fetchRealRoomID(roomID, cookies string, logger *logrus.Entry) (string, error) {
	url := "https://live.douyin.com/" + roomID

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return roomID, err
	}
	req.Header.Set("User-Agent", userAgent)
	if cookies != "" {
		req.Header.Set("Cookie", cookies)
	}

	resp, err := client.Do(req)
	if err != nil {
		return roomID, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return roomID, err
	}

	// 从页面 JSON 中提取 roomId
	matches := reRoomID.FindSubmatch(body)
	if len(matches) >= 2 {
		realID := string(matches[1])
		if realID != "0" {
			logger.Infof("从页面解析到真实 roomId: %s (web_rid: %s)", realID, roomID)
			return realID, nil
		}
	}

	matches2 := reRoomID2.FindSubmatch(body)
	if len(matches2) >= 2 {
		realID := string(matches2[1])
		logger.Infof("从页面解析到真实 roomId: %s (web_rid: %s)", realID, roomID)
		return realID, nil
	}

	logger.Debug("页面中未找到 roomId，使用原始 roomID")
	return roomID, nil
}

// generateSignature 生成 WebSocket 连接签名
// 参考 DouyinLiveWebFetcher: 对特定参数 MD5 哈希后，调用 sign.js 的 get_sign 函数
func generateSignature(wssURL string, logger *logrus.Entry) (string, error) {
	if signJSCode == "" {
		return "", fmt.Errorf("sign.js 未加载")
	}

	// 提取需要签名的参数（按固定顺序）
	params := []string{"live_id", "aid", "version_code", "webcast_sdk_version",
		"room_id", "sub_room_id", "sub_channel_id", "did_rule",
		"user_unique_id", "device_platform", "device_type", "ac", "identity"}

	// 从 URL 中提取参数值
	queryStart := len(wssURL)
	if idx := len("wss://"); idx < len(wssURL) {
		if qIdx := strings.Index(wssURL[idx:], "?"); qIdx >= 0 {
			queryStart = idx + qIdx + 1
		}
	}
	query := wssURL[queryStart:]
	paramMap := make(map[string]string)
	for _, pair := range strings.Split(query, "&") {
		if kv := strings.SplitN(pair, "=", 2); len(kv) == 2 {
			paramMap[kv[0]] = kv[1]
		}
	}

	// 构建签名输入字符串
	var tplParams []string
	for _, p := range params {
		tplParams = append(tplParams, p+"="+paramMap[p])
	}
	paramStr := strings.Join(tplParams, ",")

	// MD5 哈希
	hash := md5.Sum([]byte(paramStr))
	md5Param := fmt.Sprintf("%x", hash)

	// 调用 sign.js 的 get_sign 函数
	program, err := getCompiledSignJS()
	if err != nil {
		return "", fmt.Errorf("编译 sign.js 失败: %w", err)
	}

	vm := goja.New()
	// sign.js 内部用无 var 的赋值（如 document = {}）声明全局对象，
	// 在 goja 中会因变量未声明报错。通过 this 赋值注入全局属性，
	// 确保预编译的 RunProgram 能正确访问。
	vm.Set("window", vm.GlobalObject())
	vm.Set("document", vm.NewObject())
	nav := vm.NewObject()
	nav.Set("userAgent", userAgent)
	vm.Set("navigator", nav)
	for _, g := range []string{"location", "screen", "history", "localStorage", "sessionStorage", "crypto", "performance"} {
		vm.Set(g, vm.NewObject())
	}
	for _, g := range []string{"Image", "WebSocket", "XMLHttpRequest", "fetch", "setTimeout", "setInterval", "clearTimeout", "clearInterval"} {
		vm.Set(g, vm.NewObject())
	}
	if _, err := vm.RunProgram(program); err != nil {
		return "", fmt.Errorf("执行 sign.js 失败: %w", err)
	}

	getSign, ok := goja.AssertFunction(vm.Get("get_sign"))
	if !ok {
		return "", fmt.Errorf("sign.js 中未找到 get_sign 函数")
	}

	result, err := getSign(goja.Undefined(), vm.ToValue(md5Param))
	if err != nil {
		return "", fmt.Errorf("调用 get_sign 失败: %w", err)
	}

	signature := result.String()
	if signature == "" {
		return "", fmt.Errorf("get_sign 返回空值")
	}

	return signature, nil
}
