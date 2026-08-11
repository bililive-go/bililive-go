package log

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
)

const (
	defaultSnapshotLogFiles = 3
	defaultSnapshotLogBytes = int64(8 << 20)
	maxSnapshotLogFiles     = 5
	maxSnapshotLogBytes     = int64(16 << 20)
)

var snapshotExportGate = func() chan struct{} {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return gate
}()

// SnapshotArtifact 是已关闭、长度固定的文本日志调查包。调用方发送完成后应删除 Path。
type SnapshotArtifact struct {
	Name    string
	Path    string
	Size    int64
	ModTime time.Time
}

type snapshotLogFile struct {
	path       string
	name       string
	modTime    time.Time
	sourceSize int64
	offset     int64
}

type snapshotLogManifest struct {
	Schema     string                    `json:"schema"`
	CapturedAt time.Time                 `json:"captured_at"`
	Files      []snapshotLogManifestFile `json:"files"`
}

type snapshotLogManifestFile struct {
	Name          string    `json:"name"`
	LastModified  time.Time `json:"last_modified"`
	SourceSize    int64     `json:"source_size"`
	IncludedBytes int64     `json:"included_bytes"`
	TruncatedHead bool      `json:"truncated_head"`
}

// BuildSnapshotArchive 把最近的 bililive-go 文本日志复制成固定长度 tar.gz。
// 当前日志可以继续增长；每个文件只读取首次 stat 时已经存在的尾部字节。
func BuildSnapshotArchive(maxFiles int, maxBytesPerFile int64) (SnapshotArtifact, error) {
	return BuildSnapshotArchiveContext(
		context.Background(),
		maxFiles,
		maxBytesPerFile,
	)
}

