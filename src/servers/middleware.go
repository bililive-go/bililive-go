package servers

import (
	"net/http"

	"github.com/bililive-go/bililive-go/src/instance"
)

func log(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		instance.GetInstance(r.Context()).Logger.WithFields(map[string]any{
			"Method":     r.Method,
			"Path":       r.RequestURI,
			"RemoteAddr": r.RemoteAddr,
		}).Debug("Http Request")
		handler.ServeHTTP(w, r)
	})
}
