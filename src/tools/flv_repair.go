package tools

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	blog "github.com/bililive-go/bililive-go/src/log"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FLVStats 是结构扫描结果，不代表逐帧解码或直播源内容完整。
type FLVStats struct {
	flags    byte
	Size     int64
	Packets  [2]int64 // 音频、视频数据包（不包含编码头和结束标记）
	First    [2]uint32
	Last     [2]uint32
	Backward [2]int64
}

// InspectFLV 只读扫描全部标签，检查边界和 PreviousTagSize，不依赖外部工具。
func InspectFLV(ctx context.Context, path string) (FLVStats, error) {
	return inspectFLV(ctx, path, nil)
}

// 所有校验复用这一处标签读取；缓冲区复用，不为每个媒体包分配内存。
func inspectFLV(ctx context.Context, path string, visit func([]byte, []byte) error) (stats FLVStats, err error) {
	f, err := os.Open(path)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return stats, err
	}
	stats.Size = st.Size()
	r := bufio.NewReaderSize(f, 256*1024)
	var header [9]byte
	if _, err = io.ReadFull(r, header[:]); err != nil {
		return stats, err
	}
	if string(header[:3]) != "FLV" || header[3] != 1 || header[4]&^byte(5) != 0 {
		return stats, fmt.Errorf("invalid FLV header")
	}
	stats.flags = header[4]
	offset := int64(binary.BigEndian.Uint32(header[5:]))
	if offset < 9 || offset > stats.Size-4 {
		return stats, fmt.Errorf("invalid FLV offset")
	}
	if _, err = io.CopyN(io.Discard, r, offset-9); err != nil {
		return stats, err
	}
	var prev [4]byte
	if _, err = io.ReadFull(r, prev[:]); err != nil {
		return stats, err
	}
	if binary.BigEndian.Uint32(prev[:]) != 0 {
		return stats, fmt.Errorf("invalid initial PreviousTagSize")
	}
	payload := make([]byte, 64*1024)
	var last [2]uint32
	for pos := offset + 4; pos < stats.Size; {
		if err = ctx.Err(); err != nil {
			return stats, err
		}
		var tag [11]byte
		if _, err = io.ReadFull(r, tag[:]); err != nil {
			return stats, err
		}
		n := int(tag[1])<<16 | int(tag[2])<<8 | int(tag[3])
		if pos+int64(n)+15 > stats.Size {
			return stats, fmt.Errorf("truncated payload at %d", pos)
		}
		if (tag[0] != 8 && tag[0] != 9 && tag[0] != 18) || tag[8] != 0 || tag[9] != 0 || tag[10] != 0 {
			return stats, fmt.Errorf("unsupported FLV tag at %d", pos)
		}
		take := n
		if visit == nil && take > 5 {
			take = 5
		}
		if cap(payload) < take {
			payload = make([]byte, take)
		}
		payload = payload[:take]
		if _, err = io.ReadFull(r, payload); err != nil {
			return stats, err
		}
		index := -1
		if tag[0] == 8 && n > 0 {
			index = 0
			if payload[0]>>4 == 10 && (n < 2 || payload[1] != 1) {
				index = -1
			}
		}
		if tag[0] == 9 && n > 0 {
			index = 1
			if payload[0]&15 == 7 && (n < 5 || payload[1] != 1) {
				index = -1
			}
		}
		if index >= 0 {
			ts := uint32(tag[7])<<24 | uint32(tag[4])<<16 | uint32(tag[5])<<8 | uint32(tag[6])
			if stats.Packets[index] == 0 {
				stats.First[index] = ts
			} else if int64(ts)+2000 < int64(last[index]) {
				stats.Backward[index]++
			}
			last[index] = ts
			if ts > stats.Last[index] {
				stats.Last[index] = ts
			}
			stats.Packets[index]++
		}
		if _, err = io.CopyN(io.Discard, r, int64(n-take)); err != nil {
			return stats, err
		}
		if _, err = io.ReadFull(r, prev[:]); err != nil {
			return stats, err
		}
		if binary.BigEndian.Uint32(prev[:]) != uint32(n+11) {
			return stats, fmt.Errorf("invalid PreviousTagSize at %d", pos)
		}
		if visit != nil {
			if err = visit(tag[:], payload); err != nil {
				return stats, err
			}
		}
		pos += int64(n) + 15
	}
	if stats.Packets[0]+stats.Packets[1] == 0 {
		return stats, fmt.Errorf("no media packets")
	}
	return stats, nil
}

// tailWriter 限制内存中的诊断长度；完整输出同时写入工作目录的日志。
type tailWriter struct{ data []byte }

func (w *tailWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.data = append(w.data, p...)
	if len(w.data) > 65536 {
		w.data = append([]byte(nil), w.data[len(w.data)-65536:]...)
	}
	return n, nil
}

