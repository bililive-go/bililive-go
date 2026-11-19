---
name: test-local-update
description: 使用仓库内的 Go Mock 版本 API 测试 bililive-go 本地自动升级。用户要求启动、配置、验证或排查本地版本检测、升级包下载、SHA256、Range 断点续传、Launcher 热更新、容器或 NAS 跨主机升级时使用。
---

# 本地自动升级测试

使用 `test/update-mock-server/main.go` 启动 Mock 版本 API。该工具既能把当前平台的开发二进制临时打包成 zip，也能直接提供已有的 zip 或 tar.gz 发布包。

除非用户明确要求只查看文档，否则按下面的流程实际运行命令并报告结果。不得把本机 IP、私有主机名或本地绝对路径写入受 Git 跟踪的文件。

## 快速选择测试方式

根据被测环境选择升级包来源：

1. **同一台开发机上调试主程序或 Launcher**：使用 `-source` 自动打包当前平台二进制。
2. **从容器、NAS 或另一台机器测试真实发布包**：使用 `-package` 直接提供已构建的 zip/tar.gz 包，并设置外部可访问的监听地址和公开地址。
3. **需要反复使用同一组参数**：复制变量文件示例，实际值放入已被 Git 忽略的 `test/update-mock-server.env`。

`-source` 与 `-package` 互斥。如果都不设置，服务器默认自动打包当前平台的 `bin/bililive-dev`（Windows 为 `bin/bililive-dev.exe`）。

## 标准工作流

### 1. 在仓库根目录准备二进制或升级包

自动打包本机开发二进制：

```bash
make dev-incremental
test -f bin/bililive-dev || test -f bin/bililive-dev.exe
```

如果测试 Linux NAS 的发布包，先按项目既有构建流程生成类似下面的文件：

```text
bin/bililive-linux-amd64.tar.gz
```

不要在 Windows 或 macOS 上把本机二进制自动打包后交给 Linux NAS；跨平台测试应传入对应平台的发布包。

### 2. 启动 Mock 服务器

本机测试的最短命令：

```bash
go run ./test/update-mock-server
```

显式指定本机开发二进制和版本：

```bash
go run ./test/update-mock-server \
  -source bin/bililive-dev \
  -version 99.0.0-local.1 \
  -changelog "本地升级测试版本"
```

Windows PowerShell 使用 `bin/bililive-dev.exe`，并按 PowerShell 语法换行；也可以写成一行。

容器或 NAS 测试已有升级包：

```bash
go run ./test/update-mock-server \
  -host 0.0.0.0 \
  -port 8099 \
  -public-host 192.0.2.10 \
  -package bin/bililive-linux-amd64.tar.gz \
  -version 99.0.0-local.1 \
  -prerelease \
  -always-update
```

将示例地址 `192.0.2.10` 替换为被测容器或设备能访问的开发机地址。不要把实际地址提交到 Git。

服务器启动后会输出：

- 实际监听地址；
- 版本检测 API；
- 升级包下载 URL；
- 版本号、文件大小和 SHA256；
- 应传给被测 bgo 的完整 `VERSION_API_URL`。

保持该进程运行，完成测试后按 `Ctrl+C`。服务器会优雅关闭并删除自动生成的临时 zip。

### 3. 让被测 bgo 使用 Mock API

本机启动主程序时设置：

```bash
make dev
VERSION_API_URL=http://localhost:8888/api/versions \
  ./bin/bililive-$(go env GOOS)-$(go env GOARCH) -c config.yml
```

Windows 或需要断点调试时优先使用下面的 VSCode 配置，避免手动判断可执行文件名。

也可以复制 `.vscode/launch.example.json` 为 `.vscode/launch.json`，然后使用以下配置：

- `🔄 主程序更新测试 (Mock API + Main Program)`：同时启动 Mock API 与主程序；
- `🚀 本地升级测试 (Mock API + Launcher)`：同时启动 Mock API 与 Launcher；
- `Debug Main Program (Local Update Test)`：只调试主程序的更新检测。

容器或 NAS 中应在创建或重建容器时设置服务器日志中打印的值，例如：

```yaml
environment:
  VERSION_API_URL: http://192.0.2.10:8099/api/versions
```

`localhost` 在容器内指向容器本身，不能用来访问宿主机。确认宿主机防火墙允许对应端口，并确保 `-host` 不是 `127.0.0.1`。当监听地址仅为环回地址而 `-public-host` 是外部地址时，服务器会打印警告。

### 4. 验证端点

检查服务状态：

```bash
curl -i http://localhost:8888/health
```

模拟版本检查：

```bash
curl "http://localhost:8888/api/versions?current=0.8.0&platform=linux-amd64"
```

从返回 JSON 的 `download.urls[0]` 下载，或直接验证断点续传：

```bash
curl -I http://localhost:8888/download/<升级包文件名>
curl -i -H 'Range: bytes=0-99' http://localhost:8888/download/<升级包文件名>
```

预期结果：

- `/`、`/health`、`/api/versions` 和正确的 `/download/<文件名>` 支持 `GET` 与 `HEAD`；
- Range 请求返回 `206 Partial Content`、`Content-Range` 和 `Accept-Ranges: bytes`；
- zip 与 tar.gz 使用各自正确的响应类型；
- API 中的 `filename`、`size` 和 `sha256` 与实际升级包一致；
- IPv6 公开地址在 URL 中自动添加方括号，特殊文件名会进行 URL 转义。

