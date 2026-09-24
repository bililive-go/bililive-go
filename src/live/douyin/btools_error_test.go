package douyin

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBToolsErrorClassification(t *testing.T) {
	for _, tt := range []struct {
		name, body, code, kind string
		status                 int
	}{
		{"旧版全端点冷却", `{"error":"所有 API 端点都不可用，请稍后重试"}`, "BGO_BALANCE_COOLDOWN", "", 0},
		{"旧版上游拒绝", `{"error":"所有 API 调用都失败了。最后一个错误: Request failed with status code 444"}`, "BGO_UPSTREAM", "http", 444},
		{"旧版解析失败", `{"error":"No match found in HTML"}`, "BGO_UPSTREAM", "parse", 0},
		{"旧版房间解析失败", `{"error":"No room match found in HTML"}`, "BGO_UPSTREAM", "parse", 0},
		{"临时补丁繁忙", `{"error":"BGO_BALANCE_BUSY: queue full"}`, "BGO_BALANCE_BUSY", "", 0},
		{"临时补丁上游错误", `{"error":"BGO_BALANCE_UPSTREAM"}`, "BGO_BALANCE_UPSTREAM", "", 0},
		{"结构化繁忙", `{"code":"BGO_BALANCE_BUSY"}`, "BGO_BALANCE_BUSY", "", 0},
		{"结构化网络错误", `{"code":"BGO_BALANCE_UPSTREAM","kind":"network"}`, "BGO_BALANCE_UPSTREAM", "network", 0},
		{"结构化上游超时", `{"code":"BGO_UPSTREAM","kind":"timeout"}`, "BGO_UPSTREAM", "timeout", 0},
		{"总预算超时", `{"code":"BGO_REQUEST_TIMEOUT"}`, "BGO_REQUEST_TIMEOUT", "", 0},
		{"调用方取消", `{"code":"BGO_REQUEST_CANCELED"}`, "BGO_REQUEST_CANCELED", "", 0},
		{"空正文", "", "", "", 0},
		{"HTML错误页", "<html>secret</html>", "", "", 0},
		{"未知JSON", `{"error":"Cookie: secret; https://example.com/?sign=secret"}`, "", "", 0},
		{"伪造诊断字段", `{"code":"secret","kind":"secret","api":"secret","upstreamStatus":12345,"retryAt":"secret"}`, "", "", 0},
		{"非字符串错误", `{"error":{"cookie":"secret"}}`, "", "", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(tt.body))}
			err := readBToolsError(resp)
			var detail *btoolsRequestError
			require.ErrorAs(t, err, &detail)
			require.Equal(t, tt.code, detail.Code)
			require.Equal(t, tt.kind, detail.Kind)
			require.Equal(t, tt.status, detail.UpstreamStatus)
			require.Contains(t, err.Error(), "500 Internal Server Error")
			require.NotContains(t, err.Error(), "secret")
			require.NotContains(t, err.Error(), "\n")
		})
	}
}

func TestBToolsErrorStructuredDetails(t *testing.T) {
	body := `{"code":"BGO_BALANCE_COOLDOWN","kind":"http","api":"web","upstreamStatus":444,"retryAt":"2026-09-25T09:03:00+09:00","error":"cookies: secret\nhttps://example.com/?sign=secret"}`
	resp := &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(body))}
	err := readBToolsError(resp)
	require.Equal(t, "请求失败: 503 Service Unavailable; code=BGO_BALANCE_COOLDOWN; kind=http; api=web; upstream_status=444; retry_at=2026-09-25T00:03:00Z", err.Error())
}

type countingErrorBody struct{ bytes int }

func (b *countingErrorBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.bytes += len(p)
	return len(p), nil
}
func (*countingErrorBody) Close() error { return nil }

func TestBToolsErrorBodyIsBounded(t *testing.T) {
	body := &countingErrorBody{}
	err := readBToolsError(&http.Response{StatusCode: 500, Body: body})
	require.Equal(t, maxBToolsErrorBody+1, body.bytes)
	require.Equal(t, "请求失败: 500 Internal Server Error", err.Error())
}

func TestBToolsRequestReusesConnectionAndPreservesFailure(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, btoolsConsts.authToken, r.Header.Get("Authorization"))
		if r.URL.Path == "/failure" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"code":"BGO_BALANCE_COOLDOWN","retryAt":"2026-09-25T00:03:00Z"}`)
			return
		}
		_, _ = io.WriteString(w, `{"title":"测试直播间","owner":"测试主播","living":false}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	oldClient := btoolsClient
	btoolsClient = server.Client()
	btoolsClient.Timeout = time.Second
	t.Cleanup(func() { btoolsClient = oldClient })
	for i := 0; i < 2; i++ {
		body, err := doBToolsRequest(server.URL + "/failure")
		require.Nil(t, body)
		var detail *btoolsRequestError
		require.True(t, errors.As(err, &detail))
		require.Equal(t, "BGO_BALANCE_COOLDOWN", detail.Code)
	}
	body, err := doBToolsRequest(server.URL + "/offline")
	require.NoError(t, err)
	var info liveInfoResp
	require.NoError(t, json.Unmarshal(body, &info))
	require.False(t, info.Living)
	require.Equal(t, "测试直播间", info.Title)
	require.Equal(t, int32(1), connections.Load())
}
