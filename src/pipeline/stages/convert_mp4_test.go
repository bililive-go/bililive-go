package stages

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

func TestConvertMp4Stage_SkippedInputs(t *testing.T) {
	dir := t.TempDir()
	mp4Path := filepath.Join(dir, "已有视频.mp4")
	require.NoError(t, os.WriteFile(mp4Path, []byte("已有文件"), 0644))
	cases := []struct {
		name  string
		input []pipeline.FileInfo
		want  []pipeline.FileInfo
	}{
		{name: "没有输入"},
		{
			name:  "非视频文件",
			input: []pipeline.FileInfo{{Path: "封面.jpg", Type: pipeline.FileTypeCover}},
			want:  []pipeline.FileInfo{{Path: "封面.jpg", Type: pipeline.FileTypeCover}},
		},
		{
			name:  "已经是 MP4",
			input: []pipeline.FileInfo{{Path: mp4Path, Type: pipeline.FileTypeVideo}},
			want:  []pipeline.FileInfo{{Path: mp4Path, Type: pipeline.FileTypeVideo}},
		},
		{
			name:  "视频文件不存在",
			input: []pipeline.FileInfo{{Path: filepath.Join(dir, "不存在.flv"), Type: pipeline.FileTypeVideo}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stage, err := NewConvertMp4Stage(pipeline.StageConfig{
				Options: map[string]any{pipeline.OptionDeleteSource: true},
			})
			require.NoError(t, err)
			// 跳过的输入不应启动进程，也不需要触发工具下载。
			output, err := stage.Execute(&pipeline.PipelineContext{
				Ctx: context.Background(), FFmpegPath: "不应执行的 FFmpeg",
			}, tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.want, output)
			require.Empty(t, stage.(*ConvertMp4Stage).GetCommands())
		})
	}
}

func TestConvertMp4Stage_ParseProgress(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	type loggerContextKey struct{}
	entryCtx := context.WithValue(t.Context(), loggerContextKey{}, "直播间上下文")
	liveLogger := &livelogger.LiveLogger{Entry: logrus.NewEntry(logger).WithContext(entryCtx).WithFields(logrus.Fields{
		"room": "测试直播间", "task_id": "测试任务",
	})}
	t.Cleanup(hook.Reset)
	readErr := errors.New("模拟进度读取失败")
	cancelledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	cases := []struct {
		name     string
		ctx      context.Context
		reader   io.Reader
		duration float64
		wantErr  error
	}{
		{
			name: "正常结束", ctx: t.Context(), duration: 3,
			reader: strings.NewReader("out_time_us=1000000\nprogress=continue\nout_time_us=3000000\nprogress=end\n"),
		},
		{
			name: "时长未知", ctx: t.Context(),
			reader: strings.NewReader("out_time_us=1000000\nprogress=end\n"),
		},
		{
			name: "读取失败", ctx: t.Context(), duration: 3, wantErr: readErr,
			reader: io.MultiReader(strings.NewReader("out_time_us=1000000\n"), iotest.ErrReader(readErr)),
		},
		{
			name: "进度行过长", ctx: t.Context(), duration: 3, wantErr: bufio.ErrTooLong,
			reader: strings.NewReader(strings.Repeat("x", bufio.MaxScanTokenSize+1) + "\n"),
		},
		{
			name: "取消读取", ctx: cancelledCtx, duration: 3,
			reader: strings.NewReader("out_time_us=1000000\n"),
		},
		{
			name: "取消时读取失败", ctx: cancelledCtx, duration: 3,
			reader: iotest.ErrReader(readErr),
		},
		{
			name: "进度管道已关闭", ctx: t.Context(), duration: 3,
			reader: iotest.ErrReader(os.ErrClosed),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hook.Reset()
			stage := &ConvertMp4Stage{logger: liveLogger}
			stage.parseProgress(tc.ctx, tc.reader, tc.duration)
			entries := hook.AllEntries()
			if tc.wantErr != nil {
				require.Len(t, entries, 1)
				require.Equal(t, logrus.WarnLevel, entries[0].Level)
				require.Equal(t, "读取 FFmpeg 进度失败", entries[0].Message)
				require.Equal(t, "测试直播间", entries[0].Data["room"])
				require.Equal(t, "测试任务", entries[0].Data["task_id"])
				require.Same(t, entryCtx, entries[0].Context)
				err, ok := entries[0].Data[logrus.ErrorKey].(error)
				require.True(t, ok)
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.Empty(t, entries)
			}
		})
	}
}