func repairFLV(ctx context.Context, input string, onPID func(int), command func(string, string) (*exec.Cmd, error)) (outputs []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	input, err = filepath.Abs(input)
	if err != nil {
		return nil, err
	}
	before, err := os.Stat(input)
	if err != nil {
		return nil, err
	}
	source, err := InspectFLV(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("original FLV validation failed: %w", err)
	}
	if source.Size != before.Size() {
		return nil, fmt.Errorf("original changed during validation")
	}
	dir, err := os.MkdirTemp(filepath.Dir(input), ".bililive-fix-")
	if err != nil {
		return nil, err
	}
	// 失败只清理本次独立目录中的媒体产物，保留诊断日志和原片。
	defer func() {
		if err != nil {
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if strings.EqualFold(filepath.Ext(entry.Name()), ".flv") {
					_ = os.Remove(filepath.Join(dir, entry.Name()))
				}
			}
			err = fmt.Errorf("%w; 原片保留，诊断目录: %s", err, dir)
		}
	}()
	output := filepath.Join(dir, filepath.Base(input))
	cmd, err := command(input, output)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "command.txt"), []byte(cmd.String()+"\n"), 0600); err != nil {
		return nil, err
	}
	stdout, err := os.Create(filepath.Join(dir, "stdout.json"))
	if err != nil {
		return nil, err
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(dir, "stderr.log"))
	if err != nil {
		return nil, err
	}
	defer stderr.Close()
	var outTail, errTail tailWriter
	cmd.Stdout = io.MultiWriter(stdout, &outTail)
	cmd.Stderr = io.MultiWriter(stderr, &errTail)
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	if onPID != nil {
		onPID(cmd.Process.Pid)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()
	err = cmd.Wait()
	close(done)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("修复工具退出: %w; stdout=%s; stderr=%s", err, outTail.data, errTail.data)
	}
	if _, err = stdout.Seek(0, 0); err != nil {
		return nil, err
	}
	var result struct {
		Status string
		Data   struct {
			OutputPaths     []string
			OutputFiles     []string
			OutputFileCount int
		}
	}
	if err = json.NewDecoder(io.LimitReader(stdout, 4<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid repair JSON: %w", err)
	}
	if !strings.EqualFold(result.Status, "OK") {
		return nil, fmt.Errorf("repair status %q: %s", result.Status, outTail.data)
	}
	paths := result.Data.OutputPaths
	if len(paths) == 0 {
		paths = result.Data.OutputFiles
	}
	if len(paths) == 0 {
		if result.Data.OutputFileCount < 1 || result.Data.OutputFileCount > 10000 {
			return nil, fmt.Errorf("invalid output count")
		}
		for i := 1; i <= result.Data.OutputFileCount; i++ {
			paths = append(paths, fmt.Sprintf("%s.fix_p%03d.flv", strings.TrimSuffix(output, filepath.Ext(output)), i))
		}
	}
	var total int64
	seen := map[string]bool{}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		path = filepath.Clean(path)
		if filepath.Dir(path) != dir || seen[path] {
			return nil, fmt.Errorf("unsafe or duplicate repair output: %s", path)
		}
		seen[path] = true
		st, e := os.Lstat(path)
		if e != nil {
			return nil, e
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("non-regular output: %s", path)
		}
		total += st.Size()
		outputs = append(outputs, path)
	}
	small := total*10 < source.Size*9
	if small && len(outputs) != 1 {
		return nil, fmt.Errorf("multi-part filler verification is not supported")
	}
	var filler fillerFingerprint
	media := newMediaFingerprint()
	var scannedTotal int64
	var packets [2]int64
	var durations [2]int64
	for _, path := range outputs {
		var scanned FLVStats
		var e error
		switch {
		case small:
			scanned, filler, e = inspectWithoutFiller(ctx, path)
		case len(outputs) > 1:
			scanned, e = inspectFLV(ctx, path, media.visitor())
		default:
			scanned, e = InspectFLV(ctx, path)
		}
		if e != nil {
			return nil, fmt.Errorf("repair output validation: %w", e)
		}
		scannedTotal += scanned.Size
		for i := 0; i < 2; i++ {
			if scanned.Backward[i] > 0 {
				return nil, fmt.Errorf("repair output has backward timestamps")
			}
			packets[i] += scanned.Packets[i]
			durations[i] += int64(scanned.Last[i]) - int64(scanned.First[i])
		}
	}
	if scannedTotal != total {
		return nil, fmt.Errorf("repair output changed during validation")
	}
	if small {
		_, original, verifyErr := inspectWithoutFiller(ctx, input)
		if verifyErr != nil {
			return nil, verifyErr
		}
		evidence, verifyErr := compareFillerRemoval(original, filler)
		if verifyErr != nil {
			return nil, fmt.Errorf("sum of fixed parts (%d) < 90%% of original (%d); filler-only verification failed: %w", total, source.Size, verifyErr)
		}
		data, marshalErr := json.MarshalIndent(evidence, "", "  ")
		if marshalErr != nil {
			return nil, marshalErr
		}
		if err = os.WriteFile(filepath.Join(dir, "filler-verification.json"), data, 0600); err != nil {
			return nil, err
		}
	}
	for i := 0; i < 2; i++ {
		if source.Packets[i] == 0 {
			continue
		}
		if packets[i]*100 < source.Packets[i]*98 {
			return nil, fmt.Errorf("repair stream %d packet coverage below 98%%", i)
		}
		span := int64(source.Last[i]) - int64(source.First[i])
		if durations[i]*100 < span*98 {
			return nil, fmt.Errorf("repair stream %d duration coverage below 98%%", i)
		}
	}
	if len(outputs) > 1 {
		original := newMediaFingerprint()
		if _, err = inspectFLV(ctx, input, original.visitor()); err != nil {
			return nil, err
		}
		if err = original.compare(media); err != nil {
			return nil, err
		}
	}

	after, e := os.Stat(input)
	if e != nil {
		return nil, e
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("original changed during repair")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// 先刷新修复产物，再利用同一文件系统的 rename 替换单文件原片。
	// 多分段先完整发布、刷新目录，最后删除原片；中途失败保留原片。
	for _, path := range outputs {
		f, e := os.OpenFile(path, os.O_RDWR, 0)
		if e != nil {
			return nil, e
		}
		e = f.Sync()
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if len(outputs) == 1 {
		if err = os.Rename(outputs[0], input); err != nil {
			return nil, fmt.Errorf("替换失败，保留原片: %w", err)
		}
		outputs = []string{input}
	}
	if len(outputs) > 1 {
		if outputs, err = publishMultipart(input, outputs); err != nil {
			return nil, err
		}
	}
	// 多分段成功后清理工作目录；单文件保留现有诊断策略，失败日志由 defer 保留。
	if len(outputs) > 1 {
		if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
			blog.GetLogger().WithError(cleanupErr).Warnf("修复已完成，工作目录清理失败: %s", dir)
		}
	}
	return outputs, nil
}

