package servers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	dy "github.com/bililive-go/bililive-go/src/live/douyu"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/types"
)

func ctxWithInstance(inst *instance.Instance) context.Context {
	return context.WithValue(context.Background(), instance.Key, inst)
}

func writableConfig(t *testing.T) *configs.Config {
	t.Helper()
	cfg := configs.NewConfig()
	cfg.File = filepath.Join(t.TempDir(), "config.yml")
	configs.SetCurrentConfig(cfg)
	return cfg
}

// /api/cookies 是"手改登录态"的入口。斗鱼分支必须只在换账号或显式退出时作废长期凭证：
// 同账号补/删字段很常见，顺手清票据会让自动续期从此静默失效（用户只看到"过几天又断流"）。
func TestPutLiveHostCookieDouyuBranches(t *testing.T) {
	const ticket = "ticket.value"
	put := func(t *testing.T, host, cookie string) *configs.Config {
		t.Helper()
		orig := configs.GetCurrentConfig()
		t.Cleanup(func() { configs.SetCurrentConfig(orig) })
		cfg := writableConfig(t)
		cfg.Cookies = map[string]string{dy.CookieHost: "acf_uid=111; acf_auth=old; dy_did=keep-did"}
		cfg.DouyuAuth = configs.DouyuAuth{LTP0: ticket, DyDid: "scan-did", NextRefreshAt: 1893456000, LastRenewSuccessAt: 1893000000, NeedRescan: true}
		configs.SetCurrentConfig(cfg)

		req := httptest.NewRequest(http.MethodPost, "/api/cookies/"+host, strings.NewReader(`{"Host":"`+host+`","Cookie":"`+cookie+`"}`))
		req = req.WithContext(ctxWithInstance(&instance.Instance{Ctx: context.Background()}))
		rec := httptest.NewRecorder()
		putLiveHostCookie(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("保存失败: code=%d body=%s", rec.Code, rec.Body.String())
		}
		return configs.GetCurrentConfig()
	}

	t.Run("同账号微调保留票据并剥离 LTP0", func(t *testing.T) {
		got := put(t, dy.CookieHost, "acf_uid=111; acf_auth=new; LTP0=leaky.ticket")
		if got.DouyuAuth.LTP0 != ticket {
			t.Errorf("同账号修改不应作废长期凭证: %q", got.DouyuAuth.LTP0)
		}
		stored := got.Cookies[dy.CookieHost]
		if strings.Contains(stored, "LTP0") {
			t.Errorf("浏览器导出的 LTP0 未被剥离: %q", stored)
		}
		if !strings.Contains(stored, "acf_auth=new") {
			t.Errorf("新 cookie 未落盘: %q", stored)
		}
	})

	t.Run("换成另一账号的 cookie 时作废票据", func(t *testing.T) {
		got := put(t, dy.CookieHost, "acf_uid=222; acf_auth=other")
		if got.DouyuAuth.LTP0 != "" || got.DouyuAuth.DyDid != "" {
			t.Errorf("换账号后长期凭证未清除: %+v", got.DouyuAuth)
		}
	})

	t.Run("存一份未登录 cookie 视为退出登录", func(t *testing.T) {
		got := put(t, dy.CookieHost, "PHPSESSID=anon")
		if got.DouyuAuth.LTP0 != "" {
			t.Errorf("显式退出后票据未清除: %q", got.DouyuAuth.LTP0)
		}
	})

	t.Run("别名 host 也要走斗鱼分支", func(t *testing.T) {
		got := put(t, "m.douyu.com", "acf_uid=222; acf_auth=other")
		if got.DouyuAuth.LTP0 != "" {
			t.Errorf("别名 host 未走斗鱼分支（票据未清）: %+v", got.DouyuAuth)
		}
		if _, ok := got.Cookies["m.douyu.com"]; ok {
			t.Errorf("别名键未归一，房间将查不到 cookie: %v", got.Cookies)
		}
	})

	t.Run("他平台 host 不受斗鱼分支影响", func(t *testing.T) {
		got := put(t, "live.bilibili.com", "SESSDATA=abc")
		if got.DouyuAuth.LTP0 != ticket {
			t.Errorf("改 B 站 cookie 误清了斗鱼票据: %+v", got.DouyuAuth)
		}
		if got.Cookies["live.bilibili.com"] != "SESSDATA=abc" {
			t.Errorf("B 站 cookie 未写入: %v", got.Cookies)
		}
	})
}

