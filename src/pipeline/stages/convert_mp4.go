package stages

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/bililive-go/bililive-go/src/tools"
	"github.com/sirupsen/logrus"
)

// ConvertMp4Stage MP4 转换阶段
type ConvertMp4Stage struct {
	config       pipeline.StageConfig
	deleteSource bool
	commands     []string
	logs         string
	logger       *livelogger.LiveLogger // 当前任务的 logger，在启动进度协程前设置，执行期间不为 nil 且保持不变。
}

// NewConvertMp4Stage 创建 MP4 转换阶段工厂
func NewConvertMp4Stage(config pipeline.StageConfig) (pipeline.Stage, error) {
	deleteSource := config.GetBoolOption(pipeline.OptionDeleteSource, false)
	return &ConvertMp4Stage{
		config:       config,
		deleteSource: deleteSource,
	}, nil
}

func (s *ConvertMp4Stage) Name() string {
	return pipeline.StageNameConvertMp4
}

func (s *ConvertMp4Stage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有输入文件"
		return input, nil
	}
	s.logger = ctx.Logger
	if s.logger == nil {
		// 未提供任务 logger 时回退到全局 logger，确保告警路径不会因空指针中断转换。
		s.logger = &livelogger.LiveLogger{Entry: logrus.NewEntry(logrus.StandardLogger())}
	}

	ffmpegPath := ctx.FFmpegPath
	if ffmpegPath == "" {
		// FFmpeg 可能仍在后台异步下载（首次启动场景），等待其就绪后再查找，
		// 避免短直播录制结束时后处理任务因 FFmpeg 未下载完成而失败。
		// 等待被中断（如 pipeline ctx 取消）时立即退出，不再启动转码子进程
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

		// 检查文件是否存在
		if _, err := os.Stat(file.Path); os.IsNotExist(err) {
			s.logs += fmt.Sprintf("文件不存在: %s\n", file.Path)
			continue
		}

		// 如果已经是 MP4 文件，跳过
		ext := strings.ToLower(filepath.Ext(file.Path))
		if ext == ".mp4" {
			s.logs += fmt.Sprintf("文件 %s 已经是 MP4 格式，跳过转换。\n", filepath.Base(file.Path))
			output = append(output, file)
			continue
		}

		// 确定输出文件名
		base := strings.TrimSuffix(file.Path, ext)
		outputPath := base + ".mp4"

		// 临时文件路径
		dir := filepath.Dir(outputPath)
		baseName := filepath.Base(outputPath)
		tempFile := filepath.Join(dir, ".converting_"+baseName)

		s.logger.Infof("转换 MP4: %s -> %s", file.Path, outputPath)

		// 获取视频时长用于进度计算，并找出缺少 MP4 封装必需参数的音轨。
		inputInfo := s.probeInput(ctx.Ctx, ffmpegPath, file.Path)

		// 构建 ffmpeg 命令
		args := []string{
			"-hide_banner",
			"-nostdin",
			"-nostats",
			"-loglevel", "warning",
			"-y",
		}
		args = append(args, getFFmpegInputArgs(file.Path)...)
		args = append(args,
			// 保留首路视频和全部音轨；允许纯音频或无音频录制。
			"-map", "0:v:0?",
			"-map", "0:a?",
		)
		// 探测与转换使用相同的输入参数，流索引一致；负向映射只排除确认无效的音轨。
		// 探测窗口之后才新建的流不会被 FFmpeg 映射，无需处理。
		for _, stream := range inputInfo.invalidAudio {
			args = append(args, "-map", fmt.Sprintf("-0:%d", stream.index))
			s.logs += fmt.Sprintf("已排除缺少封装参数的音轨 0:%d (Audio: %s)\n", stream.index, stream.description)
			s.logger.Warnf("已排除缺少封装参数的音轨 0:%d (Audio: %s): %s", stream.index, stream.description, file.Path)
		}
		args = append(args,
			// 字幕、数据流和附件不主动映射，避免不兼容 MP4 的流导致封装失败。
			"-map_metadata", "0",
			"-map_chapters", "0",
			// 流复制不修复编码数据。复杂的编码参数变化应先由 fix_flv 阶段修复、分段。
			"-c", "copy",
			// 保留解复用器的时间基，减少可变帧率流复制时的时间戳舍入问题。
			"-copytb", "1",
			// 整体平移起始时间戳，保留音视频相对偏移和断网期间的时间空档。
			"-avoid_negative_ts", "make_zero",
			"-movflags", "+faststart",
			"-progress", "pipe:1",
			"-f", "mp4",
			tempFile,
		)

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
			s.parseProgress(goCtx, stdout, inputInfo.duration)
		})

		// 持续排空 stderr，诊断只保存有限内容，避免坏包警告撑大内存和任务日志。
		diagnostics, readErr := readFFmpegDiagnostics(stderr)
		if readErr != nil && ctx.Ctx.Err() == nil && !errors.Is(readErr, os.ErrClosed) {
			s.logger.WithError(readErr).Warn("读取 FFmpeg 诊断失败")
		}

		// 等待命令完成
		if err := cmd.Wait(); err != nil {
			os.Remove(tempFile)
			s.logs += fmt.Sprintf("ffmpeg 转换失败: %s - %s\n", file.Path, err.Error())
			s.logs += fmt.Sprintf("ffmpeg stderr: %s\n", diagnostics)
			return nil, fmt.Errorf("ffmpeg conversion failed for %s: %w", file.Path, err)
		}

		// FFmpeg 可能跳过坏包或调整时间戳后正常退出，保留诊断以便排查录制损坏。
		if diagnostics != "" {
			s.logs += fmt.Sprintf("ffmpeg 转换诊断（%s）:\n%s\n", file.Path, diagnostics)
			s.logger.Warnf("MP4 转换存在警告: %s\n%s", file.Path, diagnostics)
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

		// 标记原始文件为可删除（由 Executor 在管道全部成功后统一删除）
		if s.deleteSource && file.Path != outputPath {
			if len(inputInfo.invalidAudio) > 0 {
				// 缺少参数的音轨也可能是探测窗口后才出现的真实音频，MP4 不能完全替代原始文件。
				s.logs += fmt.Sprintf("转换时排除了音轨，保留原始文件: %s\n", file.Path)
				s.logger.Warnf("转换时排除了音轨，保留原始文件: %s", file.Path)
			} else {
				file.Deletable = true
				s.logs += fmt.Sprintf("已标记原始文件待删除: %s\n", file.Path)
				s.logger.Infof("已标记原始文件待删除: %s", file.Path)
			}
		}
		output = append(output, file)

		s.logs += fmt.Sprintf("转换完成: %s -> %s\n", filepath.Base(file.Path), filepath.Base(outputPath))
		s.logger.Infof("MP4 转换完成: %s", outputPath)
	}

	return output, nil
}

