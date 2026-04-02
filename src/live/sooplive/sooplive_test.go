package sooplive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	livepkg "github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/stretchr/testify/assert"
)

func resetVerifyCookieCacheForTest() {
	verifyCookieCacheMu.Lock()
	defer verifyCookieCacheMu.Unlock()
	verifyCookieCache = map[string]cachedVerifyResult{}
}

func TestParseChannelAndBroadNoFromURL(t *testing.T) {
	t.Run("仅频道", func(t *testing.T) {
		u, err := url.Parse("https://play.sooplive.com/mbntv")
		assert.NoError(t, err)

		channel, broadNo, err := parseChannelAndBroadNoFromURL(u)
		assert.NoError(t, err)
		assert.Equal(t, "mbntv", channel)
		assert.Empty(t, broadNo)
	})

	t.Run("频道加场次", func(t *testing.T) {
		u, err := url.Parse("https://play.sooplive.com/mbntv/292157719")
		assert.NoError(t, err)

		channel, broadNo, err := parseChannelAndBroadNoFromURL(u)
		assert.NoError(t, err)
		assert.Equal(t, "mbntv", channel)
		assert.Equal(t, "292157719", broadNo)
	})
}

func TestParseBroadNoFromPage(t *testing.T) {
	broadNo, found := parseBroadNoFromPage(`window.nBroadNo = 292157719;`)
	assert.True(t, found)
	assert.Equal(t, "292157719", broadNo)

	broadNo, found = parseBroadNoFromPage(`window.nBroadNo = null;`)
	assert.True(t, found)
	assert.Empty(t, broadNo)

	broadNo, found = parseBroadNoFromPage(`window.foo = "bar";`)
	assert.False(t, found)
	assert.Empty(t, broadNo)
}

func TestResolvePageBroadNo(t *testing.T) {
	t.Run("页面给出有效场次号时优先使用页面值", func(t *testing.T) {
		broadNo, isLiving, explicitlyOffline := resolvePageBroadNo("292100000", "292157719", true, false)
		assert.Equal(t, "292157719", broadNo)
		assert.True(t, isLiving)
		assert.False(t, explicitlyOffline)
	})

	t.Run("页面明确为 null 时不能回退到路径场次号", func(t *testing.T) {
		broadNo, isLiving, explicitlyOffline := resolvePageBroadNo("292157719", "", true, false)
		assert.Empty(t, broadNo)
		assert.False(t, isLiving)
		assert.True(t, explicitlyOffline)
	})

	t.Run("页面字段缺失时允许回退到路径场次号", func(t *testing.T) {
		broadNo, isLiving, explicitlyOffline := resolvePageBroadNo("292157719", "", false, false)
		assert.Equal(t, "292157719", broadNo)
		assert.True(t, isLiving)
		assert.False(t, explicitlyOffline)
	})

	t.Run("最终 URL 为 null 时视为明确离线", func(t *testing.T) {
		broadNo, isLiving, explicitlyOffline := resolvePageBroadNo("292157719", "", false, true)
		assert.Empty(t, broadNo)
		assert.False(t, isLiving)
		assert.True(t, explicitlyOffline)
	})

	t.Run("页面字段缺失且路径也没有场次号时视为离线", func(t *testing.T) {
		broadNo, isLiving, explicitlyOffline := resolvePageBroadNo("", "", false, false)
		assert.Empty(t, broadNo)
		assert.False(t, isLiving)
		assert.False(t, explicitlyOffline)
	})
}

func TestParseWindowString(t *testing.T) {
	body := `
window.szBjNick = 'MBN공식';
window.szBroadTitle = "스트리머가 오프라인입니다.";
`
	assert.Equal(t, "MBN공식", parseWindowString(body, "szBjNick"))
	assert.Equal(t, "스트리머가 오프라인입니다.", parseWindowString(body, "szBroadTitle"))
}

