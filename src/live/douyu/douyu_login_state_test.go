package douyu

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
)

func setCfgForTest(t *testing.T, cfg *configs.Config) {
	t.Helper()
	orig := configs.GetCurrentConfig()
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(orig) })
}

func newLiveWithCookie(t *testing.T, cookie string) *Live {
	t.Helper()
	u, err := url.Parse("https://www.douyu.com/123")
	if err != nil {
		t.Fatal(err)
	}
	l := &Live{}
	l.Url = u
	l.SetOptions(live.MustNewOptions(live.WithKVStringCookies(u, cookie)))
	return l
}

const testLoginCookie = "acf_uid=42; acf_auth=auth.value; acf_devid=devid.value"

// 主站 cookie 有效期约 6.1 天，判死阈值 = 有效期 + 24h 余量 ≈ 171h。
// 太早判死会把还能用的登录态主动降级成匿名（丢清晰度），太晚则继续把确凿过期的票据发出去。
func TestLoginCookieKnownExpiredBoundary(t *testing.T) {
	now := time.Unix(1893456000, 0)
	day := int64(24 * time.Hour / time.Second)
	cases := []struct {
		name        string
		ltp0        string
		lastSuccess int64
		want        bool
	}{
		{"无长期凭证（手填 cookie）不判死", "", now.Unix() - 30*day, false},
		{"从未成功过（历史配置）不判死", "ticket", 0, false},
		{"刚续期", "ticket", now.Unix(), false},
		{"6 天前（仍在有效期内）", "ticket", now.Unix() - 6*day, false},
		{"170 小时前（阈值之内）", "ticket", now.Unix() - 170*3600, false},
		{"172 小时前（已过阈值）", "ticket", now.Unix() - 172*3600, true},
		{"进程停摆 10 天后重启", "ticket", now.Unix() - 10*day, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LoginCookieKnownExpired(c.ltp0, c.lastSuccess, now); got != c.want {
				t.Fatalf("判定=%v 期望=%v", got, c.want)
			}
		})
	}
}

// 闸门真正作用在取流侧：判死之后 cookie 不再发出，回到"从未登录过"那条行为已验证的路径。
// 基准必须用真实时间：闸门内部读的是 time.Now()，用未来时间戳会让"过去"变成"未来"而永不判死。
func TestGetCookieStringGate(t *testing.T) {
	now := time.Now()
	day := int64(24 * time.Hour / time.Second)
	cases := []struct {
		name  string
		auth  configs.DouyuAuth
		want  string
		empty bool
	}{
		{"健康续期状态照常发出", configs.DouyuAuth{LTP0: "ticket", LastRenewSuccessAt: now.Unix()}, testLoginCookie, false},
		{"手填 cookie（无票据）照常发出", configs.DouyuAuth{}, testLoginCookie, false},
		{"票据在但从未成功过照常发出", configs.DouyuAuth{LTP0: "ticket"}, testLoginCookie, false},
		{"确凿过期则停用", configs.DouyuAuth{LTP0: "ticket", LastRenewSuccessAt: now.Add(-10 * time.Duration(day) * time.Second).Unix()}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setCfgForTest(t, &configs.Config{DouyuAuth: c.auth, Cookies: map[string]string{CookieHost: testLoginCookie}})
			l := newLiveWithCookie(t, testLoginCookie)
			got := l.getCookieString()
			if c.empty {
				if got != "" {
					t.Fatalf("已判死的 cookie 仍被发出: %q", got)
				}
				return
			}
			for _, kv := range strings.Split(c.want, "; ") {
				if !strings.Contains(got, kv) {
					t.Errorf("cookie 缺少 %q, got=%q", kv, got)
				}
			}
		})
	}
	// Options 尚未就绪（房间初始化早期）不得 panic
	setCfgForTest(t, &configs.Config{})
	if got := (&Live{}).getCookieString(); got != "" {
		t.Errorf("无 Options 时应返回空串, got=%q", got)
	}
}

// 设备号与"登录态是否可用"无关（斗鱼侧设备号本身不过期）：cookie 被停用后仍应沿用同一个 did，
// 换成随机 did 等于换一台新设备，反而是签名侧要避免的风控诱因。
func TestGetDIDSurvivesDeadCookieGate(t *testing.T) {
	setCfgForTest(t, &configs.Config{
		DouyuAuth: configs.DouyuAuth{LTP0: "ticket", LastRenewSuccessAt: time.Now().Add(-10 * 24 * time.Hour).Unix()},
		Cookies:   map[string]string{CookieHost: testLoginCookie},
	})
	l := newLiveWithCookie(t, testLoginCookie)
	if got := l.getDID(); got != "devid.value" {
		t.Fatalf("cookie 停用后 did 未沿用: %q", got)
	}
	if got := l.getCookieString(); got != "" {
		t.Fatalf("闸门未生效: %q", got)
	}
}

