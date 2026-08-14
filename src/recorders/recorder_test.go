package recorders

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/bluele/gcache"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	evtmock "github.com/bililive-go/bililive-go/src/pkg/events/mock"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
)

func TestRecorderCloseAndWaitWaitsForRunExit(t *testing.T) {
	ctrl := gomock.NewController(t)
	liveMock := livemock.NewMockLive(ctrl)
	ed := evtmock.NewMockDispatcher(ctrl)
	liveMock.EXPECT().GetLogger().Return(livelogger.New(0, nil))
	ed.EXPECT().DispatchEvent(events.NewEvent(RecorderStop, liveMock))

	r := &recorder{
		Live:       liveMock,
		ed:         ed,
		state:      running,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		parserLock: new(sync.RWMutex),
	}
	closeReturned := make(chan struct{})
	go func() {
		r.CloseAndWait()
		close(closeReturned)
	}()

	<-r.stop
	select {
	case <-closeReturned:
		t.Fatal("run 退出前 CloseAndWait 不应返回")
	default:
	}

	close(r.done)
	<-closeReturned
}

type parserInstalledAfterClose struct {
	stopped bool
}

func (p *parserInstalledAfterClose) ParseLiveStream(context.Context, *live.StreamUrlInfo, live.Live, string) error {
	return nil
}

func (p *parserInstalledAfterClose) Stop() error {
	p.stopped = true
	return nil
}

func TestRecorderRejectsParserInstalledAfterClose(t *testing.T) {
	r := &recorder{state: stopped, parserLock: new(sync.RWMutex)}
	p := &parserInstalledAfterClose{}

	assert.False(t, r.setAndCloseParser(p))
	assert.True(t, p.stopped, "关闭后的新 parser 应立即停止")
	assert.Nil(t, r.getParser())
}

func TestTryRecordStopsWithoutPanicWhenFilenameRenderFails(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	cfg := configs.NewConfig()
	cfg.OutputTmpl = `{{ fail "render failed" }}`
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() {
		configs.SetCurrentConfig(previousConfig)
	})

	ctrl := gomock.NewController(t)
	l := livemock.NewMockLive(ctrl)
	logger := livelogger.New(0, logrus.Fields{"test": t.Name()})
	streamURL := &url.URL{Scheme: "https", Host: "example.com", Path: "/stream.flv"}

	l.EXPECT().GetRawUrl().Return("https://example.com/room").AnyTimes()
	l.EXPECT().GetStreamInfos().Return([]*live.StreamUrlInfo{{Url: streamURL}}, nil)
	l.EXPECT().GetLogger().Return(logger)

	cache := gcache.New(1).LRU().Build()
	if err := cache.Set(l, &live.Info{Live: l}); err != nil {
		t.Fatalf("写入直播信息缓存失败: %v", err)
	}
	r := &recorder{Live: l, cache: cache}

	r.tryRecord(context.Background())

	logs := logger.GetLogs()
	if !strings.Contains(logs, "failed to render filename, recording aborted") {
		t.Fatalf("未记录文件名渲染失败日志: %s", logs)
	}
	if !strings.Contains(logs, "render failed") {
		t.Fatalf("日志未保留原始模板错误: %s", logs)
	}
}
