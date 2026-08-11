package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/trace"
	"sort"
	"strings"
	"sync"
	"time"
)

type flightRecorder struct {
	dir    string
	cfg    FlightConfig
	fr     *trace.FlightRecorder
	mu     sync.Mutex
	next   uint64
	active bool

	periodicMu      sync.Mutex
	periodicStarted bool
	periodicStop    chan struct{}
	periodicDone    chan struct{}
	onError         func(error)
}

type flightLatest struct {
	Schema     string    `json:"schema"`
	Name       string    `json:"name"`
	CapturedAt time.Time `json:"captured_at"`
	Size       int64     `json:"size"`
}

func newFlightRecorder(dir string, cfg FlightConfig) *flightRecorder {
	return &flightRecorder{
		dir:          dir,
		cfg:          cfg,
		periodicStop: make(chan struct{}),
		periodicDone: make(chan struct{}),
	}
}

func (f *flightRecorder) Start() error {
	if f == nil || !f.cfg.Enabled {
		return nil
	}
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return err
	}
	recorder := trace.NewFlightRecorder(trace.FlightRecorderConfig{
		MinAge:   f.cfg.MinAge,
		MaxBytes: f.cfg.MaxBytes,
	})
	if err := recorder.Start(); err != nil {
		return err
	}
	f.mu.Lock()
	f.fr = recorder
	f.active = true
	f.next = nextFlightSequence(f.dir)
	f.mu.Unlock()
	return nil
}

func (f *flightRecorder) StartPeriodic() {
	if f == nil {
		return
	}
	f.periodicMu.Lock()
	if f.periodicStarted {
		f.periodicMu.Unlock()
		return
	}
	f.mu.Lock()
	active := f.active
	f.mu.Unlock()
	if !active {
		f.periodicMu.Unlock()
		close(f.periodicDone)
		return
	}
	f.periodicStarted = true
	f.periodicMu.Unlock()
	go func() {
		defer close(f.periodicDone)
		ticker := time.NewTicker(f.cfg.SnapshotInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := f.Snapshot(); err != nil && f.onError != nil {
					f.onError(err)
				}
			case <-f.periodicStop:
				return
			}
		}
	}()
}

func (f *flightRecorder) StopPeriodic() {
	if f == nil {
		return
	}
	f.periodicMu.Lock()
	started := f.periodicStarted
	if started {
		select {
		case <-f.periodicStop:
		default:
			close(f.periodicStop)
		}
	}
	f.periodicMu.Unlock()
	<-f.periodicDone
}

func (f *flightRecorder) Stop() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active && f.fr != nil {
		f.fr.Stop()
	}
	f.active = false
}

// Snapshot 发布版本化 trace 文件。禁用时返回 (nil, nil)，旧快照不会因新快照失败而丢失。
func (f *flightRecorder) Snapshot() (*Artifact, error) {
	if f == nil {
		return nil, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.active || f.fr == nil {
		return nil, nil
	}
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return nil, err
	}
	sequence := f.next
	if sequence == 0 {
		sequence = nextFlightSequence(f.dir)
	}
	var path string
	for {
		path = filepath.Join(f.dir, fmt.Sprintf("flight-v1-%06d.trace", sequence))
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			break
		} else if err != nil {
			return nil, err
		}
		sequence++
	}
	if err := atomicWriteFile(path, 0o600, false, func(writer io.Writer) error {
		_, err := f.fr.WriteTo(writer)
		return err
	}); err != nil {
		return nil, err
	}
	f.next = sequence + 1
	artifact, err := artifactForPath(path, "application/vnd.go.trace")
	if err != nil {
		return nil, err
	}
	if err = atomicWriteJSON(filepath.Join(f.dir, "latest.json"), flightLatest{
		Schema:     "bililive.diagnostics-flight-latest/v1",
		Name:       artifact.Name,
		CapturedAt: artifact.ModTime,
		Size:       artifact.Size,
	}, true); err != nil {
		return nil, err
	}
	if err = retainFlightSnapshots(f.dir, f.cfg.KeepSnapshots); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func nextFlightSequence(dir string) uint64 {
	paths, _ := flightSnapshotPaths(dir)
	var max uint64
	for _, path := range paths {
		var sequence uint64
		if _, err := fmt.Sscanf(filepath.Base(path), "flight-v1-%06d.trace", &sequence); err == nil && sequence > max {
			max = sequence
		}
	}
	return max + 1
}

func flightSnapshotPaths(dir string) ([]string, error) {
	if err := ensureDirectoryNoSymlink(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	paths := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && strings.HasPrefix(name, "flight-v1-") && strings.HasSuffix(name, ".trace") {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func retainFlightSnapshots(dir string, keep int) error {
	paths, err := flightSnapshotPaths(dir)
	if err != nil {
		return err
	}
	if keep < 1 {
		keep = 1
	}
	for len(paths) > keep {
		if err = os.Remove(paths[0]); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		paths = paths[1:]
	}
	return syncDir(dir)
}

func hasFlightSnapshot(dir string) bool {
	paths, err := flightSnapshotPaths(dir)
	return err == nil && len(paths) > 0
}

// LatestFlightPath 返回指定 run 最近一个稳定 Flight 快照。
func (m *Manager) LatestFlightPath(runID string) (Artifact, error) {
	return m.LatestFlightPathContext(context.Background(), runID)
}

// LatestFlightPathContext 把冻结边界中的最近 trace 复制到一次性导出文件。
func (m *Manager) LatestFlightPathContext(
	ctx context.Context,
	runID string,
) (Artifact, error) {
	if m == nil {
		return Artifact{}, ErrNotInitialized
	}
	release, err := m.acquireExport(ctx)
	if err != nil {
		return Artifact{}, err
	}
	defer release()
	snapshot, err := m.freezeRun(ctx, runID, true, false)
	if err != nil {
		return Artifact{}, err
	}
	defer snapshot.Close()
	var source *frozenEvidenceFile
	for index := len(snapshot.order) - 1; index >= 0; index-- {
		name := snapshot.order[index]
		if strings.HasPrefix(name, "flight/flight-v1-") &&
			strings.HasSuffix(name, ".trace") {
			source = snapshot.files[name]
			break
		}
	}
	if source == nil {
		return Artifact{}, ErrFlightUnavailable
	}
	destination, err := m.newExportPath(runID, "flight", ".trace")
	if err != nil {
		return Artifact{}, err
	}
	err = atomicWriteFile(destination, 0o600, false, func(output io.Writer) error {
		_, copyErr := copyNWithContext(
			ctx,
			output,
			io.NewSectionReader(source.file, 0, source.size),
			source.size,
		)
		return copyErr
	})
	if err != nil {
		return Artifact{}, err
	}
	return artifactForPath(destination, "application/vnd.go.trace")
}

func artifactForPath(path, contentType string) (Artifact, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Artifact{}, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Artifact{}, errors.New("diagnostics 导出结果不是普通文件")
	}
	return Artifact{
		Name:        filepath.Base(path),
		Path:        path,
		ContentType: contentType,
		Size:        info.Size(),
		ModTime:     info.ModTime().UTC(),
	}, nil
}
