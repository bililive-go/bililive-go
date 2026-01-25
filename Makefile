
.PHONY: help
help:
	@echo "Available commands:"
	@echo "  make build        - Build release version"
	@echo "  make dev          - Build development version (with debug info)"
	@echo "  make test         - Run unit tests"
	@echo "  make test-e2e     - Run E2E tests with Playwright"
	@echo "  make test-e2e-ui  - Run E2E tests with Playwright UI mode"
	@echo "  make show-report  - Open Playwright test report in browser"
	@echo "  make serve-report - Start report server (fetches source from GitHub)"
	@echo "  make install-e2e  - Install E2E test dependencies"
	@echo "  make build-web    - Build frontend"
	@echo "  make generate     - Run go generate"
	@echo "  make clean        - Clean build artifacts"
	@echo "  make lint         - Run linter"
	@echo "  make release      - Build release for all platforms"

build: bililive
.PHONY: build

bililive:
	@go run build.go release

.PHONY: dev
dev:
	@go run build.go dev

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
	@rm -rf bin ./src/webapp/build
	@echo "All clean"

.PHONY: generate
generate:
	go run build.go generate

.PHONY: build-web
build-web:
	go run build.go build-web

.PHONY: run
run:
	foreman start || exit 0

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
