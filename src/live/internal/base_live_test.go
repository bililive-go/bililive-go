package internal

import (
	"context"
	"net/url"
	"sync"
	"testing"

	"github.com/bililive-go/bililive-go/src/configs"
)

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
