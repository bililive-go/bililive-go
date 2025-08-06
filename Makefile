
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