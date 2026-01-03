package task

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/sirupsen/logrus"
)

// ConvertMP4Executor MP4转换执行器
type ConvertMP4Executor struct {
	deleteOriginal bool
}

// NewConvertMP4Executor 创建MP4转换执行器
func NewConvertMP4Executor(deleteOriginal bool) *ConvertMP4Executor {
	return &ConvertMP4Executor{
		deleteOriginal: deleteOriginal,
	}
}

// Execute 执行MP4转换
func (e *ConvertMP4Executor) Execute(ctx context.Context, task *Task, progressCallback func(int)) error {
	if task.InputFile == "" {
		return fmt.Errorf("input file is required")
	}

	// 检查输入文件是否存在
	if _, err := os.Stat(task.InputFile); os.IsNotExist(err) {
		return fmt.Errorf("input file does not exist: %s", task.InputFile)
	}

	ffmpegPath, err := utils.GetFFmpegPath(ctx)
	if err != nil {
		return fmt.Errorf("ffmpeg not available: %w", err)
	}

	// 确定输出文件名
	outputFile := task.OutputFile
	if outputFile == "" {
		// 替换扩展名为 .mp4
		ext := filepath.Ext(task.InputFile)
		base := strings.TrimSuffix(task.InputFile, ext)
		outputFile = base + ".mp4"
		task.OutputFile = outputFile
	}

	// 创建临时文件路径
	dir := filepath.Dir(outputFile)
	baseName := filepath.Base(outputFile)
	tempFile := filepath.Join(dir, ".converting_"+baseName)
	task.TempFiles = []string{tempFile}

	logrus.WithFields(logrus.Fields{
		"task_id": task.ID,
		"input":   task.InputFile,
		"output":  outputFile,
	}).Info("starting MP4 conversion")

	// 首先获取视频时长
	duration, err := e.getVideoDuration(ctx, task.InputFile)
	if err != nil {
		logrus.WithError(err).Warn("failed to get video duration, progress will not be accurate")
		duration = 0
	}

	// 构建 ffmpeg 命令
	// -c copy 直接复制流，不重新编码（最快）
	// 如果遇到问题，可以使用 -c:v libx264 -c:a aac 重新编码
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-i", task.InputFile,
		"-c", "copy",
		"-movflags", "+faststart", // 将 moov atom 移到文件开头，支持边下载边播放
		"-y",
		"-progress", "pipe:1", // 输出进度信息到 stdout
		tempFile,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// 解析进度
	progressCallback(5)
	go e.parseProgress(ctx, stdout, duration, progressCallback)

	// 等待命令完成
	if err := cmd.Wait(); err != nil {
		os.Remove(tempFile)
		return fmt.Errorf("ffmpeg conversion failed: %w", err)
	}

	progressCallback(90)

	// 检查临时文件是否生成成功
	if _, err := os.Stat(tempFile); os.IsNotExist(err) {
		return fmt.Errorf("temp file was not created")
	}

	// 重命名临时文件为输出文件
	if err := os.Rename(tempFile, outputFile); err != nil {
		os.Remove(tempFile)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	// 检查是否需要删除原始文件
	// 优先从 task.Metadata 读取，如果没有则使用默认配置
	shouldDelete := e.deleteOriginal
	if task.Metadata != nil {
		if v, ok := task.Metadata["delete_original"]; ok {
			if delete, ok := v.(bool); ok {
				shouldDelete = delete
			}
		}
	}

	// 删除原始文件（如果配置了）
	if shouldDelete && task.InputFile != outputFile {
		if err := os.Remove(task.InputFile); err != nil {
			logrus.WithError(err).WithField("file", task.InputFile).Warn("failed to delete original file")
		} else {
			logrus.WithField("file", task.InputFile).Info("deleted original file after conversion")
		}
	}

	progressCallback(100)

	logrus.WithFields(logrus.Fields{
		"task_id": task.ID,
		"output":  outputFile,
	}).Info("MP4 conversion completed")

	return nil
}

// getVideoDuration 获取视频时长（秒）
func (e *ConvertMP4Executor) getVideoDuration(ctx context.Context, inputFile string) (float64, error) {
	ffmpegPath, err := utils.GetFFmpegPath(ctx)
	if err != nil {
		return 0, err
	}

	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-i", inputFile,
		"-hide_banner",
	)

	output, _ := cmd.CombinedOutput() // ffprobe returns error for -i without output

	// 解析 Duration: HH:MM:SS.ms
	re := regexp.MustCompile(`Duration: (\d{2}):(\d{2}):(\d{2})\.(\d{2})`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 5 {
		return 0, fmt.Errorf("could not parse duration")
	}

	hours, _ := strconv.ParseFloat(matches[1], 64)
	minutes, _ := strconv.ParseFloat(matches[2], 64)
	seconds, _ := strconv.ParseFloat(matches[3], 64)
	ms, _ := strconv.ParseFloat(matches[4], 64)

	return hours*3600 + minutes*60 + seconds + ms/100, nil
}

// parseProgress 解析 ffmpeg 进度输出
func (e *ConvertMP4Executor) parseProgress(ctx context.Context, stdout io.Reader, totalDuration float64, callback func(int)) {
	scanner := bufio.NewScanner(stdout)
	re := regexp.MustCompile(`out_time_us=(\d+)`)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		matches := re.FindStringSubmatch(line)
		if len(matches) >= 2 {
			timeUs, _ := strconv.ParseFloat(matches[1], 64)
			currentTime := timeUs / 1000000 // 转换为秒

			if totalDuration > 0 {
				progress := int((currentTime/totalDuration)*85) + 5 // 5-90%
				if progress > 90 {
					progress = 90
				}
				callback(progress)
			}
		}
	}
}

// Cleanup 清理临时文件
func (e *ConvertMP4Executor) Cleanup(task *Task) {
	for _, tempFile := range task.TempFiles {
		if tempFile != "" {
			if err := os.Remove(tempFile); err != nil && !os.IsNotExist(err) {
				logrus.WithError(err).WithField("file", tempFile).Warn("failed to cleanup temp file")
			}
		}
	}
}