func TestFetchPageMetaSendsCookies(t *testing.T) {
	configs.SetCurrentConfig(configs.NewConfig())

	var receivedCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`
window.nBroadNo = 292157719;
window.szBjNick = '主播';
window.szBroadTitle = '标题';
`))
	}))
	defer server.Close()

	u, err := url.Parse(server.URL + "/mbntv")
	assert.NoError(t, err)

	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions(livepkg.WithKVStringCookies(u, "SESS=valid; AUTH=ok"))

	meta, err := l.fetchPageMeta()
	assert.NoError(t, err)
	assert.True(t, meta.IsLiving)
	assert.Equal(t, "292157719", meta.BroadNo)
	assert.Empty(t, meta.PathBroadNo)
	assert.Equal(t, "292157719", meta.PageBroadNo)
	assert.True(t, meta.PageBroadNoFound)
	assert.False(t, meta.PageExplicitlyOffline)
	assert.Equal(t, "主播", meta.HostName)
	assert.Equal(t, "标题", meta.RoomName)
	assert.True(t, strings.Contains(receivedCookie, "SESS=valid"))
	assert.True(t, strings.Contains(receivedCookie, "AUTH=ok"))
}

func TestFetchPageMetaDoesNotFallbackToPathBroadNoWhenPageExplicitlyOffline(t *testing.T) {
	configs.SetCurrentConfig(configs.NewConfig())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`
window.nBroadNo = null;
window.szBjNick = 'host';
window.szBroadTitle = 'offline';
`))
	}))
	defer server.Close()

	u, err := url.Parse(server.URL + "/mbntv/292157719")
	assert.NoError(t, err)

	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions()

	meta, err := l.fetchPageMeta()
	assert.NoError(t, err)
	assert.Equal(t, "mbntv", meta.Channel)
	assert.Equal(t, "292157719", meta.PathBroadNo)
	assert.True(t, meta.PageBroadNoFound)
	assert.True(t, meta.PageExplicitlyOffline)
	assert.Empty(t, meta.PageBroadNo)
	assert.Empty(t, meta.BroadNo)
	assert.False(t, meta.IsLiving)
}

func TestFetchPageMetaFallsBackToPathBroadNoWhenPageFieldMissing(t *testing.T) {
	configs.SetCurrentConfig(configs.NewConfig())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`
window.szBjNick = 'host';
window.szBroadTitle = 'missing nBroadNo';
`))
	}))
	defer server.Close()

	u, err := url.Parse(server.URL + "/mbntv/292157719")
	assert.NoError(t, err)

	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions()

	meta, err := l.fetchPageMeta()
	assert.NoError(t, err)
	assert.Equal(t, "mbntv", meta.Channel)
	assert.Equal(t, "292157719", meta.PathBroadNo)
	assert.False(t, meta.PageBroadNoFound)
	assert.False(t, meta.PageExplicitlyOffline)
	assert.Empty(t, meta.PageBroadNo)
	assert.Equal(t, "292157719", meta.BroadNo)
	assert.True(t, meta.IsLiving)
}

func TestFetchPageMetaTreatsRedirectToNullAsExplicitOffline(t *testing.T) {
	configs.SetCurrentConfig(configs.NewConfig())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mbntv/292157719":
			http.Redirect(w, r, "/mbntv/null", http.StatusFound)
		case "/mbntv/null":
			_, _ = w.Write([]byte(`
window.szBjNick = 'host';
window.szBroadTitle = 'offline by null path';
`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	u, err := url.Parse(server.URL + "/mbntv/292157719")
	assert.NoError(t, err)

	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions()

	meta, err := l.fetchPageMeta()
	assert.NoError(t, err)
	assert.Equal(t, "mbntv", meta.Channel)
	assert.Empty(t, meta.PathBroadNo)
	assert.True(t, meta.PageExplicitlyOffline)
	assert.Empty(t, meta.BroadNo)
	assert.False(t, meta.IsLiving)
}

func TestGetStreamInfosReturnsOfflineSentinelWhenPageExplicitlyOffline(t *testing.T) {
	configs.SetCurrentConfig(configs.NewConfig())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`
window.nBroadNo = null;
window.szBjNick = 'host';
window.szBroadTitle = 'offline';
`))
	}))
	defer server.Close()

	u, err := url.Parse(server.URL + "/mbntv/292157719")
	assert.NoError(t, err)

	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions()

	_, err = l.GetStreamInfos()
	assert.Error(t, err)
	assert.ErrorIs(t, err, livepkg.ErrLiveOffline)
}