### 5. 验证应用升级流程

1. 打开 bgo Web UI 的更新页面，或调用项目已有的更新检查 API。
2. 确认发现 `-version` 指定的版本，并显示 `-changelog` 内容。
3. 触发“立即更新”，观察 Mock 服务器的版本检查和下载日志。
4. 确认客户端完成 SHA256 校验、解压并启动目标二进制。
5. 调试 Launcher 时，可在 `src/cmd/launcher/main.go` 的更新应用逻辑设置断点；Launcher 配置可从 `test/launcher-config-local.example.json` 复制。

## 使用变量文件

复制不会包含真实本机信息的示例：

```bash
cp test/update-mock-server.env.example test/update-mock-server.env
```

编辑 `test/update-mock-server.env` 后直接运行：

```bash
go run ./test/update-mock-server
```

实际变量文件已在 `.gitignore` 中忽略。运行前仍应使用 `git status --short --ignored test/` 确认它显示为 ignored，而不是把敏感值加入 Git。

配置优先级为：

```text
命令行参数 > 当前进程环境变量 > 变量文件 > 默认值
```

默认读取 `test/update-mock-server.env`。可用以下任一方式指定其他变量文件：

```bash
go run ./test/update-mock-server -env-file /path/to/local.env
BGO_UPDATE_ENV_FILE=/path/to/local.env go run ./test/update-mock-server
```

变量文件支持空行、`#` 注释、可选的 `export ` 前缀，以及单引号或双引号包裹的完整值；它不是完整 shell 脚本，不进行变量展开或命令替换。

## 参数与环境变量

| 命令行参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-env-file` | `BGO_UPDATE_ENV_FILE` | `test/update-mock-server.env` | 变量文件，显式指定但不存在时会报错 |
| `-host` | `BGO_UPDATE_BIND_HOST` | `127.0.0.1` | HTTP 监听地址；IPv4、IPv6、主机名均可 |
| `-port` | `BGO_UPDATE_PORT` | `8888` | 监听端口，范围 1–65535 |
| `-public-host` | `BGO_UPDATE_PUBLIC_HOST` | `localhost` | 写入下载 URL、供被测进程访问的地址 |
| `-version` | `BGO_UPDATE_VERSION` | `99.0.0` | API 返回的目标版本号 |
| `-changelog` | `BGO_UPDATE_CHANGELOG` | 本地测试说明 | API 返回的更新日志；仍兼容旧的 `MOCK_CHANGELOG` |
| `-prerelease` | `BGO_UPDATE_PRERELEASE` | `false` | 把版本标记为预发布版 |
| `-always-update` | `BGO_UPDATE_ALWAYS_UPDATE` | `false` | 不比较 `current`，始终返回有更新 |
| `-source` | `BGO_UPDATE_SOURCE` | 当前平台 `bin/bililive-dev[.exe]` | 自动打包的本机二进制 |
| `-package` | `BGO_UPDATE_PACKAGE` | 空 | 直接提供已有 zip/tar.gz 升级包 |

所有相对文件路径都相对仓库根目录解析，因此从仓库子目录运行也不会改变含义。布尔命令行参数启用时可写 `-prerelease`；显式关闭应写 `-prerelease=false`。查看 Go flag 自动生成的最新帮助：

```bash
go run ./test/update-mock-server -help
```

## 常见问题

### 客户端没有发现更新

- 默认逻辑仅在请求包含 `current` 且它与 `-version` 不同时返回 `update_available=true`。
- 要专注测试下载与安装流程，使用 `-always-update`。
- 预发布测试需同时让 Mock 返回 `prerelease=true`，并在 bgo 配置中允许预发布版本。
- 检查 bgo 日志中的实际 `VERSION_API_URL`，避免它回退到线上 API。

### 容器或 NAS 无法连接

- `-host` 使用 `0.0.0.0` 或 `::`；`-public-host` 使用被测设备能访问的宿主机 IP/主机名。
- 不要把 `0.0.0.0` 或 `::` 当作客户端下载地址。
- 检查端口映射、路由、VPN 和宿主机防火墙。
- 使用 `curl http://<公开地址>:<端口>/health` 从被测网络侧验证。

### 报告找不到源文件或升级包

- 路径相对仓库根目录，不相对当前 shell 目录。
- `-source` 需要普通二进制文件；先运行 `make dev-incremental`。
- `-package` 应指向已经生成的单个 zip 或 tar.gz 文件。
- 不得同时配置 `BGO_UPDATE_SOURCE` 与 `BGO_UPDATE_PACKAGE`。复制示例后，请注释掉不用的一项。

### 下载或解压失败

- 比对 API 中的 SHA256 与 `sha256sum <升级包>`。
- 确认包扩展名和实际格式一致；客户端根据扩展名选择解压方式。
- 确认包内顶层二进制名称符合目标平台，例如 `bililive-linux-amd64`。
- 用 Range 命令检查反向代理或网络设备是否保留断点续传响应头。

## 修改工具后的验证

如果修改了 `test/update-mock-server` 的 Go 代码，至少执行：

```bash
gofmt -w test/update-mock-server/*.go
go test ./test/update-mock-server
make dev
```

提交前遵循仓库 `AGENTS.md`，完整执行：

```bash
make build-web dev
make lint
make test
```
