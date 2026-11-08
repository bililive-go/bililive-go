//go:build dev

package dev

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/bililive-go/bililive-go/src/live/internal"
)

func newDevLiveForTest(t *testing.T, rawURL string) *Live {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return &Live{BaseLive: internal.BaseLive{Url: u}}
}

func TestGetInfoRoomPaths(t *testing.T) {
	for _, path := range []string{
		"/hls/room.m3u8",
		"/hls/room",
		"/live/room.flv",
		"/live/room.m3u8",
		"/live/room",
		"/files/room.flv",
		"/files/room.m3u8",
		"/files/room",
		"/room",
	} {
		t.Run(path, func(t *testing.T) {
			for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
				t.Run(fmt.Sprint(status), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != "/api/streams/room" {
							t.Errorf("直播信息 API 路径错误：%s", r.URL.Path)
							http.NotFound(w, r)
							return
						}
						w.WriteHeader(status)
						if status == http.StatusOK {
							fmt.Fprint(w, `{"streamer":"测试 API 主播","live":false}`)
						}
					}))
					defer server.Close()

					info, err := newDevLiveForTest(t, server.URL+path).GetInfo()
					if err != nil {
						t.Fatal(err)
					}
					if info.RoomName != "room" {
						t.Errorf("房间名未正确解析：%q", info.RoomName)
					}
					if status == http.StatusOK {
						if info.HostName != "测试 API 主播" || info.Status {
							t.Errorf("未使用 API 返回的主播和直播状态：%+v", info)
						}
					} else if info.HostName != "测试主播" || !info.Status {
						t.Errorf("API 不可用时未保留默认直播信息：%+v", info)
					}
				})
			}
		})
	}
}

func TestGetStreamInfosRoomPaths(t *testing.T) {
	for _, path := range []string{"/hls/room", "/live/room", "/files/room", "/room"} {
		t.Run(path, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/streams/room/available" {
					t.Errorf("可用流 API 路径错误：%s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				fmt.Fprintf(w, `[{"url":"http://%s/hls/room.m3u8","format":"hls","quality":"720p","codec":"h264","width":1280,"height":720}]`, r.Host)
			}))
			defer server.Close()

			infos, err := newDevLiveForTest(t, server.URL+path).GetStreamInfos()
			if err != nil {
				t.Fatal(err)
			}
			if len(infos) != 1 {
				t.Fatalf("应使用 API 返回的单路流，实际有 %d 路", len(infos))
			}
			info := infos[0]
			if info.Url.String() != server.URL+"/hls/room.m3u8" || info.Format != "hls" ||
				info.Quality != "720p" || info.Codec != "h264" || info.Width != 1280 || info.Height != 720 {
				t.Errorf("API 流信息不匹配：%+v", info)
			}
		})
	}
}

func TestGetStreamInfosFallbackPaths(t *testing.T) {
	for _, path := range []string{"/hls/room", "/live/room", "/files/room", "/room"} {
		t.Run(path, func(t *testing.T) {
			for _, status := range []int{http.StatusNotFound, http.StatusOK} {
				t.Run(fmt.Sprint(status), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != "/api/streams/room/available" {
							t.Errorf("回退前的可用流 API 路径错误：%s", r.URL.Path)
							http.NotFound(w, r)
							return
						}
						w.WriteHeader(status)
						if status == http.StatusOK {
							fmt.Fprint(w, "[]")
						}
					}))
					defer server.Close()

					l := newDevLiveForTest(t, server.URL+path)
					infos, err := l.GetStreamInfos()
					if err != nil {
						t.Fatal(err)
					}
					if len(infos) != 7 {
						t.Fatalf("应生成 7 路默认测试流，实际有 %d 路", len(infos))
					}
					counts := map[string]int{}
					for _, info := range infos {
						counts[info.Format]++
						wantPath := "/live/room.flv"
						if info.Format == "hls" {
							wantPath = "/hls/room.m3u8"
						}
						if info.Url.Path != wantPath || info.Url.Host != l.Url.Host || info.Url.Scheme != "http" {
							t.Errorf("%s 回退流地址错误：%s，应使用路径 %s", info.Format, info.Url, wantPath)
						}
						query := info.Url.Query()
						if query.Get("quality") != info.Quality || normalizeCodec(query.Get("codec")) != info.Codec {
							t.Errorf("回退流查询参数与元数据不匹配：%+v", info)
						}
					}
					if counts["hls"] != 2 || counts["flv"] != 5 {
						t.Errorf("默认流格式数量错误：%v", counts)
					}
				})
			}
		})
	}
}

func TestGetStreamInfosDirectPaths(t *testing.T) {
	for _, tc := range []struct {
		path   string
		format string
	}{
		{"/hls/room.m3u8", "hls"},
		{"/live/room.m3u8", "hls"},
		{"/live/room.flv", "flv"},
		{"/files/room.m3u8", "hls"},
		{"/files/room.flv", "flv"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("直接流地址不应请求 API：%s", r.URL.Path)
				http.NotFound(w, r)
			}))
			defer server.Close()

			rawURL := server.URL + tc.path + "?codec=hevc&quality=720p"
			infos, err := newDevLiveForTest(t, rawURL).GetStreamInfos()
			if err != nil {
				t.Fatal(err)
			}
			if len(infos) != 1 {
				t.Fatalf("直接流地址应只返回一路流，实际有 %d 路", len(infos))
			}
			info := infos[0]
			if info.Url.String() != rawURL || info.Format != tc.format || info.Codec != "h265" ||
				info.Quality != "720p" || info.Width != 1280 || info.Height != 720 {
				t.Errorf("直接流地址或参数未保留：%+v", info)
			}
		})
	}
}
