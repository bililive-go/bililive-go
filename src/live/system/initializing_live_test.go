package system

import (
	"context"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
)

// 初始化完成后会用 OriginalLive 顶掉包装对象，期间热应用的房间选项必须同时落到原始 Live，
// 否则表现为"改了设置/重新扫码，老房间仍按改前的状态取流"。
func TestInitializingLiveForwardsOptionsToOriginal(t *testing.T) {
	ctrl := gomock.NewController(t)
	original := livemock.NewMockLive(ctrl)
	u, err := url.Parse("https://www.douyu.com/123456")
	assert.NoError(t, err)
	room := &configs.LiveRoom{Url: "https://www.douyu.com/123456", AudioOnly: true, NickName: "主播"}
	original.EXPECT().UpdateLiveOptionsbyConfig(gomock.Any(), room).Return(nil)
	underlyingOptions := live.MustNewOptions(live.WithAudioOnly(true), live.WithNickName("主播"))
	original.EXPECT().GetOptions().Return(underlyingOptions)

	l := &InitializingLive{BaseLive: internal.NewBaseLive(u), OriginalLive: original}
	assert.NoError(t, l.UpdateLiveOptionsbyConfig(context.Background(), room))

	opts := l.GetOptions()
	if assert.NotNil(t, opts) {
		assert.True(t, opts.AudioOnly, "初始化期间必须使用实际取流对象的选项")
		assert.Equal(t, "主播", opts.NickName)
	}
	assert.True(t, l.BaseLive.GetOptions().AudioOnly)
}

type blockingOriginalOptionsLive struct {
	live.Live
	options atomic.Pointer[live.Options]
	started chan struct{}
	release chan struct{}
}

func (original *blockingOriginalOptionsLive) GetOptions() *live.Options {
	return original.options.Load()
}

func (original *blockingOriginalOptionsLive) UpdateLiveOptionsbyConfig(_ context.Context, room *configs.LiveRoom) error {
	close(original.started)
	<-original.release
	original.options.Store(live.MustNewOptions(live.WithAudioOnly(room.AudioOnly)))
	return nil
}

func TestInitializingLiveReadsOriginalOptionsDuringUpdate(t *testing.T) {
	u, err := url.Parse("https://live.bilibili.com/123")
	assert.NoError(t, err)
	original := &blockingOriginalOptionsLive{started: make(chan struct{}), release: make(chan struct{})}
	oldOptions := live.MustNewOptions(live.WithAudioOnly(false))
	original.options.Store(oldOptions)
	initializing := &InitializingLive{BaseLive: internal.NewBaseLive(u), OriginalLive: original}
	done := make(chan error, 1)
	go func() {
		done <- initializing.UpdateLiveOptionsbyConfig(context.Background(), &configs.LiveRoom{Url: u.String(), AudioOnly: true})
	}()
	<-original.started
	assert.Same(t, oldOptions, initializing.GetOptions())
	close(original.release)
	assert.NoError(t, <-done)
	assert.True(t, initializing.GetOptions().AudioOnly)
}

// 原始 Live 拒绝时错误必须原样返回，交给上层记日志，而不是静默只更新一半
func TestInitializingLivePropagatesOriginalUpdateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	original := livemock.NewMockLive(ctrl)
	u, err := url.Parse("https://www.douyu.com/123456")
	assert.NoError(t, err)
	room := &configs.LiveRoom{Url: "https://www.douyu.com/123456"}
	original.EXPECT().UpdateLiveOptionsbyConfig(gomock.Any(), room).Return(context.DeadlineExceeded)

	l := &InitializingLive{BaseLive: internal.NewBaseLive(u), OriginalLive: original}
	assert.ErrorIs(t, l.UpdateLiveOptionsbyConfig(context.Background(), room), context.DeadlineExceeded)
}
