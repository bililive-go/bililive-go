package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgdiagnostics "github.com/bililive-go/bililive-go/src/pkg/diagnostics"
)

type fakeDiagnosticBackend struct {
	mu sync.Mutex

	runs          any
	startup       any
	acknowledged  map[string]bool
	snapshot      any
	artifactBytes []byte
	artifactName  string
	artifactType  string
	artifactError error

	openCalls  int
	closeCalls int
}

func (f *fakeDiagnosticBackend) ListRuns(context.Context) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs, nil
}

func (f *fakeDiagnosticBackend) StartupStatus(context.Context) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startup, nil
}

func (f *fakeDiagnosticBackend) Acknowledge(
	_ context.Context,
	runID string,
) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.acknowledged == nil {
		f.acknowledged = make(map[string]bool)
	}
	f.acknowledged[runID] = true
	return map[string]any{
		"run_id":       runID,
		"acknowledged": true,
	}, nil
}

func (f *fakeDiagnosticBackend) SnapshotCurrent(
	_ context.Context,
	runID string,
) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapshot == nil {
		return map[string]any{"run_id": runID}, nil
	}
	return f.snapshot, nil
}

func (f *fakeDiagnosticBackend) OpenLogs(
	context.Context,
) (*diagnosticArtifact, error) {
	return f.openArtifact()
}

func (f *fakeDiagnosticBackend) OpenViewer(
	context.Context,
	string,
) (*diagnosticArtifact, error) {
	return f.openArtifact()
}

func (f *fakeDiagnosticBackend) OpenArchive(
	context.Context,
	string,
) (*diagnosticArtifact, error) {
	return f.openArtifact()
}

func (f *fakeDiagnosticBackend) OpenFlightRecorder(
	context.Context,
	string,
) (*diagnosticArtifact, error) {
	return f.openArtifact()
}

func (f *fakeDiagnosticBackend) openArtifact() (*diagnosticArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls++
	if f.artifactError != nil {
		return nil, f.artifactError
	}

	return &diagnosticArtifact{
		Name:        f.artifactName,
		ContentType: f.artifactType,
		ModTime:     time.Unix(1_700_000_000, 0),
		Reader:      bytes.NewReader(f.artifactBytes),
		Close: diagnosticCloserFunc(func() error {
			f.mu.Lock()
			f.closeCalls++
			f.mu.Unlock()
			return nil
		}),
	}, nil
}

type diagnosticCloserFunc func() error

func (f diagnosticCloserFunc) Close() error {
	return f()
}

func newDiagnosticTestRouter(backend diagnosticBackend) *mux.Router {
	router := mux.NewRouter()
	apiRoute := router.PathPrefix("/api").Subrouter()
	registerDiagnosticHandlersWithProvider(apiRoute, func() diagnosticBackend {
		return backend
	})
	return router
}

func performDiagnosticRequest(
	router http.Handler,
	method string,
	target string,
	headers map[string]string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestDiagnosticRoutesReturnRawSuccessJSONAndSecurityHeaders(t *testing.T) {
	backend := &fakeDiagnosticBackend{
		runs: []map[string]any{{
			"run_id":       "run-old-01",
			"status":       "suspected_abnormal",
			"acknowledged": false,
		}},
		startup: map[string]any{
			"current_run_id": "run-current-01",
			"abnormal_runs":  []string{"run-old-01"},
		},
		snapshot: map[string]any{
			"run_id":           "run-current-01",
			"latest_event_seq": float64(42),
		},
	}
	router := newDiagnosticTestRouter(backend)

	tests := []struct {
		name   string
		method string
		target string
		key    string
	}{
		{
			name:   "运行列表",
			method: http.MethodGet,
			target: "/api/diagnostics/runs",
			key:    "runs",
		},
		{
			name:   "启动状态",
			method: http.MethodGet,
			target: "/api/diagnostics/startup-status",
			key:    "current_run_id",
		},
		{
			name:   "创建快照",
			method: http.MethodPost,
			target: "/api/diagnostics/runs/run-current-01/snapshot",
			key:    "latest_event_seq",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := performDiagnosticRequest(router, tt.method, tt.target, nil)
			assert.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
			assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
			assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))

			var body map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Contains(t, body, tt.key)
			assert.NotContains(t, body, "err_no", "成功响应不应套 commonResp")
		})
	}
}

func TestDiagnosticRoutesReturn503WhenBackendIsNotInitialized(t *testing.T) {
	router := newDiagnosticTestRouter(nil)

	response := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs",
		nil,
	)

	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))

	var body commonResp
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, -1, body.ErrNo)
	assert.Contains(t, body.ErrMsg, "未初始化")
}

