
.PHONY: help
help:
	@echo "Available commands:"
	@echo "  make build            - Build release version"
	@echo "  make dev              - Build development version (with debug info)"
	@echo "  make dev-version VERSION=x.x.x - Build dev with custom version"
	@echo "  make dev-incremental  - Incremental dev build (only rebuild if sources changed)"
	@echo "  make test             - Run unit tests"
	@echo "  make test-e2e         - Run E2E tests with Playwright"
	@echo "  make test-e2e-ui      - Run E2E tests with Playwright UI mode"
	@echo "  make show-report      - Open Playwright test report in browser"
	@echo "  make serve-report     - Start report server (fetches source from GitHub)"
	@echo "  make install-e2e      - Install E2E test dependencies"
	@echo "  make build-web        - Build frontend"
	@echo "  make generate         - Run go generate"
	@echo "  make clean            - Clean build artifacts"
	@echo "  make lint             - Run linter"
	@echo "  make release          - Build release for all platforms"

build: bililive
.PHONY: build

bililive:
	@go run build.go release

.PHONY: dev
dev:
	@go run build.go dev

# 自定义版本号编译（用于本地测试升级功能）
# 用法: make dev-version VERSION=v0.8.0-rc.1
.PHONY: dev-version
dev-version:
ifndef VERSION
	$(error VERSION is required. Usage: make dev-version VERSION=v0.8.0-rc.1)
endif
	@go run build.go dev --version $(VERSION)

# 收集所有 Go 源文件作为依赖（使用通配符，兼容 Windows）
GO_SOURCES := $(wildcard src/**/*.go src/**/**/*.go src/**/**/**/*.go)
GO_MOD_FILES := go.mod go.sum

# 开发版二进制输出路径（跨平台统一名称）
ifeq ($(OS),Windows_NT)
    DEV_BINARY := bin/bililive-dev.exe
else
    DEV_BINARY := bin/bililive-dev
endif

# 增量编译：只在源码变化时重新编译
# Make 会比较目标文件和依赖文件的修改时间，只在需要时执行编译
$(DEV_BINARY): $(GO_SOURCES) $(GO_MOD_FILES)
	@go run build.go dev-incremental

# dev-incremental 目标：便于记忆的别名
.PHONY: dev-incremental
dev-incremental: $(DEV_BINARY)


.PHONY: release
release: build-web generate
	@./src/hack/release.sh

.PHONY: release-no-web
release-no-web: generate
	@./src/hack/release.sh

.PHONY: release-docker
release-docker:
	@./src/hack/release-docker.sh

.PHONY: test
test:
	@go run build.go test

.PHONY: clean
clean:
	@go run build.go clean

.PHONY: generate
generate:
	go run build.go generate

.PHONY: build-web
build-web:
	go run build.go build-web

# run 目标已移除（foreman 是平台特定的）
# 请直接使用 make dev 后运行生成的二进制文件

.PHONY: lint
lint:
	golangci-lint run --path-mode=abs --build-tags=dev

# 同步 AGENTS.md 到其他 AI 指示文件
.PHONY: sync-agents
sync-agents:
	@go run build.go sync-agents

# 检查 AI 指示文件是否一致（用于 CI）
.PHONY: check-agents
check-agents:
	@go run build.go check-agents

# E2E 测试（使用 Playwright）
.PHONY: test-e2e
test-e2e:
	npx playwright test

# 安装 E2E 测试依赖
.PHONY: install-e2e
install-e2e:
	yarn install --frozen-lockfile
	npx playwright install --with-deps chromium

# 运行 E2E 测试（带 UI）
.PHONY: test-e2e-ui
test-e2e-ui:
	npx playwright test --ui

# 查看 E2E 测试报告（带源码支持，需要本地源码）
.PHONY: show-report
show-report:
	npx playwright show-report playwright-report

# 启动报告服务器（可从 GitHub 获取源码，适合查看 CI 报告）
# 用法: 
#   make serve-report                    # 本地模式
#   make serve-report COMMIT=abc123      # 从 GitHub 获取源码
.PHONY: serve-report
serve-report:
	@node scripts/report-server.js $(if $(COMMIT),--commit $(COMMIT),)
