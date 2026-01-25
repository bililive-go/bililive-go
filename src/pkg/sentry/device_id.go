package sentry

import (
	"context"
	"strings"
	"sync"
	"time"

	uuid "github.com/satori/go.uuid"

	"github.com/bililive-go/bililive-go/src/pkg/metadata"
)

var (
	// cachedDeviceID 缓存的设备 ID
	cachedDeviceID string
	// deviceIDOnce 确保设备 ID 只生成一次
	deviceIDOnce sync.Once
)

// GetAnonymousDeviceID 获取匿名设备 ID
// 首次调用时会从 metadata 数据库读取或生成新的 UUID，后续调用返回缓存的值
// 返回 32 位十六进制字符串（去掉连字符的 UUID）
func GetAnonymousDeviceID() string {
	deviceIDOnce.Do(func() {
		cachedDeviceID = loadOrCreateDeviceID()
	})
	return cachedDeviceID
}

// loadOrCreateDeviceID 从 metadata 数据库加载或创建设备 ID
func loadOrCreateDeviceID() string {
	store := metadata.GetStore()
	if store == nil {
		// metadata 存储未初始化，返回临时 UUID
		return generateUUID()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 尝试读取现有的设备 ID
	deviceID, err := store.Get(ctx, metadata.NamespaceDevice, metadata.KeyDeviceID)
	if err == nil && deviceID != "" {
		return deviceID
	}

	// 没有现有的设备 ID，生成新的
	deviceID = generateUUID()

	// 保存到 metadata 数据库（保存失败不影响返回）
	_ = store.Set(ctx, metadata.NamespaceDevice, metadata.KeyDeviceID, deviceID)

	return deviceID
}

// generateUUID 生成一个新的 UUID（去掉连字符）
func generateUUID() string {
	id := uuid.Must(uuid.NewV4())
	// 返回去掉连字符的 UUID，32 位十六进制字符串
	// 例如: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx -> xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
	return strings.ReplaceAll(id.String(), "-", "")
}