func TestDiagnosticRunIDValidationRejectsPathsBeforeCallingBackend(t *testing.T) {
	backend := &fakeDiagnosticBackend{artifactBytes: []byte("secret")}
	handler := &diagnosticHTTPHandler{backend: func() diagnosticBackend {
		return backend
	}}

	invalidIDs := []string{
		"../outside",
		"..",
		"not-a-run",
		"run.with.dot",
		"run/with/slash",
		`run\with\backslash`,
		"run-%2Foutside",
		"run-with space",
		"run-with-$",
		"run-" + strings.Repeat("a", 201),
	}
	for _, runID := range invalidIDs {
		t.Run(runID, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/diagnostics/runs/placeholder/download",
				nil,
			)
			request = mux.SetURLVars(request, map[string]string{"runID": runID})
			recorder := httptest.NewRecorder()
			diagnosticResponseHeaders(http.HandlerFunc(handler.download)).
				ServeHTTP(recorder, request)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
		})
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	assert.Zero(t, backend.openCalls, "无效 ID 不应到达文件后端")
}

func TestDiagnosticDownloadRejectsHeadWithoutBuildingAndReturnsOneCompleteFrozenFile(t *testing.T) {
	backend := &fakeDiagnosticBackend{
		artifactBytes: []byte("0123456789"),
		artifactName:  "../unsafe/diagnostics.zip",
		artifactType:  "application/zip",
	}
	router := newDiagnosticTestRouter(backend)

	head := performDiagnosticRequest(
		router,
		http.MethodHead,
		"/api/diagnostics/runs/run-01/download",
		nil,
	)
	assert.Equal(t, http.StatusMethodNotAllowed, head.Code)
	assert.Empty(t, head.Body.Bytes())
	assert.Equal(t, http.MethodGet, head.Header().Get("Allow"))
	backend.mu.Lock()
	assert.Zero(t, backend.openCalls, "HEAD 不应为计算长度而构建调查包")
	backend.mu.Unlock()

	rangeResponse := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/run-01/download",
		map[string]string{"Range": "bytes=2-5"},
	)
	assert.Equal(t, http.StatusOK, rangeResponse.Code)
	assert.Equal(t, "0123456789", rangeResponse.Body.String())
	assert.Equal(t, "none", rangeResponse.Header().Get("Accept-Ranges"))
	assert.Empty(t, rangeResponse.Header().Get("Content-Range"))
	assert.Equal(t, "private, no-store", rangeResponse.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", rangeResponse.Header().Get("X-Content-Type-Options"))

	invalidRange := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/run-01/download",
		map[string]string{"Range": "bytes=99-100"},
	)
	assert.Equal(t, http.StatusOK, invalidRange.Code)
	assert.Equal(t, "0123456789", invalidRange.Body.String())
	assert.Equal(t, "application/zip", invalidRange.Header().Get("Content-Type"))
	assert.Equal(t, "private, no-store", invalidRange.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", invalidRange.Header().Get("X-Content-Type-Options"))

	backend.mu.Lock()
	defer backend.mu.Unlock()
	assert.Equal(t, 2, backend.openCalls)
	assert.Equal(t, 2, backend.closeCalls)
}

func TestDiagnosticLogDownloadDoesNotNeedRunIDAndUsesFrozenArtifact(t *testing.T) {
	backend := &fakeDiagnosticBackend{
		artifactBytes: []byte("stable-log-archive"),
		artifactName:  "bililive-go-logs-test.tar.gz",
		artifactType:  diagnosticArchiveContentType,
	}
	router := newDiagnosticTestRouter(backend)

	head := performDiagnosticRequest(
		router,
		http.MethodHead,
		"/api/diagnostics/logs/download",
		nil,
	)
	assert.Equal(t, http.StatusMethodNotAllowed, head.Code)
	assert.Empty(t, head.Body.Bytes())
	assert.Equal(t, http.MethodGet, head.Header().Get("Allow"))

	response := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/logs/download",
		map[string]string{"Range": "bytes=1-3"},
	)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "stable-log-archive", response.Body.String())
	assert.Equal(t, diagnosticArchiveContentType, response.Header().Get("Content-Type"))
	assert.Contains(t, response.Header().Get("Content-Disposition"), ".tar.gz")
	assert.Equal(t, "none", response.Header().Get("Accept-Ranges"))
	assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))

	backend.mu.Lock()
	defer backend.mu.Unlock()
	assert.Equal(t, 1, backend.openCalls)
	assert.Equal(t, 1, backend.closeCalls)
}