// 现有登录 cookie 已有设备号时挡掉续期新签发的随机值；一个都没有时补种，避免每次续期换一台设备。
func TestApplyDeviceIDPolicy(t *testing.T) {
	fresh := map[string]string{
		"acf_uid": "42", "acf_auth": "new", "dy_did": "fresh-dy", "acf_devid": "fresh-devid", "acf_did": "fresh-acfdid",
	}
	cases := []struct {
		name        string
		stored      string
		wantDropped bool
	}{
		{"已有 dy_did 时全部设备号字段都被挡", "acf_uid=42; dy_did=scan-did", true},
		{"已有 acf_devid 同样挡住", "acf_uid=42; acf_devid=scan-devid", true},
		{"已有 acf_did 同样挡住", "acf_uid=42; acf_did=scan-did", true},
		{"一个设备号都没有时补种", "acf_uid=42; acf_auth=old", false},
		{"空设备号视为没有（不得把随机值挡掉）", "acf_uid=42; dy_did=", false},
		{"完全空的 cookie 串", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := ApplyDeviceIDPolicy(c.stored, fresh)
			for _, k := range deviceIDCookieKeys {
				_, present := out[k]
				if c.wantDropped && present {
					t.Errorf("设备号字段 %s 未被挡掉", k)
				}
				if !c.wantDropped && out[k] != fresh[k] {
					t.Errorf("设备号字段 %s 未补种: got=%q want=%q", k, out[k], fresh[k])
				}
			}
			// 与设备号无关的登录字段一律保留
			if out["acf_uid"] != "42" || out["acf_auth"] != "new" {
				t.Errorf("登录字段被误伤: %v", out)
			}
			// 必须返回副本：就地改写会让调用方持有的 map 被悄悄改掉
			if _, ok := fresh["acf_uid"]; !ok {
				t.Fatal("入参 map 被改写")
			}
			for _, k := range deviceIDCookieKeys {
				if _, ok := fresh[k]; !ok {
					t.Fatalf("入参 map 的设备号 %s 被就地删除", k)
				}
			}
		})
	}
}

// 热应用（重新扫码 / 自动续期）之后必须作废缓存的 did，否则新 cookie 配上旧设备号。
func TestUpdateLiveOptionsbyConfigClearsCachedDID(t *testing.T) {
	setCfgForTest(t, &configs.Config{
		DouyuAuth: configs.DouyuAuth{LTP0: "ticket", LastRenewSuccessAt: time.Now().Unix()},
		Cookies:   map[string]string{CookieHost: "acf_uid=99; acf_auth=n; dy_did=new-account-did"},
	})
	l := newLiveWithCookie(t, "acf_uid=42; acf_auth=o; dy_did=old-account-did")
	if got := l.getDID(); got != "old-account-did" {
		t.Fatalf("前置条件不成立: did=%q", got)
	}
	room := &configs.LiveRoom{Url: "https://www.douyu.com/123"}
	if err := l.UpdateLiveOptionsbyConfig(context.Background(), room); err != nil {
		t.Fatal(err)
	}
	if got := l.getDID(); got != "new-account-did" {
		t.Fatalf("热应用后 did 未跟随新 cookie: %q", got)
	}
	if got := l.getCookieString(); !strings.Contains(got, "acf_uid=99") {
		t.Fatalf("热应用未把新 cookie 落到 Options: %q", got)
	}
}

// 别名 URL 房间热应用后必须仍能取到 www.douyu.com 那份 cookie（房间 URL 在加载时已归一）
func TestNormalizeLiveRoomUrlAliases(t *testing.T) {
	cases := map[string]string{
		"https://m.douyu.com/123":     "https://www.douyu.com/123",
		"https://douyu.com/123":       "https://www.douyu.com/123",
		"https://www.douyu.com/123":   "https://www.douyu.com/123",
		"https://live.bilibili.com/1": "https://live.bilibili.com/1",
	}
	for in, want := range cases {
		if got := configs.NormalizeLiveRoomUrl(in); got != want {
			t.Errorf("%s 归一为 %q, 期望 %q", in, got, want)
		}
	}
}
