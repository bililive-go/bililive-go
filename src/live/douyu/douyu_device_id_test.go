package douyu

import (
	"net/url"
	"regexp"
	"testing"

	"github.com/bililive-go/bililive-go/src/live"
)

// 签名里的 did 必须与登录 cookie 里的设备号一致，否则等于用"已登录的 cookie + 陌生设备号"取流。
// 而扫码链是纯 HTTP、不执行页面 JS，斗鱼实际下发的设备号字段是 acf_devid（浏览器里才是 dy_did），
// 只认 dy_did/acf_did 会让这段复用逻辑永远落空、退化成每次重启换一个随机设备号。
func TestGetDIDReusesDeviceIDFromCookie(t *testing.T) {
	u, err := url.Parse("https://www.douyu.com/123")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		cookie string
		want   string
	}{
		{"acf_devid（扫码链实际形态）", "acf_devid=devid-value; acf_uid=42", "devid-value"},
		{"dy_did（浏览器登录形态）", "dy_did=dydid-value; acf_uid=42", "dydid-value"},
		{"acf_did", "acf_did=acfdid-value", "acfdid-value"},
		{"多种并存时按 dy_did 优先", "acf_devid=devid-value; dy_did=dydid-value", "dydid-value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := &Live{}
			l.Url = u
			l.SetOptions(live.MustNewOptions(live.WithKVStringCookies(u, c.cookie)))
			if got := l.getDID(); got != c.want {
				t.Fatalf("签名 did=%q 期望=%q（cookie=%s）", got, c.want, c.cookie)
			}
		})
	}
}

// 完全没有设备号字段时才自行生成，且必须在 Live 生命周期内固定，避免每次签名都换新设备号
func TestGetDIDFallsBackToStableGeneratedID(t *testing.T) {
	u, err := url.Parse("https://www.douyu.com/123")
	if err != nil {
		t.Fatal(err)
	}
	l := &Live{}
	l.Url = u
	l.SetOptions(live.MustNewOptions(live.WithKVStringCookies(u, "acf_uid=42; acf_auth=x")))
	first := l.getDID()
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(first) {
		t.Fatalf("生成的 did 形态异常: %q", first)
	}
	if second := l.getDID(); second != first {
		t.Fatalf("did 未固定: %q != %q", second, first)
	}
}
