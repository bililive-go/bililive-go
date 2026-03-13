package hlsproxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRewritePlaylistFiltersPreloading(t *testing.T) {
	upstreamURL, err := url.Parse("https://cdn.example.com/live/index.m3u8")
	assert.NoError(t, err)
	localURL, err := url.Parse("http://127.0.0.1:18080/stream.m3u8")
	assert.NoError(t, err)

	content := `#EXTM3U
#EXT-X-VERSION:3
#EXTINF:2.0,
segment1.ts
#EXTINF:2.0,
preloading_segment.ts
#EXTINF:2.0,
segment2.ts
`

	rewritten, err := rewritePlaylist(content, upstreamURL, localURL, true)
	assert.NoError(t, err)
	assert.NotContains(t, rewritten, "preloading_segment.ts")
	assert.False(t, strings.Contains(rewritten, "#EXTINF:2.0,\nhttp://127.0.0.1:18080/media?url=https%3A%2F%2Fcdn.example.com%2Flive%2Fpreloading_segment.ts"))
	assert.Contains(t, rewritten, "segment1.ts")
	assert.Contains(t, rewritten, "segment2.ts")
}

func TestRewritePlaylistRewritesExtXMapAndSegments(t *testing.T) {
	upstreamURL, err := url.Parse("https://cdn.example.com/live/index.m3u8")
	assert.NoError(t, err)
	localURL, err := url.Parse("http://127.0.0.1:18080/stream.m3u8")
	assert.NoError(t, err)

	content := `#EXTM3U
#EXT-X-MAP:URI="init.mp4"
#EXTINF:2.0,
segment1.m4s
`

	rewritten, err := rewritePlaylist(content, upstreamURL, localURL, false)
	assert.NoError(t, err)
	assert.Contains(t, rewritten, `#EXT-X-MAP:URI="http://127.0.0.1:18080/media?url=https%3A%2F%2Fcdn.example.com%2Flive%2Finit.mp4"`)
	assert.Contains(t, rewritten, "http://127.0.0.1:18080/media?url=https%3A%2F%2Fcdn.example.com%2Flive%2Fsegment1.m4s")
}

func TestProxyRawStreamsUpstreamResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("segment-data"))
	}))
	defer upstream.Close()

	targetURL, err := url.Parse(upstream.URL)
	assert.NoError(t, err)

	proxy := &Proxy{
		upstreamURL: targetURL,
		headers: map[string]string{
			"User-Agent": "bililive-go-test",
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/media", nil)

	proxy.proxyRaw(recorder, request, targetURL)

	assert.Equal(t, http.StatusAccepted, recorder.Code)
	assert.Equal(t, "video/mp2t", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
	assert.Equal(t, "segment-data", recorder.Body.String())
}