func TestGetStreamInfosReturnsOfflineSentinelWhenRedirectedToNull(t *testing.T) {
	configs.SetCurrentConfig(configs.NewConfig())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mbntv/292157719":
			http.Redirect(w, r, "/mbntv/null", http.StatusFound)
		case "/mbntv/null":
			_, _ = w.Write([]byte(`
window.szBjNick = 'host';
window.szBroadTitle = 'offline by null path';
`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	u, err := url.Parse(server.URL + "/mbntv/292157719")
	assert.NoError(t, err)

	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions()

	_, err = l.GetStreamInfos()
	assert.Error(t, err)
	assert.ErrorIs(t, err, livepkg.ErrLiveOffline)
}

func TestMapCDNType(t *testing.T) {
	assert.Equal(t, "gs_cdn_pc_web", mapCDNType("gs_cdn"))
	assert.Equal(t, "lg_cdn_pc_web", mapCDNType("lg_cdn"))
	assert.Equal(t, "custom_cdn", mapCDNType("custom_cdn"))
}

func TestBuildBroadKey(t *testing.T) {
	assert.Equal(t, "292157719-common-original-hls", buildBroadKey("292157719", "original"))
}

func TestExplainChannelResult(t *testing.T) {
	assert.Equal(t, "成功", explainChannelResult(channelResultOK))
	assert.Equal(t, "需要登录", explainChannelResult(channelResultLogin))
	assert.Contains(t, explainChannelResult(channelResultEmpty), "未返回有效直播信息")
	assert.Contains(t, explainChannelResult(channelResultBlock), "访问受限")
	assert.Contains(t, explainChannelResult(999), "未知业务码 999")
}

func TestSortViewPresetsByPriority(t *testing.T) {
	presets := []viewPreset{
		{Label: "360p", Name: "sd", LabelResolution: 360, BPS: 500},
		{Label: "1440p", Name: "original", LabelResolution: 1440, BPS: 16000},
		{Label: "1080p", Name: "hd8k", LabelResolution: 1080, BPS: 8000},
		{Label: "720p", Name: "hd4k", LabelResolution: 720, BPS: 4000},
	}

	sortViewPresetsByPriority(presets)

	assert.Equal(t, "1440p", presets[0].Label)
	assert.Equal(t, "1080p", presets[1].Label)
	assert.Equal(t, "720p", presets[2].Label)
	assert.Equal(t, "360p", presets[3].Label)
}

func TestTryVerifyAndReloginIfNeededKeepsCookieWhenVerifyEndpointFails(t *testing.T) {
	oldVerify := verifyCookieFunc
	oldLogin := loginAndGetCookie
	defer func() {
		verifyCookieFunc = oldVerify
		loginAndGetCookie = oldLogin
	}()
	resetVerifyCookieCacheForTest()

	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{
		domainPlaySoop: "SESS=expired",
	}
	cfg.SoopLiveAuth.Username = ""
	cfg.SoopLiveAuth.Password = ""
	configs.SetCurrentConfig(cfg)

	verifyCookieFunc = func(cookie string) (*CookieVerifyResult, error) {
		assert.Equal(t, "SESS=expired", cookie)
		return nil, assert.AnError
	}
	loginAndGetCookie = func(username, password string) (*LoginResult, error) {
		t.Fatal("不应在无账号密码时触发自动登录")
		return nil, nil
	}

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)
	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions(livepkg.WithKVStringCookies(u, "SESS=expired"))

	err = l.tryVerifyAndReloginIfNeeded()
	assert.NoError(t, err)
	assert.False(t, l.ignoreStoredCookie)
	assert.Equal(t, "expired", l.getCookieMap()["SESS"])
}

func TestTryVerifyAndReloginIfNeededIgnoreInvalidCookieForPublicRoom(t *testing.T) {
	oldVerify := verifyCookieFunc
	oldLogin := loginAndGetCookie
	defer func() {
		verifyCookieFunc = oldVerify
		loginAndGetCookie = oldLogin
	}()
	resetVerifyCookieCacheForTest()

	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{
		domainPlaySoop: "SESS=expired",
	}
	cfg.SoopLiveAuth.Username = ""
	cfg.SoopLiveAuth.Password = ""
	configs.SetCurrentConfig(cfg)

	verifyCookieFunc = func(cookie string) (*CookieVerifyResult, error) {
		assert.Equal(t, "SESS=expired", cookie)
		return &CookieVerifyResult{IsLogin: false}, nil
	}
	loginAndGetCookie = func(username, password string) (*LoginResult, error) {
		t.Fatal("不应在无账号密码时触发自动登录")
		return nil, nil
	}

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)
	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions(livepkg.WithKVStringCookies(u, "SESS=expired"))

	err = l.tryVerifyAndReloginIfNeeded()
	assert.NoError(t, err)
	assert.True(t, l.ignoreStoredCookie)
	assert.Empty(t, l.getCookieMap())
}

