# bgo 自动化测试框架

> **位置**: `src/live/dev/`  
> **构建标签**: `dev`  
> **依赖**: osrp-stream-tester

## 功能概述

bgo 的 `dev` 包提供了完整的自动化测试框架，用于验证直播录制功能在各种场景下的表现。

### 核心功能

- ✅ 模拟多格式直播流（FLV、HLS）
- ✅ 模拟多编码格式（H.264、H.265、Annex B HEVC）
- ✅ 模拟多分辨率流（1080p、720p、480p）
- ✅ 网络故障注入（断连、延迟、限速）
- ✅ 流结构异常模拟（分辨率变化、时间戳跳跃）
- ✅ 自动化结果验证
- ✅ 测试报告生成

## 文件结构

```
src/live/dev/
├── dev.go              # Live 接口实现，与测试服务器交互
├── test_scenarios.go   # 测试场景定义（11个预定义场景）
└── test_runner.go      # 测试运行器和结果验证
```

## 使用方法

### 1. 启动测试服务器

首先需要启动 osrp-stream-tester 服务器：

```bash
cd ../kira-works/osrp-stream-tester
go run ./cmd/stream-tester serve --port 8888
```

### 2. 运行测试

#### 方式A：在代码中运行

```go
//go:build dev

package main

import (
    "context"
    "github.com/bililive-go/bililive-go/src/live/dev"
)

func main() {
    ctx := context.Background()
    
    // 创建测试运行器
    runner := dev.NewTestRunner("http://localhost:8888", "./test_output")
    
    // 运行所有场景
    results, err := runner.RunAllScenarios(ctx)
    if err != nil {
        panic(err)
    }
    
    // 打印报告
    dev.PrintReport(results)
}
```

#### 方式B：单独运行场景

```go
scenario := dev.TestScenario{
    Name:        "custom_test",
    Description: "自定义测试",
    Stream: dev.StreamConfig{
        Format:   "flv",
        Codec:    "avc",
        Duration: 30 * time.Second,
        Quality:  "1080p",
    },
    Expected: dev.Expected{
        OutputPlayable: true,
        MinDuration:    25 * time.Second,
    },
}

result, _ := runner.RunScenario(ctx, scenario)
```

### 3. 构建带dev标签

```bash
go build -tags dev ./...
```

## 预定义测试场景

### 基础功能

| 场景 | 描述 |
|------|------|
| `basic_flv_h264` | FLV H.264 基础录制 |
| `basic_flv_hevc` | FLV H.265 录制 |
| `basic_hls_h264` | HLS H.264 录制 |
| `annexb_hevc` | 非标准 Annex B HEVC |

### 网络故障

| 场景 | 描述 |
|------|------|
| `network_disconnect_5s` | 网络断开5秒后恢复 |
| `slow_network` | 网络限速到1Mbps |

### 流结构异常

| 场景 | 描述 |
|------|------|
| `resolution_change` | 分辨率变化 1080p→720p |
| `timestamp_jump` | 时间戳向前跳跃10秒 |
| `timestamp_reset` | 时间戳归零 |
| `drop_frames` | 30%丢帧率 |

### 多流测试

| 场景 | 描述 |
|------|------|
| `multi_stream_selection` | 多流选择功能 |

## 测试结果

测试完成后会生成详细报告：

```
==============================================================
测试报告
==============================================================
总计: 11 | 通过: 9 | 失败: 2
--------------------------------------------------------------
✅ PASS  basic_flv_h264  (35.2s)
✅ PASS  basic_flv_hevc  (33.8s)
⚠ FAIL  annexb_hevc  (31.5s)
         错误: 输出文件不可播放
✅ PASS  basic_hls_h264  (34.1s)
✅ PASS  network_disconnect_5s  (68.3s)
...
==============================================================
⚠  2 个测试失败
```

## 与 osrp-stream-tester 集成

### 测试服务器 API

dev 包会调用以下 API：

```
GET  /health             - 健康检查
GET  /api/streams/{id}   - 获取流信息
GET  /live/{name}.flv    - FLV 流
GET  /hls/{name}.m3u8    - HLS 流
```

### 查询参数

流URL支持以下查询参数：

| 参数 | 说明 | 示例 |
|------|------|------|
| `codec` | 视频编码 | `avc`, `hevc`, `hevc-annexb` |
| `quality` | 分辨率 | `1080p`, `720p`, `480p` |
| `duration` | 流时长(秒) | `30`, `60` |

