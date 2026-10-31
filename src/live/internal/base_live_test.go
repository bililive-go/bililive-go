package internal

import (
	"context"
	"net/url"
	"sync"
	"testing"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
)

func TestRecordingOptionsVersionTracksRelevantChanges(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(previousConfig) })
	config := configs.NewConfig()
	config.Cookies = map[string]string{"live.bilibili.com": "session=first"}
	configs.SetCurrentConfig(config)
	u, err := url.Parse("https://live.bilibili.com/123")
	if err != nil {
		t.Fatal(err)
	}
	liveRoom := NewBaseLive(u)
	room := configs.LiveRoom{Url: u.String(), Quality: 80}
	update := func() *live.Options {
		t.Helper()
		if err := liveRoom.UpdateLiveOptionsbyConfig(context.Background(), &room); err != nil {
			t.Fatal(err)
		}
		return liveRoom.GetOptions()
	}
	initial := update()
	room.NickName = "新昵称"
	renamed := update()
	if !live.SameRecordingOptions(initial, renamed) {
		t.Fatal("仅改昵称不应使取流版本失效")
	}
	room.AudioOnly = true
	audio := update()
	room.AudioOnly = false
	restored := update()
	if live.SameRecordingOptions(initial, audio) || live.SameRecordingOptions(initial, restored) {
		t.Fatal("音频模式发生过变化时，即使恢复原值也必须丢弃在途取流")
	}
	room.Quality = 10000
	quality := update()
	if live.SameRecordingOptions(restored, quality) {
		t.Fatal("清晰度变化应使取流版本失效")
	}
	config.Cookies["live.bilibili.com"] = "session=second"
	cookie := update()
	if live.SameRecordingOptions(quality, cookie) {
		t.Fatal("Cookie 变化应使取流版本失效")
	}
}

func TestBaseLiveOptionsAreAtomicSnapshots(t *testing.T) {
	u, err := url.Parse("https://live.bilibili.com/123")
	if err != nil {
		t.Fatal(err)
	}
	l := NewBaseLive(u)
	first := configs.LiveRoom{Url: u.String(), Quality: 80, AudioOnly: true, NickName: "音频"}
	second := configs.LiveRoom{Url: u.String(), Quality: 10000, AudioOnly: false, NickName: "视频"}
	if err := l.UpdateLiveOptionsbyConfig(context.Background(), &first); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			room := &first
			if i%2 == 0 {
				room = &second
			}
			if err := l.UpdateLiveOptionsbyConfig(context.Background(), room); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			options := l.GetOptions()
			switch options.NickName {
			case "音频":
				if options.Quality != 80 || !options.AudioOnly {
					t.Errorf("读到混合的音频快照：%+v", options)
					return
				}
			case "视频":
				if options.Quality != 10000 || options.AudioOnly {
					t.Errorf("读到混合的视频快照：%+v", options)
					return
				}
			default:
				t.Errorf("读到未知快照：%+v", options)
				return
			}
		}
	}()
	wg.Wait()
}
