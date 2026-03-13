package sooplive

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBuildCookieStringFromJar(t *testing.T) {
	jar, err := cookiejar.New(nil)
	assert.NoError(t, err)

	jar.SetCookies(playSoopURL, parseCookies(
		"Soo=1",
		"Auth=abc",
	))
	jar.SetCookies(loginSoopURL, parseCookies(
		"Auth=override",
		"Session=xyz",
	))

	cookie := buildCookieStringFromJar(jar)
	assert.Contains(t, cookie, "Soo=1")
	assert.Contains(t, cookie, "Auth=override")
	assert.Contains(t, cookie, "Session=xyz")
}

func parseCookies(values ...string) []*http.Cookie {
	cookies := make([]*http.Cookie, 0, len(values))
	for _, item := range values {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Name:  parts[0],
			Value: parts[1],
		})
	}
	return cookies
}

func TestVerifyCookieString(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/auth", r.URL.Path)
		assert.Contains(t, r.Header.Get("Cookie"), "SESS=abc")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"CHANNEL":{"LOGIN_ID":"tester"}}`))
	}))
	defer server.Close()

	restore := overrideSoopAuthEndpoints(t, server.URL+"/auth", server.URL+"/login", server.URL)
	defer restore()

	result, err := VerifyCookieString("SESS=abc")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.IsLogin)
	assert.Equal(t, "tester", result.LoginID)
}

func TestVerifyCookieStringNotLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"CHANNEL":{"LOGIN_ID":""}}`))
	}))
	defer server.Close()

	restore := overrideSoopAuthEndpoints(t, server.URL+"/auth", server.URL+"/login", server.URL)
	defer restore()

	result, err := VerifyCookieString("SESS=abc")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.IsLogin)
	assert.Empty(t, result.LoginID)
}

func TestLoginAndGetCookieSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			assert.Equal(t, http.MethodPost, r.Method)
			_ = r.ParseForm()
			assert.Equal(t, "login", r.FormValue("szWork"))
			assert.Equal(t, "tester", r.FormValue("szUid"))
			assert.Equal(t, "secret", r.FormValue("szPassword"))
			http.SetCookie(w, &http.Cookie{Name: "SESS", Value: "abc", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "AUTH", Value: "xyz", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"RESULT":1}`))
		case "/auth":
			assert.Contains(t, r.Header.Get("Cookie"), "SESS=abc")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"CHANNEL":{"LOGIN_ID":"tester"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := overrideSoopAuthEndpoints(t, server.URL+"/auth", server.URL+"/login", server.URL)
	defer restore()

	result, err := LoginAndGetCookie("tester", "secret")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "tester", result.Username)
	assert.True(t, result.Verify.IsLogin)
	assert.Contains(t, result.Cookie, "SESS=abc")
	assert.Contains(t, result.Cookie, "AUTH=xyz")
}

func TestLoginAndGetCookieFailureResultCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"RESULT":0}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	restore := overrideSoopAuthEndpoints(t, server.URL+"/auth", server.URL+"/login", server.URL)
	defer restore()

	result, err := LoginAndGetCookie("tester", "secret")
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "平台返回业务码")
}

func TestLoginAndGetCookieFailureWhenVerifyNotLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "SESS", Value: "abc", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "AUTH", Value: "xyz", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"RESULT":1}`))
		case "/auth":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"CHANNEL":{"LOGIN_ID":""}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := overrideSoopAuthEndpoints(t, server.URL+"/auth", server.URL+"/login", server.URL)
	defer restore()

	result, err := LoginAndGetCookie("tester", "secret")
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "登录态二次校验未通过")
}