// 落盘只能发生一次且必须在锁内：曾经的做法是 handler 在锁外再 Marshal 一次自己那份快照，
// 会把期间后台续期刚写好的 DouyuAuth/新 cookie 盖回旧值（盘上比内存旧，重启才显现）。
func TestPutLiveHostCookiePersistsOnceInsideLock(t *testing.T) {
	orig := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(orig) })
	cfg := writableConfig(t)
	cfg.Cookies = map[string]string{dy.CookieHost: "acf_uid=111; acf_auth=old"}
	cfg.DouyuAuth = configs.DouyuAuth{LTP0: "ticket.value"}
	configs.SetCurrentConfig(cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/cookies/"+dy.CookieHost,
		strings.NewReader(`{"Host":"www.douyu.com","Cookie":"acf_uid=111; acf_auth=fresh"}`))
	req = req.WithContext(ctxWithInstance(&instance.Instance{Ctx: context.Background()}))
	rec := httptest.NewRecorder()
	putLiveHostCookie(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}

	onDisk, err := os.ReadFile(cfg.File)
	if err != nil {
		t.Fatal(err)
	}
	text := string(onDisk)
	if !strings.Contains(text, "acf_auth=fresh") {
		t.Errorf("盘上仍是旧 cookie（写盘被跳过或顺序错了）: %s", tail(text))
	}
	if strings.Contains(text, "acf_auth=old") {
		t.Errorf("盘上留有旧 cookie（被并发快照覆盖）")
	}
}

// 用户删掉全部斗鱼房间后，列表行只由"运行中的房间 host"推导 —— 那时既看不到红标也无法重扫。
func TestGetLiveHostCookieAddsDouyuRowWithoutRooms(t *testing.T) {
	orig := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(orig) })

	cases := []struct {
		name     string
		cfg      *configs.Config
		wantRows int
	}{
		{"无房间但存有长期凭证", &configs.Config{DouyuAuth: configs.DouyuAuth{LTP0: "t"}}, 1},
		{"无房间只有登录 cookie", &configs.Config{Cookies: map[string]string{dy.CookieHost: "acf_uid=1"}}, 1},
		{"完全没有斗鱼痕迹", &configs.Config{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			configs.SetCurrentConfig(c.cfg)
			rec := httptest.NewRecorder()
			getLiveHostCookie(rec, httptest.NewRequest(http.MethodGet, "/api/cookies", nil).
				WithContext(ctxWithInstance(&instance.Instance{Ctx: context.Background()})))
			var rows []map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
				t.Fatalf("响应不是数组: %v body=%s", err, rec.Body.String())
			}
			if len(rows) != c.wantRows {
				t.Fatalf("行数=%d 期望=%d, body=%s", len(rows), c.wantRows, rec.Body.String())
			}
			for _, r := range rows {
				if r["Host"] != dy.CookieHost {
					t.Errorf("补的行 host 不对: %v", r)
				}
				if _, ok := r["Cookie"].(string); !ok {
					t.Errorf("缺少 Cookie 字段: %v", r)
				}
			}
		})
	}
}

// 记录被热应用了哪些房间，用于验证 applyCookiesToLives 真的触达 Live 且只触达目标 host
type recordingLive struct {
	rawUrl string
	cnName string
	mu     sync.Mutex
	rooms  []configs.LiveRoom
}

func (r *recordingLive) GetPlatformCNName() string { return r.cnName }
func (r *recordingLive) GetOptions() *live.Options { return nil }
func (r *recordingLive) SetLiveIdByString(string)  {}
func (r *recordingLive) GetLiveId() types.LiveID   { return types.LiveID(r.rawUrl) }
func (r *recordingLive) GetRawUrl() string         { return r.rawUrl }
func (r *recordingLive) GetInfo() (*live.Info, error) {
	return nil, nil
}
func (r *recordingLive) GetInfoWithInterval(context.Context) (*live.Info, error) {
	return nil, nil
}
func (r *recordingLive) GetStreamUrls() ([]*url.URL, error)             { return nil, nil }
func (r *recordingLive) GetStreamInfos() ([]*live.StreamUrlInfo, error) { return nil, nil }
func (r *recordingLive) Close()                                         {}
func (r *recordingLive) GetLastStartTime() time.Time                    { return time.Time{} }
func (r *recordingLive) SetLastStartTime(time.Time)                     {}
func (r *recordingLive) GetLogger() *livelogger.LiveLogger              { return nil }
func (r *recordingLive) UpdateLiveOptionsbyConfig(_ context.Context, room *configs.LiveRoom) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rooms = append(r.rooms, *room)
	return nil
}