func TestParseFFmpegInputInfo(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  ffmpegInputInfo
	}{
		{
			// 真实音频晚于探测窗口出现：参数完整但缺少采样格式，必须保留。
			name: "保留缺少采样格式的音轨",
			input: `[in#0/flv @ 0x77cb020000] Could not find codec parameters for stream 1 (Audio: aac, 44100 Hz, stereo, 69 kb/s): unspecified sample format
Consider increasing the value for the 'analyzeduration' (10000000) and 'probesize' (32000000) options
Input #0, flv, from 'late-audio.flv':
  Metadata:
    encoder         : Lavf63.1.102
  Duration: 00:00:20.00, start: 0.067000, bitrate: N/A
  Stream #0:0: Video: h264 (High), yuv420p(progressive), 160x96 [SAR 1:1 DAR 5:3], 30 fps, 30 tbr, 1k tbn, start 0.067000
  Stream #0:1: Audio: aac, 44100 Hz, stereo, 69 kb/s, start 0.067000
At least one output file must be specified
`,
			want: ffmpegInputInfo{duration: 20},
		},
		{
			name: "排除丢字节后缺少声道的音轨",
			input: `[in#0/flv @ 0x758703c000] Could not find codec parameters for stream 2 (Video: none ([44][21]Ez / 0x7A45152C), none): unknown codec
[in#0/flv @ 0x758703c000] Could not find codec parameters for stream 3 (Audio: none (RQ[164][7] / 0x7A45152), 0 channels, 69 kb/s): unknown codec
Input #0, flv, from 'invalid-audio.flv':
  Duration: 00:00:03.00, start: 0.000000, bitrate: N/A
  Stream #0:0: Video: h264 (High), yuv420p(progressive), 160x96 [SAR 1:1 DAR 5:3], 30 fps, 30 tbr, 1k tbn, start 0.067000
  Stream #0:1: Audio: aac (LC), 44100 Hz, mono, fltp, 69 kb/s, start 0.044000
  Stream #0:2: Video: none ([44][21]Ez / 0x7A45152C), none, 30 fps, 1k tbr, 1k tbn, start 0.900000
  Stream #0:3: Audio: none (RQ[164][7] / 0x7A45152), 0 channels, 69 kb/s, start 0.903000
`,
			want: ffmpegInputInfo{duration: 3, invalidAudio: []ffmpegInvalidStream{
				{index: 3, description: "none (RQ[164][7] / 0x7A45152), 0 channels, 69 kb/s, start 0.903000"},
			}},
		},
		{
			name:  "排除未知编码的音轨",
			input: "  Duration: 00:00:03.00, start: 0.000000, bitrate: N/A\n  Stream #0:1: Audio: aac (LC), 44100 Hz, mono, fltp, 69 kb/s\n  Stream #0:2: Audio: none ([15][0][0][0] / 0x000F), 44100 Hz, mono, 69 kb/s\n",
			want: ffmpegInputInfo{duration: 3, invalidAudio: []ffmpegInvalidStream{
				{index: 2, description: "none ([15][0][0][0] / 0x000F), 44100 Hz, mono, 69 kb/s"},
			}},
		},
		{
			name: "流 ID 和语言标记",
			input: `  Duration: 01:02:03.45, start: 1.400000, bitrate: 2000 kb/s
  Program 1
    Metadata:
      service_name    : Service01
  Stream #0:0[0x100]: Video: h264 (High) ([27][0][0][0] / 0x001B), yuv420p(progressive), 1920x1080, 30 fps, 30 tbr, 90k tbn
  Stream #0:1[0x101](eng): Audio: aac (LC) ([15][0][0][0] / 0x000F), 48000 Hz, stereo, fltp, 130 kb/s
  Stream #0:2[0x102](zho): Audio: aac ([15][0][0][0] / 0x000F), 0 channels
  Stream #0:3(jpn): Audio: mp3, 0 channels, fltp
`,
			want: ffmpegInputInfo{duration: 3723.45, invalidAudio: []ffmpegInvalidStream{
				{index: 2, description: "aac ([15][0][0][0] / 0x000F), 0 channels"},
				{index: 3, description: "mp3, 0 channels, fltp"},
			}},
		},
		{
			name:  "Windows 换行",
			input: "  Duration: 00:00:03.00, start: 0.000000, bitrate: N/A\r\n  Stream #0:1: Audio: none, 0 channels\r\n",
			want:  ffmpegInputInfo{duration: 3, invalidAudio: []ffmpegInvalidStream{{index: 1, description: "none, 0 channels"}}},
		},
		{
			// 元数据、其他输入、探测失败提示和流组中重复列出的流都不能重复或误判。
			name: "忽略非输入 0 的流信息",
			input: `[flv @ 0x1234] Could not find codec parameters for stream 2 (Audio: none, 0 channels): unknown codec
  Metadata:
    title           : Stream #0:5: Audio: none
  Duration: N/A, start: 0.000000, bitrate: N/A
  Stream #0:1: Audio: none, 0 channels
  Stream #0:1: Audio: none, 0 channels
  Stream #1:0: Audio: none, 0 channels
`,
			want: ffmpegInputInfo{invalidAudio: []ffmpegInvalidStream{{index: 1, description: "none, 0 channels"}}},
		},
		{
			name:  "跳过超长行",
			input: strings.Repeat("损坏", 10000) + "\n  Duration: 00:00:03.00, start: 0.000000, bitrate: N/A\n  Stream #0:1: Audio: none, 0 channels",
			want:  ffmpegInputInfo{duration: 3, invalidAudio: []ffmpegInvalidStream{{index: 1, description: "none, 0 channels"}}},
		},
		{name: "没有输出", input: "", want: ffmpegInputInfo{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := strings.NewReader(tc.input)
			require.Equal(t, tc.want, parseFFmpegInputInfo(reader))
			require.Zero(t, reader.Len(), "必须排空输出，避免 FFmpeg 因管道堵塞挂起")
		})
	}
}

func TestMP4TestTools_VersionRequirements(t *testing.T) {
	cases := []struct {
		version string
		major   int
		minor   int
		vfr     bool
		mixed   bool
	}{
		{"ffmpeg version 4.4.5", 4, 4, false, false},
		{"ffmpeg version 5", 5, 0, false, false},
		{"ffmpeg version 5.0.3", 5, 0, false, false},
		{"ffmpeg version 5.1.7", 5, 1, true, false},
		{"ffmpeg version 6.1.1-3ubuntu5", 6, 1, true, false},
		{"ffmpeg version n7.0-2-g123456", 7, 0, true, false},
		{"ffmpeg version 8.0", 8, 0, true, true},
		{"ffmpeg version 9.0.2", 9, 0, true, true},
		{"ffmpeg version N-123456-g123456", 0, 0, false, false},
		{"ffmpeg version git-2020-01-01-123456", 0, 0, false, false},
		{"未知版本", 0, 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			major, minor := parseMP4TestFFmpegVersion(tc.version)
			tools := mp4TestTools{major: major, minor: minor}
			require.Equal(t, tc.major, tools.major)
			require.Equal(t, tc.minor, tools.minor)
			require.Equal(t, tc.vfr, tools.supportsVersion(5, 1))
			require.Equal(t, tc.mixed, tools.supportsVersion(8, 0))
		})
	}
}