func TestVerifyCookieCacheUsesHashedKey(t *testing.T) {
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
	nowFunc = func() time.Time { return time.Unix(1700000000, 0) }
	verifyCookieFunc = func(cookie string) (*CookieVerifyResult, error) {
		return &CookieVerifyResult{IsLogin: true, LoginID: "tester"}, nil
	}

	result, err := verifyCookieWithCache("SESS=secret-value; AUTH=secret-token")
	assert.NoError(t, err)
	assert.NotNil(t, result)

	verifyCookieCacheMu.Lock()
	defer verifyCookieCacheMu.Unlock()
	assert.Len(t, verifyCookieCache, 1)
	for key := range verifyCookieCache {
		assert.NotContains(t, key, "SESS=secret-value")
		assert.NotContains(t, key, "AUTH=secret-token")
		assert.Equal(t, 64, len(key))
	}
}

func TestVerifyCookieCacheHasMaxEntries(t *testing.T) {
	oldVerify := verifyCookieFunc
	oldTTL := verifyCookieCacheTTL
	oldMax := verifyCookieCacheMax
	oldNow := nowFunc
	defer func() {
		verifyCookieFunc = oldVerify
		verifyCookieCacheTTL = oldTTL
		verifyCookieCacheMax = oldMax
		nowFunc = oldNow
		verifyCookieCacheMu.Lock()
		verifyCookieCache = map[string]cachedVerifyResult{}
		verifyCookieCacheMu.Unlock()
	}()

	verifyCookieCacheTTL = time.Hour
	verifyCookieCacheMax = 2
	now := time.Unix(1700000000, 0)
	nowFunc = func() time.Time { return now }
	verifyCookieFunc = func(cookie string) (*CookieVerifyResult, error) {
		now = now.Add(time.Second)
		return &CookieVerifyResult{IsLogin: true, LoginID: cookie}, nil
	}

	_, err := verifyCookieWithCache("SESS=1")
	assert.NoError(t, err)
	_, err = verifyCookieWithCache("SESS=2")
	assert.NoError(t, err)
	_, err = verifyCookieWithCache("SESS=3")
	assert.NoError(t, err)

	verifyCookieCacheMu.Lock()
	defer verifyCookieCacheMu.Unlock()
	assert.Len(t, verifyCookieCache, 2)
}

func TestVerifyCookieStringCachedUsesCache(t *testing.T) {
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
	nowFunc = func() time.Time { return time.Unix(1700000000, 0) }

	var calls int
	verifyCookieFunc = func(cookie string) (*CookieVerifyResult, error) {
		calls++
		return &CookieVerifyResult{IsLogin: true, LoginID: "tester"}, nil
	}

	resultA, err := VerifyCookieStringCached("SESS=abc")
	assert.NoError(t, err)
	assert.NotNil(t, resultA)
	resultB, err := VerifyCookieStringCached("SESS=abc")
	assert.NoError(t, err)
	assert.NotNil(t, resultB)
	assert.Equal(t, 1, calls)
}

func overrideSoopAuthEndpoints(t *testing.T, authURL, loginURL, baseURL string) func() {
	t.Helper()

	oldAuthEndpoint := authCheckEndpoint
	oldLoginEndpoint := loginEndpoint
	oldNewHTTPClient := newHTTPClient
	oldPlaySoopURL := playSoopURL
	oldLoginSoopURL := loginSoopURL
	oldLiveSoopURL := liveSoopURL
	oldAfeventSoopURL := afeventSoopURL

	baseParsed, err := url.Parse(baseURL)
	assert.NoError(t, err)

	authParsed, err := url.Parse(authURL)
	assert.NoError(t, err)

	loginParsed, err := url.Parse(loginURL)
	assert.NoError(t, err)

	authCheckEndpoint = authURL
	loginEndpoint = loginURL
	newHTTPClient = func() *http.Client {
		return &http.Client{}
	}
	playSoopURL = baseParsed
	loginSoopURL = loginParsed
	liveSoopURL = baseParsed
	afeventSoopURL = authParsed

	return func() {
		authCheckEndpoint = oldAuthEndpoint
		loginEndpoint = oldLoginEndpoint
		newHTTPClient = oldNewHTTPClient
		playSoopURL = oldPlaySoopURL
		loginSoopURL = oldLoginSoopURL
		liveSoopURL = oldLiveSoopURL
		afeventSoopURL = oldAfeventSoopURL
	}
}
