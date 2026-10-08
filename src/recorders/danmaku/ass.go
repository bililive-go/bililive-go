package danmaku

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
)

// AssWriter writes danmaku entries to an ASS subtitle file.
type AssWriter struct {
	mu           sync.Mutex
	file         *os.File
	closed       bool
	writeErr     bool // 首次写入出错后置 true，后续跳过无意义写入
	startAt      time.Time
	cfg          configs.DanmakuConfig
	title        string
	resX         int
	resY         int
	bannerSpeed  int // ASS Banner speed (ms per pixel)，滚动速度的唯一口径
	laneStart    int // first usable lane index
	laneEnd      int // last usable lane index (exclusive)
	laneNum      int // total lanes in the usable range
	nextLane     int
	laneLast     []int64 // last tail-clear time (centiseconds) per lane
}

func parseResolution(res string) (int, int) {
	parts := strings.SplitN(res, "x", 2)
	if len(parts) != 2 {
		return 1920, 1080
	}
	x, err1 := strconv.Atoi(parts[0])
	y, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 1920, 1080
	}
	return x, y
}

func NewAssWriter(filePath string, startAt time.Time, cfg configs.DanmakuConfig, title string) (*AssWriter, error) {
	f, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create ass file: %w", err)
	}

	resX, resY := parseResolution(cfg.Resolution)
	scrollTimeMs := cfg.ScrollTime * 1000
	// ASS 的 Banner 速度只接受整数"毫秒/像素"，直接截断会让实际滚动比配置值快一个台阶
	// （1920 宽配 5 秒截成 2ms/px → 实际 3.84 秒，快 23%）。四舍五入把偏差压到半步以内。
	bannerSpeed := (scrollTimeMs + resX/2) / resX
	if bannerSpeed < 1 {
		bannerSpeed = 1
	}

	laneHeight := cfg.FontSize + 4
	totalLanes := resY / laneHeight

	// Determine usable lane range based on scroll_area
	laneStart := 0
	laneEnd := totalLanes
	area := cfg.ScrollArea
	if area == "" {
		area = "full"
	}
	switch area {
	case "top":
		laneEnd = totalLanes / 2
	case "bottom":
		// 上对齐（Alignment=8）下 MarginV 越大越靠下，起始车道取"首个整体落到屏幕
		// 中线以下"的那条（按像素中线取上整，而不是按车道数对半分）：跨中线车道若被
		// 分进下半区，web 预览按 marginV/resY >= 0.5 过滤时会整条丢掉。
		laneStart = (resY/2 + laneHeight - 1) / laneHeight
	case "quarter":
		laneEnd = totalLanes / 4
	case "three-quarter":
		laneEnd = totalLanes * 3 / 4
	}
	laneNum := laneEnd - laneStart
	if laneNum < 1 {
		laneNum = 1
	}

	w := &AssWriter{
		file:         f,
		startAt:      startAt,
		cfg:          cfg,
		title:        title,
		resX:         resX,
		resY:         resY,
		bannerSpeed:  bannerSpeed,
		laneStart:    laneStart,
		laneEnd:      laneEnd,
		laneNum:      laneNum,
		nextLane:     0,
		laneLast:     make([]int64, laneNum),
	}

	if err := w.writeHeader(); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// scTierColor 返回 SC 价位对应的 ASS 背景色 (B站原始配色)
func scTierColor(price int) string {
	switch {
	case price >= 2000:
		return "&H80E73CC6" // 紫色 #C678F5
	case price >= 1000:
		return "&H8030CAFF" // 橙金 #FFCA30
	case price >= 500:
		return "&H80396AFF" // 红色 #FF6A39
	case price >= 200:
		return "&H8032A0FF" // 橙色 #FFA032
	case price >= 100:
		return "&H804BB6E3" // 金色 #E3B64B
	case price >= 50:
		return "&H80C7C34F" // 青色 #4FC3C7
	case price >= 30:
		return "&H80B2602A" // 蓝色 #2A60B2
	case price >= 2:
		return "&H80BFBFBF" // 浅灰 #BFBFBF
	default:
		return "&H8014A500" // 默认绿色 #00A514
	}
}

// scTierStyle 返回 SC 价位对应的 ASS 样式名
func scTierStyle(price int) string {
	switch {
	case price >= 2000:
		return "SC2000"
	case price >= 1000:
		return "SC1000"
	case price >= 500:
		return "SC500"
	case price >= 200:
		return "SC200"
	case price >= 100:
		return "SC100"
	case price >= 50:
		return "SC50"
	case price >= 30:
		return "SC30"
	case price >= 2:
		return "SC2"
	default:
		return "SCDefault"
	}
}

