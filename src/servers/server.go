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
	"github.com/bililive-go/bililive-go/src/pipeline"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/tools"
	"github.com/bililive-go/bililive-go/src/types"
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
	} /* , log */)

	// api router
	apiRoute := m.PathPrefix(apiRouterPrefix).Subrouter()
	apiRoute.Use(mux.CORSMethodMiddleware(apiRoute))
	apiRoute.HandleFunc("/info", getInfo).Methods("GET")
	apiRoute.HandleFunc("/config", getConfig).Methods("GET")
	apiRoute.HandleFunc("/config", putConfig).Methods("PUT")
	apiRoute.HandleFunc("/config", updateConfig).Methods("PATCH")               // 新增：部分更新配置
	apiRoute.HandleFunc("/config/effective", getEffectiveConfig).Methods("GET") // 新增：获取实际生效的配置
	apiRoute.HandleFunc("/config/platforms", getPlatformStats).Methods("GET")   // 新增：获取平台统计
	apiRoute.HandleFunc("/config/platforms/{platform}", updatePlatformConfig).Methods("PUT", "PATCH")
	apiRoute.HandleFunc("/config/platforms/{platform}", deletePlatformConfig).Methods("DELETE")
	apiRoute.HandleFunc("/config/rooms/id/{id}", updateRoomConfigById).Methods("PUT", "PATCH") // 更具体的路由必须在通配符之前
	apiRoute.HandleFunc("/config/rooms/{url:.*}", updateRoomConfig).Methods("PUT", "PATCH")
	apiRoute.HandleFunc("/config/preview-template", previewOutputTmpl).Methods("POST") // 新增：模板预览
	apiRoute.HandleFunc("/raw-config", getRawConfig).Methods("GET")
	apiRoute.HandleFunc("/raw-config", putRawConfig).Methods("PUT")
	apiRoute.HandleFunc("/lives", getAllLives).Methods("GET")
	apiRoute.HandleFunc("/lives", addLives).Methods("POST")
	apiRoute.HandleFunc("/lives/{id}", getLive).Methods("GET")
	apiRoute.HandleFunc("/lives/{id}", removeLive).Methods("DELETE")
	apiRoute.HandleFunc("/lives/{id}/logs", getLiveLogs).Methods("GET")
	apiRoute.HandleFunc("/lives/{id}/sessions", getLiveSessionHistory).Methods("GET")    // 获取直播会话历史
	apiRoute.HandleFunc("/lives/{id}/name-history", getLiveNameHistory).Methods("GET")   // 获取名称变更历史
	apiRoute.HandleFunc("/lives/{id}/history", getLiveHistory).Methods("GET")            // 获取统一历史事件（支持分页筛选）
	apiRoute.HandleFunc("/lives/{id}/switchStream", switchStreamHandler).Methods("POST") // 切换流设置（需要请求体，必须在通配符之前）
	apiRoute.HandleFunc("/lives/{id}/{action}", parseLiveAction).Methods("GET")          // 通配符路由必须放在最后
	apiRoute.HandleFunc("/file/{path:.*}", getFileInfo).Methods("GET")
	apiRoute.HandleFunc("/file/{path:.*}", renameFile).Methods("PUT")
	apiRoute.HandleFunc("/file/{path:.*}", deleteFile).Methods("DELETE")
	apiRoute.HandleFunc("/batch/file/rename", batchRenameFiles).Methods("PUT")
	apiRoute.HandleFunc("/batch/file/delete", batchDeleteFiles).Methods("POST")
	apiRoute.HandleFunc("/cookies", getLiveHostCookie).Methods("GET")
	apiRoute.HandleFunc("/cookies", putLiveHostCookie).Methods("PUT")

	// Bilibili Login
	apiRoute.HandleFunc("/bilibili/qrcode", getBilibiliQRCode).Methods("GET")
	apiRoute.HandleFunc("/bilibili/qrcode/poll", pollBilibiliQRCode).Methods("GET")
	apiRoute.HandleFunc("/bilibili/cookie/verify", verifyBilibiliCookie).Methods("POST")
	apiRoute.HandleFunc("/sse", sseHandler).Methods("GET") // SSE 实时推送端点
	// 远程 WebUI 路由
	apiRoute.HandleFunc("/webui/remote/status", getRemoteWebuiStatus).Methods("GET")  // 获取远程 WebUI 状态
	apiRoute.HandleFunc("/webui/remote/check", checkRemoteWebuiUpdate).Methods("GET") // 检查远程 WebUI 更新
	apiRoute.HandleFunc("/memory", getMemoryStats).Methods("GET")                     // 获取内存统计信息
	// 更新 API 路由
	apiRoute.HandleFunc("/update/check", checkUpdate).Methods("GET")          // 检查更新
	apiRoute.HandleFunc("/update/latest", getLatestRelease).Methods("GET")    // 获取最新版本信息
	apiRoute.HandleFunc("/update/download", downloadUpdate).Methods("POST")   // 下载更新
	apiRoute.HandleFunc("/update/status", getUpdateStatus).Methods("GET")     // 获取更新状态
	apiRoute.HandleFunc("/update/apply", applyUpdate).Methods("POST")         // 应用更新
	apiRoute.HandleFunc("/update/cancel", cancelUpdate).Methods("POST")       // 取消下载
	apiRoute.HandleFunc("/update/channel", setUpdateChannel).Methods("PUT")   // 设置更新通道
	apiRoute.HandleFunc("/update/launcher", getLauncherStatus).Methods("GET") // 获取启动器状态
	apiRoute.HandleFunc("/update/rollback", getRollbackInfo).Methods("GET")   // 获取回滚信息
	apiRoute.HandleFunc("/update/rollback", doRollback).Methods("POST")       // 执行回滚
	apiRoute.Handle("/metrics", promhttp.Handler())

	// IO 统计 API 路由
	apiRoute.HandleFunc("/iostats", getIOStats).Methods("GET")
	apiRoute.HandleFunc("/iostats/requests", getRequestStatus).Methods("GET")
	apiRoute.HandleFunc("/iostats/filters", getIOStatsFilters).Methods("GET")
	apiRoute.HandleFunc("/iostats/disk", getDiskIOStats).Methods("GET")                   // 系统磁盘 I/O 统计
	apiRoute.HandleFunc("/iostats/devices", getDiskDevices).Methods("GET")                // 可用磁盘设备列表
	apiRoute.HandleFunc("/iostats/memory", getMemoryStatsHistory).Methods("GET")          // 内存统计历史数据
	apiRoute.HandleFunc("/iostats/memory/categories", getMemoryCategories).Methods("GET") // 可用内存类别列表

	// OpenList (云上传) API 路由
	apiRoute.HandleFunc("/openlist/status", getOpenListStatus).Methods("GET")
	apiRoute.HandleFunc("/openlist/check-storage", checkOpenListStorageHealth).Methods("GET")

	// Pipeline 任务路由
	inst := instance.GetInstance(ctx)
	if pm := pipeline.GetManager(inst); pm != nil {
		RegisterPipelineHandlers(apiRoute, pm)
	}

	// OSRP 开放直播录制协议路由
	RegisterOSRPRoutes(m, inst)

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
	bilisentry.GoWithContext(ctx, func(ctx context.Context) {
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
	})

	fs, err := webapp.FS()
	if err != nil {
		applog.GetLogger().Fatal(err)
	}
	m.PathPrefix("/").Handler(http.FileServer(fs))

	// pprof
	if configs.IsDebug() {
		m.PathPrefix("/debug/").Handler(http.DefaultServeMux)
		apiRoute.HandleFunc("/debug/sentry-test", func(w http.ResponseWriter, r *http.Request) {
			eventID := bilisentry.CaptureTestMessage()
			w.Write([]byte("Sentry test message sent, Event ID: " + eventID))
		}).Methods("GET")
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

	// 设置录制器状态广播回调
	setupRecorderStatusBroadcast()

	return server
}

