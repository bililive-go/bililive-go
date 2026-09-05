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
	scrollTimeMs int // 滚动总毫秒数
	bannerSpeed  int // ASS Banner speed (ms per pixel)
	laneStart    int // first usable lane index
	laneEnd      int // last usable lane index (exclusive)
	laneNum      int // total lanes in the usable range
	nextLane     int
	laneLast     []int64 // last tail-clear time (centiseconds) per lane

	maxLaneDelayCS int64 // 弹幕排队等待 lane 的最大延迟（厘秒）
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
	bannerSpeed := scrollTimeMs / resX
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
		laneStart = totalLanes / 2
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
		scrollTimeMs: scrollTimeMs,
		bannerSpeed:  bannerSpeed,
		laneStart:    laneStart,
		laneEnd:      laneEnd,
		laneNum:      laneNum,
		nextLane:     0,
		laneLast:     make([]int64, laneNum),
		// 排队延迟最多不超过一个完整滚动周期，避免弹幕高峰期间延迟无限累积，
		// 导致 ass 文件末尾的时间戳远超实际录制时长 (#1178)。
		maxLaneDelayCS: int64(scrollTimeMs) / 10,
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
	opacity := 128 // default
	if w.cfg.Opacity != nil {
		opacity = *w.cfg.Opacity
	}
	outline := 1 // default
	if w.cfg.Outline != nil {
		outline = *w.cfg.Outline
	}
	assAlpha := 255 - opacity
	backColor := fmt.Sprintf("&H%02X000000&", assAlpha)
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
Style: Danmaku,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,0,0,0,0,100,100,0,0,1,%d,0,8,0,0,0,1
Style: Gift,%s,%d,&H0000D4FF,&H000000FF,&H00000000,%s,0,0,0,0,100,100,0,0,1,%d,0,8,0,0,0,1
Style: Guard,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,1,0,1,0,0,60,1
Style: SC2,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,1,0,1,0,0,100,1
Style: SC30,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,1,0,1,0,0,100,1
Style: SC50,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,1,0,1,0,0,100,1
Style: SC100,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,1,0,1,0,0,100,1
Style: SC200,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,1,0,1,0,0,100,1
Style: SC500,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,1,0,1,0,0,100,1
Style: SC1000,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,1,0,1,0,0,100,1
Style: SC2000,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,1,0,1,0,0,100,1
Style: SCDefault,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,%s,1,0,0,0,100,100,0,0,3,1,0,1,0,0,100,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`, w.title, w.resX, w.resY,
		w.cfg.FontName, w.cfg.FontSize, backColor, outline,
		w.cfg.FontName, w.cfg.FontSize-6, backColor, outline,
		w.cfg.FontName, w.cfg.FontSize, guardBackColor,
		w.cfg.FontName, w.cfg.FontSize, sc2,
		w.cfg.FontName, w.cfg.FontSize, sc30,
		w.cfg.FontName, w.cfg.FontSize, sc50,
		w.cfg.FontName, w.cfg.FontSize, sc100,
		w.cfg.FontName, w.cfg.FontSize, sc200,
		w.cfg.FontName, w.cfg.FontSize, sc500,
		w.cfg.FontName, w.cfg.FontSize, sc1000,
		w.cfg.FontName, w.cfg.FontSize, sc2000,
		w.cfg.FontName, w.cfg.FontSize, scDefault)
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

	fullText := username + ": " + text
	textWidth := w.estimateTextWidth(fullText)
	totalDistance := w.resX + textWidth
	// 使用 scrollTimeMs 精确计算，避免 bannerSpeed 整数截断
	durationCS := int64(w.scrollTimeMs) * int64(totalDistance) / int64(w.resX) / 10
	if durationCS < 200 {
		durationCS = 200
	}
	endCS := startCS + durationCS

	lane, adjustedStartCS := w.assignLane(startCS, textWidth)
	// 基于文字宽度的防重叠：使用调整后的起始时间
	if adjustedStartCS != startCS {
		startCS = adjustedStartCS
		endCS = startCS + durationCS
	}
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

	var fullText string
	if coinType == "gold" && price > 0 {
		totalPrice := float64(price) * float64(num) / 1000.0
		fullText = fmt.Sprintf("[礼物 ¥%.1f] %s 赠送 %s x%d", totalPrice, username, giftName, num)
	} else {
		fullText = fmt.Sprintf("%s 赠送 %s x%d", username, giftName, num)
	}
	textWidth := w.estimateTextWidth(fullText)
	totalDistance := w.resX + textWidth
	durationCS := int64(w.scrollTimeMs) * int64(totalDistance) / int64(w.resX) / 10
	if durationCS < 200 {
		durationCS = 200
	}
	endCS := startCS + durationCS

	lane, adjustedStartCS := w.assignLane(startCS, textWidth)
	// 基于文字宽度的防重叠：使用调整后的起始时间
	if adjustedStartCS != startCS {
		startCS = adjustedStartCS
		endCS = startCS + durationCS
	}
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

	fullText := fmt.Sprintf("[%s ¥%d] %s 开通了%s", giftName, price/1000, username, giftName)
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

	fullText := fmt.Sprintf("[SC ¥%d] %s: %s", price, username, text)
	alignment, marginV := positionToAlignment(w.cfg.ScPosition, 100)
	styleName := scTierStyle(price)
	line := fmt.Sprintf("Dialogue: 1,%s,%s,%s,,0,0,%d,,{\\an%d}{\\q0}%s\n",
		formatTime(startCS), formatTime(endCS), styleName, marginV, alignment, escapeText(fullText))
	if _, err := w.file.WriteString(line); err != nil {
		w.writeErr = true
	}
}

// assignLane 分配一个空闲 lane，基于文字宽度的防重叠。
// 参数：startCS 弹幕开始时间，textWidth 文字像素宽度。
// 返回值：lane 索引、调整后的 startCS（可能延迟）。
func (w *AssWriter) assignLane(startCS int64, textWidth int) (int, int64) {
	// 安全间距：防止因字体渲染差异导致的重叠
	safeTextWidth := textWidth + w.cfg.FontSize
	// 优先找空闲 lane（前一条弹幕的尾部已离开屏幕右侧）
	for i := 0; i < w.laneNum; i++ {
		idx := (w.nextLane + i) % w.laneNum
		if w.laneLast[idx] <= startCS {
			// 存储该弹幕尾部离开右侧的时间点（用于下一条弹幕判断）
			tailClearCS := startCS + int64(w.scrollTimeMs)*int64(safeTextWidth)/int64(w.resX)/10
			w.laneLast[idx] = tailClearCS
			w.nextLane = (idx + 1) % w.laneNum
			return idx, startCS
		}
	}
	// 所有 lane 都被占用，延迟到最早可用的时间点
	earliest := 0
	for i := 1; i < w.laneNum; i++ {
		if w.laneLast[i] < w.laneLast[earliest] {
			earliest = i
		}
	}
	newStartCS := w.laneLast[earliest]
	// 弹幕高峰持续超过屏幕容量时，排队延迟会不断累积。一旦超过上限就放弃排队，
	// 直接按实际到达时间显示（允许短暂重叠），防止延迟随录制时长无限增长。
	if newStartCS-startCS > w.maxLaneDelayCS {
		newStartCS = startCS
	}
	// 存储新弹幕尾部离开右侧的时间点
	tailClearCS := newStartCS + int64(w.scrollTimeMs)*int64(safeTextWidth)/int64(w.resX)/10
	w.laneLast[earliest] = tailClearCS
	w.nextLane = (earliest + 1) % w.laneNum
	return earliest, newStartCS
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
