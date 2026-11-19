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

---

## 自动更新测试

### 测试场景

bililive-go 的自动更新流程涉及以下组件：

1. **主程序** - 检测新版本、下载更新包、通知 Launcher
2. **Launcher** - 接收更新请求、验证 SHA256、解压替换、重启主程序

### 方法 1：一键启动（推荐）

使用 VS Code 的复合调试配置，同时启动 Mock Server 和主程序：

1. 打开 **Run and Debug** 面板 (`Ctrl+Shift+D`)
2. 选择 **🔄 主程序更新测试 (Mock API + Main Program)**
3. 按 `F5` 启动

这会：
- 先启动 Mock 版本 API 服务器
- 再启动主程序（配置使用本地 Mock API）

启动后，在主程序的 Web UI 中：
1. 访问 `http://localhost:8080`（端口取决于你的 config.yml）
2. 点击 **关于** 或 **设置** 中的更新检测功能
3. 应该能看到检测到"新版本 99.0.0"
4. 点击下载，观察下载进度
5. 下载完成后可以应用更新

### 方法 2：分步调试

如果需要单步调试具体代码：

#### 步骤 1：启动 Mock 服务器

```bash
# 先构建开发版本作为"新版本"的源文件
make dev-incremental

# 启动 Mock 服务器
go run ./test/update-mock-server -port 8888 -version 99.0.0
```

Mock 服务器启动后会显示：
```
═══════════════════════════════════════════════════════════════
  🚀 Mock 版本 API 服务器已启动
═══════════════════════════════════════════════════════════════
  监听地址:    http://localhost:8888
  模拟版本:    99.0.0
  更新包大小:  35.24 MB
  SHA256:      abc123...
═══════════════════════════════════════════════════════════════
```

#### 步骤 2：调试主程序更新检测

在 VS Code 中：
1. 选择 **Debug Main Program (Local Update Test)**
2. 在 `src/pkg/update/checker.go` 或 `src/servers/update_handler.go` 设置断点
3. 按 `F5` 启动调试
4. 在 Web UI 中触发更新检测

#### 步骤 3：调试 Launcher 更新应用

如果需要测试 Launcher 的更新解压和替换逻辑：
1. 选择 **Debug Launcher (Local Update)**
2. 在 `src/cmd/launcher/main.go` 的 `performUpdate` 函数设置断点

---

## update-mock-server

用于测试自动升级功能的 Mock 版本 API 服务器。它已整合原 `scripts/update-server.py` 的跨主机测试能力，完整操作流程、参数说明和故障排查请阅读 [本地自动升级测试 Skill](../.agents/skills/test-local-update/SKILL.md)。

### 功能

- **版本检测 API** - 模拟 `bililive-go.com/api/versions` 接口
- **两种包来源** - 自动将本地二进制打包成 zip，或直接提供已有 zip/tar.gz 升级包
- **跨主机测试** - 分别设置监听地址和被测容器/NAS 可访问的公开地址
- **完整下载语义** - 支持 GET、HEAD、Range、正确的 Content-Type 和 URL 转义
- **完整包元信息** - 返回实际文件名、大小和 SHA256 供客户端校验
- **本地配置** - 支持命令行、系统环境变量及被 Git 忽略的变量文件

### 快速运行

```bash
# 自动打包 bin/bililive-dev（Windows 为 bin/bililive-dev.exe）
make dev-incremental
go run ./test/update-mock-server

# 使用已有发布包，供局域网中的容器或 NAS 访问
go run ./test/update-mock-server \
  -host 0.0.0.0 \
  -port 8099 \
  -public-host 192.0.2.10 \
  -package bin/bililive-linux-amd64.tar.gz \
  -version 99.0.0-local.1 \
  -prerelease \
  -always-update
```

实际使用时，将示例地址替换为被测设备能访问的开发机地址，但不要将真实地址提交到 Git。服务器启动日志会打印应设置的完整 `VERSION_API_URL`。

### 变量文件

```bash
cp test/update-mock-server.env.example test/update-mock-server.env
# 编辑实际值后运行；该实际文件已被 .gitignore 忽略
go run ./test/update-mock-server
```

配置优先级为“命令行参数 > 当前进程环境变量 > 变量文件 > 默认值”。查看全部最新参数：

```bash
go run ./test/update-mock-server -help
```

---

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
