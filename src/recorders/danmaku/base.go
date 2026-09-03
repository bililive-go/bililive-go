package danmaku

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/bililive-go/bililive-go/src/configs"
)

// DanmakuBroadcastCallback 弹幕实时广播回调函数类型
// 当设置时，每条弹幕/礼物/SC/舰长消息都会同时通过此回调广播
type DanmakuBroadcastCallback func(msgType, username, content string, extra map[string]interface{})

// baseRecorder 提供三个平台弹幕录制器的公共字段和方法。
type baseRecorder struct {
	mu          sync.Mutex
	running     bool
	count       int
	assWriter   *AssWriter
	outputFile  string
	cfg         configs.DanmakuConfig
	logger      *logrus.Entry
	startAt     time.Time
	broadcastCb DanmakuBroadcastCallback
}

func (b *baseRecorder) OutputFile() string {
	return b.outputFile
}

func (b *baseRecorder) GetCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

func (b *baseRecorder) IsRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

func (b *baseRecorder) GetStatus() map[string]interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()

	status := map[string]interface{}{
		"danmaku_running": b.running,
		"danmaku_count":   b.count,
		"danmaku_output":  b.outputFile,
	}
	if b.running {
		status["danmaku_start_time"] = b.startAt.Format(time.RFC3339)
	}
	return status
}

// stopBase 通用停止逻辑：标记停止、清空 writer、返回旧引用供调用方关闭。
func (b *baseRecorder) stopBase() *AssWriter {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return nil
	}
	b.running = false
	w := b.assWriter
	b.assWriter = nil
	b.mu.Unlock()
	return w
}

// SetBroadcastCallback 设置弹幕广播回调（用于 SSE 实时推送）
func (b *baseRecorder) SetBroadcastCallback(cb DanmakuBroadcastCallback) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.broadcastCb = cb
}

// addDanmaku 弹幕回调的通用处理：加锁、检查运行状态、写入 ASS、计数。
func (b *baseRecorder) addDanmaku(recvAt time.Time, username, content string, color int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running || b.assWriter == nil {
		return
	}
	b.assWriter.AddDanmaku(recvAt, username, content, color)
	b.count++
	if b.broadcastCb != nil {
		b.broadcastCb("danmaku", username, content, map[string]interface{}{
			"color":     color,
			"timestamp": recvAt.Unix(),
		})
	}
}

// addGift 礼物回调的通用处理。
func (b *baseRecorder) addGift(recvAt time.Time, username, giftName string, num int, price int, coinType string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running || b.assWriter == nil {
		return
	}
	b.assWriter.AddGift(recvAt, username, giftName, num, price, coinType)
	b.count++
	if b.broadcastCb != nil {
		b.broadcastCb("gift", username, giftName, map[string]interface{}{
			"gift_name": giftName,
			"num":       num,
			"price":     price,
			"coin_type": coinType,
			"timestamp": recvAt.Unix(),
		})
	}
}

// addSuperChat SC 回调的通用处理。
func (b *baseRecorder) addSuperChat(recvAt time.Time, username, message string, price int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running || b.assWriter == nil {
		return
	}
	b.assWriter.AddSuperChat(recvAt, username, message, price)
	b.count++
	if b.broadcastCb != nil {
		b.broadcastCb("super_chat", username, message, map[string]interface{}{
			"price":     price,
			"timestamp": recvAt.Unix(),
		})
	}
}

// addGuard 舰长回调的通用处理。
func (b *baseRecorder) addGuard(recvAt time.Time, username, giftName string, price int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running || b.assWriter == nil {
		return
	}
	b.assWriter.AddGuard(recvAt, username, giftName, price)
	b.count++
	if b.broadcastCb != nil {
		b.broadcastCb("guard", username, giftName, map[string]interface{}{
			"gift_name": giftName,
			"price":     price,
			"timestamp": recvAt.Unix(),
		})
	}
}
