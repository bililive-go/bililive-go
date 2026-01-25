# 测试工具目录

本目录包含用于本地开发和测试的工具。

## 开发环境设置

首次 clone 项目后，运行以下命令安装开发工具（包括 delve 调试器、gopls 语言服务器等）：

```bash
# 一键安装所有开发工具
go generate ./tools/devtools.go

# 或者分别安装
go install github.com/go-delve/delve/cmd/dlv@latest
go install golang.org/x/tools/gopls@latest
go install honnef.co/go/tools/cmd/staticcheck@latest
```

工具版本已锁定在 `go.mod` 中，确保团队成员使用相同版本。

## update-mock-server

用于测试自动升级功能的 Mock 版本 API 服务器。

### 快速开始

1. 使用 VSCode 的调试配置一键启动：
   - 打开 **Run and Debug** 面板 (Ctrl+Shift+D)
   - 选择 **🚀 本地升级测试 (Mock API + Launcher)**
   - 按 F5 启动

2. 或者手动运行：
   ```bash
   go run ./test/update-mock-server -port 8888 -version 99.0.0
   ```

### 参数说明

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-port` | 8888 | 监听端口 |
| `-version` | 99.0.0 | 模拟的最新版本号 |
| `-changelog` | (环境变量) | 更新日志，也可通过 `MOCK_CHANGELOG` 环境变量设置 |

## launcher-config-local.json

本地测试用的 Launcher 配置文件。

### 使用方法

1. 复制示例文件：
   ```bash
   cp test/launcher-config-local.example.json test/launcher-config-local.json
   ```

2. 根据需要修改配置

3. 使用 VSCode 调试配置 **Debug Launcher (Local Update)**

### 注意事项

- `launcher-config-local.json` 不会被提交到 Git（已在 .gitignore 中）
- 请使用 `launcher-config-local.example.json` 作为模板
