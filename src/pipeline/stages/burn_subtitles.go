package stages

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

	"github.com/bililive-go/bililive-go/src/pipeline"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/bililive-go/bililive-go/src/tools"
)

// BurnSubtitlesStage 弹幕字幕烧录阶段
type BurnSubtitlesStage struct {
	config       pipeline.StageConfig
	codec        string
	crf          string
	preset       string
	deleteAss    bool
	deleteSource bool
	commands     []string
	logs         string
}

// isNvencCodec 判断编码器是否为 NVIDIA NVENC 硬件编码器（h264_nvenc / hevc_nvenc / av1_nvenc 等）。
// NVENC 编码器不支持 -crf，恒定质量参数使用 -cq，因此 FFmpeg 参数构造逻辑不同。
func isNvencCodec(codec string) bool {
	return strings.Contains(strings.ToLower(codec), "nvenc")
}

// buildVideoEncodeArgs 根据编码器生成 FFmpeg 视频编码参数。
// 软编码（libx264/libx265 等）使用 -crf + -preset；
// NVENC 硬件编码不支持 CRF，改为 -rc:v vbr -cq <值> -b:v 0 -preset <p1-p7> 实现恒定质量输出。
// 注：配置项 burn_subtitles_crf 对 NVENC 的语义为 CQ 质量值（1-51，越小画质越好），
// 0 表示"自动质量"（FFmpeg 的 -cq 选项定义：0 means automatic），此时省略 -cq 交由
// 编码器自动决定质量，字段名保持不变以兼容已有配置。
func buildVideoEncodeArgs(codec, crf, preset string) []string {
	if isNvencCodec(codec) {
		args := []string{
			"-c:v", codec,
			"-rc:v", "vbr",
		}
		// -cq 为 0 时是"自动质量"而非最高画质（显式传 -cq 0 与 FFmpeg 默认行为一致），
		// 这里直接省略该参数，避免用户把 0 误解为最佳画质
		if !isZeroQuality(crf) {
			args = append(args, "-cq", crf)
		}
		return append(args,
			"-b:v", "0",
			"-preset", preset,
		)
	}
	return []string{
		"-c:v", codec,
		"-crf", crf,
		"-preset", preset,
	}
}

// isZeroQuality 判断质量值在数值上是否为 0（如 "0"、"00"、"0.0"），即 NVENC 的自动质量。
// 非数字值不视为 0，原样传给 FFmpeg 以保留其明确的报错信息。
func isZeroQuality(quality string) bool {
	value, err := strconv.ParseFloat(strings.TrimSpace(quality), 64)
	return err == nil && value == 0
}

// 烧录阶段各配置项的默认值
const (
	defaultBurnCodec   = "libx264"
	defaultBurnQuality = "18"
	defaultBurnPreset  = "medium"
)

// normalizeNonBlank 归一化配置字符串：去除首尾空白，空白值回退为默认值。
// 空白的质量值/预设（前端表单清空，或 YAML/API 直接写入 ""）会生成
// "-crf """/"-preset """ 这类无效 FFmpeg 参数导致烧录失败，在这里统一兜底。
func normalizeNonBlank(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

// NewBurnSubtitlesStage 创建弹幕字幕烧录阶段工厂
func NewBurnSubtitlesStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	codec := normalizeNonBlank(config.GetStringOption(pipeline.OptionCodec, defaultBurnCodec), defaultBurnCodec)
	crf := normalizeNonBlank(config.GetStringOption(pipeline.OptionCrf, defaultBurnQuality), defaultBurnQuality)
	preset := normalizeNonBlank(config.GetStringOption(pipeline.OptionPreset, defaultBurnPreset), defaultBurnPreset)
	deleteAss := config.GetBoolOption(pipeline.OptionBurnDeleteAss, false)
	deleteSource := config.GetBoolOption(pipeline.OptionBurnDeleteSource, false)
	return &BurnSubtitlesStage{
		config:       config,
		codec:        codec,
		crf:          crf,
		preset:       preset,
		deleteAss:    deleteAss,
		deleteSource: deleteSource,
	}, nil
}

func (s *BurnSubtitlesStage) Name() string {
	return pipeline.StageNameBurnSubtitles
}