func TestDiagnosticViewerReturnsBundleJSONWithoutEnvelope(t *testing.T) {
	backend := &fakeDiagnosticBackend{
		artifactBytes: []byte(`{"schema_version":"1.0","run":{"run_id":"run-01"}}`),
		artifactName:  "run-01-viewer.json",
		artifactType:  "application/json",
	}
	router := newDiagnosticTestRouter(backend)

	response := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/run-01/viewer",
		nil,
	)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(
		t,
		`{"schema_version":"1.0","run":{"run_id":"run-01"}}`,
		response.Body.String(),
	)
	assert.Empty(t, response.Header().Get("Content-Disposition"))
}

func TestDiagnosticFlightRecorderRejectsHeadAndIgnoresCrossRequestRanges(t *testing.T) {
	backend := &fakeDiagnosticBackend{
		artifactBytes: []byte("go-flight-recorder"),
		artifactName:  "flight-v1-000001.trace",
		artifactType:  "application/vnd.go.trace",
	}
	router := newDiagnosticTestRouter(backend)

	head := performDiagnosticRequest(
		router,
		http.MethodHead,
		"/api/diagnostics/runs/run-01/flight-recorder",
		nil,
	)
	assert.Equal(t, http.StatusMethodNotAllowed, head.Code)
	assert.Empty(t, head.Body.Bytes())
	assert.Equal(t, http.MethodGet, head.Header().Get("Allow"))
	backend.mu.Lock()
	assert.Zero(t, backend.openCalls, "HEAD 不应复制 Flight Recorder")
	backend.mu.Unlock()

	rangeResponse := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/run-01/flight-recorder",
		map[string]string{"Range": "bytes=3-8"},
	)
	assert.Equal(t, http.StatusOK, rangeResponse.Code)
	assert.Equal(t, "go-flight-recorder", rangeResponse.Body.String())
	assert.Equal(t, "none", rangeResponse.Header().Get("Accept-Ranges"))
	assert.Empty(t, rangeResponse.Header().Get("Content-Range"))
}

func TestDiagnosticArtifactNotFoundUsesProjectJSONError(t *testing.T) {
	backend := &fakeDiagnosticBackend{artifactError: errDiagnosticArtifactGone}
	router := newDiagnosticTestRouter(backend)

	response := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/run-missing/flight-recorder",
		nil,
	)

	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	var body commonResp
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, -1, body.ErrNo)
	assert.NotEmpty(t, body.ErrMsg)
}

func TestAcknowledgingAbnormalRunDoesNotDeleteIt(t *testing.T) {
	const oldRunID = "run-abnormal-01"
	backend := &fakeDiagnosticBackend{
		runs: []map[string]any{{
			"run_id": oldRunID,
			"status": "suspected_abnormal",
		}},
	}
	router := newDiagnosticTestRouter(backend)

	ack := performDiagnosticRequest(
		router,
		http.MethodPost,
		"/api/diagnostics/startup-status/"+oldRunID+"/ack",
		nil,
	)
	require.Equal(t, http.StatusOK, ack.Code)

	list := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs",
		nil,
	)
	require.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, list.Body.String(), oldRunID)

	backend.mu.Lock()
	defer backend.mu.Unlock()
	assert.True(t, backend.acknowledged[oldRunID])
}

func TestDiagnosticBackendFailureReturnsInternalServerError(t *testing.T) {
	backend := &fakeDiagnosticBackend{
		artifactError: errors.New("disk failure"),
	}
	router := newDiagnosticTestRouter(backend)

	response := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/run-01/download",
		nil,
	)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.True(t, strings.Contains(response.Body.String(), "读取诊断文件失败"))
}

func TestCanceledDiagnosticExportReturnsRequestTimeout(t *testing.T) {
	backend := &fakeDiagnosticBackend{
		artifactError: context.Canceled,
	}
	router := newDiagnosticTestRouter(backend)

	response := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/run-01/download",
		nil,
	)

	assert.Equal(t, http.StatusRequestTimeout, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Body.String(), "已取消")
}

func TestDiagnosticRunActiveUsesConflictJSONError(t *testing.T) {
	backend := &fakeDiagnosticBackend{
		artifactError: pkgdiagnostics.ErrRunActive,
	}
	router := newDiagnosticTestRouter(backend)

	response := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/run-active-01/download",
		nil,
	)

	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Body.String(), pkgdiagnostics.ErrRunActive.Error())
}

