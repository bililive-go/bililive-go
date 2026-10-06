package stages

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestReadFFmpegDiagnostics(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"空输出", "", ""},
		{"空白输出", " \r\n\t\n", ""},
		{"末行没有换行", "Packet corrupt\nNon-monotonous DTS", "Packet corrupt\nNon-monotonous DTS"},
		{"合并重复警告", "Packet corrupt\r\nPacket corrupt\nPacket corrupt", "Packet corrupt [出现 3 次]"},
		{"大量重复警告", strings.Repeat("Packet corrupt\n", 100000), "Packet corrupt [出现 100000 次]"},
		{"合并数字变化的警告", "Non-monotonous DTS in stream 0 previous: 1234, current: 1200\nNon-monotonous DTS in stream 0 previous: 2234, current: 2200",
			"Non-monotonous DTS in stream 0 previous: 1234, current: 1200 [出现 2 次]"},
		{"合并负数和小数变化的警告", "Timestamp -1.25, offset +2.5\nTimestamp -3.75, offset +4.0", "Timestamp -1.25, offset +2.5 [出现 2 次]"},
		{"合并实例地址变化的警告", "[flv @ 00000224c6011dc0] Packet corrupt (stream = 0, dts = 1234)\n[flv @ 0xabcdef123456] Packet corrupt (stream = 0, dts = 5678)",
			"[flv @ 00000224c6011dc0] Packet corrupt (stream = 0, dts = 1234) [出现 2 次]"},
		{"区分不同流的同类警告", "Non-monotonous DTS in output stream 0:0; previous: 100, current: 90\nNon-monotonous DTS in output stream 0:1; previous: 200, current: 190\nNon-monotonous DTS in output stream 0:1; previous: 300, current: 290",
			"Non-monotonous DTS in output stream 0:0; previous: 100, current: 90\nNon-monotonous DTS in output stream 0:1; previous: 200, current: 190 [出现 2 次]"},
		{"区分不同流的损坏包", "[flv @ 0x1234] Packet corrupt (stream = 0, dts = 100)\n[flv @ 0x5678] Packet corrupt (stream = 1, dts = 100)",
			"[flv @ 0x1234] Packet corrupt (stream = 0, dts = 100)\n[flv @ 0x5678] Packet corrupt (stream = 1, dts = 100)"},
		{"区分不同输入流的前缀", "[aist#0:1/aac @ 0x1234] Error parsing frame\n[aist#0:2/aac @ 0x5678] Error parsing frame\n[aist#0:2/aac @ 0x9abc] Error parsing frame",
			"[aist#0:1/aac @ 0x1234] Error parsing frame\n[aist#0:2/aac @ 0x5678] Error parsing frame [出现 2 次]"},
		{"区分不同流的探测失败", "Could not find codec parameters for stream 2 (Audio: none, 0 channels)\nCould not find codec parameters for stream 3 (Audio: none, 0 channels)",
			"Could not find codec parameters for stream 2 (Audio: none, 0 channels)\nCould not find codec parameters for stream 3 (Audio: none, 0 channels)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := strings.NewReader(tc.input)
			result, err := readFFmpegDiagnostics(reader)
			require.NoError(t, err)
			require.Equal(t, tc.want, result)
			require.Zero(t, reader.Len(), "必须排空输出，避免 FFmpeg 因管道堵塞挂起")
		})
	}
}

func TestReadFFmpegDiagnostics_KeepsLaterWarningTypes(t *testing.T) {
	var input strings.Builder
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&input, "Non-monotonous DTS in stream 0 previous: %d, current: %d\n", i+100, i)
	}
	input.WriteString("Packet corrupt\nCould not find codec parameters for stream 2\n")
	reader := strings.NewReader(input.String())
	result, err := readFFmpegDiagnostics(reader)
	require.NoError(t, err)
	require.Equal(t, "Non-monotonous DTS in stream 0 previous: 100, current: 0 [出现 10000 次]\nPacket corrupt\nCould not find codec parameters for stream 2", result)
	require.Zero(t, reader.Len())
}

func TestReadFFmpegDiagnostics_Limits(t *testing.T) {
	for _, width := range []int{20, 800} {
		t.Run(fmt.Sprintf("每行 %d 字节", width), func(t *testing.T) {
			var input strings.Builder
			for i := 0; i < 1000; i++ {
				// 不同的文字表示不同警告类型，不能被数字归一化合并。
				fmt.Fprintf(&input, "警告 %c %s\n", '一'+i, strings.Repeat("x", width))
			}
			// 超过保存额度后仍统计已保存警告的重复次数。
			fmt.Fprintf(&input, "警告 一 %s\n", strings.Repeat("x", width))
			reader := strings.NewReader(input.String())
			result, err := readFFmpegDiagnostics(reader)
			require.NoError(t, err)
			require.Contains(t, result, "警告 一")
			require.Contains(t, result, "[出现 2 次]")
			require.NotContains(t, result, fmt.Sprintf("警告 %c", '一'+999))
			require.Contains(t, result, "已截断，共 1001 行")
			require.LessOrEqual(t, len(result), ffmpegDiagnosticsMaxBytes)
			require.LessOrEqual(t, len(strings.Split(result, "\n")), ffmpegDiagnosticsMaxLines+1)
			require.Zero(t, reader.Len())
			require.True(t, utf8.ValidString(result))
		})
	}
}

