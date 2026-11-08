//go:build dev

package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TestRunner 仅运行 FFmpeg 媒体冒烟测试，不经过 bgo 的 recorder、选流和重连逻辑。
type TestRunner struct {
	ServerURL string
	OutputDir string
	// BGOPath 为兼容旧调用保留；非空时明确报错，不能静默忽略。
	BGOPath     string
	FFmpegPath  string
	FFprobePath string
	Verbose     bool

	// commandContext 允许测试隔离外部进程；正常运行使用 exec.CommandContext。
	commandContext func(context.Context, string, ...string) *exec.Cmd
}

// NewTestRunner 创建 FFmpeg 媒体冒烟运行器。
func NewTestRunner(serverURL, outputDir string) *TestRunner {
	return &TestRunner{ServerURL: serverURL, OutputDir: outputDir, Verbose: true}
}

// RunScenario 只验证支持的媒体冒烟场景，未实现的能力必须失败，不能报告通过。
func (tr *TestRunner) RunScenario(ctx context.Context, scenario TestScenario) (*TestResult, error) {
	startTime := time.Now()
	result := &TestResult{ScenarioName: scenario.Name}
	defer func() { result.Duration = time.Since(startTime) }()
	if err := tr.validateScenario(scenario); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		result.ErrorMessage = fmt.Sprintf("测试已取消: %v", err)
		return result, err
	}

	tr.log("FFmpeg 媒体冒烟场景: %s（不验证 bgo 录制链路）", scenario.Name)
	serverURL := normalizeServerURL(tr.ServerURL)
	if err := NewTestServer(serverURL).HealthCheck(ctx); err != nil {
		result.ErrorMessage = fmt.Sprintf("测试服务器不可用: %v", err)
		return result, ctx.Err()
	}
	if err := os.MkdirAll(tr.OutputDir, 0755); err != nil {
		result.ErrorMessage = fmt.Sprintf("创建输出目录失败: %v", err)
		return result, nil
	}
	// 每次运行使用独立目录，失败或没有产物时不能误读上一次的成功文件。
	runDir, err := os.MkdirTemp(tr.OutputDir, scenario.Name+"-")
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("创建本次测试目录失败: %v", err)
		return result, nil
	}
	ext := ".flv"
	if scenario.Stream.Format == "hls" {
		ext = ".ts"
	}
	result.OutputPath = filepath.Join(runDir, scenario.Name+ext)
	recordCtx, cancel := context.WithTimeout(ctx, scenario.Stream.Duration+30*time.Second)
	defer cancel()

	if err := tr.runFFmpeg(recordCtx, tr.buildStreamURL(scenario), result.OutputPath, scenario.Stream.Duration); err != nil {
		if recordCtx.Err() != nil {
			err = recordCtx.Err()
		}
		result.ErrorMessage = fmt.Sprintf("录制失败: %v", err)
		return result, ctx.Err()
	}
	if err := recordCtx.Err(); err != nil {
		result.ErrorMessage = fmt.Sprintf("录制未正常完成: %v", err)
		return result, ctx.Err()
	}
	if err := tr.validateOutput(recordCtx, result.OutputPath, scenario.Expected, result); err != nil {
		result.ErrorMessage = fmt.Sprintf("验证失败: %v", err)
		return result, ctx.Err()
	}
	result.Success = true
	tr.log("  媒体冒烟测试通过，时长 %.3f 秒", result.OutputDuration.Seconds())
	return result, nil
}

func (tr *TestRunner) validateScenario(s TestScenario) error {
	if tr.BGOPath != "" || s.RequiresBGO {
		return fmt.Errorf("不支持 bgo 端到端测试：此运行器只调用 FFmpeg")
	}
	if len(s.Faults) != 0 {
		return fmt.Errorf("不支持故障注入：Faults 尚未接入执行，不能验证此场景")
	}
	if s.Expected.DownloaderReconnects || len(s.Expected.ComplianceWarnings) != 0 || s.Expected.MaxFileSizeDiff != 0 {
		return fmt.Errorf("不支持重连、合规警告或文件大小差异断言")
	}
	if !s.Expected.OutputPlayable {
		return fmt.Errorf("不支持预期不可播放的场景，请显式设置 OutputPlayable")
	}
	if s.Name == "" || s.Name == "." || s.Name == ".." || strings.ContainsAny(s.Name, "/\\") {
		return fmt.Errorf("场景名称必须是单个安全的文件名")
	}
	if s.Stream.Format != "flv" && s.Stream.Format != "hls" {
		return fmt.Errorf("不支持流格式 %q", s.Stream.Format)
	}
	if s.Stream.Duration <= 0 || s.Stream.Duration > time.Duration(math.MaxInt64)-30*time.Second || s.Expected.MinDuration < 0 {
		return fmt.Errorf("录制时长必须为正数且不能溢出，预期最小时长不能为负数")
	}
	return nil
}

