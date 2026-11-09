# dev 测试平台与录制验证

## 能力边界

本目录包含两种不同用途的代码：

- `dev.go` 实现 bgo 的开发测试平台适配器，可从测试服务器读取直播间及候选流。
- `TestRunner` 直接启动 FFmpeg，仅执行媒体冒烟测试，不启动 bgo，不经过 recorder、流选择、重连、后处理或前端控制流程。

`GetAvailableScenarios()` 中的 11 个场景是测试需求目录，不代表 11 项都已实现。当前运行器只支持基础 FLV H.264、FLV HEVC 与 HLS H.264 媒体冒烟。实际编码支持还取决于测试服务器与 FFmpeg。

以下请求明确失败，不能静默忽略后显示 PASS：

- 任意 `Faults`，包括断连、限速、分辨率或时间戳变化、丢帧。
- `DownloaderReconnects`、`ComplianceWarnings`、`MaxFileSizeDiff` 断言。
- 非空 `BGOPath` 或 `RequiresBGO: true`。预定义多流选择场景要求真实 bgo。
- 预期不可播放的场景。

因此直接运行 `RunAllScenarios` 会报告尚不支持的场景失败，这是有意的能力检查，不应解释为 bgo 录制回归。

## 已有自动验证

```bash
# 构建后端，遵循仓库构建入口
make dev

# runner 控制逻辑及媒体冒烟，依赖缺失会明确 skip 真实媒体部分
go test -tags dev ./src/live/dev -count=1 -v

# 必须运行真实媒体测试：缺少 FFmpeg 或 ffprobe 时直接失败
BGO_REQUIRE_MEDIA_TESTS=1 go test -tags dev ./src/live/dev -count=1 -v

# 真实 bgo recorder → HTTP 探测代理 → native parser → 文件落盘
go test ./src/recorders -run "TestTryRecord(NativeHTTPOutput|FFmpegMediaOutput)" -count=1 -v
```

`TestFFmpegMediaSmoke` 在临时目录生成两秒合成画面与音频，用随机回环端口提供 FLV/HLS。它检查输出大小、音视频流、有效时长、完整解码，并以过短时长要求和损坏文件验证失败路径。它不依赖 osrp-stream-tester，也不会访问真实直播平台。

`TestTryRecordNativeHTTPOutput` 不 mock parser，验证完整 FLV 标签落盘、截断标签处理、非法签名、上游请求头与错误报告。其手工 PCM 标签只用于结构验证，不能据此声称媒体可播放。当前 native parser 把有限流 EOF 报告为错误，这一现状也被测试明确记录。

TestTryRecordFFmpegMediaOutput 使用两秒合成视频经过真实 bgo recorder 和 FFmpeg parser，验证请求头、输出时长以及完整解码。只替换平台元信息，未替换 parser。此测试仍不覆盖监听管理器、UI、故障重连或后处理。

单元测试中的子进程助手仅验证 runner 控制逻辑，不算真实媒体录制。CI 已安装 FFmpeg，整个 test job 设置 `BGO_REQUIRE_MEDIA_TESTS=1`：`make test` 以 release 标签执行 recorder 媒体测试并计入覆盖率，再单独运行仅在 dev 标签下可用的 dev 包。缺少媒体工具时两处都明确失败，recorders 不再整包重复执行。

## 使用外部流服务器

先启动兼容 osrp-stream-tester 的服务器，再选择一个受支持场景：

```go
runner := dev.NewTestRunner("http://127.0.0.1:8888", "./test_output")
scenario := dev.GetAvailableScenarios()[0] // basic_flv_h264
result, err := runner.RunScenario(ctx, scenario)
if err != nil {
    // context 取消或截止时停止后续场景
    return err
}
if !result.Success {
    return fmt.Errorf("%s", result.ErrorMessage)
}
```

服务器必须提供以下接口：

- `GET /health`
- `GET /live/{name}.flv`
- `GET /hls/{name}.m3u8`

流请求携带 `codec`、`quality`、`duration` 查询参数。可以用 `FFmpegPath`、`FFprobePath` 指定工具，否则从 PATH 查找。

录制与产物验证各有独立超时预算，默认均为场景录制时长加 30 秒。验证阶段从录制完成后开始，包含 ffprobe 与完整解码；可用正数 `ValidationTimeout` 调整验证预算，零值使用默认值。两个阶段仍受调用方的 context 约束。取消或任一阶段超时会返回可通过 `errors.Is` 判断的 context 错误，并停止 `RunAllScenarios`；普通媒体验证失败仍记录在 `TestResult` 中。

每次运行在输出目录下创建独立子目录，避免残留文件导致假阳性。录制进程非零退出、取消或超时都算失败；ffprobe 缺失、返回错误、没有音视频流、时长无效、时长不足或 FFmpeg 完整解码失败、未解码出任何音视频内容也都算失败。最小时长目前比较 ffprobe 报告的容器时长，不等于独立统计已解码帧的时间跨度。

## 后续修改方案

1. 为外部流服务器确认和测试启动、故障注入、停止 API 的真实契约，不能仅凭定义了 `FaultConfig` 就声称故障已发生。
2. 单独建立 bgo 端到端运行器，使用隔离配置、临时输出目录和本地测试流，显式启动/停止实际 bgo。
3. 从上游请求及 bgo 日志观测断连与重连，给每个故障定义执行证据及恢复断言。
4. 用候选流请求记录和最终录制媒体验证多流选择；为时间戳、编码警告、输出大小差异定义可测的观测数据。
5. 替换 `tests/e2e/recording.spec.ts` 中没有实际断言或恒真的 UI 检查，再将其纳入端到端录制覆盖。当前这些页面/API 冒烟检查不证明录制成功。