func (s *BurnSubtitlesStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有输入文件"
		return input, nil
	}

	ffmpegPath := ctx.FFmpegPath
	if ffmpegPath == "" {
		// FFmpeg 可能仍在后台异步下载（首次启动场景），等待其就绪后再查找，
		// 避免短直播录制结束时后处理任务因 FFmpeg 未下载完成而失败。
		// 等待被中断（如 pipeline ctx 取消）时立即退出，不再启动烧录子进程
		if waitErr := tools.WaitFFmpegAsyncInitDone(ctx.Ctx, nil); waitErr != nil {
			s.logs = fmt.Sprintf("等待 FFmpeg 就绪被中断: %s", waitErr.Error())
			return nil, waitErr
		}
		var err error
		ffmpegPath, err = utils.GetFFmpegPath(ctx.Ctx)
		if err != nil {
			s.logs = fmt.Sprintf("ffmpeg 不可用: %s", err.Error())
			return nil, fmt.Errorf("ffmpeg not available: %w", err)
		}
	}

	var output []pipeline.FileInfo

	for _, file := range input {
		// 只处理视频文件
		if file.Type != pipeline.FileTypeVideo {
			output = append(output, file)
			continue
		}

		// 跳过已标记待删除的文件（如 ConvertMp4 阶段标记的原始 FLV），
		// 但仍将其透传到 output，保证 Executor 的延迟删除机制正常工作。
		if file.Deletable {
			output = append(output, file)
			continue
		}

		// 检查文件是否存在
		if _, err := os.Stat(file.Path); os.IsNotExist(err) {
			s.logs += fmt.Sprintf("文件不存在: %s\n", file.Path)
			continue
		}

		// 查找同名 .ass 文件
		assPath := s.findAssFile(file.Path)
		if assPath == "" {
			// 无 ASS 文件，跳过，原样传递
			s.logs += fmt.Sprintf("未找到 ASS 字幕文件，跳过烧录: %s\n", filepath.Base(file.Path))
			output = append(output, file)
			continue
		}

		// 确定输出文件名（.mkv）
		ext := strings.ToLower(filepath.Ext(file.Path))
		base := strings.TrimSuffix(file.Path, ext)
		outputPath := base + ".mkv"

		// 如果输出路径与输入路径相同（已经是 .mkv），使用临时名
		if outputPath == file.Path {
			outputPath = base + ".burned.mkv"
		}

		// 临时文件路径
		dir := filepath.Dir(outputPath)
		baseName := filepath.Base(outputPath)
		tempFile := filepath.Join(dir, ".burning_"+baseName)

		ctx.Logger.Infof("烧录字幕: %s + %s -> %s", file.Path, filepath.Base(assPath), outputPath)

		// 获取视频时长用于进度计算
		duration := s.getVideoDuration(ctx.Ctx, ffmpegPath, file.Path)

		// 构建 FFmpeg 命令
		escapedAssPath := escapeAssPath(assPath)
		vfArg := fmt.Sprintf("ass=%s", escapedAssPath)

		args := []string{
			"-i", file.Path,
			"-vf", vfArg,
		}
		// 按编码器生成质量/预设参数（NVENC 与软编码参数不同）
		args = append(args, buildVideoEncodeArgs(s.codec, s.crf, s.preset)...)
		args = append(args,
			"-c:a", "copy",
			"-y",
			"-progress", "pipe:1",
			tempFile,
		)

		ctx.Logger.Infof("烧录字幕 FFmpeg 命令: %s %s", ffmpegPath, strings.Join(args, " "))
		ctx.Logger.Infof("烧录字幕 ASS 路径: %s (原始: %s)", escapedAssPath, assPath)

		cmdStr := fmt.Sprintf("%s %s", ffmpegPath, strings.Join(args, " "))
		s.commands = append(s.commands, cmdStr)

		cmd := exec.CommandContext(ctx.Ctx, ffmpegPath, args...)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			s.logs += fmt.Sprintf("创建输出管道失败: %s\n", err.Error())
			return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			s.logs += fmt.Sprintf("创建错误管道失败: %s\n", err.Error())
			return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
		}

		if err := cmd.Start(); err != nil {
			s.logs += fmt.Sprintf("启动 ffmpeg 失败: %s\n", err.Error())
			return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
		}

		// 解析进度（后台）
		bilisentry.GoWithContext(ctx.Ctx, func(goCtx context.Context) {
			s.parseProgress(goCtx, stdout, duration)
		})

		// 读取 stderr 用于错误诊断
		stderrBytes, _ := io.ReadAll(stderr)

		// 等待命令完成
		if err := cmd.Wait(); err != nil {
			os.Remove(tempFile)
			stderrOutput := string(stderrBytes)
			s.logs += fmt.Sprintf("ffmpeg 烧录失败: %s - %s\n", file.Path, err.Error())
			s.logs += fmt.Sprintf("ffmpeg stderr: %s\n", stderrOutput)
			ctx.Logger.Errorf("ffmpeg stderr: %s", stderrOutput)
			return nil, fmt.Errorf("ffmpeg burn subtitles failed for %s: %w\nstderr: %s", file.Path, err, stderrOutput)
		}

		// 检查临时文件
		if _, err := os.Stat(tempFile); os.IsNotExist(err) {
			s.logs += fmt.Sprintf("临时文件未创建: %s\n", tempFile)
			return nil, fmt.Errorf("temp file was not created: %s", tempFile)
		}

		// 重命名临时文件
		if err := os.Rename(tempFile, outputPath); err != nil {
			os.Remove(tempFile)
			return nil, fmt.Errorf("failed to rename temp file: %w", err)
		}

		// 添加输出文件
		output = append(output, pipeline.FileInfo{
			Path:       outputPath,
			Type:       pipeline.FileTypeVideo,
			SourcePath: file.Path,
		})

		// 可选：标记 ASS 文件为可删除（由 Executor 在管道全部成功后统一删除）
		if s.deleteAss {
			output = append(output, pipeline.FileInfo{
				Path:      assPath,
				Type:      pipeline.FileTypeOther,
				Deletable: true,
			})
			s.logs += fmt.Sprintf("已标记 ASS 文件待删除: %s\n", assPath)
			ctx.Logger.Infof("已标记 ASS 文件待删除: %s", assPath)
		}

		// 可选：标记源视频文件为可删除（由 Executor 在管道全部成功后统一删除）
		if s.deleteSource && file.Path != outputPath {
			file.Deletable = true
			s.logs += fmt.Sprintf("已标记源视频文件待删除: %s\n", file.Path)
			ctx.Logger.Infof("已标记源视频文件待删除: %s", file.Path)
		}
		output = append(output, file)

		s.logs += fmt.Sprintf("字幕烧录完成: %s -> %s\n", filepath.Base(file.Path), filepath.Base(outputPath))
		ctx.Logger.Infof("字幕烧录完成: %s", outputPath)
	}

	return output, nil
}

