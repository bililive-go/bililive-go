package servers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	applog "github.com/bililive-go/bililive-go/src/log"
	pkgdiagnostics "github.com/bililive-go/bililive-go/src/pkg/diagnostics"
)

const (
	diagnosticsRoutePrefix = "/diagnostics"

	diagnosticArchiveContentType = "application/gzip"
	diagnosticTraceContentType   = "application/octet-stream"
)

var (
	// run ID 必须使用 diagnostics 核心生成的 run- 前缀格式，且只允许有限长度
	// 的 ASCII 字母、数字、点、短横线和下划线。点不构成路径分隔符，
	// 生成 ID 的 UTC 时间戳会使用它表示小数秒。
	// 它会作为不透明标识交给诊断模块，HTTP 层永远不接受文件路径。
	diagnosticRunIDPattern = regexp.MustCompile(`^run-[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

	errDiagnosticRunNotFound  = errors.New("诊断运行不存在")
	errDiagnosticArtifactGone = errors.New("诊断文件不存在")
	errDiagnosticNotCurrent   = errors.New("只能为当前运行创建快照")
)

// diagnosticArtifact 是已经冻结、可随机读取的诊断文件。
//
// 后端必须保证 Reader 对应的内容在一次请求期间不再增长或被改写。
// Close 可为空（例如测试中的 bytes.Reader）。
type diagnosticArtifact struct {
	Name        string
	ContentType string
	ModTime     time.Time
	Reader      io.ReadSeeker
	Close       io.Closer
}

// diagnosticRunSummary 是可安全发送给 WebUI 的运行摘要。特别注意它不包含
// diagnostics.RunInfo.Path，防止把宿主机 AppData 绝对路径泄漏给局域网客户端。
type diagnosticRunSummary struct {
	RunID                   string     `json:"run_id"`
	StartedAt               time.Time  `json:"started_at"`
	LastHeartbeat           *time.Time `json:"last_heartbeat_at,omitempty"`
	LeaseRenewedAt          *time.Time `json:"lease_renewed_at,omitempty"`
	LeaseExpiresAt          *time.Time `json:"lease_expires_at,omitempty"`
	EndedAt                 *time.Time `json:"ended_at,omitempty"`
	Status                  string     `json:"status"`
	Active                  bool       `json:"active"`
	ActiveReason            string     `json:"active_reason,omitempty"`
	Current                 bool       `json:"current"`
	OwnerPID                int        `json:"owner_pid,omitempty"`
	Clean                   bool       `json:"clean"`
	Acknowledged            bool       `json:"acknowledged"`
	HasPanic                bool       `json:"has_panic"`
	EventCount              uint64     `json:"event_count"`
	EventSegments           int        `json:"event_segments"`
	FlightRecorderAvailable bool       `json:"flight_recorder_available"`
	SizeBytes               int64      `json:"size_bytes"`
}

type diagnosticStartupStatus struct {
	CurrentRunID string                 `json:"current_run_id"`
	PreviousRun  *diagnosticRunSummary  `json:"previous_run,omitempty"`
	ActiveRuns   []diagnosticRunSummary `json:"active_runs"`
	AbnormalRuns []diagnosticRunSummary `json:"abnormal_runs"`
}

type diagnosticArtifactSummary struct {
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
}

type diagnosticSnapshotResponse struct {
	RunID          string                     `json:"run_id"`
	CapturedAt     time.Time                  `json:"captured_at"`
	LatestEventSeq uint64                     `json:"latest_event_seq"`
	DroppedEvents  uint64                     `json:"dropped_events"`
	Flight         *diagnosticArtifactSummary `json:"flight,omitempty"`
}

// diagnosticBackend 隔离 HTTP 层和诊断数据的具体持久化实现。
//
// 列表、状态、确认和快照使用便于 WebUI 直接解析的原始 JSON；只有错误延续
// 项目的 commonResp JSON 格式。Viewer 同样返回原始 JSON 文件。
type diagnosticBackend interface {
	ListRuns(context.Context) (any, error)
	StartupStatus(context.Context) (any, error)
	Acknowledge(context.Context, string) (any, error)
	SnapshotCurrent(context.Context, string) (any, error)
	OpenLogs(context.Context) (*diagnosticArtifact, error)
	OpenViewer(context.Context, string) (*diagnosticArtifact, error)
	OpenArchive(context.Context, string) (*diagnosticArtifact, error)
	OpenFlightRecorder(context.Context, string) (*diagnosticArtifact, error)
}

type diagnosticsManagerBackend struct {
	manager *pkgdiagnostics.Manager
}

func getDiagnosticBackend() diagnosticBackend {
	manager := pkgdiagnostics.Default()
	if manager == nil {
		return nil
	}
	return &diagnosticsManagerBackend{manager: manager}
}

func (b *diagnosticsManagerBackend) ListRuns(context.Context) (any, error) {
	runs, err := b.manager.ListRuns()
	if err != nil {
		return nil, err
	}
	summaries := make([]diagnosticRunSummary, 0, len(runs))
	for _, run := range runs {
		summaries = append(summaries, newDiagnosticRunSummary(run))
	}
	return summaries, nil
}

func (b *diagnosticsManagerBackend) StartupStatus(
	context.Context,
) (any, error) {
	status := b.manager.StartupStatus()
	response := diagnosticStartupStatus{
		CurrentRunID: status.CurrentRunID,
		ActiveRuns:   make([]diagnosticRunSummary, 0, len(status.ActiveRuns)),
		AbnormalRuns: make([]diagnosticRunSummary, 0, len(status.UncleanRuns)),
	}
	if status.PreviousRun != nil {
		previous := newDiagnosticRunSummary(*status.PreviousRun)
		response.PreviousRun = &previous
	}
	for _, run := range status.UncleanRuns {
		response.AbnormalRuns = append(response.AbnormalRuns, newDiagnosticRunSummary(run))
	}
	for _, run := range status.ActiveRuns {
		response.ActiveRuns = append(response.ActiveRuns, newDiagnosticRunSummary(run))
	}
	return response, nil
}

func (b *diagnosticsManagerBackend) Acknowledge(
	_ context.Context,
	runID string,
) (any, error) {
	if err := b.manager.Acknowledge(runID); err != nil {
		return nil, err
	}
	return map[string]any{
		"run_id":       runID,
		"acknowledged": true,
	}, nil
}

func (b *diagnosticsManagerBackend) SnapshotCurrent(
	_ context.Context,
	runID string,
) (any, error) {
	// SnapshotCurrent 的核心 API 有意不接收 run ID，因此 HTTP 层先确认 URL
	// 指向仍处于 active 状态的当前运行，避免用户误以为可以修改历史证据。
	runs, err := b.manager.ListRuns()
	if err != nil {
		return nil, err
	}
	isCurrent := false
	for _, run := range runs {
		if run.RunID == runID && run.Current {
			isCurrent = true
			break
		}
	}
	if !isCurrent {
		return nil, errDiagnosticNotCurrent
	}

	snapshot, err := b.manager.SnapshotCurrent()
	if err != nil {
		return nil, err
	}
	response := diagnosticSnapshotResponse{
		RunID:          snapshot.RunID,
		CapturedAt:     snapshot.CapturedAt,
		LatestEventSeq: snapshot.LatestEventSeq,
		DroppedEvents:  snapshot.DroppedEvents,
	}
	if snapshot.Flight != nil {
		response.Flight = &diagnosticArtifactSummary{
			Name:        snapshot.Flight.Name,
			ContentType: snapshot.Flight.ContentType,
			Size:        snapshot.Flight.Size,
			ModTime:     snapshot.Flight.ModTime,
		}
	}
	return response, nil
}

func (b *diagnosticsManagerBackend) OpenLogs(
	ctx context.Context,
) (*diagnosticArtifact, error) {
	artifact, err := applog.BuildSnapshotArchiveContext(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	return openExportedDiagnosticArtifact(pkgdiagnostics.Artifact{
		Name:        artifact.Name,
		Path:        artifact.Path,
		ContentType: diagnosticArchiveContentType,
		Size:        artifact.Size,
		ModTime:     artifact.ModTime,
	})
}

func (b *diagnosticsManagerBackend) OpenViewer(
	ctx context.Context,
	runID string,
) (*diagnosticArtifact, error) {
	artifact, err := b.manager.BuildViewerBundleContext(ctx, runID)
	if err != nil {
		return nil, err
	}
	return openExportedDiagnosticArtifact(artifact)
}

func (b *diagnosticsManagerBackend) OpenArchive(
	ctx context.Context,
	runID string,
) (*diagnosticArtifact, error) {
	artifact, err := b.manager.BuildArchiveContext(ctx, runID)
	if err != nil {
		return nil, err
	}
	return openExportedDiagnosticArtifact(artifact)
}

func (b *diagnosticsManagerBackend) OpenFlightRecorder(
	ctx context.Context,
	runID string,
) (*diagnosticArtifact, error) {
	artifact, err := b.manager.LatestFlightPathContext(ctx, runID)
	if err != nil {
		return nil, err
	}
	return openExportedDiagnosticArtifact(artifact)
}

func newDiagnosticRunSummary(run pkgdiagnostics.RunInfo) diagnosticRunSummary {
	return diagnosticRunSummary{
		RunID:                   run.RunID,
		StartedAt:               run.StartedAt,
		LastHeartbeat:           run.LastHeartbeat,
		LeaseRenewedAt:          run.LeaseRenewedAt,
		LeaseExpiresAt:          run.LeaseExpiresAt,
		EndedAt:                 run.EndedAt,
		Status:                  run.Status,
		Active:                  run.Active,
		ActiveReason:            run.ActiveReason,
		Current:                 run.Current,
		OwnerPID:                run.OwnerPID,
		Clean:                   run.Clean,
		Acknowledged:            run.Acknowledged,
		HasPanic:                run.HasPanic,
		EventCount:              run.EventCount,
		EventSegments:           run.EventSegments,
		FlightRecorderAvailable: run.FlightRecorderAvailable,
		SizeBytes:               run.SizeBytes,
	}
}

func openExportedDiagnosticArtifact(
	artifact pkgdiagnostics.Artifact,
) (*diagnosticArtifact, error) {
	if artifact.Path == "" {
		return nil, errDiagnosticArtifactGone
	}

	info, err := os.Lstat(artifact.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errDiagnosticArtifactGone
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		_ = os.Remove(artifact.Path)
		return nil, fmt.Errorf("diagnostics 导出结果不是普通文件")
	}

	file, err := os.Open(artifact.Path)
	if err != nil {
		_ = os.Remove(artifact.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errDiagnosticArtifactGone
		}
		return nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = os.Remove(artifact.Path)
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() {
		_ = file.Close()
		_ = os.Remove(artifact.Path)
		return nil, fmt.Errorf("diagnostics 导出结果不是普通文件")
	}
	if !os.SameFile(info, openedInfo) {
		_ = file.Close()
		_ = os.Remove(artifact.Path)
		return nil, fmt.Errorf("diagnostics 导出文件在打开期间被替换")
	}
	if openedInfo.Size() != artifact.Size {
		_ = file.Close()
		_ = os.Remove(artifact.Path)
		return nil, fmt.Errorf(
			"diagnostics 导出文件大小发生变化：期望 %d，实际 %d",
			artifact.Size,
			openedInfo.Size(),
		)
	}

	modTime := artifact.ModTime
	if modTime.IsZero() {
		modTime = openedInfo.ModTime()
	}
	return &diagnosticArtifact{
		Name:        artifact.Name,
		ContentType: artifact.ContentType,
		ModTime:     modTime,
		// SectionReader 把可见长度固定为首次 stat 的大小，即使底层文件因缺陷
		// 被继续追加，也不会把增长中的内容混进本次响应。
		Reader: io.NewSectionReader(file, 0, openedInfo.Size()),
		Close: &diagnosticExportCloser{
			file: file,
			path: artifact.Path,
		},
	}, nil
}

type diagnosticExportCloser struct {
	once sync.Once
	file *os.File
	path string
	err  error
}

func (c *diagnosticExportCloser) Close() error {
	c.once.Do(func() {
		c.err = errors.Join(c.file.Close(), os.Remove(c.path))
	})
	return c.err
}

// registerDiagnosticHandlers 注册诊断 API。即使诊断模块尚未初始化也始终注册，
// 以便调用方得到稳定的 503 JSON 响应，而不是误落入 SPA 路由。
func registerDiagnosticHandlers(apiRoute *mux.Router) {
	registerDiagnosticHandlersWithProvider(apiRoute, getDiagnosticBackend)
}

func registerDiagnosticHandlersWithProvider(
	apiRoute *mux.Router,
	provider func() diagnosticBackend,
) {
	route := apiRoute.PathPrefix(diagnosticsRoutePrefix).Subrouter()
	route.Use(diagnosticResponseHeaders)

	handler := &diagnosticHTTPHandler{backend: provider}
	route.HandleFunc("/runs", handler.listRuns).Methods(http.MethodGet)
	route.HandleFunc("/startup-status", handler.startupStatus).Methods(http.MethodGet)
	route.HandleFunc(
		"/startup-status/{runID}/ack",
		handler.acknowledge,
	).Methods(http.MethodPost)
	route.HandleFunc(
		"/runs/{runID}/snapshot",
		handler.snapshotCurrent,
	).Methods(http.MethodPost)
	route.HandleFunc(
		"/runs/{runID}/viewer",
		handler.viewer,
	).Methods(http.MethodGet)
	route.HandleFunc(
		"/runs/{runID}/download",
		handler.download,
	).Methods(http.MethodGet, http.MethodHead)
	route.HandleFunc(
		"/runs/{runID}/flight-recorder",
		handler.flightRecorder,
	).Methods(http.MethodGet, http.MethodHead)
	route.HandleFunc(
		"/logs/download",
		handler.logsDownload,
	).Methods(http.MethodGet, http.MethodHead)
}

type diagnosticHTTPHandler struct {
	backend func() diagnosticBackend
}

type diagnosticResponseWriter struct {
	http.ResponseWriter
}

func (w diagnosticResponseWriter) ensureHeaders() {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (w diagnosticResponseWriter) WriteHeader(statusCode int) {
	w.ensureHeaders()
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w diagnosticResponseWriter) Write(body []byte) (int, error) {
	w.ensureHeaders()
	return w.ResponseWriter.Write(body)
}

func diagnosticResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := diagnosticResponseWriter{ResponseWriter: w}
		writer.ensureHeaders()
		next.ServeHTTP(writer, r)
	})
}

// diagnosticServeContentWriter 延迟提交 ServeContent 产生的错误状态，
// 让 Range/条件请求等错误仍能转换为项目统一的 JSON，而成功响应继续流式写出。
type diagnosticServeContentWriter struct {
	http.ResponseWriter
	errorStatus int
	committed   bool
}

func (w *diagnosticServeContentWriter) WriteHeader(statusCode int) {
	// 每次 API 调用都会冻结一个新的导出文件；不同请求之间的 gzip 时间戳、
	// bundle_id 和当前事件边界可能不同。因此不能宣称跨请求 Range 可续传，
	// 否则多个 206 片段可能来自不同字节流。
	w.Header().Set("Accept-Ranges", "none")
	if !w.committed && statusCode >= http.StatusBadRequest {
		w.errorStatus = statusCode
		return
	}
	w.committed = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *diagnosticServeContentWriter) Write(body []byte) (int, error) {
	if w.errorStatus != 0 && !w.committed {
		// http.Error 写入的纯文本由外层替换为 commonResp JSON。
		return len(body), nil
	}
	w.committed = true
	return w.ResponseWriter.Write(body)
}

func (h *diagnosticHTTPHandler) getBackend(w http.ResponseWriter) diagnosticBackend {
	if h.backend == nil {
		writeDiagnosticError(w, http.StatusServiceUnavailable, "诊断模块未初始化")
		return nil
	}
	backend := h.backend()
	if backend == nil {
		writeDiagnosticError(w, http.StatusServiceUnavailable, "诊断模块未初始化")
		return nil
	}
	return backend
}

func (h *diagnosticHTTPHandler) listRuns(w http.ResponseWriter, r *http.Request) {
	backend := h.getBackend(w)
	if backend == nil {
		return
	}

	runs, err := backend.ListRuns(r.Context())
	if err != nil {
		writeDiagnosticBackendError(w, err, "读取诊断运行列表失败")
		return
	}
	writeDiagnosticSuccess(w, http.StatusOK, map[string]any{"runs": runs})
}

func (h *diagnosticHTTPHandler) startupStatus(w http.ResponseWriter, r *http.Request) {
	backend := h.getBackend(w)
	if backend == nil {
		return
	}

	status, err := backend.StartupStatus(r.Context())
	if err != nil {
		writeDiagnosticBackendError(w, err, "读取上次运行状态失败")
		return
	}
	writeDiagnosticSuccess(w, http.StatusOK, status)
}

func (h *diagnosticHTTPHandler) acknowledge(w http.ResponseWriter, r *http.Request) {
	runID, ok := diagnosticRunID(w, r)
	if !ok {
		return
	}
	backend := h.getBackend(w)
	if backend == nil {
		return
	}

	result, err := backend.Acknowledge(r.Context(), runID)
	if err != nil {
		writeDiagnosticBackendError(w, err, "确认上次运行状态失败")
		return
	}
	writeDiagnosticSuccess(w, http.StatusOK, result)
}

func (h *diagnosticHTTPHandler) snapshotCurrent(w http.ResponseWriter, r *http.Request) {
	runID, ok := diagnosticRunID(w, r)
	if !ok {
		return
	}
	backend := h.getBackend(w)
	if backend == nil {
		return
	}

	result, err := backend.SnapshotCurrent(r.Context(), runID)
	if err != nil {
		writeDiagnosticBackendError(w, err, "创建诊断快照失败")
		return
	}
	writeDiagnosticSuccess(w, http.StatusOK, result)
}

func (h *diagnosticHTTPHandler) viewer(w http.ResponseWriter, r *http.Request) {
	h.serveArtifact(w, r, "viewer.json", "application/json", false, func(
		ctx context.Context,
		backend diagnosticBackend,
		runID string,
	) (*diagnosticArtifact, error) {
		return backend.OpenViewer(ctx, runID)
	})
}

func (h *diagnosticHTTPHandler) download(w http.ResponseWriter, r *http.Request) {
	if rejectDiagnosticHead(w, r) {
		return
	}
	h.serveArtifact(w, r, "diagnostics.tar.gz", diagnosticArchiveContentType, true, func(
		ctx context.Context,
		backend diagnosticBackend,
		runID string,
	) (*diagnosticArtifact, error) {
		return backend.OpenArchive(ctx, runID)
	})
}

func (h *diagnosticHTTPHandler) flightRecorder(w http.ResponseWriter, r *http.Request) {
	if rejectDiagnosticHead(w, r) {
		return
	}
	h.serveArtifact(w, r, "flight-recorder.trace", diagnosticTraceContentType, true, func(
		ctx context.Context,
		backend diagnosticBackend,
		runID string,
	) (*diagnosticArtifact, error) {
		return backend.OpenFlightRecorder(ctx, runID)
	})
}

func (h *diagnosticHTTPHandler) logsDownload(w http.ResponseWriter, r *http.Request) {
	if rejectDiagnosticHead(w, r) {
		return
	}
	backend := h.getBackend(w)
	if backend == nil {
		return
	}
	artifact, err := backend.OpenLogs(r.Context())
	h.serveOpenedArtifact(
		w,
		r,
		"bililive-go-logs.tar.gz",
		diagnosticArchiveContentType,
		true,
		artifact,
		err,
	)
}

func rejectDiagnosticHead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodHead {
		return false
	}
	// 这些 URL 每次都会即时冻结并构建一个新文件。HEAD 若为了计算
	// Content-Length 也完整执行 gzip，既浪费资源又可被反复触发。
	w.Header().Set("Allow", http.MethodGet)
	w.WriteHeader(http.StatusMethodNotAllowed)
	return true
}

func (h *diagnosticHTTPHandler) serveArtifact(
	w http.ResponseWriter,
	r *http.Request,
	defaultName string,
	defaultContentType string,
	attachment bool,
	open func(context.Context, diagnosticBackend, string) (*diagnosticArtifact, error),
) {
	runID, ok := diagnosticRunID(w, r)
	if !ok {
		return
	}
	backend := h.getBackend(w)
	if backend == nil {
		return
	}

	artifact, err := open(r.Context(), backend, runID)
	h.serveOpenedArtifact(
		w,
		r,
		runID+"-"+defaultName,
		defaultContentType,
		attachment,
		artifact,
		err,
	)
}

func (h *diagnosticHTTPHandler) serveOpenedArtifact(
	w http.ResponseWriter,
	r *http.Request,
	defaultName string,
	defaultContentType string,
	attachment bool,
	artifact *diagnosticArtifact,
	err error,
) {
	if err != nil {
		writeDiagnosticBackendError(w, err, "读取诊断文件失败")
		return
	}
	if artifact == nil || artifact.Reader == nil {
		writeDiagnosticError(w, http.StatusNotFound, "诊断文件不存在")
		return
	}
	if artifact.Close != nil {
		defer artifact.Close.Close()
	}

	name := artifact.Name
	if name == "" {
		name = defaultName
	}
	contentType := artifact.ContentType
	if contentType == "" {
		contentType = defaultContentType
	}
	modTime := artifact.ModTime
	if modTime.IsZero() {
		// ServeContent 会根据 ModTime 生成 Last-Modified。没有可靠时间时不要伪造。
		modTime = time.Time{}
	}

	w.Header().Set("Content-Type", contentType)
	if attachment {
		w.Header().Set("Content-Disposition", contentDispositionAttachment(name))
	}
	serveWriter := &diagnosticServeContentWriter{ResponseWriter: w}
	serveRequest := r
	if r.Header.Get("Range") != "" || r.Header.Get("If-Range") != "" {
		// 忽略 Range 并返回一个完整、内部一致的 200 响应。若未来增加带 export
		// ID 和 TTL 的不可变缓存，再重新开放断点续传。
		serveRequest = r.Clone(r.Context())
		serveRequest.Header = r.Header.Clone()
		serveRequest.Header.Del("Range")
		serveRequest.Header.Del("If-Range")
	}
	http.ServeContent(serveWriter, serveRequest, name, modTime, artifact.Reader)
	if serveWriter.errorStatus != 0 && !serveWriter.committed {
		// ServeContent 的错误默认是 text/plain；清理它添加的实体头后改写为
		// 项目通用 JSON。Content-Range（416 时）有诊断价值，予以保留。
		w.Header().Del("Content-Disposition")
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Type")
		message := "读取诊断文件失败"
		if serveWriter.errorStatus == http.StatusRequestedRangeNotSatisfiable {
			message = "请求的文件范围无效"
		}
		writeDiagnosticError(w, serveWriter.errorStatus, message)
	}
}

func diagnosticRunID(w http.ResponseWriter, r *http.Request) (string, bool) {
	runID := mux.Vars(r)["runID"]
	if !diagnosticRunIDPattern.MatchString(runID) {
		writeDiagnosticError(w, http.StatusBadRequest, "无效的运行 ID")
		return "", false
	}
	return runID, true
}

func contentDispositionAttachment(name string) string {
	// 文件名只用于下载提示，不允许后端意外返回目录或控制字符。
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		name = "diagnostics.bin"
	}

	value := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if value == "" {
		return `attachment; filename="diagnostics.bin"`
	}
	return value
}

func writeDiagnosticSuccess(w http.ResponseWriter, status int, data any) {
	writeJsonWithStatusCode(w, status, data)
}

func writeDiagnosticError(w http.ResponseWriter, status int, message string) {
	writeJsonWithStatusCode(w, status, commonResp{
		ErrNo:  -1,
		ErrMsg: message,
	})
}

func writeDiagnosticBackendError(w http.ResponseWriter, err error, operation string) {
	switch {
	case errors.Is(err, pkgdiagnostics.ErrNotInitialized):
		writeDiagnosticError(w, http.StatusServiceUnavailable, "诊断模块未初始化")
	case errors.Is(err, pkgdiagnostics.ErrInvalidRunID):
		writeDiagnosticError(w, http.StatusBadRequest, "无效的运行 ID")
	case errors.Is(err, pkgdiagnostics.ErrRunNotFound),
		errors.Is(err, errDiagnosticRunNotFound):
		writeDiagnosticError(w, http.StatusNotFound, "诊断运行不存在")
	case errors.Is(err, pkgdiagnostics.ErrFlightUnavailable):
		writeDiagnosticError(w, http.StatusNotFound, "该运行没有可用的 Flight Recorder 快照")
	case errors.Is(err, errDiagnosticArtifactGone):
		writeDiagnosticError(w, http.StatusNotFound, "诊断文件不存在")
	case errors.Is(err, pkgdiagnostics.ErrClosed),
		errors.Is(err, pkgdiagnostics.ErrRunActive),
		errors.Is(err, errDiagnosticNotCurrent):
		writeDiagnosticError(w, http.StatusConflict, err.Error())
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		writeDiagnosticError(w, http.StatusRequestTimeout, "诊断导出请求已取消")
	default:
		// 意外错误可能包含宿主机绝对路径，只记入本地日志，不回传给 WebUI。
		applog.GetLogger().WithError(err).Error(operation)
		writeDiagnosticError(w, http.StatusInternalServerError, operation)
	}
}
