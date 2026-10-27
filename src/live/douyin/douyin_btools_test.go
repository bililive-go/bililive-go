package douyin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/stretchr/testify/require"
)

// 使用隔离的本地解析服务，不访问真实平台或正在运行的录制服务。
func mockBtools(t *testing.T, handler http.HandlerFunc, audioOnly bool) *btoolsLive {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	old := btoolsConsts
	btoolsConsts.port = port
	t.Cleanup(func() { btoolsConsts = old })
	roomURL, err := url.Parse("https://live.douyin.com/123")
	require.NoError(t, err)
	l := &Live{BaseLive: internal.BaseLive{Url: roomURL, Options: live.MustNewOptions(live.WithAudioOnly(audioOnly))}}
	result := NewBtoolsLive(l)
	result.roomId = "123"
	return &result
}

func TestAudioOnlyStreamSelection(t *testing.T) {
	const audioURL = "https://cdn.example/live.flv?sign=abc%2Bdef&only_audio=1"
	for _, tc := range []struct {
		name    string
		sources string
		want    string
	}{
		{"选择音频并保留原始签名", `[{"streams":[{"quality":"origin","flv":"https://cdn.example/video.flv"},{"quality":"ao","flv":"` + audioURL + `"}]}]`, audioURL},
		{"支持音频HLS", `[{"streams":[{"quality":"ao","flv":"","hls":"https://cdn.example/audio.m3u8?only_audio=1"}]}]`, "https://cdn.example/audio.m3u8?only_audio=1"},
		{"旧工具缺少元数据", `null`, ""},
		{"不能回退原画", `[{"streams":[{"quality":"origin","flv":"https://cdn.example/video.flv"}]}]`, ""},
		{"拒绝ao中的视频地址", `[{"streams":[{"quality":"ao","flv":"https://cdn.example/video.flv?only_audio=0"}]}]`, ""},
		{"拒绝相对地址", `[{"streams":[{"quality":"ao","flv":"/audio.flv?only_audio=1"}]}]`, ""},
		{"拒绝非HTTP地址", `[{"streams":[{"quality":"ao","flv":"file://host/audio.flv?only_audio=1"}]}]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := mockBtools(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/bgo/stream-info", r.URL.Path)
				require.Equal(t, "1", r.URL.Query().Get("dev"))
				require.Equal(t, "douyin", r.URL.Query().Get("platform"))
				require.Equal(t, "123", r.URL.Query().Get("roomId"))
				require.Equal(t, btoolsConsts.authToken, r.Header.Get("Authorization"))
				_, _ = w.Write([]byte(`{"stream":"https://cdn.example/video.flv","dev":{"sources":` + tc.sources + `}}`))
			}, true)
			streams, err := l.GetStreamInfos()
			if tc.want == "" {
				require.NoError(t, err)
				require.Len(t, streams, 1)
				require.Equal(t, "https://cdn.example/video.flv", streams[0].Url.String())
			} else {
				require.NoError(t, err)
				require.Len(t, streams, 1)
				require.Equal(t, tc.want, streams[0].Url.String())
				require.Equal(t, "https://cdn.example/video.flv", streams[0].AudioFallbackURL.String())
			}
		})
	}
}

func TestVideoModeUnchanged(t *testing.T) {
	l := mockBtools(t, func(w http.ResponseWriter, r *http.Request) {
		require.False(t, r.URL.Query().Has("dev"))
		_ = json.NewEncoder(w).Encode(map[string]string{"stream": "https://cdn.example/video.flv"})
	}, false)
	streams, err := l.GetStreamInfos()
	require.NoError(t, err)
	require.Equal(t, "https://cdn.example/video.flv", streams[0].Url.String())
}

func TestGetInfoPropagatesAudioOnly(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			l := mockBtools(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/bgo/live-info", r.URL.Path)
				_, _ = w.Write([]byte(`{"living":true,"title":"测试","owner":"主播"}`))
			}, enabled)
			info, err := l.GetInfo()
			require.NoError(t, err)
			require.True(t, info.Status)
			require.Equal(t, enabled, info.AudioOnly)
		})
	}
}

func TestAudioOnlyUpstreamFailure(t *testing.T) {
	l := mockBtools(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "暂时不可用", http.StatusBadGateway)
	}, true)
	streams, err := l.GetStreamInfos()
	require.ErrorContains(t, err, "502")
	require.Empty(t, streams)
}

func TestAudioOnlyLegacyEndpointFallback(t *testing.T) {
	calls := 0
	l := mockBtools(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Has("dev") {
			http.Error(w, "不支持", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"stream":"https://cdn.example/legacy.flv"}`))
	}, true)
	streams, err := l.GetStreamInfos()
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "https://cdn.example/legacy.flv", streams[0].Url.String())
}
