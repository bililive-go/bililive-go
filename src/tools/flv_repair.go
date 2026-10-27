package tools

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FLVStats 是结构扫描结果，不代表逐帧解码或直播源内容完整。
type FLVStats struct {
	Size     int64
	Packets  [2]int64 // 音频、视频数据包（不包含编码头和结束标记）
	First    [2]uint32
	Last     [2]uint32
	Backward [2]int64
}

// InspectFLV 只读扫描全部标签，检查边界和 PreviousTagSize，不依赖外部工具。
func InspectFLV(ctx context.Context, path string) (FLVStats, error) {
	var stats FLVStats
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
	header := make([]byte, 9)
	if _, err = io.ReadFull(r, header); err != nil {
		return stats, err
	}
	if string(header[:3]) != "FLV" || header[3] != 1 {
		return stats, fmt.Errorf("invalid FLV header")
	}
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
	pos := offset + 4
	var last [2]uint32
	for pos < stats.Size {
		if err = ctx.Err(); err != nil {
			return stats, err
		}
		var tag [11]byte
		if _, err = io.ReadFull(r, tag[:]); err != nil {
			return stats, fmt.Errorf("truncated tag at %d: %w", pos, err)
		}
		n := int64(tag[1])<<16 | int64(tag[2])<<8 | int64(tag[3])
		if pos+11+n+4 > stats.Size {
			return stats, fmt.Errorf("truncated payload at %d", pos)
		}
		if tag[0] != 8 && tag[0] != 9 && tag[0] != 18 {
			return stats, fmt.Errorf("unsupported FLV tag %d at %d", tag[0], pos)
		}
		var prefix [5]byte
		take := n
		if take > 5 {
			take = 5
		}
		if _, err = io.ReadFull(r, prefix[:take]); err != nil {
			return stats, err
		}
		index := -1
		if tag[0] == 8 && n > 0 {
			index = 0
			if prefix[0]>>4 == 10 && (n < 2 || prefix[1] != 1) {
				index = -1
			}
		}
		if tag[0] == 9 && n > 0 {
			index = 1
			if prefix[0]&15 == 7 && (n < 5 || prefix[1] != 1) {
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
		if _, err = io.CopyN(io.Discard, r, n-take); err != nil {
			return stats, err
		}
		if _, err = io.ReadFull(r, prev[:]); err != nil {
			return stats, err
		}
		if int64(binary.BigEndian.Uint32(prev[:])) != n+11 {
			return stats, fmt.Errorf("invalid PreviousTagSize at %d", pos)
		}
		pos += 11 + n + 4
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
	paths := result.Data.OutputFiles
	if len(paths) == 0 {
		if result.Data.OutputFileCount < 1 || result.Data.OutputFileCount > 10000 {
			return nil, fmt.Errorf("invalid output count")
		}
		for i := 1; i <= result.Data.OutputFileCount; i++ {
			paths = append(paths, fmt.Sprintf("%s.fix_p%03d.flv", strings.TrimSuffix(output, filepath.Ext(output)), i))
		}
	}
	var total int64
	var packets [2]int64
	var durations [2]int64
	seen := map[string]bool{}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		path = filepath.Clean(path)
		// 只允许本次目录的普通文件，防止错误路径或符号链接指向原片。
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
		scanned, e := InspectFLV(ctx, path)
		if e != nil {
			return nil, fmt.Errorf("repair output validation: %w", e)
		}
		total += scanned.Size
		for i := 0; i < 2; i++ {
			if scanned.Backward[i] > 0 {
				return nil, fmt.Errorf("repair output has backward timestamps")
			}
			packets[i] += scanned.Packets[i]
			durations[i] += int64(scanned.Last[i]) - int64(scanned.First[i])
		}
		outputs = append(outputs, path)
	}
	if total*10 < source.Size*9 {
		// 大幅缩小仍默认拒绝，仅为单文件、有效媒体完全相同的 H.264 填充删除放行。
		if len(outputs) != 1 {
			return nil, fmt.Errorf("sum of fixed parts (%d) < 90%% of original (%d); multi-part filler verification is not supported", total, source.Size)
		}
		evidence, verifyErr := verifyH264FillerRemoval(ctx, input, outputs[0])
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
	after, e := os.Stat(input)
	if e != nil {
		return nil, e
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("original changed during repair")
	}
	// 先刷新修复产物，再利用同一文件系统的 rename 替换单文件原片。
	// rename 失败绝不提前删除原片；多分段暂保留原片，避免跨文件提交不完整。
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
	return outputs, nil
}