func (w *AssWriter) writeHeader() error {
	opacity := 255 // default：文字完全不透明
	if w.cfg.Opacity != nil {
		opacity = *w.cfg.Opacity
	}
	// 房间级弹幕配置不走启动校验，手改出界会写出 &H-91FFFFFF 这类畸形色值
	if opacity < 0 {
		opacity = 0
	} else if opacity > 255 {
		opacity = 255
	}
	outline := 1 // default
	if w.cfg.Outline != nil {
		outline = *w.cfg.Outline
	}
	assAlpha := 255 - opacity
	// opacity 语义是"文字不透明度"，必须落在文字颜色 PrimaryColour 的 alpha 位上。
	// 写进 BackColour 是无效的：Danmaku/Gift 用 BorderStyle=1 且 Shadow=0，
	// libass 在该组合下根本不绘制 BackColour，配置项调多少渲染结果都不变。
	// 行内的 \c 覆盖只替换 RGB 三分量，alpha 仍继承样式，所以滚动弹幕一并生效。
	// SC/上舰是带固定配色色块的运营消息，其色块 alpha 已按 B 站原配色写死，
	// 再套文字透明度会让白字在彩色底块上发灰，故这两类样式保持不透明。
	danmakuColor := fmt.Sprintf("&H%02XFFFFFF", assAlpha)
	giftColor := fmt.Sprintf("&H%02X00D4FF", assAlpha)
	// 描边必须跟着一起变透明：BorderStyle=1 下 OutlineColour 决定文字外轮廓，
	// 只虚化填充会留下一圈实心黑边，opacity=0 也画不出"完全透明"。
	outlineColor := fmt.Sprintf("&H%02X000000", assAlpha)
	guardBackColor := "&H800080FF"

	// SC 各价位背景色 (B站原始配色)
	sc2 := scTierColor(2)
	sc30 := scTierColor(30)
	sc50 := scTierColor(50)
	sc100 := scTierColor(100)
	sc200 := scTierColor(200)
	sc500 := scTierColor(500)
	sc1000 := scTierColor(1000)
	sc2000 := scTierColor(2000)
	scDefault := scTierColor(0)

	header := fmt.Sprintf(`[Script Info]
Title: %s
ScriptType: v4.00+
WrapStyle: 2
ScaledBorderAndShadow: yes
PlayResX: %d
PlayResY: %d

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Danmaku,%s,%d,%s,&H000000FF,%s,&H00000000,0,0,0,0,100,100,0,0,1,%d,0,8,0,0,0,1
Style: Gift,%s,%d,%s,&H000000FF,%s,&H00000000,0,0,0,0,100,100,0,0,1,%d,0,8,0,0,0,1
Style: Guard,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,%d,0,1,0,0,60,1
Style: SC2,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,%d,0,1,0,0,100,1
Style: SC30,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,%d,0,1,0,0,100,1
Style: SC50,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,%d,0,1,0,0,100,1
Style: SC100,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,%d,0,1,0,0,100,1
Style: SC200,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,%d,0,1,0,0,100,1
Style: SC500,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,%d,0,1,0,0,100,1
Style: SC1000,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,%d,0,1,0,0,100,1
Style: SC2000,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,%d,0,1,0,0,100,1
Style: SCDefault,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,%d,0,1,0,0,100,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`, w.title, w.resX, w.resY,
		w.cfg.FontName, w.cfg.FontSize, danmakuColor, outlineColor, outline,
		w.cfg.FontName, w.cfg.FontSize-6, giftColor, outlineColor, outline,
		w.cfg.FontName, w.cfg.FontSize, guardBackColor, outline,
		w.cfg.FontName, w.cfg.FontSize, sc2, outline,
		w.cfg.FontName, w.cfg.FontSize, sc30, outline,
		w.cfg.FontName, w.cfg.FontSize, sc50, outline,
		w.cfg.FontName, w.cfg.FontSize, sc100, outline,
		w.cfg.FontName, w.cfg.FontSize, sc200, outline,
		w.cfg.FontName, w.cfg.FontSize, sc500, outline,
		w.cfg.FontName, w.cfg.FontSize, sc1000, outline,
		w.cfg.FontName, w.cfg.FontSize, sc2000, outline,
		w.cfg.FontName, w.cfg.FontSize, scDefault, outline)
	_, err := w.file.WriteString(header)
	return err
}

func (w *AssWriter) estimateTextWidth(text string) int {
	width := 0
	for _, r := range text {
		if r > 0x7F {
			width += w.cfg.FontSize
		} else {
			width += w.cfg.FontSize / 2
		}
	}
	return width
}