func TestTryVerifyAndReloginIfNeededSkipsAutoLoginWhenVerifyEndpointFails(t *testing.T) {
	oldVerify := verifyCookieFunc
	oldLogin := loginAndGetCookie
	defer func() {
		verifyCookieFunc = oldVerify
		loginAndGetCookie = oldLogin
	}()
	resetVerifyCookieCacheForTest()

	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{
		domainPlaySoop: "SESS=expired",
	}
	cfg.SoopLiveAuth.Username = "tester"
	cfg.SoopLiveAuth.Password = "secret"
	configs.SetCurrentConfig(cfg)

	verifyCookieFunc = func(cookie string) (*CookieVerifyResult, error) {
		assert.Equal(t, "SESS=expired", cookie)
		return nil, assert.AnError
	}
	loginAndGetCookie = func(username, password string) (*LoginResult, error) {
		t.Fatal("校验接口异常时不应触发自动登录")
		return nil, nil
	}

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)
	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions(livepkg.WithKVStringCookies(u, "SESS=expired"))

	err = l.tryVerifyAndReloginIfNeeded()
	assert.NoError(t, err)
	assert.False(t, l.ignoreStoredCookie)
	assert.Empty(t, l.runtimeCookie)
	assert.Equal(t, "expired", l.getCookieMap()["SESS"])
}

func TestTryVerifyAndReloginIfNeededAutoLoginWhenCredentialsExist(t *testing.T) {
	oldVerify := verifyCookieFunc
	oldLogin := loginAndGetCookie
	defer func() {
		verifyCookieFunc = oldVerify
		loginAndGetCookie = oldLogin
	}()

	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{
		domainPlaySoop: "SESS=expired",
	}
	cfg.SoopLiveAuth.Username = "tester"
	cfg.SoopLiveAuth.Password = "secret"
	configs.SetCurrentConfig(cfg)

	verifyCookieFunc = func(cookie string) (*CookieVerifyResult, error) {
		return &CookieVerifyResult{IsLogin: false}, nil
	}
	loginAndGetCookie = func(username, password string) (*LoginResult, error) {
		assert.Equal(t, "tester", username)
		assert.Equal(t, "secret", password)
		return &LoginResult{
			Cookie:   "SESS=fresh; AUTH=ok",
			Verify:   CookieVerifyResult{IsLogin: true, LoginID: "tester"},
			Username: username,
		}, nil
	}

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)
	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions()

	err = l.tryVerifyAndReloginIfNeeded()
	assert.NoError(t, err)
	assert.False(t, l.ignoreStoredCookie)
	assert.Equal(t, "SESS=fresh; AUTH=ok", l.runtimeCookie)
	assert.Equal(t, "fresh", l.getCookieMap()["SESS"])
}