// 集成测试依赖 PATH 中的 ffmpeg、ffprobe 和 libx264、AAC 编码器。
// 样本在临时目录中生成，无需下载录播文件；go test -short 可跳过这些测试。
func TestConvertMp4Stage_FFmpeg(t *testing.T) {
	tools := findMP4TestTools(t)
	seedDir := t.TempDir()
	av := tools.generate(t, filepath.Join(seedDir, "av.flv"), true, true, "160x96", false)
	video := tools.generate(t, filepath.Join(seedDir, "video.flv"), true, false, "160x96", false)
	audio := tools.generate(t, filepath.Join(seedDir, "audio.flv"), false, true, "", false)

	cases := []struct {
		name            string
		fileName        string
		data            []byte
		deleteSource    bool
		comparePackets  bool
		decode          bool
		wantDiagnostics bool
	}{
		{"音视频与中文空格路径", "直播 录制.flv", av, true, true, true, false},
		{"只有视频", "video.flv", video, false, true, true, false},
		{"只有音频", "audio.flv", audio, false, true, true, false},
		{"起始时间戳偏移", "offset.flv", shiftMP4TestFLVTimestamps(t, av, 0, 12000), false, true, true, false},
		{"保留断网时间空档", "gap.flv", shiftMP4TestFLVTimestamps(t, av, 1500, 20000), false, true, true, false},
		{"丢失视频包", "lost.flv", dropMP4TestFLVPackets(t, av), false, true, false, false},
		{"尾部截断", "truncated.flv", av[:len(av)-80], false, false, false, true},
		{"标签内部字节丢失", "broken.flv", damageMP4TestFLVTag(t, av), false, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inputPath := filepath.Join(t.TempDir(), tc.fileName)
			require.NoError(t, os.WriteFile(inputPath, tc.data, 0644))
			outputPath, stage := tools.convert(t, inputPath, tc.deleteSource)
			before, after := tools.probe(t, inputPath), tools.probe(t, outputPath)
			if tc.fileName == "直播 录制.flv" {
				require.NotContains(t, stage.GetLogs(), "ffmpeg 转换诊断", "正常录制不应产生转换告警")
				require.NotContains(t, stage.GetLogs(), "已排除缺少封装参数的音轨", "正常录制不应排除音轨")
			}
			assertMP4TestOutput(t, outputPath, after)
			assertMP4TestStreams(t, before, after)
			if tc.comparePackets {
				assertMP4TestPacketCopy(t, before, after)
			}
			if tc.decode {
				tools.decode(t, outputPath)
			}
			if tc.wantDiagnostics {
				require.Contains(t, stage.GetLogs(), "ffmpeg 转换诊断")
				// 损坏后仍应读到接近录制末尾的数据，而不是只封装开头。
				checked := make(map[string]bool)
				for _, stream := range after.Streams {
					// 损坏的 FLV 在重新同步时可能出现额外短音轨，检查主音视频的恢复进度。
					if checked[stream.CodecType] {
						continue
					}
					checked[stream.CodecType] = true
					packets := mp4TestPacketsForStream(after, stream.Index)
					require.Greater(t, mp4TestTimestamp(t, packets[len(packets)-1].PTS), 2.5)
				}
			}
		})
	}

	t.Run("恢复丢字节后的主音视频", func(t *testing.T) {
		var damaged []byte
		for _, tag := range parseMP4TestFLVTags(t, av) {
			if tag.kind == 9 && tag.timestamp == 900 {
				// 删除首个 900 ms 视频标签的前 10 字节载荷，保留声明长度和 PreviousTagSize。
				require.Greater(t, tag.end-tag.start, 25)
				damaged = append([]byte(nil), av[:tag.start+11]...)
				damaged = append(damaged, av[tag.start+21:]...)
				break
			}
		}
		require.NotEmpty(t, damaged, "样本必须包含被破坏的 900 ms 视频标签")
		inputPath := filepath.Join(t.TempDir(), "invalid-audio.flv")
		require.NoError(t, os.WriteFile(inputPath, damaged, 0644))
		before := tools.probe(t, inputPath)
		invalidAudio := 0
		for _, stream := range before.Streams {
			// 编码名缺失即为无效，不能要求采样率和声道也同时缺失。
			if stream.CodecType == "audio" && (stream.CodecName == "" || stream.SampleRate == "0" || stream.SampleRate == "" || stream.Channels == 0) {
				invalidAudio++
			}
		}
		// 解复用后的伪音轨取决于 FFmpeg 和样本编码器版本，不能作为损坏恢复的前提。
		// 另一个未知编码样本固定生成无效音轨，独立验证过滤规则。
		t.Logf("丢字节样本识别出 %d 路无效音轨，继续检查主音视频恢复", invalidAudio)
		outputPath, _ := tools.convert(t, inputPath, false)
		after := tools.probe(t, outputPath)
		require.Len(t, after.Streams, 2, "应只保留主视频和真实音轨")
		assertMP4TestOutput(t, outputPath, after)
		for _, stream := range after.Streams {
			packets := mp4TestPacketsForStream(after, stream.Index)
			require.Greater(t, len(packets), 80, "损坏后仍应恢复主音视频的大部分数据")
			require.Greater(t, mp4TestTimestamp(t, packets[len(packets)-1].PTS), 3.0, "主音视频都应恢复至录制末尾")
			if stream.CodecType == "audio" {
				require.Equal(t, "aac", stream.CodecName)
				require.Equal(t, "44100", stream.SampleRate)
				require.Positive(t, stream.Channels)
			}
			// 每个输出包都应来自同类输入流，确认恢复过程仍使用流复制。
			hashes := make(map[string]bool)
			for _, sourceStream := range before.Streams {
				if sourceStream.CodecType == stream.CodecType && sourceStream.CodecName == stream.CodecName {
					for _, packet := range mp4TestPacketsForStream(before, sourceStream.Index) {
						hashes[packet.Hash] = true
					}
				}
			}
			for _, packet := range packets {
				require.NotEmpty(t, packet.Hash)
				require.True(t, hashes[packet.Hash], "恢复的媒体包不得重新编码")
			}
		}
	})

	t.Run("过滤未知编码的无效音轨", func(t *testing.T) {
		// 插入声明完整但使用未知编码的音频标签，避免依赖丢字节后的重同步结果。
		// 0xfe 的 SoundFormat 为 15，FFmpeg 无法识别；其他位声明 44100 Hz、16 位、单声道。
		invalidTag := make([]byte, 17)
		invalidTag[0], invalidTag[3], invalidTag[11] = 8, 2, 0xfe
		binary.BigEndian.PutUint32(invalidTag[13:], 13)
		// 放在 AAC 配置标签之后，使真实音轨已确定编码、无效音轨在输入探测时可见。
		var data []byte
		for _, tag := range parseMP4TestFLVTags(t, av) {
			if tag.kind == 8 && av[tag.start+11]>>4 == 10 && av[tag.start+12] == 0 {
				setMP4TestFLVTimestamp(invalidTag, 0, tag.timestamp)
				data = append([]byte(nil), av[:tag.end]...)
				data = append(data, invalidTag...)
				data = append(data, av[tag.end:]...)
				break
			}
		}
		require.NotEmpty(t, data, "样本必须包含 AAC 配置标签")
		inputPath := filepath.Join(t.TempDir(), "unknown-audio.flv")
		require.NoError(t, os.WriteFile(inputPath, data, 0644))
		before := tools.probe(t, inputPath)
		unknownAudio := 0
		for _, stream := range before.Streams {
			if stream.CodecType == "audio" && stream.CodecName == "" {
				unknownAudio++
			}
		}
		require.Positive(t, unknownAudio, "样本必须实际包含未知编码的无效音轨")
		// 排除音轨后 MP4 不能完全替代原始文件，即使开启删除也应保留源文件。
		outputPath, stage := tools.convertExpect(t, inputPath, true, false)
		require.Contains(t, stage.GetLogs(), "已排除缺少封装参数的音轨")
		require.Contains(t, stage.GetLogs(), "转换时排除了音轨，保留原始文件")
		after := tools.probe(t, outputPath)
		require.Len(t, after.Streams, 2, "应过滤未知编码音轨，保留真实音视频")
		assertMP4TestOutput(t, outputPath, after)
		// 有效流必须完整复制，不能通过丢弃真实音轨或重新编码来避开封装错误。
		assertMP4TestPacketCopy(t, tools.probe(t, filepath.Join(seedDir, "av.flv")), after)
		tools.decode(t, outputPath)

		source := pipeline.FileInfo{Path: inputPath, Type: pipeline.FileTypeVideo}
		// 输入已带删除标记时也应撤销，保留源文件不依赖上游阶段的标记状态。
		marked := source
		marked.Deletable = true
		_, stage = tools.convertFile(t, marked, false, source)
		require.Contains(t, stage.GetLogs(), "转换时排除了音轨，保留原始文件")
		// 已上传的源文件在云端有完整副本，保留上传标记，本地文件仍按上传设置清理。
		uploaded := source
		uploaded.Deletable = true
		uploaded.Metadata = map[string]any{"uploaded": true}
		wantUploaded := uploaded
		wantUploaded.Deletable = false
		_, stage = tools.convertFile(t, uploaded, true, wantUploaded)
		require.Contains(t, stage.GetLogs(), "转换时排除了音轨，原始文件已上传，本地文件按上传设置清理")
		require.NotContains(t, stage.GetLogs(), "保留原始文件")
	})

	t.Run("保留探测窗口后出现的音轨", func(t *testing.T) {
		// 音频从第 12 秒开始，晚于 10 秒探测窗口；探测时音轨参数完整，但尚未解码出采样格式。
		inputPath := filepath.Join(t.TempDir(), "late-audio.flv")
		tools.run(t, tools.ffmpeg, "-hide_banner", "-nostdin", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc2=size=160x96:rate=30",
			"-itsoffset", "12", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:duration=3",
			"-map", "0:v", "-map", "1:a",
			"-c:v", "libx264", "-threads:v", "1", "-preset", "veryfast", "-g", "30", "-bf", "2",
			"-c:a", "aac", "-ar", "44100", "-t", "20", "-f", "flv", inputPath)
		before := tools.probe(t, inputPath)
		require.Len(t, before.Streams, 2, "样本应包含视频和晚出现的音轨")
		audioPackets := mp4TestPacketsForStream(before, before.Streams[1].Index)
		require.NotEmpty(t, audioPackets)
		require.Greater(t, mp4TestTimestamp(t, audioPackets[0].PTS), 10.0, "样本音频必须晚于探测窗口出现")
		// 音轨完整保留时，源文件可以按配置删除。
		outputPath, _ := tools.convert(t, inputPath, true)
		after := tools.probe(t, outputPath)
		assertMP4TestOutput(t, outputPath, after)
		assertMP4TestPacketCopy(t, before, after)
		tools.decode(t, outputPath)
	})

	t.Run("可变帧率", func(t *testing.T) {
		if !tools.supportsVersion(5, 1) {
			t.Skip("生成可变帧率样本需要支持 -fps_mode 的 FFmpeg 5.1 或更新版本")
		}
		inputPath := filepath.Join(t.TempDir(), "vfr.flv")
		tools.generate(t, inputPath, true, false, "160x96", true)
		outputPath, _ := tools.convert(t, inputPath, false)
		before, after := tools.probe(t, inputPath), tools.probe(t, outputPath)
		assertMP4TestOutput(t, outputPath, after)
		assertMP4TestPacketCopy(t, before, after)
		// 确认样本确实存在不同的帧间隔。
		intervals := make(map[int64]bool)
		packets := mp4TestPacketsForStream(before, before.Streams[0].Index)
		for i := 1; i < len(packets); i++ {
			delta := mp4TestTimestamp(t, packets[i].DTS) - mp4TestTimestamp(t, packets[i-1].DTS)
			intervals[int64(delta*1000+0.5)] = true
		}
		require.Greater(t, len(intervals), 1)
		tools.decode(t, outputPath)
	})

	t.Run("保留全部音轨并跳过字幕", func(t *testing.T) {
		dir := t.TempDir()
		secondAudio := filepath.Join(dir, "second-audio.flv")
		tools.run(t, tools.ffmpeg, "-hide_banner", "-nostdin", "-loglevel", "error",
			"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=44100", "-c:a", "aac", "-ar", "44100", "-t", "3", "-f", "flv", secondAudio)
		subtitle := filepath.Join(dir, "subtitle.srt")
		require.NoError(t, os.WriteFile(subtitle, []byte("1\n00:00:00,000 --> 00:00:02,000\n直播字幕\n"), 0644))
		inputPath := filepath.Join(dir, "multi.mkv")
		tools.run(t, tools.ffmpeg, "-hide_banner", "-nostdin", "-loglevel", "error",
			"-i", filepath.Join(seedDir, "av.flv"), "-i", secondAudio, "-i", subtitle,
			"-map", "0:v:0", "-map", "0:a:0", "-map", "1:a:0", "-map", "2:s:0", "-c", "copy",
			"-metadata:s:a:0", "language=zho", "-metadata:s:a:1", "language=eng", inputPath)
		outputPath, _ := tools.convert(t, inputPath, false)
		before, after := tools.probe(t, inputPath), tools.probe(t, outputPath)
		require.Len(t, before.Streams, 4, "样本应包含视频、两路音频和字幕")
		firstPackets := mp4TestPacketsForStream(before, before.Streams[1].Index)
		secondPackets := mp4TestPacketsForStream(before, before.Streams[2].Index)
		require.Greater(t, len(firstPackets), 10)
		require.Greater(t, len(secondPackets), 10)
		require.NotEqual(t, firstPackets[10].Hash, secondPackets[10].Hash, "样本必须包含不同声音的两路音轨")
		require.Len(t, after.Streams, 3)
		assertMP4TestOutput(t, outputPath, after)
		assertMP4TestPacketCopy(t, before, after)
		require.Equal(t, "zho", after.Streams[1].Tags["language"])
		require.Equal(t, "eng", after.Streams[2].Tags["language"])
		tools.decode(t, outputPath)
	})

	for _, format := range []struct{ name, extension string }{{"matroska", ".mkv"}, {"mpegts", ".ts"}} {
		t.Run("其他容器/"+format.name, func(t *testing.T) {
			inputPath := filepath.Join(t.TempDir(), "recording"+format.extension)
			tools.run(t, tools.ffmpeg, "-hide_banner", "-nostdin", "-loglevel", "error",
				"-i", filepath.Join(seedDir, "av.flv"), "-c", "copy", "-f", format.name, inputPath)
			outputPath, _ := tools.convert(t, inputPath, false)
			before, after := tools.probe(t, inputPath), tools.probe(t, outputPath)
			assertMP4TestOutput(t, outputPath, after)
			assertMP4TestStreams(t, before, after)
			if format.name == "matroska" {
				assertMP4TestPacketCopy(t, before, after)
			} else {
				// TS 转 MP4 会自动改写 Annex B、ADTS 封装，检查每路包数和实际解码。
				for i, stream := range after.Streams {
					require.Len(t, mp4TestPacketsForStream(after, stream.Index),
						len(mp4TestPacketsForStream(before, before.Streams[i].Index)))
				}
			}
			tools.decode(t, outputPath)
		})
	}

	t.Run("同一编码切换分辨率", func(t *testing.T) {
		if !tools.supportsVersion(8, 0) {
			t.Skip("多份 MP4 编码配置的检查需要 FFmpeg 8 或更新版本")
		}
		second := tools.generate(t, filepath.Join(t.TempDir(), "second.flv"), true, false, "320x180", false)
		mixed := append([]byte(nil), video...)
		for _, tag := range parseMP4TestFLVTags(t, second) {
			if tag.kind == 9 {
				data := append([]byte(nil), second[tag.start:tag.end]...)
				setMP4TestFLVTimestamp(data, 0, tag.timestamp+3000)
				mixed = append(mixed, data...)
			}
		}
		inputPath := filepath.Join(t.TempDir(), "mixed.flv")
		require.NoError(t, os.WriteFile(inputPath, mixed, 0644))
		outputPath, _ := tools.convert(t, inputPath, false)
		before, after := tools.probe(t, inputPath), tools.probe(t, outputPath)
		assertMP4TestOutput(t, outputPath, after)
		assertMP4TestPacketCopy(t, before, after)
		tools.decode(t, outputPath)
		var frames struct {
			Frames []struct{ Width, Height int }
		}
		require.NoError(t, json.Unmarshal(tools.run(t, tools.ffprobe, "-v", "error",
			"-show_entries", "frame=width,height", "-of", "json", outputPath), &frames))
		counts := make(map[[2]int]int)
		for _, frame := range frames.Frames {
			counts[[2]int{frame.Width, frame.Height}]++
		}
		require.Equal(t, map[[2]int]int{{160, 96}: 90, {320, 180}: 90}, counts)
	})

	t.Run("未设置 Logger", func(t *testing.T) {
		// 截断样本会产生转换诊断，覆盖告警日志路径。
		inputPath := filepath.Join(t.TempDir(), "no-logger.flv")
		require.NoError(t, os.WriteFile(inputPath, av[:len(av)-80], 0644))
		stage, ctx := tools.stageContext(t, false)
		ctx.Logger = nil
		var output []pipeline.FileInfo
		var err error
		require.NotPanics(t, func() {
			output, err = stage.Execute(ctx, []pipeline.FileInfo{{Path: inputPath, Type: pipeline.FileTypeVideo}})
		})
		require.NoError(t, err, "%s", stage.GetLogs())
		require.Len(t, output, 2)
		require.Contains(t, stage.GetLogs(), "ffmpeg 转换诊断")
		require.NotNil(t, stage.logger, "未提供 Logger 时应回退到全局 logger")
	})

	t.Run("转换失败保留源文件和已有输出", func(t *testing.T) {
		inputPath := filepath.Join(t.TempDir(), "invalid.flv")
		outputPath := strings.TrimSuffix(inputPath, ".flv") + ".mp4"
		tempPath := filepath.Join(filepath.Dir(inputPath), ".converting_invalid.mp4")
		source, existing := []byte("FLV-invalid"), []byte("已有输出")
		require.NoError(t, os.WriteFile(inputPath, source, 0644))
		require.NoError(t, os.WriteFile(outputPath, existing, 0644))
		require.NoError(t, os.WriteFile(tempPath, []byte("未完成的转换"), 0644))
		stage, ctx := tools.stageContext(t, true)
		output, err := stage.Execute(ctx, []pipeline.FileInfo{{Path: inputPath, Type: pipeline.FileTypeVideo}})
		require.Error(t, err)
		require.Empty(t, output)
		require.Contains(t, stage.GetLogs(), "ffmpeg 转换失败")
		require.Contains(t, stage.GetLogs(), "ffmpeg stderr")
		data, err := os.ReadFile(inputPath)
		require.NoError(t, err)
		require.Equal(t, source, data)
		data, err = os.ReadFile(outputPath)
		require.NoError(t, err)
		require.Equal(t, existing, data)
		require.NoFileExists(t, tempPath)
	})
}