示例：
```
http://localhost:8888/live/test.flv?codec=hevc&quality=1080p&duration=30
```

## 多流支持

dev 平台实现了完整的多流支持。`GetStreamInfos()` 会返回多个流选项：

```go
streams, _ := live.GetStreamInfos()

// 返回：
// - 1080p FLV H.264 (6000kbps)
// - 1080p FLV H.265 (4000kbps)
// - 720p FLV H.264 (3000kbps)
// - 480p FLV H.264 (1500kbps)
// - 1080p HLS H.264 (6000kbps)
// - 720p HLS H.264 (3000kbps)
// - 1080p FLV HEVC-AnnexB (4000kbps) // 特殊测试
```

这使得可以测试 bgo 的流选择功能。

## 扩展场景

### 添加新场景

在 `GetAvailableScenarios()` 中添加：

```go
{
    Name:        "my_custom_test",
    Description: "自定义测试场景",
    Stream: StreamConfig{
        Format:   "flv",
        Codec:    "avc",
        Duration: 60 * time.Second,
        Quality:  "1080p",
    },
    Faults: []FaultConfig{
        {
            Type:     "disconnect",
            At:       20 * time.Second,
            Duration: 10 * time.Second,
        },
    },
    Expected: Expected{
        OutputPlayable:       true,
        MinDuration:          50 * time.Second,
        DownloaderReconnects: true,
    },
},
```

### 导出场景到文件

```go
scenarios := dev.GetAvailableScenarios()
dev.SaveScenarios("./scenarios", scenarios)
// 生成 JSON 文件供 osrp-stream-tester 使用
```

## 依赖

- **osrp-stream-tester**: 测试流服务器
- **ffmpeg**: 用于录制
- **ffprobe**: 用于验证输出

确保 ffmpeg 和 ffprobe 在 PATH 中。

## CI/CD 集成

### GitHub Actions 示例

```yaml
name: BGO Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    
    steps:
    - uses: actions/checkout@v3
    
    - uses: actions/setup-go@v4
      with:
        go-version: '1.23'
    
    - name: Install FFmpeg
      run: sudo apt-get install -y ffmpeg
    
    - name: Start Test Server
      run: |
        go run ./cmd/stream-tester serve --port 8888 &
        sleep 3
      working-directory: ../osrp-stream-tester
    
    - name: Run Tests
      run: go test -tags dev ./src/live/dev/...
    
    - name: Upload Results
      uses: actions/upload-artifact@v3
      with:
        name: test-results
        path: ./test_output/
```

## 故障排查

### 测试服务器连接失败

```
测试服务器不可达: connection refused
```

确保 osrp-stream-tester 正在运行：
```bash
curl http://localhost:8888/health
# 应返回: {"status":"ok"}
```

### FFmpeg 未找到

```
exec: "ffmpeg": executable file not found
```

安装 FFmpeg：
```bash
# macOS
brew install ffmpeg

# Ubuntu
sudo apt install ffmpeg

# Windows
choco install ffmpeg
```

### 输出文件为空

检查流URL是否正确，或测试服务器日志。

## 下一步

1. **已完成：Playwright E2E 测试集成**
   - ✅ 添加 Playwright 到项目
   - ✅ 创建测试配置和基础测试用例
   - ✅ 集成 osrp-stream-tester 作为测试流服务器
   - ✅ 添加 GitHub Actions 工作流

2. **完善 osrp-stream-tester**
   - 实现更多故障注入
   - 添加流控制 API

3. **添加更多场景**
   - 长时间录制（24小时+）
   - 极端网络条件
   - 并发录制测试

## Playwright E2E 测试

### 运行测试

```bash
# 安装依赖（首次）
make install-e2e

# 运行测试
make test-e2e

# 带 UI 运行测试（调试用）
make test-e2e-ui
```

### 测试配置

- 配置文件: `playwright.config.ts`
- 测试目录: `tests/e2e/`
- 测试配置: `tests/e2e/fixtures/test-config.yml`

### 测试用例

| 文件 | 描述 |
|------|------|
| `basic.spec.ts` | 基础 UI 功能测试 |
| `recording.spec.ts` | 直播录制功能测试 |

---

**构建标签**: `dev`  
**最后更新**: 2026-01-25