func TestTryVerifyAndReloginIfNeededFallsBackToAnonymousWhenAutoLoginFails(t *testing.T) {
	oldVerify := verifyCookieFunc
	oldLogin := loginAndGetCookie
	defer func() {
		verifyCookieFunc = oldVerify
		loginAndGetCookie = oldLogin
	}()
	resetVerifyCookieCacheForTest()

	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{
		domainPlaySoop: "SESS=config-expired",
	}
	cfg.SoopLiveAuth.Username = "tester"
	cfg.SoopLiveAuth.Password = "wrong-password"
	configs.SetCurrentConfig(cfg)

	verifyCookieFunc = func(cookie string) (*CookieVerifyResult, error) {
		assert.Equal(t, "SESS=runtime-expired; AUTH=stale", cookie)
		return &CookieVerifyResult{IsLogin: false}, nil
	}
	loginAndGetCookie = func(username, password string) (*LoginResult, error) {
		assert.Equal(t, "tester", username)
		assert.Equal(t, "wrong-password", password)
		return nil, assert.AnError
	}

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)
	l := &Live{
		BaseLive:      internal.NewBaseLive(u),
		runtimeCookie: "SESS=runtime-expired; AUTH=stale",
	}
	l.Options = livepkg.MustNewOptions(livepkg.WithKVStringCookies(u, "SESS=option-expired"))

	err = l.tryVerifyAndReloginIfNeeded()
	assert.NoError(t, err)
	assert.True(t, l.ignoreStoredCookie)
	assert.Empty(t, l.runtimeCookie)
	assert.Empty(t, l.getCookieMap())
}

func TestGetCookieMapPrefersLatestConfigCookieOverStaleOptions(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{
		domainPlaySoop: "SESS=fresh; AUTH=new",
	}
	configs.SetCurrentConfig(cfg)

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)

	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions(livepkg.WithKVStringCookies(u, "SESS=expired; AUTH=old"))

	cookieMap := l.getCookieMap()
	assert.Equal(t, "fresh", cookieMap["SESS"])
	assert.Equal(t, "new", cookieMap["AUTH"])
}

func TestGetPrimaryCookieStringPrefersRuntimeCookie(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{
		domainPlaySoop: "SESS=expired",
	}
	configs.SetCurrentConfig(cfg)

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)

	l := &Live{
		BaseLive:      internal.NewBaseLive(u),
		runtimeCookie: "SESS=fresh; AUTH=ok",
	}
	l.Options = livepkg.MustNewOptions(livepkg.WithKVStringCookies(u, "SESS=older"))

	assert.Equal(t, "SESS=fresh; AUTH=ok", l.getPrimaryCookieString())
}

func TestUpdateLiveOptionsbyConfigClearsRuntimeState(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{
		domainPlaySoop: "SESS=config; AUTH=new",
	}
	configs.SetCurrentConfig(cfg)

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)

	l := &Live{
		BaseLive:           internal.NewBaseLive(u),
		runtimeCookie:      "SESS=runtime; AUTH=old",
		ignoreStoredCookie: true,
	}
	l.Options = livepkg.MustNewOptions(livepkg.WithKVStringCookies(u, "SESS=runtime; AUTH=old"))

	room := &configs.LiveRoom{
		Url: u.String(),
	}

	err = l.UpdateLiveOptionsbyConfig(context.Background(), room)
	assert.NoError(t, err)
	assert.Empty(t, l.runtimeCookie)
	assert.False(t, l.ignoreStoredCookie)

	cookieMap := l.getCookieMap()
	assert.Equal(t, "config", cookieMap["SESS"])
	assert.Equal(t, "new", cookieMap["AUTH"])
}

func TestUpdateLiveOptionsbyConfigKeepsClearActionEffective(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{}
	configs.SetCurrentConfig(cfg)

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)

	l := &Live{
		BaseLive:      internal.NewBaseLive(u),
		runtimeCookie: "SESS=runtime; AUTH=old",
	}
	l.Options = livepkg.MustNewOptions(livepkg.WithKVStringCookies(u, "SESS=runtime; AUTH=old"))

	room := &configs.LiveRoom{
		Url: u.String(),
	}

	err = l.UpdateLiveOptionsbyConfig(context.Background(), room)
	assert.NoError(t, err)
	assert.Empty(t, l.getPrimaryCookieString())
	assert.Empty(t, l.getCookieMap())
}