// travelCS 返回按 bannerSpeed 滚动 distancePx 像素所需的厘秒数。
// 时长与车道净空都必须走这里：libass 只会执行量化后的整数 ms/px，
// 若改用 scrollTimeMs/resX 的精确口径，事件会比文字实际离开屏幕早/晚结束，
// 弹幕在半屏处被切断，车道预留时间也与画面不符。
func (w *AssWriter) travelCS(distancePx int) int64 {
	return int64(w.bannerSpeed) * int64(distancePx) / 10
}

// AddDanmaku appends a single scrolling danmaku line.
func (w *AssWriter) AddDanmaku(recvAt time.Time, username, text string, color int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.writeErr {
		return
	}

	elapsed := recvAt.Sub(w.startAt)
	startCS := int64(elapsed / (10 * time.Millisecond))
	if startCS < 0 {
		startCS = 0
	}

	fullText := text
	if username != "" {
		fullText = username + ": " + text
	}
	textWidth := w.estimateTextWidth(fullText)
	totalDistance := w.resX + textWidth
	durationCS := w.travelCS(totalDistance)
	if durationCS < 200 {
		durationCS = 200
	}
	endCS := startCS + durationCS

	lane := w.assignLane(startCS, textWidth)
	laneHeight := w.cfg.FontSize + 4
	marginV := (lane + w.laneStart) * laneHeight

	if color <= 0 {
		color = 16777215
	}
	assColor := rgbToAssColor(color)

	line := fmt.Sprintf("Dialogue: 0,%s,%s,Danmaku,,0,0,%d,Banner;%d;0;30,{\\c%s}%s\n",
		formatTime(startCS), formatTime(endCS), marginV, w.bannerSpeed, assColor, escapeText(fullText))
	if _, err := w.file.WriteString(line); err != nil {
		w.writeErr = true
	}
}

// AddGift appends a gift message as a smaller scrolling line.
// price 为金瓜子单价，coinType 为 "gold"(付费) 或 "silver"(免费)。
// 仅 gold 类型且 price > 0 时显示金额（1 RMB = 1000 金瓜子）。
func (w *AssWriter) AddGift(recvAt time.Time, username, giftName string, num int, price int, coinType string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.writeErr {
		return
	}

	elapsed := recvAt.Sub(w.startAt)
	startCS := int64(elapsed / (10 * time.Millisecond))
	if startCS < 0 {
		startCS = 0
	}

	giftText := fmt.Sprintf("赠送 %s x%d", giftName, num)
	if username != "" {
		giftText = fmt.Sprintf("%s 赠送 %s x%d", username, giftName, num)
	}
	var fullText string
	if coinType == "gold" && price > 0 {
		totalPrice := float64(price) * float64(num) / 1000.0
		fullText = fmt.Sprintf("[礼物 ¥%.1f] %s", totalPrice, giftText)
	} else {
		fullText = giftText
	}
	textWidth := w.estimateTextWidth(fullText)
	totalDistance := w.resX + textWidth
	durationCS := w.travelCS(totalDistance)
	if durationCS < 200 {
		durationCS = 200
	}
	endCS := startCS + durationCS

	lane := w.assignLane(startCS, textWidth)
	laneHeight := w.cfg.FontSize + 4
	marginV := (lane + w.laneStart) * laneHeight

	line := fmt.Sprintf("Dialogue: 0,%s,%s,Gift,,0,0,%d,Banner;%d;0;30,%s\n",
		formatTime(startCS), formatTime(endCS), marginV, w.bannerSpeed, escapeText(fullText))
	if _, err := w.file.WriteString(line); err != nil {
		w.writeErr = true
	}
}

// positionToAlignment maps position string to ASS \an alignment value and margin.
// ASS numpad layout: 7=top-left, 8=top-center, 9=top-right, 4/5/6=middle, 1/2/3=bottom
func positionToAlignment(pos string, bottomMargin int) (alignment int, marginV int) {
	switch pos {
	case "top-left":
		return 7, 20
	case "top-right":
		return 9, 20
	case "bottom-right":
		return 3, bottomMargin
	default: // "bottom-left"
		return 1, bottomMargin
	}
}

// AddGuard appends a guard purchase message.
func (w *AssWriter) AddGuard(recvAt time.Time, username, giftName string, price int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.writeErr {
		return
	}

	elapsed := recvAt.Sub(w.startAt)
	startCS := int64(elapsed / (10 * time.Millisecond))
	if startCS < 0 {
		startCS = 0
	}
	endCS := startCS + 500 // 5 seconds

	fullText := fmt.Sprintf("[%s ¥%d] 开通了%s", giftName, price/1000, giftName)
	if username != "" {
		fullText = fmt.Sprintf("[%s ¥%d] %s 开通了%s", giftName, price/1000, username, giftName)
	}
	alignment, marginV := positionToAlignment(w.cfg.GuardPosition, 60)
	line := fmt.Sprintf("Dialogue: 1,%s,%s,Guard,,0,0,%d,,{\\an%d}{\\q0}%s\n",
		formatTime(startCS), formatTime(endCS), marginV, alignment, escapeText(fullText))
	if _, err := w.file.WriteString(line); err != nil {
		w.writeErr = true
	}
}