// 续期/扫码成功后必须把新登录态推给运行中的房间，否则要等到下次重启才生效。
func TestApplyCookiesToLivesTargetsOnlyRequestedHost(t *testing.T) {
	previous := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })
	douyuRoom := configs.LiveRoom{Url: "https://www.douyu.com/123", LiveId: types.LiveID("https://www.douyu.com/123")}
	huyaRoom := configs.LiveRoom{Url: "https://www.huya.com/456", LiveId: types.LiveID("https://www.huya.com/456")}
	l1 := &recordingLive{rawUrl: douyuRoom.Url, cnName: "斗鱼"}
	l2 := &recordingLive{rawUrl: huyaRoom.Url, cnName: "虎牙"}
	inst := &instance.Instance{Ctx: context.Background()}
	inst.Lives.Set(l1.GetLiveId(), l1)
	inst.Lives.Set(l2.GetLiveId(), l2)
	ctx := ctxWithInstance(inst)

	newCfg := configs.NewConfig()
	newCfg.LiveRooms = []configs.LiveRoom{douyuRoom, huyaRoom}
	newCfg.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(newCfg)
	applyCookiesToLives(ctx, newCfg, dy.CookieHost)

	if len(l1.rooms) != 1 {
		t.Fatalf("斗鱼房间被热应用次数=%d 期望 1", len(l1.rooms))
	}
	if len(l2.rooms) != 0 {
		t.Errorf("虎牙房间被误伤: %d 次", len(l2.rooms))
	}
}

// 配置写不进去（磁盘满/目录不可写）时的行为：updateCASImpl 在持久化失败时连内存都不替换。
// 这条用例把"残余项"钉成可观察事实：内存里 NeedRescan 仍为 false，因此失效重试的 24h 升档
// 在该场景下永远停在 6h（升档判据读的是配置里的 NeedRescan）。
func TestHandleRenewInvalidWithUnwritableConfigKeepsMemoryBackoff(t *testing.T) {
	orig := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(orig) })
	clearRenewScheduleOverride()
	t.Cleanup(clearRenewScheduleOverride)

	cfg := configs.NewConfig()
	cfg.File = filepath.Join(t.TempDir(), "no-such-dir", "config.yml") // 目录不存在 ⇒ 必然写盘失败
	cfg.DouyuAuth = configs.DouyuAuth{LTP0: "ticket.value", NextRefreshAt: 1}
	configs.SetCurrentConfig(cfg)

	handleRenewInvalid("ticket.value", dy.ErrLoginInvalid)

	if configs.GetCurrentConfig().DouyuAuth.NeedRescan {
		t.Errorf("写盘失败时内存被改写（与注释口径不符）")
	}
	override := renewScheduleOverride.Load()
	if override == 0 {
		t.Fatal("写盘失败未记内存退避 ⇒ 下一轮会立刻拿同一张失效票据再打 safeAuth")
	}
	now := time.Now().Unix()
	if delta := override - now; delta < 5*3600 || delta > 7*3600 {
		t.Errorf("内存退避不是 6 小时档: delta=%ds", delta)
	}
}