func TestDiagnosticManagerHTTPIntegrationKeepsAbnormalEvidenceAndRemovesExports(
	t *testing.T,
) {
	appDataPath := t.TempDir()
	config := pkgdiagnostics.Config{
		AppDataPath:       appDataPath,
		AppVersion:        "http-test",
		HeartbeatInterval: time.Hour,
		EventSyncInterval: time.Hour,
		Flight: pkgdiagnostics.FlightConfig{
			Enabled: false,
		},
	}

	previous, err := pkgdiagnostics.Init(config)
	require.NoError(t, err)
	previousRunID := previous.RunID()
	require.NoError(t, previous.Abort())

	current, err := pkgdiagnostics.Init(config)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = current.Abort()
	})
	currentRunID := current.RunID()

	router := mux.NewRouter()
	apiRoute := router.PathPrefix("/api").Subrouter()
	registerDiagnosticHandlers(apiRoute)

	startup := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/startup-status",
		nil,
	)
	require.Equal(t, http.StatusOK, startup.Code)
	assert.Contains(t, startup.Body.String(), previousRunID)
	assert.Contains(t, startup.Body.String(), `"active_runs":[]`)
	assert.NotContains(t, startup.Body.String(), appDataPath)
	assert.NotContains(t, startup.Body.String(), `"path"`)

	ack := performDiagnosticRequest(
		router,
		http.MethodPost,
		"/api/diagnostics/startup-status/"+previousRunID+"/ack",
		nil,
	)
	require.Equal(t, http.StatusOK, ack.Code)

	runs := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs",
		nil,
	)
	require.Equal(t, http.StatusOK, runs.Code)
	assert.Contains(t, runs.Body.String(), previousRunID)
	assert.Contains(t, runs.Body.String(), `"acknowledged":true`)
	assert.NotContains(t, runs.Body.String(), appDataPath)
	assert.NotContains(t, runs.Body.String(), `"path"`)
	_, statErr := os.Stat(filepath.Join(
		appDataPath,
		"diagnostics",
		"runs",
		previousRunID,
	))
	assert.NoError(t, statErr, "ACK 只能写标记，不能删除旧异常运行")

	startupAfterAck := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/startup-status",
		nil,
	)
	require.Equal(t, http.StatusOK, startupAfterAck.Code)
	assert.Contains(t, startupAfterAck.Body.String(), `"acknowledged":true`)

	snapshot := performDiagnosticRequest(
		router,
		http.MethodPost,
		"/api/diagnostics/runs/"+currentRunID+"/snapshot",
		nil,
	)
	require.Equal(t, http.StatusOK, snapshot.Code)
	assert.Contains(t, snapshot.Body.String(), currentRunID)
	assert.NotContains(t, snapshot.Body.String(), `"path"`)

	viewer := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/"+currentRunID+"/viewer",
		nil,
	)
	require.Equal(t, http.StatusOK, viewer.Code)
	assert.Equal(t, "application/json", viewer.Header().Get("Content-Type"))
	assert.Contains(t, viewer.Body.String(), pkgdiagnostics.BundleSchema)

	downloadHead := performDiagnosticRequest(
		router,
		http.MethodHead,
		"/api/diagnostics/runs/"+currentRunID+"/download",
		nil,
	)
	require.Equal(t, http.StatusMethodNotAllowed, downloadHead.Code)
	assert.Empty(t, downloadHead.Body.Bytes())
	assert.Equal(t, http.MethodGet, downloadHead.Header().Get("Allow"))

	downloadRange := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/"+currentRunID+"/download",
		map[string]string{"Range": "bytes=0-31"},
	)
	require.Equal(t, http.StatusOK, downloadRange.Code)
	assert.Greater(t, len(downloadRange.Body.Bytes()), 32)
	assert.Equal(t, "none", downloadRange.Header().Get("Accept-Ranges"))

	noFlight := performDiagnosticRequest(
		router,
		http.MethodGet,
		"/api/diagnostics/runs/"+currentRunID+"/flight-recorder",
		nil,
	)
	require.Equal(t, http.StatusNotFound, noFlight.Code)
	assert.Equal(t, "application/json", noFlight.Header().Get("Content-Type"))

	exports, err := os.ReadDir(filepath.Join(
		appDataPath,
		"diagnostics",
		"exports",
	))
	require.NoError(t, err)
	assert.Empty(t, exports, "HTTP 响应完成后应删除一次性导出副本")
}

var _ io.Closer = diagnosticCloserFunc(nil)
