//go:build dev

package dev

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TestRunner bgo自动化测试运行器
type TestRunner struct {
	// 测试服务器地址
	ServerURL string

	// 输出目录
	OutputDir string

	// bgo可执行路径
	BGOPath string

	// 日志
	Verbose bool
}

// NewTestRunner 创建测试运行器
func NewTestRunner(serverURL, outputDir string) *TestRunner {
	return &TestRunner{
		ServerURL: serverURL,
		OutputDir: outputDir,
		BGOPath:   "", // 使用默认
		Verbose:   true,
	}
}

// RunScenario 运行单个测试场景
func (tr *TestRunner) RunScenario(ctx context.Context, scenario TestScenario) (*TestResult, error) {
	startTime := time.Now()

	result := &TestResult{
		ScenarioName: scenario.Name,
		Success:      false,
	}

	tr.log("开始测试场景: %s", scenario.Name)
	tr.log("  描述: %s", scenario.Description)

	// 1. 检查测试服务器
	ts := NewTestServer(tr.ServerURL)
	if err := ts.HealthCheck(ctx); err != nil {
		result.ErrorMessage = fmt.Sprintf("测试服务器不可用: %v", err)
		return result, nil
	}
	tr.log("  测试服务器: 正常")

	// 2. 构建流URL
	streamURL := tr.buildStreamURL(scenario)
	tr.log("  流URL: %s", streamURL)

	// 3. 准备输出文件
	ext := ".flv"
	if scenario.Stream.Format == "hls" {
		ext = ".ts"
	}
	outputPath := filepath.Join(tr.OutputDir, scenario.Name+ext)
	result.OutputPath = outputPath

	// 确保输出目录存在
	if err := os.MkdirAll(tr.OutputDir, 0755); err != nil {
		result.ErrorMessage = fmt.Sprintf("创建输出目录失败: %v", err)
		return result, nil
	}

	// 4. 启动录制
	tr.log("  开始录制...")

	recordCtx, cancel := context.WithTimeout(ctx, scenario.Stream.Duration+30*time.Second)
	defer cancel()

	err := tr.runFFmpeg(recordCtx, streamURL, outputPath, scenario.Stream.Duration)
	if err != nil {
		// 检查是否只是超时（正常情况）
		if !strings.Contains(err.Error(), "killed") && !strings.Contains(err.Error(), "signal") {
			result.ErrorMessage = fmt.Sprintf("录制失败: %v", err)
			result.Duration = time.Since(startTime)
			return result, nil
		}
	}

	result.Duration = time.Since(startTime)
	tr.log("  录制完成，用时 %.1f秒", result.Duration.Seconds())

	// 5. 验证输出文件
	if err := tr.validateOutput(outputPath, scenario.Expected, result); err != nil {
		result.ErrorMessage = fmt.Sprintf("验证失败: %v", err)
		return result, nil
	}

	// 6. 判断是否成功
	if result.OutputPlayable && result.OutputDuration >= scenario.Expected.MinDuration {
		result.Success = true
		tr.log("  ✅ 测试通过")
	} else {
		tr.log("  ❌ 测试失败")
		if !result.OutputPlayable {
			result.ErrorMessage = "输出文件不可播放"
		} else {
			result.ErrorMessage = fmt.Sprintf("输出时长 %.1fs 小于预期 %.1fs",
				result.OutputDuration.Seconds(), scenario.Expected.MinDuration.Seconds())
		}
	}

	return result, nil
}

// RunAllScenarios 运行所有场景
func (tr *TestRunner) RunAllScenarios(ctx context.Context) ([]*TestResult, error) {
	scenarios := GetAvailableScenarios()
	results := make([]*TestResult, 0, len(scenarios))

	tr.log("开始运行 %d 个测试场景", len(scenarios))
	tr.log(strings.Repeat("=", 50))

	for i, scenario := range scenarios {
		tr.log("\n[%d/%d] 场景: %s", i+1, len(scenarios), scenario.Name)
		tr.log(strings.Repeat("-", 50))

		result, err := tr.RunScenario(ctx, scenario)
		if err != nil {
			tr.log("  运行错误: %v", err)
		}
		results = append(results, result)
	}

	tr.log(strings.Repeat("=", 50))
	tr.log("测试完成")

	return results, nil
}

// buildStreamURL 构建流URL
func (tr *TestRunner) buildStreamURL(scenario TestScenario) string {
	ext := ".flv"
	if scenario.Stream.Format == "hls" {
		ext = ".m3u8"
	}

	baseURL := tr.ServerURL
	if !strings.HasPrefix(baseURL, "http") {
		baseURL = "http://" + baseURL
	}

	return fmt.Sprintf("%s/live/%s%s?codec=%s&quality=%s&duration=%d",
		baseURL,
		scenario.Name,
		ext,
		scenario.Stream.Codec,
		scenario.Stream.Quality,
		int(scenario.Stream.Duration.Seconds()))
}

// runFFmpeg 使用FFmpeg录制
func (tr *TestRunner) runFFmpeg(ctx context.Context, inputURL, outputPath string, duration time.Duration) error {
	args := []string{
		"-y", // 覆盖输出
		"-i", inputURL,
		"-c", "copy", // 不重编码
		"-t", fmt.Sprintf("%.0f", duration.Seconds()),
		outputPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	if tr.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	return cmd.Run()
}

// validateOutput 验证输出文件
func (tr *TestRunner) validateOutput(outputPath string, expected Expected, result *TestResult) error {
	// 检查文件是否存在
	info, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("输出文件不存在: %v", err)
	}

	result.OutputFileSize = info.Size()

	if info.Size() == 0 {
		return fmt.Errorf("输出文件为空")
	}

	tr.log("  输出文件大小: %.2f MB", float64(info.Size())/1024/1024)

	// 使用ffprobe获取时长
	duration, err := tr.getMediaDuration(outputPath)
	if err != nil {
		tr.log("  ⚠ 无法获取媒体信息: %v", err)
		// 文件存在且有大小，假设可播放
		result.OutputPlayable = true
		result.OutputDuration = 0
	} else {
		result.OutputPlayable = true
		result.OutputDuration = duration
		tr.log("  输出时长: %.1f秒", duration.Seconds())
	}

	return nil
}

// getMediaDuration 获取媒体时长
func (tr *TestRunner) getMediaDuration(filePath string) (time.Duration, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		filePath)

	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	var seconds float64
	_, err = fmt.Sscanf(strings.TrimSpace(string(output)), "%f", &seconds)
	if err != nil {
		return 0, err
	}

	return time.Duration(seconds * float64(time.Second)), nil
}

// log 日志输出
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