func TestRuntimeStateHelpersConcurrentAccess(t *testing.T) {
	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)

	l := &Live{
		BaseLive: internal.NewBaseLive(u),
	}
	l.Options = livepkg.MustNewOptions()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(index int) {
			defer wg.Done()
			l.setRuntimeState("SESS=value"+strconv.Itoa(index), index%2 == 0)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = l.getRuntimeState()
			_ = l.getPrimaryCookieString()
		}()
	}
	wg.Wait()

	cookie, _ := l.getRuntimeState()
	assert.Contains(t, cookie, "SESS=value")
}

func TestTryVerifyAndReloginIfNeededCachesVerifyResult(t *testing.T) {
	oldVerify := verifyCookieFunc
	oldTTL := verifyCookieCacheTTL
	oldNow := nowFunc
	defer func() {
		verifyCookieFunc = oldVerify
		verifyCookieCacheTTL = oldTTL
		nowFunc = oldNow
		verifyCookieCacheMu.Lock()
		verifyCookieCache = map[string]cachedVerifyResult{}
		verifyCookieCacheMu.Unlock()
	}()

	verifyCookieCacheTTL = time.Minute
	now := time.Unix(1700000000, 0)
	nowFunc = func() time.Time { return now }

	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{
		domainPlaySoop: "SESS=valid",
	}
	configs.SetCurrentConfig(cfg)

	var verifyCalls int32
	verifyCookieFunc = func(cookie string) (*CookieVerifyResult, error) {
		atomic.AddInt32(&verifyCalls, 1)
		return &CookieVerifyResult{IsLogin: true, LoginID: "tester"}, nil
	}

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)
	l := &Live{BaseLive: internal.NewBaseLive(u)}
	l.Options = livepkg.MustNewOptions()

	assert.NoError(t, l.tryVerifyAndReloginIfNeeded())
	assert.NoError(t, l.tryVerifyAndReloginIfNeeded())
	assert.Equal(t, int32(1), atomic.LoadInt32(&verifyCalls))
}

func TestTryVerifyAndReloginIfNeededDeduplicatesAutoLogin(t *testing.T) {
	oldVerify := verifyCookieFunc
	oldLogin := loginAndGetCookie
	oldSetCookies := setCookiesFunc
	oldTTL := verifyCookieCacheTTL
	defer func() {
		verifyCookieFunc = oldVerify
		loginAndGetCookie = oldLogin
		setCookiesFunc = oldSetCookies
		verifyCookieCacheTTL = oldTTL
		verifyCookieCacheMu.Lock()
		verifyCookieCache = map[string]cachedVerifyResult{}
		verifyCookieCacheMu.Unlock()
	}()

	verifyCookieCacheTTL = 0

	cfg := configs.NewConfig()
	cfg.Cookies = map[string]string{
		domainPlaySoop: "SESS=expired",
	}
	cfg.SoopLiveAuth.Username = "tester"
	cfg.SoopLiveAuth.Password = "secret"
	configs.SetCurrentConfig(cfg)

	verifyCookieFunc = func(cookie string) (*CookieVerifyResult, error) {
		if cookie == "SESS=fresh; AUTH=ok" {
			return &CookieVerifyResult{IsLogin: true, LoginID: "tester"}, nil
		}
		return &CookieVerifyResult{IsLogin: false}, nil
	}

	var loginCalls int32
	loginAndGetCookie = func(username, password string) (*LoginResult, error) {
		atomic.AddInt32(&loginCalls, 1)
		time.Sleep(30 * time.Millisecond)
		return &LoginResult{
			Cookie:   "SESS=fresh; AUTH=ok",
			Verify:   CookieVerifyResult{IsLogin: true, LoginID: "tester"},
			Username: username,
		}, nil
	}
	setCookiesFunc = func(hostCookies map[string]string) (*configs.Config, error) {
		return configs.GetCurrentConfig(), nil
	}

	makeLive := func() *Live {
		u, err := url.Parse("https://play.sooplive.com/mbntv")
		assert.NoError(t, err)
		l := &Live{BaseLive: internal.NewBaseLive(u)}
		l.Options = livepkg.MustNewOptions(livepkg.WithKVStringCookies(u, "SESS=expired"))
		return l
	}

	liveA := makeLive()
	liveB := makeLive()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		assert.NoError(t, liveA.tryVerifyAndReloginIfNeeded())
	}()
	go func() {
		defer wg.Done()
		assert.NoError(t, liveB.tryVerifyAndReloginIfNeeded())
	}()
	wg.Wait()

	assert.Equal(t, int32(1), atomic.LoadInt32(&loginCalls))
	assert.Equal(t, "fresh", liveA.getCookieMap()["SESS"])
	assert.Equal(t, "fresh", liveB.getCookieMap()["SESS"])
}

