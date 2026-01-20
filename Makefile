
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