type mp4TestTools struct {
	ffmpeg, ffprobe string
	major, minor    int
}

func findMP4TestTools(t *testing.T) mp4TestTools {
	t.Helper()
	if testing.Short() {
		t.Skip("短测试模式跳过真实 FFmpeg 样本检查")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("集成测试需要 PATH 中的 ffmpeg")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("集成测试需要 PATH 中的 ffprobe")
	}
	tools := mp4TestTools{ffmpeg: ffmpeg, ffprobe: ffprobe}
	encoders := string(tools.run(t, ffmpeg, "-hide_banner", "-encoders"))
	if !strings.Contains(encoders, " libx264 ") || !strings.Contains(encoders, " aac ") {
		t.Skip("生成测试样本需要 FFmpeg 的 libx264、AAC 编码器")
	}
	version := string(tools.run(t, ffmpeg, "-version"))
	tools.major, tools.minor = parseMP4TestFFmpegVersion(version)
	t.Logf("测试使用 %s", strings.SplitN(version, "\n", 2)[0])
	return tools
}

func parseMP4TestFFmpegVersion(version string) (major, minor int) {
	match := regexp.MustCompile(`ffmpeg version n?(\d+)(?:\.(\d+))?`).FindStringSubmatch(version)
	if len(match) != 3 {
		return 0, 0
	}
	major, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, 0
	}
	if match[2] != "" {
		minor, err = strconv.Atoi(match[2])
		if err != nil {
			return 0, 0
		}
	}
	return major, minor
}

