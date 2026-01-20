package servers

import (
	"net/http"

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
