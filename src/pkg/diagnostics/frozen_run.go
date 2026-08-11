package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxFrozenMetadataBytes = int64(8 << 20)

// frozenEvidenceFile 持有一次调查导出边界上的稳定文件描述符和固定长度。
// 源路径随后即使继续追加、rename 或删除，也不会改变本次导出可见的字节。
type frozenEvidenceFile struct {
	relativePath string
	file         *os.File
	size         int64
	modTime      int64
}

type frozenRunSnapshot struct {
	runID   string
	active  bool
	current bool
	files   map[string]*frozenEvidenceFile
	order   []string

	captureStartedAt time.Time
	captureEndedAt   time.Time
}

type frozenFileSelection uint8

const (
	frozenRootFiles frozenFileSelection = 1 << iota
	frozenEventFiles
	frozenFlightFiles
)

func (s *frozenRunSnapshot) Close() error {
	if s == nil {
		return nil
	}
	var result error
	for _, name := range s.order {
		if file := s.files[name]; file != nil && file.file != nil {
			result = errors.Join(result, file.file.Close())
		}
	}
	return result
}

func (s *frozenRunSnapshot) has(relativePath string) bool {
	if s == nil {
		return false
	}
	_, ok := s.files[filepath.ToSlash(relativePath)]
	return ok
}

func (s *frozenRunSnapshot) reader(relativePath string) (*io.SectionReader, bool) {
	if s == nil {
		return nil, false
	}
	file, ok := s.files[filepath.ToSlash(relativePath)]
	if !ok {
		return nil, false
	}
	return io.NewSectionReader(file.file, 0, file.size), true
}

func (s *frozenRunSnapshot) readMetadata(relativePath string, target any) error {
	reader, ok := s.reader(relativePath)
	if !ok {
		return fs.ErrNotExist
	}
	if reader.Size() > maxFrozenMetadataBytes {
		return fmt.Errorf("diagnostics 元数据文件过大: %s", relativePath)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	// Unmarshal 会拒绝一个完整 JSON 值之后的尾随垃圾；marker 不能因只读到
	// 合法前缀就被误判为可信。
	return json.Unmarshal(data, target)
}

func (m *Manager) acquireExport(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.exportGate:
	}
	return func() {
		m.exportGate <- struct{}{}
	}, nil
}

