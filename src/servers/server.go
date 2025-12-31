package servers

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	_ "net/http/pprof"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	applog "github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/tools"
	"github.com/bililive-go/bililive-go/src/webapp"
)

const (
	apiRouterPrefix = "/api"
)

type Server struct {
	server *http.Server
}

// dynamicHandler 持有一个可热切换的 http.Handler。
// 初始为占位 handler（例如返回 503），当 tools WebUI 端口可用时切换为反向代理。
type handlerHolder struct{ H http.Handler }

// 使用 atomic.Value 存储统一的具体类型，避免不同具体类型导致的 panic。
type dynamicHandler struct{ h atomic.Value }

func (d *dynamicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if v := d.h.Load(); v != nil {
		if hh, ok := v.(handlerHolder); ok && hh.H != nil {
			hh.H.ServeHTTP(w, r)
			return
		}
	}
	http.Error(w, "Tools Web UI 未就绪", http.StatusServiceUnavailable)
}

func initMux(ctx context.Context) *mux.Router {
	m := mux.NewRouter()
	m.Use(func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handler.ServeHTTP(w,
				r.WithContext(
					context.WithValue(
						r.Context(),
						instance.Key,
						instance.GetInstance(ctx),
					),
				),
			)
		})
	}, log)

	// api router
	apiRoute := m.PathPrefix(apiRouterPrefix).Subrouter()
	apiRoute.Use(mux.CORSMethodMiddleware(apiRoute))
	apiRoute.HandleFunc("/info", getInfo).Methods("GET")
	apiRoute.HandleFunc("/config", getConfig).Methods("GET")
	apiRoute.HandleFunc("/config", putConfig).Methods("PUT")
	apiRoute.HandleFunc("/raw-config", getRawConfig).Methods("GET")
	apiRoute.HandleFunc("/raw-config", putRawConfig).Methods("PUT")
	apiRoute.HandleFunc("/lives", getAllLives).Methods("GET")
	apiRoute.HandleFunc("/lives", addLives).Methods("POST")
	apiRoute.HandleFunc("/lives/{id}", getLive).Methods("GET")
	apiRoute.HandleFunc("/lives/{id}", removeLive).Methods("DELETE")
	apiRoute.HandleFunc("/lives/{id}/{action}", parseLiveAction).Methods("GET")
	apiRoute.HandleFunc("/file/{path:.*}", getFileInfo).Methods("GET")
	apiRoute.HandleFunc("/cookies", getLiveHostCookie).Methods("GET")
	apiRoute.HandleFunc("/cookies", putLiveHostCookie).Methods("PUT")
	apiRoute.Handle("/metrics", promhttp.Handler())

	m.PathPrefix("/files/").Handler(
		CORSMiddleware(
			http.StripPrefix(
				"/files/",
				http.FileServer(
					http.Dir(
						configs.GetCurrentConfig().OutPutPath,
					),
				),
			),
		),
	)

	// /tools -> /tools/ 的 301 重定向（保留查询参数）
	m.HandleFunc("/tools", func(w http.ResponseWriter, r *http.Request) {
		target := "/tools/"
		if q := r.URL.RawQuery; q != "" {
			target += "?" + q
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})

	// /tools/ 动态反向代理：当 tools WebUI 端口未就绪时返回 503，
	// 一旦端口出现或变化，热更新为对应端口的反向代理。
	dyn := &dynamicHandler{}
	// 设置初始占位 handler（使用统一的包装类型）
	dyn.h.Store(handlerHolder{H: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Tools Web UI 未就绪", http.StatusServiceUnavailable)
	})})
	m.PathPrefix("/tools/").Handler(
		http.StripPrefix(
			"/tools",
			dyn,
		),
	)

	// 监控 tools WebUI 端口变化并热更新反向代理
	go func() {
		var lastPort int
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				port := tools.GetWebUIPort()
				if port == 0 || port == lastPort {
					continue
				}
				lastPort = port
				target, _ := url.Parse("http://localhost:" + strconv.Itoa(port))
				proxy := httputil.NewSingleHostReverseProxy(target)
				// 可选：当下游未就绪时给出明确错误
				proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
					http.Error(w, "无法连接到 Tools Web UI: "+err.Error(), http.StatusBadGateway)
				}
				// 热切换为新的 proxy（保持与初始 Store 相同的具体类型）
				dyn.h.Store(handlerHolder{H: http.Handler(proxy)})
			}
		}
	}()

	fs, err := webapp.FS()
	if err != nil {
		applog.GetLogger().Fatal(err)
	}
	m.PathPrefix("/").Handler(http.FileServer(fs))

	// pprof
	if configs.IsDebug() {
		m.PathPrefix("/debug/").Handler(http.DefaultServeMux)
	}
	return m
}

func CORSMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
		h.ServeHTTP(w, r)
	})
}

func NewServer(ctx context.Context) *Server {
	inst := instance.GetInstance(ctx)
	config := configs.GetCurrentConfig()
	httpServer := &http.Server{
		Addr:    config.RPC.Bind,
		Handler: initMux(ctx),
	}
	server := &Server{server: httpServer}
	inst.Server = server
	return server
}

func (s *Server) Start(ctx context.Context) error {
	inst := instance.GetInstance(ctx)
	inst.WaitGroup.Add(1)
	go func() {
		listener, err := net.Listen("tcp4", s.server.Addr)
		if err != nil {
			applog.GetLogger().Error(err)
			return
		}
		switch err := s.server.Serve(listener); err {
		case nil, http.ErrServerClosed:
		default:
			applog.GetLogger().Error(err)
		}
	}()
	applog.GetLogger().Infof("Server start at %s", s.server.Addr)
	return nil
}

func (s *Server) Close(ctx context.Context) {
	inst := instance.GetInstance(ctx)
	inst.WaitGroup.Done()
	ctx2, cancel := context.WithCancel(ctx)
	if err := s.server.Shutdown(ctx2); err != nil {
		applog.GetLogger().WithError(err).Error("failed to shutdown server")
	}
	defer cancel()
	applog.GetLogger().Infof("Server close")
}
