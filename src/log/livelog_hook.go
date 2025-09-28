package log

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/bililive-go/bililive-go/src/pkg/logstore"
	"github.com/bililive-go/bililive-go/src/types"
)

// LiveLogHook 用于捕获直播间相关日志并存储到LogStore
type LiveLogHook struct {
	logStore *logstore.LogStore
}

// NewLiveLogHook 创建新的直播间日志钩子
func NewLiveLogHook(logStore *logstore.LogStore) *LiveLogHook {
	return &LiveLogHook{
		logStore: logStore,
	}
}

// Levels 返回钩子关注的日志级别
func (hook *LiveLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire 处理日志条目
func (hook *LiveLogHook) Fire(entry *logrus.Entry) error {
	// 检查日志条目中是否包含直播间相关信息
	if host, ok := entry.Data["host"]; ok {
		if room, ok := entry.Data["room"]; ok {
			// 从host和room信息构造LiveID或从上下文获取
			liveID := types.LiveID(fmt.Sprintf("%s_%s", host, room))
			
			// 格式化日志消息
			message := fmt.Sprintf("[%s] %s: %s", 
				entry.Time.Format("2006-01-02 15:04:05"),
				strings.ToUpper(entry.Level.String()),
				entry.Message)
			
			// 如果有额外的字段，添加到消息中
			if len(entry.Data) > 2 { // 除了host和room
				var extras []string
				for k, v := range entry.Data {
					if k != "host" && k != "room" {
						extras = append(extras, fmt.Sprintf("%s=%v", k, v))
					}
				}
				if len(extras) > 0 {
					message = fmt.Sprintf("%s [%s]", message, strings.Join(extras, ", "))
				}
			}
			
			// 存储日志到LogStore
			hook.logStore.AddLog(liveID, strings.ToUpper(entry.Level.String()), message)
		}
	}
	
	return nil
}