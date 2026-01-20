# AI 开发指南

本文档为在此项目中工作的 AI 助手（如 GitHub Copilot、Gemini、Claude、Codex、Antigravity 等）提供指导。

> **注意**：本文件是 AI 指示的唯一源文件。修改后请运行 `make sync-agents` 同步到其他位置：
> - `.github/copilot-instructions.md` (GitHub Copilot)
> - `.agent/rules/gemini-guide.md` (Gemini CLI)
> - `.gemini/MEMORY.md` (Antigravity)

## 语言要求

永远使用中文进行交流，包括代码注释和AI生成的markdown文本。

## 编译说明

### 完整编译（前端 + 后端）
```bash
make build-web dev
```

### 单独编译前端
```bash
make build-web
```

### 单独编译后端（开发模式）
```bash
make dev
```

### 重要：Go 编译的 build tags

本项目的 Go 编译**必须指定 build tag** 才能正常编译：
- **开发模式**: 使用 `make dev` 或手动编译时添加 `-tags dev`
- **发布模式**: 使用 `make build` 或手动编译时添加 `-tags release`

⚠️ **不带 tag 直接运行 `go build ./src/cmd/bililive` 会导致编译失败**，因为项目中有些源文件依赖 build tag 来选择开发模式或发布模式的实现。

## 代码检查

### Lint 检测
在修改 Go 代码后，请运行 `make lint` 来检测代码风格和潜在问题：
```bash
make lint
```

lint 检测需要先安装 golangci-lint。如果你的环境中没有安装，可以通过以下方式安装：
```bash
# 使用 go install
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# 或在 Ubuntu/Debian 上
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
```

常见的 lint 问题包括：
- 未使用的变量或导入
- 错误处理不当（如忽略 error 返回值）
- 代码格式问题

### 运行测试
```bash
make test
```

## 提交前检查

提交 commit 之前请确保：
1. 前后端都没有编译错误（`make build-web dev`）
2. Go 代码通过 lint 检测（`make lint`）
3. 测试全部通过（`make test`）

## AI 编码要求

**重要**：在完成代码修改后，AI 助手必须运行 `make build-web dev` 验证编译是否通过。

### 前端编译注意事项
- TypeScript/React 代码必须通过 ESLint 检查，**不允许有 Error 级别的警告**
- 常见问题：
  - 未使用的变量或导入（使用 `// eslint-disable-next-line` 或删除未使用代码）
  - React Hook 依赖项缺失（添加正确的依赖项或使用 `// eslint-disable-next-line react-hooks/exhaustive-deps`）
  - 类型不匹配（检查 TypeScript 类型定义）

### 后端编译注意事项
- Go 代码必须能通过 `-tags dev` 编译
- 确保新增的包导入已使用
- 确保接口方法签名正确

### 配置修改同步要求

修改 `configs/config.go` 中的配置结构体时，必须同步修改以下文件：

1. **配置注释文件**: `configs/config_comments.go`
   - 为新增的配置项添加中文注释说明

2. **前端配置页面**: `webapp/src/component/config-info/index.tsx`
   - 在 `EffectiveConfig` 接口中添加对应的类型定义
   - 在 `GlobalSettings` 组件中添加配置项的 UI 控件

3. **后端API**: `servers/handler.go`
   - 在 `applyConfigUpdates` 函数中处理新增的配置字段

### 层级配置注意事项

对于支持三级覆盖的配置（全局 → 平台 → 房间），需要：

1. 在所有三个层级的UI中都添加对应的配置项：
   - `GlobalSettings` 组件 - 全局配置
   - `PlatformConfigForm` 组件 - 平台级配置
   - `RoomConfigForm` 组件 - 房间级配置

2. 使用 `InheritanceIndicator` 组件显示继承关系

3. 确保 `OverridableConfig` 结构体包含该字段（如果适用）

**当前支持的层级配置**：
- `stream` - 流选择配置（格式优先级、分辨率优先级、码率限制、编码偏好）
- `interval` - 检测间隔
- `out_put_path` - 输出路径
- `ffmpeg_path` - FFmpeg路径
- `out_put_tmpl` - 输出文件名模板
- `video_split_strategies` - 视频分割策略
- `on_record_finished` - 录制完成后动作
- `timeout_in_us` - 超时设置

## 环境准备（用于 CI/CD 等自动化环境）

如果你运行在自动化环境中（如 GitHub Actions），请确保安装以下依赖：
- Go 1.24.7+
- Node.js 20+
- Yarn
- FFmpeg
- golangci-lint

详见 `.github/workflows/copilot-setup-steps.yml` 中的安装步骤。
