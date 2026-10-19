package stages

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// 每次转换的诊断最多保留 64 种警告，最终文本不超过 16 KiB（含统计说明）。
	ffmpegDiagnosticsMaxLines  = 64
	ffmpegDiagnosticsMaxBytes  = 16 * 1024
	ffmpegDiagnosticsLineBytes = 1024
)

var (
	ffmpegDiagnosticsAddress = regexp.MustCompile(`@[ \t]+(?:0[xX])?[0-9a-fA-F]+`)
	// 前两个分支匹配 stream 1、stream = 1、stream #0:1 及 aist#0:1 等流标识，归一化时原样保留。
	ffmpegDiagnosticsNumber = regexp.MustCompile(`(?i:stream(?:[ \t]*=[ \t]*|[ \t]+#?)[0-9]+(?::[0-9]+)?)|#[0-9]+:[0-9]+|0[xX][0-9a-fA-F]+|[+-]?[0-9]+(?:\.[0-9]+)?`)
)

// readFFmpegDiagnostics 排空诊断流，合并归一化后相同的完整行，并限制保留内容的大小。
// 超长行分块读取，只保存前缀；不会因 Scanner 的行长限制提前停止读取管道。
func readFFmpegDiagnostics(input io.Reader) (diagnostics string, readErr error) {
	completed := false
	// 诊断收集是非关键路径：异常只作为告警返回，不允许 panic 中断转换。
	defer func() {
		panicValue := recover()
		// 使用正常完成标记，确保 panic(nil) 也会进入异常处理。
		if !completed {
			diagnostics = ""
			readErr = fmt.Errorf("FFmpeg 诊断收集发生异常: %v", panicValue)
		}
		if readErr != nil {
			// 若诊断整理或一次读取失败，仍尝试排空可读的管道，避免 FFmpeg 等待写入。
			// 底层 Reader 若持续异常，则停止尝试，且不让二次 panic 向上传播。
			func() {
				defer func() {
					if panicValue := recover(); panicValue != nil {
						readErr = errors.Join(readErr, fmt.Errorf("排空 FFmpeg 诊断发生异常: %v", panicValue))
					}
				}()
				reader := bufio.NewReaderSize(input, 4096)
				for {
					_, err := reader.ReadSlice('\n')
					if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
						return
					}
				}
			}()
		}
	}()
	type warning struct {
		text  string
		count uint64
	}
	reader := bufio.NewReaderSize(input, 4096)
	var warnings []warning
	seen := make(map[string]int)
	var line []byte
	var totalLines uint64
	storedBytes := 0
	truncated, longLine := false, false
	for {
		fragment, err := reader.ReadSlice('\n')
		// 换行符不计入单行的保存额度；CR 在整理完整行时去掉。
		fragment = bytes.TrimSuffix(fragment, []byte{'\n'})
		remaining := ffmpegDiagnosticsLineBytes - len(line)
		line = append(line, fragment[:min(len(fragment), remaining)]...)
		longLine = longLine || len(fragment) > remaining
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		// EOF 或读取错误前可能还有最后一行；空流不计为一行。
		if err == nil || len(line) > 0 || longLine {
			totalLines++
			text := strings.TrimSpace(strings.ToValidUTF8(string(line), ""))
			key := text
			if !longLine {
				key = normalizeFFmpegDiagnosticsKey(text)
			}
			if index, ok := seen[key]; ok && !longLine {
				warnings[index].count++
			} else if text != "" {
				if len(warnings) < ffmpegDiagnosticsMaxLines && storedBytes+len(text) <= ffmpegDiagnosticsMaxBytes {
					if !longLine {
						seen[key] = len(warnings)
					}
					warnings = append(warnings, warning{text: text, count: 1})
					storedBytes += len(text)
				} else {
					truncated = true
				}
			}
			truncated = truncated || longLine
		}
		line = line[:0]
		longLine = false
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}

	var result strings.Builder
	for _, warning := range warnings {
		if result.Len() > 0 {
			result.WriteByte('\n')
		}
		result.WriteString(warning.text)
		if warning.count > 1 {
			fmt.Fprintf(&result, " [出现 %d 次]", warning.count)
		}
	}
	// 留出截断说明的空间，并在 UTF-8 字符边界截断最终文本。
	text := result.String()
	if len(text) > ffmpegDiagnosticsMaxBytes-128 {
		end := ffmpegDiagnosticsMaxBytes - 128
		for !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end]
		truncated = true
	}
	if truncated {
		text += fmt.Sprintf("\n[已截断，共 %d 行]", totalLines)
	}
	completed = true
	return text, readErr
}

// normalizeFFmpegDiagnosticsKey 只归一化去重键，保留首次原文用于排查。
// 去掉数字、时间戳及实例地址的差异，避免同类警告占满保存额度；
// 保留流序号，不同流的同类警告分开统计，便于定位出问题的流。
func normalizeFFmpegDiagnosticsKey(text string) string {
	text = ffmpegDiagnosticsAddress.ReplaceAllString(text, "@ #")
	return ffmpegDiagnosticsNumber.ReplaceAllStringFunc(text, func(match string) string {
		if match[0] == '#' || match[0]|0x20 == 's' {
			return match
		}
		return "#"
	})
}