func (tools mp4TestTools) supportsVersion(major, minor int) bool {
	// 无法识别的快照版本保持为 0，按不满足版本门槛处理。
	return tools.major > major || (tools.major == major && tools.minor >= minor)
}

func (tools mp4TestTools) run(t *testing.T, executable string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	require.NoError(t, err, "命令失败: %s %v\n%s", executable, args, stderr.String())
	return output
}

func (tools mp4TestTools) generate(t *testing.T, path string, video, audio bool, size string, vfr bool) []byte {
	t.Helper()
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	if video {
		args = append(args, "-f", "lavfi", "-i", "testsrc2=size="+size+":rate=30")
	}
	if audio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100")
	}
	if video {
		args = append(args, "-c:v", "libx264", "-threads:v", "1", "-preset", "veryfast", "-g", "30", "-bf", "2")
		if vfr {
			args = append(args, "-vf", `select=not(mod(n\,2))+not(mod(n\,5))`, "-fps_mode", "vfr")
		}
	}
	if audio {
		args = append(args, "-c:a", "aac", "-ar", "44100")
	}
	args = append(args, "-t", "3", "-metadata", "title=直播测试", "-f", "flv", path)
	tools.run(t, tools.ffmpeg, args...)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func (tools mp4TestTools) stageContext(t *testing.T, deleteSource bool) (*ConvertMp4Stage, *pipeline.PipelineContext) {
	t.Helper()
	stage, err := NewConvertMp4Stage(pipeline.StageConfig{
		Options: map[string]any{pipeline.OptionDeleteSource: deleteSource},
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return stage.(*ConvertMp4Stage), &pipeline.PipelineContext{
		Ctx: ctx, FFmpegPath: tools.ffmpeg,
		Logger: &livelogger.LiveLogger{Entry: logrus.NewEntry(logger)},
	}
}

func (tools mp4TestTools) convert(t *testing.T, inputPath string, deleteSource bool) (string, *ConvertMp4Stage) {
	t.Helper()
	return tools.convertExpect(t, inputPath, deleteSource, deleteSource)
}

// convertExpect 转换并检查源文件是否被标记为可删除；排除音轨时即使开启删除也应保留源文件。
func (tools mp4TestTools) convertExpect(t *testing.T, inputPath string, deleteSource, wantDeletable bool) (string, *ConvertMp4Stage) {
	t.Helper()
	input := pipeline.FileInfo{Path: inputPath, Type: pipeline.FileTypeVideo}
	wantSource := input
	wantSource.Deletable = wantDeletable
	return tools.convertFile(t, input, deleteSource, wantSource)
}

// convertFile 转换指定输入，并检查阶段输出中原始文件的删除及上传标记。
func (tools mp4TestTools) convertFile(t *testing.T, input pipeline.FileInfo, deleteSource bool, wantSource pipeline.FileInfo) (string, *ConvertMp4Stage) {
	t.Helper()
	inputPath := input.Path
	stage, ctx := tools.stageContext(t, deleteSource)
	output, err := stage.Execute(ctx, []pipeline.FileInfo{input})
	require.NoError(t, err, "%s", stage.GetLogs())
	require.Same(t, ctx.Logger, stage.logger, "进度告警应使用当前任务的 logger")
	require.Len(t, output, 2)
	outputPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + ".mp4"
	require.Equal(t, pipeline.FileInfo{Path: outputPath, Type: pipeline.FileTypeVideo, SourcePath: inputPath}, output[0])
	require.Equal(t, wantSource, output[1])
	require.FileExists(t, inputPath, "转换阶段只标记源文件，不应提前删除")
	require.NoFileExists(t, filepath.Join(filepath.Dir(outputPath), ".converting_"+filepath.Base(outputPath)))
	return outputPath, stage
}

type mp4TestMedia struct {
	Streams []struct {
		Index      int
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		SampleRate string `json:"sample_rate"`
		Channels   int    `json:"channels"`
		Tags       map[string]string
	}
	Packets []mp4TestPacket
	Format  struct{ Tags map[string]string }
}

type mp4TestPacket struct {
	StreamIndex int    `json:"stream_index"`
	PTS         string `json:"pts_time"`
	DTS         string `json:"dts_time"`
	Hash        string `json:"data_hash"`
}

func (tools mp4TestTools) probe(t *testing.T, path string) mp4TestMedia {
	t.Helper()
	var media mp4TestMedia
	output := tools.run(t, tools.ffprobe, "-v", "error", "-show_entries",
		"stream=index,codec_type,codec_name,sample_rate,channels:stream_tags=language:packet=stream_index,dts_time,pts_time,data_hash:format_tags=title",
		"-show_data_hash", "sha256", "-of", "json", path)
	require.NoError(t, json.Unmarshal(output, &media))
	return media
}

func (tools mp4TestTools) decode(t *testing.T, path string) {
	t.Helper()
	tools.run(t, tools.ffmpeg, "-hide_banner", "-nostdin", "-v", "error", "-xerror", "-i", path, "-f", "null", "-")
}

func mp4TestPacketsForStream(media mp4TestMedia, index int) []mp4TestPacket {
	var packets []mp4TestPacket
	for _, packet := range media.Packets {
		if packet.StreamIndex == index {
			packets = append(packets, packet)
		}
	}
	return packets
}

func mp4TestTimestamp(t *testing.T, value string) float64 {
	t.Helper()
	result, err := strconv.ParseFloat(value, 64)
	require.NoError(t, err, "媒体包缺少有效时间戳")
	return result
}

func assertMP4TestStreams(t *testing.T, before, after mp4TestMedia) {
	t.Helper()
	var selected []string
	for _, kind := range []string{"video", "audio"} {
		for _, stream := range before.Streams {
			if stream.CodecType == kind {
				selected = append(selected, kind+":"+stream.CodecName)
				if kind == "video" {
					break
				}
			}
		}
	}
	var actual []string
	for _, stream := range after.Streams {
		actual = append(actual, stream.CodecType+":"+stream.CodecName)
	}
	require.Equal(t, selected, actual, "应封装首路视频和全部音轨，且编码应保持一致")
	require.Equal(t, before.Format.Tags["title"], after.Format.Tags["title"], "保留输入容器中已有的标题")
}

func assertMP4TestPacketCopy(t *testing.T, before, after mp4TestMedia) {
	t.Helper()
	assertMP4TestStreams(t, before, after)
	var shift float64
	matched := make(map[int]bool)
	for i, outputStream := range after.Streams {
		inputIndex := -1
		for _, stream := range before.Streams {
			if stream.CodecType == outputStream.CodecType && !matched[stream.Index] {
				inputIndex = stream.Index
				matched[stream.Index] = true
				break
			}
		}
		input := mp4TestPacketsForStream(before, inputIndex)
		output := mp4TestPacketsForStream(after, outputStream.Index)
		require.NotEmpty(t, input)
		require.Len(t, output, len(input), "流复制不应丢失有效包")
		if i == 0 {
			shift = mp4TestTimestamp(t, output[0].PTS) - mp4TestTimestamp(t, input[0].PTS)
		}
		for j := range input {
			require.NotEmpty(t, input[j].Hash)
			require.Equal(t, input[j].Hash, output[j].Hash, "流复制应保留编码包内容")
			// FLV 时间戳以毫秒记录，MP4 音频使用采样时间基，允许少量舍入误差。
			require.InDelta(t, mp4TestTimestamp(t, input[j].PTS)+shift,
				mp4TestTimestamp(t, output[j].PTS), 0.002, "包间隔和音视频相对偏移应保持")
		}
	}
}

func assertMP4TestOutput(t *testing.T, path string, media mp4TestMedia) {
	t.Helper()
	require.NotEmpty(t, media.Streams)
	checked := make(map[string]bool)
	for _, stream := range media.Streams {
		packets := mp4TestPacketsForStream(media, stream.Index)
		require.NotEmpty(t, packets, "每路输出都应包含实际媒体数据")
		if !checked[stream.CodecType] {
			require.Greater(t, len(packets), 20, "主音视频应包含实际媒体数据")
			checked[stream.CodecType] = true
		}
		last := -1.0
		for i, packet := range packets {
			dts := mp4TestTimestamp(t, packet.DTS)
			minimumDTS := 0.0
			if i == 0 {
				// 旧版 MP4 edit list 使用毫秒时间基，首包起点可能有不足 1 ms 的舍入误差。
				minimumDTS = -0.001
			}
			require.GreaterOrEqual(t, dts, minimumDTS)
			require.Greater(t, dts, last, "输出 DTS 应严格递增")
			last = dts
		}
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	moov, mdat := -1, -1
	for pos := 0; pos < len(data); {
		require.GreaterOrEqual(t, len(data)-pos, 8, "MP4 顶层 box 不应截断")
		size := uint64(binary.BigEndian.Uint32(data[pos : pos+4]))
		header := uint64(8)
		switch size {
		case 1:
			require.GreaterOrEqual(t, len(data)-pos, 16)
			size = binary.BigEndian.Uint64(data[pos+8 : pos+16])
			header = 16
		case 0:
			size = uint64(len(data) - pos)
		}
		require.GreaterOrEqual(t, size, header)
		require.LessOrEqual(t, size, uint64(len(data)-pos))
		switch string(data[pos+4 : pos+8]) {
		case "moov":
			moov = pos
		case "mdat":
			mdat = pos
		}
		pos += int(size)
	}
	require.GreaterOrEqual(t, moov, 0)
	require.Greater(t, mdat, moov, "faststart 应将 moov 放在媒体数据之前")
}

type mp4TestFLVTag struct {
	start, end int
	kind       byte
	timestamp  uint32
}

func parseMP4TestFLVTags(t *testing.T, data []byte) []mp4TestFLVTag {
	t.Helper()
	require.GreaterOrEqual(t, len(data), 13)
	require.Equal(t, "FLV", string(data[:3]))
	var tags []mp4TestFLVTag
	for pos := int(binary.BigEndian.Uint32(data[5:9])) + 4; pos < len(data); {
		require.GreaterOrEqual(t, len(data)-pos, 15, "生成的 FLV 标签应完整")
		size := int(data[pos+1])<<16 | int(data[pos+2])<<8 | int(data[pos+3])
		end := pos + 11 + size + 4
		require.LessOrEqual(t, end, len(data))
		timestamp := uint32(data[pos+7])<<24 | uint32(data[pos+4])<<16 | uint32(data[pos+5])<<8 | uint32(data[pos+6])
		tags = append(tags, mp4TestFLVTag{pos, end, data[pos], timestamp})
		pos = end
	}
	require.NotEmpty(t, tags)
	return tags
}

func setMP4TestFLVTimestamp(data []byte, pos int, value uint32) {
	data[pos+4], data[pos+5], data[pos+6], data[pos+7] = byte(value>>16), byte(value>>8), byte(value), byte(value>>24)
}

func shiftMP4TestFLVTimestamps(t *testing.T, source []byte, from, delta uint32) []byte {
	t.Helper()
	data := append([]byte(nil), source...)
	for _, tag := range parseMP4TestFLVTags(t, source) {
		if (tag.kind == 8 || tag.kind == 9) && tag.timestamp >= from {
			setMP4TestFLVTimestamp(data, tag.start, tag.timestamp+delta)
		}
	}
	return data
}

func dropMP4TestFLVPackets(t *testing.T, source []byte) []byte {
	t.Helper()
	tags := parseMP4TestFLVTags(t, source)
	data := append([]byte(nil), source[:tags[0].start]...)
	removed := 0
	for _, tag := range tags {
		if tag.kind == 9 && tag.timestamp > 1000 && tag.timestamp < 1150 {
			removed++
			continue
		}
		data = append(data, source[tag.start:tag.end]...)
	}
	require.Positive(t, removed, "丢包样本必须实际移除视频包")
	return data
}

func damageMP4TestFLVTag(t *testing.T, source []byte) []byte {
	t.Helper()
	for _, tag := range parseMP4TestFLVTags(t, source) {
		if tag.kind == 9 && tag.timestamp >= 1000 && tag.end-tag.start > 60 {
			// 删除标签内部 10 字节，保留声明长度和 PreviousTagSize，模拟丢字节后失去边界。
			data := append([]byte(nil), source[:tag.start+30]...)
			return append(data, source[tag.start+40:]...)
		}
	}
	t.Fatal("没有找到适合构造损坏的 FLV 标签")
	return nil
}
