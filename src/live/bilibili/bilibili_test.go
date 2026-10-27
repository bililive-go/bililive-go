package bilibili

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/hr3lxphr6j/requests"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// 保留真实请求路径和 Cookie 组装，使用隔离传输模拟平台响应。
func newTestLive(t *testing.T, userAPI func(*http.Request) (int, string, error)) *Live {
	t.Helper()
	u, err := url.Parse("https://live.bilibili.com/123")
	require.NoError(t, err)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{"code":0,"data":{"title":"正在直播","live_status":1}}`
		if strings.Contains(r.URL.Path, "get_anchor_in_room") {
			var err error
			status, body, err = userAPI(r)
			if err != nil {
				return nil, err
			}
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	return &Live{BaseLive: internal.BaseLive{
		Url:            u,
		Options:        live.MustNewOptions(live.WithKVStringCookies(u, "SESSDATA=test-value")),
		RequestSession: requests.NewSession(client),
	}, realID: "123"}
}

func TestGetInfoSurvivesUserAPIFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{"HTTP412", 412, "风控", nil},
		{"业务错误", 200, `{"code":-352}`, nil},
		{"非法JSON", 200, `not json`, nil},
		{"空名称", 200, `{"code":0,"data":{"info":{"uname":""}}}`, nil},
		{"网络错误", 0, "", fmt.Errorf("网络不可用")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			l := newTestLive(t, func(r *http.Request) (int, string, error) { calls++; return tc.status, tc.body, tc.err })
			for range 2 {
				info, err := l.GetInfo()
				require.NoError(t, err)
				require.True(t, info.Status)
				require.Equal(t, "正在直播", info.RoomName)
				require.Equal(t, "房间123", info.HostName)
			}
			require.Equal(t, 1, calls, "失败后应退避，不能每轮检测都请求主播资料")
		})
	}
}

func TestHostCacheCookieAndRecovery(t *testing.T) {
	calls := 0
	status := 200
	name := "主播甲"
	l := newTestLive(t, func(r *http.Request) (int, string, error) {
		calls++
		cookie, err := r.Cookie("SESSDATA")
		require.NoError(t, err)
		require.Equal(t, "test-value", cookie.Value)
		return status, fmt.Sprintf(`{"code":0,"data":{"info":{"uname":%q}}}`, name), nil
	})
	info, err := l.GetInfo()
	require.NoError(t, err)
	require.Equal(t, "主播甲", info.HostName)
	_, err = l.GetInfo()
	require.NoError(t, err)
	require.Equal(t, 1, calls, "成功后应缓存主播名")
	status = 412
	l.nextHostRefresh = time.Time{}
	info, err = l.GetInfo()
	require.NoError(t, err)
	require.Equal(t, "主播甲", info.HostName, "412 时保留成功缓存")
	require.True(t, info.Status)
	status = 200
	name = "主播乙"
	l.nextHostRefresh = time.Time{}
	info, err = l.GetInfo()
	require.NoError(t, err)
	require.Equal(t, "主播乙", info.HostName, "退避结束后允许恢复")
}

func TestRoomAPIFailureStillFails(t *testing.T) {
	l := newTestLive(t, func(r *http.Request) (int, string, error) {
		t.Fatal("房间接口失败时不应请求主播接口")
		return 0, "", nil
	})
	l.RequestSession = requests.NewSession(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 412, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})})
	info, err := l.GetInfo()
	require.Error(t, err)
	require.Nil(t, info)
}