// ffmpegInputInfo 正式转换前的输入探测结果。
type ffmpegInputInfo struct {
	duration float64
	// invalidAudio 记录确认缺少 MP4 封装必需参数的音轨。
	invalidAudio []ffmpegInvalidStream
}

type ffmpegInvalidStream struct {
	index       int
	description string
}

var (
	ffmpegDurationPattern = regexp.MustCompile(`Duration: (\d{2}):(\d{2}):(\d{2})\.(\d{2})`)
	// 只匹配输入 0 的音轨信息行，兼容 [0x101] 流 ID 和 (eng) 语言标记。
	ffmpegAudioStreamPattern = regexp.MustCompile(`^\s*Stream #0:(\d+)(?:\[[^\]]*\])?(?:\([^)]*\))?: Audio: (.*)$`)
	ffmpegSampleRatePattern  = regexp.MustCompile(`(?:^|[ ,])[1-9][0-9]* Hz(?:,|$)`)
	ffmpegNoChannelsPattern  = regexp.MustCompile(`(?:^|, )0 channels(?:,|$)`)
)

// probeInput 使用与正式转换相同的输入参数探测时长和音轨参数，保证流索引一致。
// 探测失败时返回空结果：时长未知只影响进度计算，音轨全部保留。
func (s *ConvertMp4Stage) probeInput(ctx context.Context, ffmpegPath, inputFile string) ffmpegInputInfo {
	args := append([]string{"-hide_banner", "-nostdin"}, getFFmpegInputArgs(inputFile)...)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ffmpegInputInfo{}
	}
	if err := cmd.Start(); err != nil {
		return ffmpegInputInfo{}
	}
	info := parseFFmpegInputInfo(stderr)
	// 只给输入、没有输出文件时 FFmpeg 必定以非零状态退出，只需回收进程。
	_ = cmd.Wait()
	return info
}