// findAssFile 查找与视频文件同名的 .ass 字幕文件
func (s *BurnSubtitlesStage) findAssFile(videoPath string) string {
	ext := strings.ToLower(filepath.Ext(videoPath))
	base := strings.TrimSuffix(videoPath, ext)
	assPath := base + ".ass"
	if _, err := os.Stat(assPath); err == nil {
		return assPath
	}
	return ""
}

// escapeAssPath 转义 ASS 文件路径，用于 FFmpeg 的 ass 滤镜
// FFmpeg ass 滤镜语法: ass='path'
// 在单引号内，只需转义 \ 和 ' 两个字符
// Windows 路径: \ 替换为 /（跨平台兼容）
func escapeAssPath(path string) string {
	// 1. Windows 反斜杠 → 正斜杠
	escaped := strings.ReplaceAll(path, "\\", "/")
	// 2. 转义单引号（在单引号字符串内必须转义）
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	// 3. 用单引号包裹，保护所有特殊字符: ^ [] = ; , # ! 空格 Emoji 等
	return "'" + escaped + "'"
}

// getVideoDuration 获取视频时长（秒）
func (s *BurnSubtitlesStage) getVideoDuration(ctx context.Context, ffmpegPath, inputFile string) float64 {
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-i", inputFile,
		"-hide_banner",
	)

	output, _ := cmd.CombinedOutput()

	// 解析 Duration: HH:MM:SS.ms
	re := regexp.MustCompile(`Duration: (\d{2}):(\d{2}):(\d{2})\.(\d{2})`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 5 {
		return 0
	}

	hours, _ := strconv.ParseFloat(matches[1], 64)
	minutes, _ := strconv.ParseFloat(matches[2], 64)
	seconds, _ := strconv.ParseFloat(matches[3], 64)
	ms, _ := strconv.ParseFloat(matches[4], 64)

	return hours*3600 + minutes*60 + seconds + ms/100
}

// parseProgress 解析 ffmpeg 进度输出
func (s *BurnSubtitlesStage) parseProgress(ctx context.Context, stdout io.Reader, totalDuration float64) {
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
		if len(matches) >= 2 && totalDuration > 0 {
			timeUs, _ := strconv.ParseFloat(matches[1], 64)
			currentTime := timeUs / 1000000
			progress := (currentTime / totalDuration) * 100
			_ = progress // 可以通过回调上报进度
		}
	}
}

func (s *BurnSubtitlesStage) GetCommands() []string {
	return s.commands
}

func (s *BurnSubtitlesStage) GetLogs() string {
	return s.logs
}