// 界面与录制侧必须同口径：cookie 已判死时 logged_in 也要为 false，
// 否则出现"界面绿着却一直在匿名录制"。
func TestGetDouyuAuthStatusFollowsDeadCookieGate(t *testing.T) {
	orig := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(orig) })
	configs.SetCurrentConfig(&configs.Config{
		Cookies:   map[string]string{dy.CookieHost: "acf_uid=42; acf_auth=x"},
		DouyuAuth: configs.DouyuAuth{LTP0: "ticket", LastRenewSuccessAt: time.Now().Add(-10 * 24 * time.Hour).Unix()},
	})
	rec := httptest.NewRecorder()
	getDouyuAuthStatus(rec, httptest.NewRequest(http.MethodGet, "/api/douyu/auth/status", nil))
	var resp struct {
		Data struct {
			LoggedIn bool `json:"logged_in"`
			HasLTP0  bool `json:"has_ltp0"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.LoggedIn {
		t.Errorf("cookie 已判死但界面仍显示已登录: %s", rec.Body.String())
	}
	if !resp.Data.HasLTP0 {
		t.Errorf("has_ltp0 应为 true（票据还在，只是登录态过期）")
	}

	// 手填 cookie（无票据、无续期记录）时不得据过期判未登录
	configs.SetCurrentConfig(&configs.Config{Cookies: map[string]string{dy.CookieHost: "acf_uid=42; acf_auth=x"}})
	rec2 := httptest.NewRecorder()
	getDouyuAuthStatus(rec2, httptest.NewRequest(http.MethodGet, "/api/douyu/auth/status", nil))
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Data.LoggedIn {
		t.Errorf("手填 cookie 被判未登录: %s", rec2.Body.String())
	}
}

// 状态接口在配置尚未加载时不得 panic（启动早期前端就会开始轮询）
func TestGetDouyuAuthStatusWithoutConfig(t *testing.T) {
	orig := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(orig) })
	configs.SetCurrentConfig(nil)
	rec := httptest.NewRecorder()
	getDouyuAuthStatus(rec, httptest.NewRequest(http.MethodGet, "/api/douyu/auth/status", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code=%d 期望 500", rec.Code)
	}
}

// 回归："设置明文"页的一次普通保存绝不能抹掉不可再生的长期凭证。
// 语义约定：YAML 里整节缺失 = "这一节没改"（旧前端缓存、不认识斗鱼字段的局部客户端），
// 写了这一节但 ltp0 留空 = 用户显式清除。不区分的话，一次保存就把票据连同续期日程
// 一起静默抹掉 —— 自动续期永久失效、界面无任何提示，只能重新扫码。
func TestPutRawConfigDouyuAuthSectionPresence(t *testing.T) {
	const ticket = "ticket.value"
	seed := func(t *testing.T) *configs.Config {
		t.Helper()
		orig := configs.GetCurrentConfig()
		t.Cleanup(func() { configs.SetCurrentConfig(orig) })
		cfg := writableConfig(t)
		cfg.Cookies = map[string]string{dy.CookieHost: "acf_uid=111; acf_auth=old"}
		cfg.DouyuAuth = configs.DouyuAuth{
			LTP0: ticket, DyDid: "scan-did",
			NextRefreshAt: 1893456000, LastRenewSuccessAt: 1893000000,
		}
		configs.SetCurrentConfig(cfg)
		return cfg
	}
	// 明文页正常展示的 YAML：走 getRawConfig 拿，凭证以掩码形态出现
	maskedYaml := func(t *testing.T) string {
		t.Helper()
		rec := httptest.NewRecorder()
		getRawConfig(rec, httptest.NewRequest(http.MethodGet, "/api/raw-config", nil))
		var body struct {
			Config string `json:"config"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Config, "douyu_auth:") {
			t.Fatalf("明文页 YAML 里没有 douyu_auth 节: %s", body.Config)
		}
		return body.Config
	}
	// 返回"内存 + 盘上"两份结果：内存对了盘上没写，等于重启就回退
	submit := func(t *testing.T, cfg *configs.Config, yamlText string) (inMemory *configs.Config, onDisk *configs.Config) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"config": yamlText})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/raw-config", strings.NewReader(string(body)))
		req = req.WithContext(ctxWithInstance(&instance.Instance{Ctx: context.Background()}))
		rec := httptest.NewRecorder()
		putRawConfig(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("保存失败: code=%d body=%s", rec.Code, rec.Body.String())
		}
		text, err := os.ReadFile(cfg.File)
		if err != nil {
			t.Fatal(err)
		}
		disk, err := configs.NewConfigWithBytes(text)
		if err != nil {
			t.Fatalf("落盘配置不可读: %v\n%s", err, tail(string(text)))
		}
		return configs.GetCurrentConfig(), disk
	}
	checkTicket := func(t *testing.T, mem, disk *configs.Config, want string) {
		t.Helper()
		if got := mem.DouyuAuth.LTP0; got != want {
			t.Errorf("内存票据=%q 期望=%q", got, want)
		}
		if got := disk.DouyuAuth.LTP0; got != want {
			t.Errorf("盘上票据=%q 期望=%q", got, want)
		}
	}

	t.Run("掩码原样保存：票据日程与 cookie 全部不动", func(t *testing.T) {
		cfg := seed(t)
		mem, disk := submit(t, cfg, maskedYaml(t))
		checkTicket(t, mem, disk, ticket)
		if mem.DouyuAuth.DyDid != "scan-did" {
			t.Errorf("设备号被掩码冲掉: %q", mem.DouyuAuth.DyDid)
		}
		if mem.DouyuAuth.NextRefreshAt != 1893456000 {
			t.Errorf("续期日程被改动: %d", mem.DouyuAuth.NextRefreshAt)
		}
		if mem.Cookies[dy.CookieHost] != "acf_uid=111; acf_auth=old" {
			t.Errorf("登录 cookie 被改掉: %q", mem.Cookies[dy.CookieHost])
		}
	})

	t.Run("两节均未提交：票据与登录 cookie 一律照抄最新值", func(t *testing.T) {
		mem, disk := submit(t, seed(t), "out_put_path: ./\n")
		checkTicket(t, mem, disk, ticket)
		// cookies 整节没提交 = 这一节没改（旧前端缓存、不认识斗鱼字段的局部客户端），
		// 不能当成"把所有登录 cookie 清空"：票据一旦同时失效，抹掉 cookie 就只能重新扫码。
		if got := mem.Cookies[dy.CookieHost]; got != "acf_uid=111; acf_auth=old" {
			t.Errorf("未提交 cookies 节却改动了登录 cookie: %q", got)
		}
		if mem.DouyuAuth.NextRefreshAt != 1893456000 {
			t.Errorf("未提交 cookies 节却改动了续期日程: %d", mem.DouyuAuth.NextRefreshAt)
		}
		if disk.DouyuAuth.NextRefreshAt != 1893456000 {
			t.Errorf("盘上续期日程被改动: %d", disk.DouyuAuth.NextRefreshAt)
		}
	})

	t.Run("只剩票据没有登录 cookie：排期改为立即", func(t *testing.T) {
		// 升级后的存量状态：盘上有 LTP0 但没有登录 cookie（或只有无 acf_uid 的匿名 cookie）。
		// 此时一次普通保存若仍按原日程排队，要空转到最长 3 天后才换回登录态，中间一直匿名录制。
		cfg := seed(t)
		delete(cfg.Cookies, dy.CookieHost)
		mem, disk := submit(t, cfg, "out_put_path: ./\n")
		checkTicket(t, mem, disk, ticket)
		if douyuCookieUid(mem.Cookies[dy.CookieHost]) != "" {
			t.Fatalf("用例前提不成立：登录 cookie 并未丢失")
		}
		if mem.DouyuAuth.NextRefreshAt != 0 {
			t.Errorf("登录态已丢却仍按旧日程排队: %d", mem.DouyuAuth.NextRefreshAt)
		}
		if disk.DouyuAuth.NextRefreshAt != 0 {
			t.Errorf("盘上日程未同步为立即: %d", disk.DouyuAuth.NextRefreshAt)
		}
	})

	t.Run("把接口响应体整份当 YAML 提交：同样不得丢票", func(t *testing.T) {
		// 这是实际的事故形态：客户端没取出 JSON 里的 config 字段，而是把整段响应体提交了回去。
		// 它按 YAML 解析是一个只含 "config" 键的流式映射，两节都不存在。
		cfg := seed(t)
		rec := httptest.NewRecorder()
		getRawConfig(rec, httptest.NewRequest(http.MethodGet, "/api/raw-config", nil))
		body, err := json.Marshal(map[string]string{"config": rec.Body.String()})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/raw-config", strings.NewReader(string(body)))
		req = req.WithContext(ctxWithInstance(&instance.Instance{Ctx: context.Background()}))
		rec2 := httptest.NewRecorder()
		putRawConfig(rec2, req)
		// 接受（视为未提交这些节）或拒绝都必须保住票据
		if code := rec2.Code; code == http.StatusOK {
			text, readErr := os.ReadFile(cfg.File)
			if readErr != nil {
				t.Fatal(readErr)
			}
			disk, err := configs.NewConfigWithBytes(text)
			if err != nil {
				t.Fatal(err)
			}
			if disk.DouyuAuth.LTP0 != ticket {
				t.Errorf("事故形态提交后盘上票据丢失: %q", disk.DouyuAuth.LTP0)
			}
		}
		if got := configs.GetCurrentConfig().DouyuAuth.LTP0; got != ticket {
			t.Errorf("事故形态提交后内存票据丢失: %q", got)
		}
	})

	t.Run("保留该节但 ltp0 留空：显式清除仍然生效", func(t *testing.T) {
		mem, disk := submit(t, seed(t), "douyu_auth:\n    ltp0: \"\"\n    next_refresh_at: 0\n")
		checkTicket(t, mem, disk, "")
	})

	t.Run("cookies 节内换了账号：票据作废以防静默混号", func(t *testing.T) {
		mem, disk := submit(t, seed(t), "douyu_auth:\n    ltp0: \""+ticket+"\"\ncookies:\n    www.douyu.com: acf_uid=222; acf_auth=other\n")
		checkTicket(t, mem, disk, "")
	})
}

func tail(s string) string {
	if len(s) > 200 {
		return s[len(s)-200:]
	}
	return s
}