func (tr *TestRunner) RunAllScenarios(ctx context.Context) ([]*TestResult, error) {
	scenarios := GetAvailableScenarios()
	results := make([]*TestResult, 0, len(scenarios))
	for _, scenario := range scenarios {
		result, err := tr.RunScenario(ctx, scenario)
		results = append(results, result)
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func normalizeServerURL(raw string) string {
	raw = strings.TrimRight(raw, "/")
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return raw
}

func (tr *TestRunner) buildStreamURL(scenario TestScenario) string {
	prefix, ext := "/live/", ".flv"
	if scenario.Stream.Format == "hls" {
		prefix, ext = "/hls/", ".m3u8"
	}
	query := url.Values{
		"codec":    {scenario.Stream.Codec},
		"quality":  {scenario.Stream.Quality},
		"duration": {strconv.FormatFloat(scenario.Stream.Duration.Seconds(), 'f', -1, 64)},
	}
	return normalizeServerURL(tr.ServerURL) + prefix + url.PathEscape(scenario.Name) + ext + "?" + query.Encode()
}

func (tr *TestRunner) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	if tr.commandContext != nil {
		return tr.commandContext(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

func toolPath(configured, fallback string) string {
	if configured != "" {
		return configured
	}
	return fallback
}

func (tr *TestRunner) runFFmpeg(ctx context.Context, inputURL, outputPath string, duration time.Duration) error {
	cmd := tr.command(ctx, toolPath(tr.FFmpegPath, "ffmpeg"),
		"-nostdin", "-y", "-i", inputURL, "-c", "copy",
		"-t", strconv.FormatFloat(duration.Seconds(), 'f', -1, 64), outputPath)
	if tr.Verbose {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	return cmd.Run()
}

func (tr *TestRunner) validateOutput(ctx context.Context, outputPath string, expected Expected, result *TestResult) error {
	result.OutputPlayable = false
	result.OutputDuration = 0
	result.OutputFileSize = 0
	info, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("输出文件不存在: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("输出必须是非空普通文件")
	}
	result.OutputFileSize = info.Size()
	duration, err := tr.getMediaDuration(ctx, outputPath)
	if err != nil {
		return fmt.Errorf("无法验证媒体信息: %w", err)
	}
	result.OutputDuration = duration
	if duration < expected.MinDuration {
		return fmt.Errorf("输出时长 %.3fs 小于预期 %.3fs", duration.Seconds(), expected.MinDuration.Seconds())
	}
	// ffprobe 读到元数据不等于媒体可解码，完整解码音视频流后才标记可播放。
	cmd := tr.command(ctx, toolPath(tr.FFmpegPath, "ffmpeg"),
		"-nostdin", "-v", "error", "-xerror", "-abort_on", "empty_output", "-i", outputPath,
		"-map", "0:v?", "-map", "0:a?", "-f", "null", "-")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("媒体解码失败: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result.OutputPlayable = true
	return nil
}

func (tr *TestRunner) getMediaDuration(ctx context.Context, filePath string) (time.Duration, error) {
	cmd := tr.command(ctx, toolPath(tr.FFprobePath, "ffprobe"),
		"-v", "error", "-show_entries", "format=duration:stream=codec_type", "-of", "json", filePath)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return parseMediaDuration(output)
}

func parseMediaDuration(output []byte) (time.Duration, error) {
	var probe struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &probe); err != nil {
		return 0, fmt.Errorf("无效的 ffprobe 响应: %w", err)
	}
	hasMedia := false
	for _, stream := range probe.Streams {
		if stream.CodecType == "video" || stream.CodecType == "audio" {
			hasMedia = true
		}
	}
	if !hasMedia {
		return 0, fmt.Errorf("没有音视频流")
	}
	seconds, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return 0, fmt.Errorf("无效的媒体时长 %q", probe.Format.Duration)
	}
	duration := time.Duration(seconds * float64(time.Second))
	if duration <= 0 {
		return 0, fmt.Errorf("媒体时长不足 1 纳秒")
	}
	return duration, nil
}

func (tr *TestRunner) log(format string, args ...interface{}) {
	if tr.Verbose {
		fmt.Printf(format+"\n", args...)
	}
}

// PrintReport 打印测试报告
func PrintReport(results []*TestResult) {
	passed := 0
	failed := 0

	for _, r := range results {
		if r.Success {
			passed++
		} else {
			failed++
		}
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("测试报告")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("总计: %d | 通过: %d | 失败: %d\n", len(results), passed, failed)
	fmt.Println(strings.Repeat("-", 60))

	for _, r := range results {
		status := "✅ PASS"
		if !r.Success {
			status = "❌ FAIL"
		}
		fmt.Printf("%s  %s  (%.1fs)\n", status, r.ScenarioName, r.Duration.Seconds())
		if r.ErrorMessage != "" {
			fmt.Printf("         错误: %s\n", r.ErrorMessage)
		}
	}

	fmt.Println(strings.Repeat("=", 60))

	if failed == 0 {
		fmt.Println("🎉 所有测试通过!")
	} else {
		fmt.Printf("⚠  %d 个测试失败\n", failed)
	}
}
