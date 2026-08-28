package douyu

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/bililive-go/bililive-go/src/live/internal"
)

func TestGetEngineWithCryptoJSReturnsErrorAndCanRetry(t *testing.T) {
	var available atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if !available.Load() {
			http.Error(writer, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = writer.Write([]byte(`var CryptoJS = {};`))
	}))
	defer server.Close()

	cryptoJSMu.Lock()
	previousScript := cryptoJS
	previousURLs := cryptoJSCDNURLs
	cryptoJS = nil
	cryptoJSCDNURLs = []string{server.URL}
	cryptoJSMu.Unlock()
	t.Cleanup(func() {
		cryptoJSMu.Lock()
		cryptoJS = previousScript
		cryptoJSCDNURLs = previousURLs
		cryptoJSMu.Unlock()
	})

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("解析测试服务器 URL 失败: %v", err)
	}
	l := &Live{BaseLive: internal.NewBaseLive(parsedURL)}

	if _, err = l.getEngineWithCryptoJS(); err == nil {
		t.Fatal("所有 CryptoJS CDN 不可用时应返回错误，而不是 panic")
	}

	available.Store(true)
	if _, err = l.getEngineWithCryptoJS(); err != nil {
		t.Fatalf("CDN 恢复后应能重试成功: %v", err)
	}
	if _, err = l.getEngineWithCryptoJS(); err != nil {
		t.Fatalf("缓存的 CryptoJS 应可继续使用: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("成功后应使用缓存，期望 2 次请求，实际 %d 次", got)
	}
}