func TestTryAutoLoginDeduplicatesCookiePersistence(t *testing.T) {
	oldLogin := loginAndGetCookie
	oldSetCookies := setCookiesFunc
	defer func() {
		loginAndGetCookie = oldLogin
		setCookiesFunc = oldSetCookies
	}()

	cfg := configs.NewConfig()
	cfg.SoopLiveAuth.Username = "tester"
	cfg.SoopLiveAuth.Password = "secret"
	configs.SetCurrentConfig(cfg)

	var loginCalls int32
	loginAndGetCookie = func(username, password string) (*LoginResult, error) {
		atomic.AddInt32(&loginCalls, 1)
		time.Sleep(30 * time.Millisecond)
		return &LoginResult{
			Cookie:   "SESS=fresh; AUTH=ok",
			Verify:   CookieVerifyResult{IsLogin: true, LoginID: "tester"},
			Username: username,
		}, nil
	}

	var persistCalls int32
	setCookiesFunc = func(hostCookies map[string]string) (*configs.Config, error) {
		atomic.AddInt32(&persistCalls, 1)
		time.Sleep(30 * time.Millisecond)
		return configs.GetCurrentConfig(), nil
	}

	makeLive := func() *Live {
		u, err := url.Parse("https://play.sooplive.com/mbntv")
		assert.NoError(t, err)
		l := &Live{BaseLive: internal.NewBaseLive(u)}
		l.Options = livepkg.MustNewOptions()
		return l
	}

	liveA := makeLive()
	liveB := makeLive()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		assert.NoError(t, liveA.tryAutoLogin())
	}()
	go func() {
		defer wg.Done()
		assert.NoError(t, liveB.tryAutoLogin())
	}()
	wg.Wait()

	assert.Equal(t, int32(1), atomic.LoadInt32(&loginCalls))
	assert.Equal(t, int32(1), atomic.LoadInt32(&persistCalls))
	assert.Equal(t, "SESS=fresh; AUTH=ok", liveA.runtimeCookie)
	assert.Equal(t, "SESS=fresh; AUTH=ok", liveB.runtimeCookie)
}

func TestTryAutoLoginRejectsUnverifiedLoginResult(t *testing.T) {
	oldLogin := loginAndGetCookie
	oldSetCookies := setCookiesFunc
	defer func() {
		loginAndGetCookie = oldLogin
		setCookiesFunc = oldSetCookies
	}()

	cfg := configs.NewConfig()
	cfg.SoopLiveAuth.Username = "tester"
	cfg.SoopLiveAuth.Password = "secret"
	configs.SetCurrentConfig(cfg)

	loginAndGetCookie = func(username, password string) (*LoginResult, error) {
		return &LoginResult{
			Cookie:   "SESS=fresh; AUTH=ok",
			Verify:   CookieVerifyResult{IsLogin: false},
			Username: username,
		}, nil
	}
	setCookiesFunc = func(hostCookies map[string]string) (*configs.Config, error) {
		t.Fatal("登录态校验未通过时不应持久化 Cookie")
		return nil, nil
	}

	u, err := url.Parse("https://play.sooplive.com/mbntv")
	assert.NoError(t, err)
	l := &Live{BaseLive: internal.NewBaseLive(u)}
	l.Options = livepkg.MustNewOptions()

	err = l.tryAutoLogin()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "登录态校验未通过")
	assert.Empty(t, l.runtimeCookie)
}