// parseFFmpegInputInfo 逐行解析 ffmpeg -i 的输入信息，不保留其余输出。
// 不符合输入 0 音轨格式的行不参与判定；损坏文件探测时可能输出大量告警，超长行直接跳过。
func parseFFmpegInputInfo(input io.Reader) ffmpegInputInfo {
	var info ffmpegInputInfo
	durationFound := false
	seen := make(map[int]bool)
	reader := bufio.NewReaderSize(input, 4096)
	skipping := false
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			skipping = true
			continue
		}
		if !skipping {
			text := strings.TrimRight(string(line), "\r\n")
			if !durationFound {
				if matches := ffmpegDurationPattern.FindStringSubmatch(text); matches != nil {
					hours, _ := strconv.ParseFloat(matches[1], 64)
					minutes, _ := strconv.ParseFloat(matches[2], 64)
					seconds, _ := strconv.ParseFloat(matches[3], 64)
					centiseconds, _ := strconv.ParseFloat(matches[4], 64)
					info.duration = hours*3600 + minutes*60 + seconds + centiseconds/100
					durationFound = true
				}
			}
			if matches := ffmpegAudioStreamPattern.FindStringSubmatch(text); matches != nil {
				index, convErr := strconv.Atoi(matches[1])
				description := strings.TrimSpace(matches[2])
				if convErr == nil && !seen[index] {
					seen[index] = true
					if isInvalidMP4AudioStream(description) {
						info.invalidAudio = append(info.invalidAudio, ffmpegInvalidStream{index: index, description: description})
					}
				}
			}
		}
		skipping = false
		if err != nil {
			return info
		}
	}
}

// isInvalidMP4AudioStream 判断音轨是否确认缺少 MP4 封装必需的编码、采样率或声道。
// 只缺采样格式的音轨可能只是数据晚于探测窗口出现，不能据此排除。
func isInvalidMP4AudioStream(description string) bool {
	codec := description
	if end := strings.IndexAny(codec, " ,"); end >= 0 {
		codec = codec[:end]
	}
	return codec == "none" ||
		!ffmpegSampleRatePattern.MatchString(description) ||
		ffmpegNoChannelsPattern.MatchString(description)
}

// parseProgress 解析 ffmpeg 进度输出
func (s *ConvertMp4Stage) parseProgress(ctx context.Context, stdout io.Reader, totalDuration float64) {
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
	// 取消或 Wait 关闭管道属于正常收尾，其余读取错误只记录日志，不影响转换结果。
	if err := scanner.Err(); err != nil && ctx.Err() == nil && !errors.Is(err, os.ErrClosed) {
		// 保留 LiveLogger 自带的 context，确保直播间缓冲区和任务字段都能收到告警。
		s.logger.WithError(err).Warn("读取 FFmpeg 进度失败")
	}
}

func (s *ConvertMp4Stage) GetCommands() []string {
	return s.commands
}

func (s *ConvertMp4Stage) GetLogs() string {
	return s.logs
}

// getFFmpegInputArgs 构建 FFmpeg 输入容错参数；输入探测和正式转换必须使用相同参数。
func getFFmpegInputArgs(inputFile string) []string {
	return []string{
		// 只补缺失的 PTS，并丢弃被解复用器标记为损坏的包，不重写已有时间戳。
		"-fflags", "+genpts+discardcorrupt",
		// 允许探测阶段的解码继续处理错误；流复制本身不解码，不能据此修复坏帧。
		"-err_detect", "ignore_err",
		// 扩大探测上限，容忍录制开头缺少关键帧、音轨较晚出现等情况。
		"-probesize", "32M",
		// 输入探测最多分析 10 秒媒体时间（单位微秒），用于识别流和编码参数，并非进程超时。
		"-analyzeduration", "10000000",
		"-i", inputFile,
	}
}