// BuildSnapshotArchiveContext 串行构建日志快照，并允许仍在排队或复制日志的
// HTTP 请求在客户端断开后取消。当前日志只读取首次 stat 时的固定长度。
func BuildSnapshotArchiveContext(
	ctx context.Context,
	maxFiles int,
	maxBytesPerFile int64,
) (SnapshotArtifact, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return SnapshotArtifact{}, ctx.Err()
	case <-snapshotExportGate:
	}
	defer func() {
		snapshotExportGate <- struct{}{}
	}()

	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		return SnapshotArtifact{}, errors.New("配置尚未初始化")
	}
	if maxFiles <= 0 {
		maxFiles = defaultSnapshotLogFiles
	}
	if maxFiles > maxSnapshotLogFiles {
		maxFiles = maxSnapshotLogFiles
	}
	if maxBytesPerFile <= 0 {
		maxBytesPerFile = defaultSnapshotLogBytes
	}
	if maxBytesPerFile > maxSnapshotLogBytes {
		maxBytesPerFile = maxSnapshotLogBytes
	}

	// 先 flush，再固定每个源文件的 size；之后的追加不会进入本次包。
	if err := Sync(); err != nil {
		return SnapshotArtifact{}, err
	}
	files, err := selectSnapshotLogs(cfg.Log.OutPutFolder, maxFiles, maxBytesPerFile)
	if err != nil {
		return SnapshotArtifact{}, err
	}

	exportDir := filepath.Join(cfg.AppDataPath, "diagnostics", "exports")
	if err = os.MkdirAll(exportDir, 0o700); err != nil {
		return SnapshotArtifact{}, err
	}
	output, err := os.CreateTemp(exportDir, "bililive-go-logs-*.tar.gz")
	if err != nil {
		return SnapshotArtifact{}, err
	}
	path := output.Name()
	complete := false
	defer func() {
		_ = output.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if err = output.Chmod(0o600); err != nil {
		return SnapshotArtifact{}, err
	}

	capturedAt := time.Now().UTC()
	gzipWriter := gzip.NewWriter(output)
	tarWriter := tar.NewWriter(gzipWriter)
	manifest := snapshotLogManifest{
		Schema:     "bililive.log-snapshot/v1",
		CapturedAt: capturedAt,
		Files:      make([]snapshotLogManifestFile, 0, len(files)),
	}
	for _, file := range files {
		if err = ctx.Err(); err != nil {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			return SnapshotArtifact{}, err
		}
		included := file.sourceSize - file.offset
		manifest.Files = append(manifest.Files, snapshotLogManifestFile{
			Name:          file.name,
			LastModified:  file.modTime,
			SourceSize:    file.sourceSize,
			IncludedBytes: included,
			TruncatedHead: file.offset > 0,
		})
		if err = writeSnapshotLog(ctx, tarWriter, file, capturedAt); err != nil {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			return SnapshotArtifact{}, err
		}
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = tarWriter.Close()
		_ = gzipWriter.Close()
		return SnapshotArtifact{}, err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err = ctx.Err(); err != nil {
		_ = tarWriter.Close()
		_ = gzipWriter.Close()
		return SnapshotArtifact{}, err
	}
	if err = tarWriter.WriteHeader(&tar.Header{
		Name:    "manifest.json",
		Mode:    0o600,
		Size:    int64(len(manifestBytes)),
		ModTime: capturedAt,
	}); err == nil {
		_, err = tarWriter.Write(manifestBytes)
	}
	if err != nil {
		_ = tarWriter.Close()
		_ = gzipWriter.Close()
		return SnapshotArtifact{}, err
	}
	if err = tarWriter.Close(); err != nil {
		_ = gzipWriter.Close()
		return SnapshotArtifact{}, err
	}
	if err = gzipWriter.Close(); err != nil {
		return SnapshotArtifact{}, err
	}
	if err = output.Sync(); err != nil {
		return SnapshotArtifact{}, err
	}
	if err = output.Close(); err != nil {
		return SnapshotArtifact{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return SnapshotArtifact{}, err
	}
	complete = true
	return SnapshotArtifact{
		Name:    filepath.Base(path),
		Path:    path,
		Size:    info.Size(),
		ModTime: info.ModTime().UTC(),
	}, nil
}

func selectSnapshotLogs(dir string, maxFiles int, maxBytes int64) ([]snapshotLogFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []snapshotLogFile{}, nil
		}
		return nil, err
	}
	files := make([]snapshotLogFile, 0)
	for _, entry := range entries {
		if !isBililiveLogName(entry.Name()) || entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		offset := info.Size() - maxBytes
		if offset < 0 {
			offset = 0
		}
		files = append(files, snapshotLogFile{
			path:       filepath.Join(dir, entry.Name()),
			name:       entry.Name(),
			modTime:    info.ModTime().UTC(),
			sourceSize: info.Size(),
			offset:     offset,
		})
	}
	sort.Slice(files, func(left, right int) bool {
		if files[left].modTime.Equal(files[right].modTime) {
			return files[left].name > files[right].name
		}
		return files[left].modTime.After(files[right].modTime)
	})
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}
	return files, nil
}

func isBililiveLogName(name string) bool {
	return strings.HasSuffix(name, ".log") &&
		(strings.HasPrefix(name, "bililive-go-") || strings.HasPrefix(name, "run-"))
}

func writeSnapshotLog(
	ctx context.Context,
	writer *tar.Writer,
	snapshot snapshotLogFile,
	capturedAt time.Time,
) error {
	before, err := os.Lstat(snapshot.path)
	if err != nil {
		return err
	}
	if before.Mode()&fs.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return errors.New("日志文件不是普通文件")
	}
	input, err := os.Open(snapshot.path)
	if err != nil {
		return err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, opened) || opened.Size() < snapshot.sourceSize {
		return errors.New("日志文件在冻结期间被替换或截断")
	}
	if _, err = input.Seek(snapshot.offset, io.SeekStart); err != nil {
		return err
	}
	size := snapshot.sourceSize - snapshot.offset
	if err = writer.WriteHeader(&tar.Header{
		Name:    filepath.ToSlash(filepath.Join("logs", snapshot.name)),
		Mode:    0o600,
		Size:    size,
		ModTime: capturedAt,
	}); err != nil {
		return err
	}
	_, err = copySnapshotNWithContext(ctx, writer, input, size)
	return err
}

func copySnapshotNWithContext(
	ctx context.Context,
	destination io.Writer,
	source io.Reader,
	size int64,
) (int64, error) {
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