func (s *Server) Start(ctx context.Context) error {
	inst := instance.GetInstance(ctx)
	inst.WaitGroup.Add(1)
	bilisentry.Go(func() {
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
	})
	applog.GetLogger().Infof("Server start at %s", s.server.Addr)
	return nil
}

func (s *Server) Close(ctx context.Context) {
	inst := instance.GetInstance(ctx)
	inst.WaitGroup.Done()
	// 先关闭所有 SSE 连接，避免 Shutdown 时等待
	GetSSEHub().Close()
	ctx2, cancel := context.WithCancel(ctx)
	if err := s.server.Shutdown(ctx2); err != nil {
		applog.GetLogger().WithError(err).Error("failed to shutdown server")
	}
	defer cancel()
	applog.GetLogger().Infof("Server close")
}

// setupRecorderStatusBroadcast 设置录制器状态广播回调
func setupRecorderStatusBroadcast() {
	// 设置回调函数，让 recorders 包能够调用 SSE 广播
	recorders.SetBroadcastRecorderStatusFunc(func(liveId types.LiveID, status map[string]interface{}) {
		GetSSEHub().BroadcastRecorderStatus(liveId, status)
	})

	// 设置录制结束回调，用于触发优雅更新检查
	recorders.SetOnRecordingEndFunc(func(ctx context.Context) {
		// 延迟一小段时间，确保录制器已完全关闭
		if CheckGracefulUpdate(ctx) {
			applog.GetLogger().Info("所有录制已结束，开始执行优雅更新")
		}
	})
}