// AddSuperChat appends a Super Chat message.
func (w *AssWriter) AddSuperChat(recvAt time.Time, username, text string, price int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.writeErr {
		return
	}

	elapsed := recvAt.Sub(w.startAt)
	startCS := int64(elapsed / (10 * time.Millisecond))
	if startCS < 0 {
		startCS = 0
	}
	endCS := startCS + 500 // 5 seconds

	fullText := fmt.Sprintf("[SC ¥%d] %s", price, text)
	if username != "" {
		fullText = fmt.Sprintf("[SC ¥%d] %s: %s", price, username, text)
	}
	alignment, marginV := positionToAlignment(w.cfg.ScPosition, 100)
	styleName := scTierStyle(price)
	line := fmt.Sprintf("Dialogue: 1,%s,%s,%s,,0,0,%d,,{\\an%d}{\\q0}%s\n",
		formatTime(startCS), formatTime(endCS), styleName, marginV, alignment, escapeText(fullText))
	if _, err := w.file.WriteString(line); err != nil {
		w.writeErr = true
	}
}

// assignLane 分配一个 lane，基于文字宽度尽量避免重叠。
// 参数：startCS 弹幕开始时间，textWidth 文字像素宽度。
// 返回值：lane 索引。时间戳一律等于真实到达时刻，任何避让都只改垂直位置。
func (w *AssWriter) assignLane(startCS int64, textWidth int) int {
	// 安全间距：防止因字体渲染差异导致的重叠
	safeTextWidth := textWidth + w.cfg.FontSize
	// 优先找空闲 lane（前一条弹幕的尾部已离开屏幕右侧）
	for i := 0; i < w.laneNum; i++ {
		idx := (w.nextLane + i) % w.laneNum
		if w.laneLast[idx] <= startCS {
			// 存储该弹幕尾部离开右侧的时间点（用于下一条弹幕判断）
			w.laneLast[idx] = startCS + w.travelCS(safeTextWidth)
			w.nextLane = (idx + 1) % w.laneNum
			return idx
		}
	}
	// 所有 lane 都被占用：复用尾部最早离开的那条，允许同车道重叠。
	// 这里绝不能改成"推迟 startCS 等车道空闲"——每推迟一条就把该车道占用点再往前推一个
	// 净空时长，到达速率一旦超过车道吞吐就级联累积且永不收敛（录制 5 分钟能排出几小时的
	// 字幕），时间轴与视频彻底对不上。宁可重叠，也不伪造弹幕的到达时间。
	// 并列最小时从 nextLane 起取第一条，避免总是下标 0 胜出把整批弹幕叠在同一车道。
	earliest := w.nextLane
	minTail := w.laneLast[earliest]
	for i := 1; i < w.laneNum; i++ {
		idx := (w.nextLane + i) % w.laneNum
		if w.laneLast[idx] < minTail {
			minTail = w.laneLast[idx]
			earliest = idx
		}
	}
	// 占用点取两者较大：短弹幕不得把该车道上尚未滚完的长弹幕的占用时刻提前抹掉，
	// 否则这条车道会一直是最早空闲的，后续弹幕全部集中叠印上来。
	tail := startCS + w.travelCS(safeTextWidth)
	if minTail > tail {
		tail = minTail
	}
	w.laneLast[earliest] = tail
	w.nextLane = (earliest + 1) % w.laneNum
	return earliest
}

func (w *AssWriter) OutputPath() string {
	return w.file.Name()
}

func (w *AssWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

func formatTime(cs int64) string {
	h := cs / 360000
	m := (cs % 360000) / 6000
	s := (cs % 6000) / 100
	c := cs % 100
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, c)
}

func rgbToAssColor(rgb int) string {
	r := (rgb >> 16) & 0xFF
	g := (rgb >> 8) & 0xFF
	b := rgb & 0xFF
	return fmt.Sprintf("&H00%02X%02X%02X&", b, g, r)
}

func escapeText(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			result = append(result, '\\', '\\')
		case '{':
			result = append(result, '\\', '{')
		case '}':
			result = append(result, '\\', '}')
		case '\n':
			result = append(result, '\\', 'n')
		case '\r':
			// skip
		default:
			result = append(result, s[i])
		}
	}
	return string(result)
}
