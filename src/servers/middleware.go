package servers

import (
	"net/http"
	"strings"

	"github.com/bililive-go/bililive-go/src/configs"
	applog "github.com/bililive-go/bililive-go/src/log"
)

// log 是一个 HTTP 中间件，用于记录请求日志（保留供本地调试使用）
//
//nolint:unused
func log(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applog.GetLogger().WithFields(map[string]any{
			"Method":     r.Method,
			"Path":       r.RequestURI,
			"RemoteAddr": r.RemoteAddr,
		}).Debug("Http Request")
		handler.ServeHTTP(w, r)
	})
}

// basicAuth 是一个 HTTP 中间件，用于Web界面的Basic认证
func basicAuth(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		config := configs.GetCurrentConfig()
		auth := config.RPC.Authentication

		// 如果未启用身份验证，直接放行
		if !auth.Enable || auth.WebUsername == "" || auth.WebPassword == "" {
			handler.ServeHTTP(w, r)
			return
		}

		// 获取Basic Auth凭证
		username, password, ok := r.BasicAuth()
		if !ok || username != auth.WebUsername || password != auth.WebPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="bililive-go"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		handler.ServeHTTP(w, r)
	})
}

// apiKeyAuth 是一个 HTTP 中间件，用于API的密钥认证
func apiKeyAuth(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		config := configs.GetCurrentConfig()
		auth := config.RPC.Authentication

		// 如果未启用身份验证，直接放行
		if !auth.Enable || auth.APIKey == "" {
			handler.ServeHTTP(w, r)
			return
		}

		// 从请求头获取API密钥
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			// 也支持从Authorization头获取 (Bearer token格式)
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				apiKey = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if apiKey != auth.APIKey {
			http.Error(w, "Unauthorized: Invalid API Key", http.StatusUnauthorized)
			return
		}

		handler.ServeHTTP(w, r)
	})
}