// freezeRun 在很短的锁窗口中固定一次 run 的证据文件身份与长度。
// JSON 解析、Viewer marshal 和 gzip 都在锁外从这些文件描述符读取。
func (m *Manager) freezeRun(
	ctx context.Context,
	runID string,
	includeFlight bool,
	snapshotFlight bool,
) (*frozenRunSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runDir, err := m.runPath(runID)
	if err != nil {
		return nil, err
	}
	runs, err := scanRunInfos(m.runsDir, activeRunID(m))
	if err != nil {
		return nil, err
	}
	var runInfo *RunInfo
	for index := range runs {
		if runs[index].RunID == runID {
			info := runs[index]
			runInfo = &info
			break
		}
	}
	if runInfo == nil {
		return nil, ErrRunNotFound
	}
	if runInfo.Active && !runInfo.Current {
		// 没有跨进程冻结协议，不能把另一进程仍在轮换的多份文件伪装成
		// 同一个一致快照。等待 owner 结束后再导出。
		return nil, ErrRunActive
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}

	current := runInfo.Current && runID == m.runID && !m.closed.Load()
	if current && snapshotFlight {
		if _, snapshotErr := m.flight.Snapshot(); snapshotErr != nil {
			// 与旧行为一致：新 Flight 快照失败时仍允许使用已有稳定快照。
			m.setError(snapshotErr)
		}
	}

	if !current {
		selection := frozenRootFiles | frozenEventFiles
		if includeFlight {
			selection |= frozenFlightFiles
		}
		return openFrozenRunFiles(
			ctx,
			runDir,
			runID,
			runInfo.Active,
			false,
			selection,
		)
	}

	snapshot := newFrozenRunSnapshot(runID, true, true)
	snapshot.captureStartedAt = time.Now().UTC()
	failed := true
	defer func() {
		if failed {
			_ = snapshot.Close()
		}
	}()

	// 三类证据分别在各自锁内打开，绝不同时持有 event/flight/liveness。
	// 这接受毫秒级的跨子系统边界，但每个文件都有稳定身份和固定长度，
	// 且不会引入与 heartbeat、SnapshotCurrent、shutdown 的锁序环。
	m.eventMu.Lock()
	if m.writer != nil {
		if syncErr := m.writer.Sync(); syncErr != nil {
			m.eventMu.Unlock()
			return nil, syncErr
		}
		m.lastSeq.Store(m.writer.latestSeq)
		m.dropped.Store(m.writer.dropped)
	}
	err = addFrozenRunFiles(ctx, snapshot, runDir, frozenEventFiles)
	m.eventMu.Unlock()
	if err != nil {
		return nil, err
	}

	if err = m.writeHeartbeat(snapshotState(m)); err != nil {
		return nil, err
	}
	m.livenessMu.Lock()
	err = addFrozenRunFiles(ctx, snapshot, runDir, frozenRootFiles)
	m.livenessMu.Unlock()
	if err != nil {
		return nil, err
	}

	if includeFlight {
		m.flight.mu.Lock()
		err = addFrozenRunFiles(ctx, snapshot, runDir, frozenFlightFiles)
		m.flight.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	sort.Strings(snapshot.order)
	snapshot.captureEndedAt = time.Now().UTC()
	failed = false
	return snapshot, nil
}

func newFrozenRunSnapshot(
	runID string,
	active bool,
	current bool,
) *frozenRunSnapshot {
	return &frozenRunSnapshot{
		runID:   runID,
		active:  active,
		current: current,
		files:   make(map[string]*frozenEvidenceFile),
		order:   make([]string, 0, 16),
	}
}

func openFrozenRunFiles(
	ctx context.Context,
	runDir string,
	runID string,
	active bool,
	current bool,
	selection frozenFileSelection,
) (*frozenRunSnapshot, error) {
	snapshot := newFrozenRunSnapshot(runID, active, current)
	snapshot.captureStartedAt = time.Now().UTC()
	if err := addFrozenRunFiles(ctx, snapshot, runDir, selection); err != nil {
		_ = snapshot.Close()
		return nil, err
	}
	sort.Strings(snapshot.order)
	snapshot.captureEndedAt = time.Now().UTC()
	return snapshot, nil
}

func addFrozenRunFiles(
	ctx context.Context,
	snapshot *frozenRunSnapshot,
	runDir string,
	selection frozenFileSelection,
) error {
	return filepath.WalkDir(runDir, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filePath == runDir {
			return nil
		}
		relativePath, err := filepath.Rel(runDir, filePath)
		if err != nil {
			return err
		}
		relativePath = filepath.ToSlash(relativePath)
		if entry.Type()&fs.ModeSymlink != 0 {
			if relativePath == "events" || relativePath == "flight" ||
				isManagedEvidencePath(relativePath, selection) {
				return errors.New("diagnostics 证据路径不能是符号链接")
			}
			return nil
		}
		if entry.IsDir() {
			switch relativePath {
			case "events":
				if selection&frozenEventFiles == 0 {
					return filepath.SkipDir
				}
			case "flight":
				if selection&frozenFlightFiles == 0 {
					return filepath.SkipDir
				}
			default:
				return filepath.SkipDir
			}
			return nil
		}
		if !isManagedEvidencePath(relativePath, selection) {
			return nil
		}
		if _, exists := snapshot.files[relativePath]; exists {
			return nil
		}
		file, info, err := openRegularFileNoSymlink(filePath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		snapshot.files[relativePath] = &frozenEvidenceFile{
			relativePath: relativePath,
			file:         file,
			size:         info.Size(),
			modTime:      info.ModTime().UTC().UnixNano(),
		}
		snapshot.order = append(snapshot.order, relativePath)
		return nil
	})
}

func isManagedEvidencePath(
	relativePath string,
	selection frozenFileSelection,
) bool {
	relativePath = filepath.ToSlash(relativePath)
	if !strings.Contains(relativePath, "/") {
		if selection&frozenRootFiles == 0 {
			return false
		}
		switch relativePath {
		case "run.json", "heartbeat.json", "lease.json", "lease.lock",
			"clean.json", "panic.json", "panic.stack", "ack.json":
			return true
		default:
			return false
		}
	}
	dir, name := filepath.ToSlash(filepath.Dir(relativePath)), filepath.Base(relativePath)
	switch dir {
	case "events":
		return selection&frozenEventFiles != 0 && (name == "index.json" ||
			(strings.HasPrefix(name, "events-") && strings.HasSuffix(name, ".jsonl")))
	case "flight":
		return selection&frozenFlightFiles != 0 && (name == "latest.json" ||
			(strings.HasPrefix(name, "flight-v1-") && strings.HasSuffix(name, ".trace")))
	default:
		return false
	}
}

func copyNWithContext(
	ctx context.Context,
	destination io.Writer,
	source io.Reader,
	size int64,
) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	buffer := make([]byte, 128<<10)
	var written int64
	for written < size {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		chunk := int64(len(buffer))
		if remaining := size - written; remaining < chunk {
			chunk = remaining
		}
		n, err := io.ReadFull(source, buffer[:chunk])
		if n > 0 {
			outputN, writeErr := destination.Write(buffer[:n])
			written += int64(outputN)
			if writeErr != nil {
				return written, writeErr
			}
			if outputN != n {
				return written, io.ErrShortWrite
			}
		}
		if err != nil {
			return written, err
		}
	}
	return written, nil
}