// 发布用硬链接拒绝覆盖同名文件；任何一步失败都撤销本次链接，原片不动。
func publishMultipart(input string, parts []string) ([]string, error) {
	published := make([]string, 0, len(parts))
	rollback := func() {
		for _, p := range published {
			_ = os.Remove(p)
		}
	}
	for i, part := range parts {
		dest := fmt.Sprintf("%s.part%03d.flv", strings.TrimSuffix(input, filepath.Ext(input)), i+1)
		if err := os.Link(part, dest); err != nil {
			rollback()
			return nil, fmt.Errorf("发布分段失败，原片保留: %w", err)
		}
		published = append(published, dest)
	}
	d, err := os.Open(filepath.Dir(input))
	if err != nil {
		rollback()
		return nil, err
	}
	err = d.Sync()
	_ = d.Close()
	if err != nil {
		rollback()
		return nil, err
	}
	if err = os.Remove(input); err != nil {
		rollback()
		return nil, err
	}
	return published, nil
}

// 多分段只允许重建时间戳；逐流编码包和生效的编码头必须完全一致。
type mediaFingerprint struct{ hashes [2]hash.Hash }

func newMediaFingerprint() *mediaFingerprint {
	return &mediaFingerprint{hashes: [2]hash.Hash{sha256.New(), sha256.New()}}
}

// 每个文件独立跟踪编码头，分段须能独立解码。
func (m *mediaFingerprint) visitor() func([]byte, []byte) error {
	var configs [2][]byte
	return func(tag, payload []byte) error {
		k := -1
		if tag[0] == 8 {
			k = 0
		}
		if tag[0] == 9 {
			k = 1
		}
		if k < 0 {
			return nil
		}
		n := len(payload)
		if n < 2 || (k == 0 && payload[0]>>4 != 10) || (k == 1 && (n < 5 || payload[0]&15 != 7)) {
			return fmt.Errorf("multi-part verification supports AAC/AVC only")
		}
		switch payload[1] {
		case 0:
			configs[k] = append(configs[k][:0], payload...)
		case 1:
			if len(configs[k]) == 0 {
				return fmt.Errorf("missing decoder configuration")
			}
			hashUint32(m.hashes[k], uint32(len(configs[k])))
			_, _ = m.hashes[k].Write(configs[k])
			hashUint32(m.hashes[k], uint32(n))
			_, _ = m.hashes[k].Write(payload)
		case 2:
		default:
			return fmt.Errorf("unsupported media packet")
		}
		return nil
	}
}
func (m *mediaFingerprint) compare(other *mediaFingerprint) error {
	for k := range m.hashes {
		if !bytes.Equal(m.hashes[k].Sum(nil), other.hashes[k].Sum(nil)) {
			return fmt.Errorf("multi-part stream %d content or decoder configuration changed", k)
		}
	}
	return nil
}