func TestReadFFmpegDiagnostics_LongLines(t *testing.T) {
	reader := strings.NewReader(strings.Repeat("损坏", 100000) + "\nPacket corrupt\n")
	result, err := readFFmpegDiagnostics(reader)
	require.NoError(t, err)
	require.Contains(t, result, "Packet corrupt", "超长行之后的输出也必须继续读取")
	require.Contains(t, result, "已截断，共 2 行")
	require.LessOrEqual(t, len(result), ffmpegDiagnosticsMaxBytes)
	require.True(t, utf8.ValidString(result), "截断不应破坏 UTF-8 字符")
	require.Zero(t, reader.Len())
}

func TestReadFFmpegDiagnostics_ReadError(t *testing.T) {
	readErr := errors.New("模拟诊断管道读取失败")
	result, err := readFFmpegDiagnostics(io.MultiReader(strings.NewReader("Packet corrupt"), iotest.ErrReader(readErr)))
	require.ErrorIs(t, err, readErr)
	require.Equal(t, "Packet corrupt", result, "读取错误前已取得的诊断应保留")
}

type mp4DiagnosticsTestReader func([]byte) (int, error)

func (read mp4DiagnosticsTestReader) Read(buffer []byte) (int, error) {
	return read(buffer)
}

type mp4DiagnosticsTestPanicValue struct{}

func (mp4DiagnosticsTestPanicValue) String() string {
	panic("模拟 panic 值格式化失败")
}

func TestReadFFmpegDiagnostics_Panics(t *testing.T) {
	var typedNil *strings.Reader
	cases := []struct {
		name       string
		reader     io.Reader
		wantReason string
	}{
		{"nil Reader", nil, "invalid memory address"},
		{"typed nil Reader", typedNil, "invalid memory address"},
		{"Reader 主动 panic", mp4DiagnosticsTestReader(func([]byte) (int, error) { panic("模拟读取异常") }), "模拟读取异常"},
		{"Reader panic error", mp4DiagnosticsTestReader(func([]byte) (int, error) { panic(errors.New("模拟错误对象")) }), "模拟错误对象"},
		{"panic 值格式化失败", mp4DiagnosticsTestReader(func([]byte) (int, error) { panic(mp4DiagnosticsTestPanicValue{}) }), "模拟 panic 值格式化失败"},
		{"Reader panic(nil)", mp4DiagnosticsTestReader(func([]byte) (int, error) { panic(nil) }), "FFmpeg 诊断收集发生异常"},
		{"Reader 返回负数长度", mp4DiagnosticsTestReader(func([]byte) (int, error) { return -1, nil }), "negative count"},
		{"Reader 返回超大长度", mp4DiagnosticsTestReader(func(buffer []byte) (int, error) { return len(buffer) + 1, nil }), "out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var result string
			var err error
			require.NotPanics(t, func() { result, err = readFFmpegDiagnostics(tc.reader) })
			require.Error(t, err, "诊断异常应返回告警原因，而不向上传播 panic")
			require.ErrorContains(t, err, tc.wantReason)
			require.Empty(t, result)
		})
	}
}

func TestReadFFmpegDiagnostics_DrainPanic(t *testing.T) {
	readErr := errors.New("模拟诊断读取失败")
	failed := false
	input := mp4DiagnosticsTestReader(func([]byte) (int, error) {
		if !failed {
			failed = true
			return 0, readErr
		}
		panic("模拟排空异常")
	})
	var err error
	require.NotPanics(t, func() { _, err = readFFmpegDiagnostics(input) })
	require.ErrorIs(t, err, readErr)
	require.ErrorContains(t, err, "模拟排空异常")
}

func TestReadFFmpegDiagnostics_DrainsAfterFailure(t *testing.T) {
	readErr := errors.New("模拟单次读取失败")
	for _, wantPanic := range []bool{false, true} {
		t.Run(fmt.Sprintf("读取异常 panic=%t", wantPanic), func(t *testing.T) {
			reader := strings.NewReader(strings.Repeat("Packet corrupt\n", 10000))
			failed := false
			input := mp4DiagnosticsTestReader(func(buffer []byte) (int, error) {
				if !failed {
					failed = true
					if wantPanic {
						panic("模拟单次读取异常")
					}
					return 0, readErr
				}
				return reader.Read(buffer)
			})
			var err error
			require.NotPanics(t, func() { _, err = readFFmpegDiagnostics(input) })
			require.Error(t, err)
			if wantPanic {
				require.ErrorContains(t, err, "模拟单次读取异常")
			} else {
				require.ErrorIs(t, err, readErr)
			}
			require.Zero(t, reader.Len(), "异常后仍可读取的 stderr 必须排空，避免转换卡住")
		})
	}
}
