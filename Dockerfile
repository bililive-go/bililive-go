# syntax=docker/dockerfile:1.7
#
# 支持两种构建模式：
#   CI 模式:   docker-context/ 已由 CI 脚本预下载好二进制和工具，直接 COPY（快）
#   本地模式:  传入 --build-arg tag=vX.Y.Z，容器内自动下载二进制并运行 sync-built-in-tools（兼容 Windows/Mac）
#
# 本地构建示例:
#   docker build --build-arg tag=v0.8.0-rc.3 -t bililive-go .
#   docker build --build-arg tag=v0.8.0-rc.3 --platform linux/arm64 -t bililive-go:arm64 .

FROM ubuntu:22.04
ARG tag
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG TARGETPLATFORM

ENV IS_DOCKER=true
ENV WORKDIR="/srv/bililive"
ENV OUTPUT_DIR="/srv/bililive" \
    CONF_DIR="/etc/bililive-go" \
    PORT=8080

ENV PUID=0 PGID=0 UMASK=022

RUN mkdir -p $OUTPUT_DIR && \
    mkdir -p $CONF_DIR && \
    mkdir -p /opt/bililive/tools && \
    apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    curl \
    tzdata \
    ca-certificates \
    libatomic1 && \
    sh -c '\
    if [ "$TARGETARCH" = "arm" ]; then \
    echo "skip gosu for arm (armv7/armhf)"; \
    else \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends gosu; \
    fi' && \
    sh -c '\
    if [ "$TARGETARCH" = "amd64" ] || [ "$TARGETARCH" = "arm64" ]; then \
    echo "skip apt ffmpeg for $TARGETARCH"; \
    else \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ffmpeg; \
    fi' && \
    cp -r -f /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# ===========================================================================
# 二进制安装（双模式）
# CI 模式: docker-context/bin/ 已包含预编译的 bililive-go → 直接 COPY
# 本地模式: 传入 --build-arg tag=vX.Y.Z → 从 GitHub Releases 下载
# ===========================================================================
COPY docker-context/bin/ /tmp/prebuilt-bin/
RUN set -eux; \
    if [ -f /tmp/prebuilt-bin/bililive-go ]; then \
        echo "=== CI 模式: 使用预编译二进制 ==="; \
        mv /tmp/prebuilt-bin/bililive-go /usr/bin/bililive-go; \
    elif [ -n "${tag}" ]; then \
        echo "=== 本地模式: 从 GitHub Releases 下载 (tag=${tag}) ==="; \
        case $(arch) in \
            aarch64) go_arch=arm64 ;; \
            arm*)    go_arch=arm   ;; \
            i386|i686) go_arch=386 ;; \
            x86_64)  go_arch=amd64 ;; \
            *) echo "Unsupported arch: $(arch)"; exit 1 ;; \
        esac; \
        cd /tmp && \
        curl -sSLO "https://github.com/bililive-go/bililive-go/releases/download/${tag}/bililive-linux-${go_arch}.tar.gz" && \
        tar zxvf "bililive-linux-${go_arch}.tar.gz" "bililive-linux-${go_arch}" && \
        chmod +x "bililive-linux-${go_arch}" && \
        mv "./bililive-linux-${go_arch}" /usr/bin/bililive-go && \
        rm "./bililive-linux-${go_arch}.tar.gz"; \
        if [ "${tag}" != "$(/usr/bin/bililive-go --version 2>&1 | tr -d '\n')" ]; then \
            echo "版本验证失败: 期望 ${tag}，实际 $(/usr/bin/bililive-go --version 2>&1 | tr -d '\n')"; \
            exit 1; \
        fi; \
    else \
        echo "错误: 必须提供预编译二进制 (docker-context/bin/) 或 --build-arg tag=vX.Y.Z"; \
        exit 1; \
    fi; \
    chmod +x /usr/bin/bililive-go; \
    rm -rf /tmp/prebuilt-bin

COPY config.docker.yml $CONF_DIR/config.yml

# ===========================================================================
# 内置工具安装（双模式）
# CI 模式: docker-context/tools/ 已包含预下载的工具 → 直接 COPY
# 本地模式: 使用 bililive-go --sync-built-in-tools-to-path 下载
# ===========================================================================
COPY docker-context/tools/ /opt/bililive/tools/
RUN set -eux; \
    TOOL_COUNT=$(find /opt/bililive/tools -mindepth 1 -not -name '.gitkeep' -print -quit | wc -l); \
    if [ "$TOOL_COUNT" -gt 0 ]; then \
        echo "=== CI 模式: 使用预下载的内置工具 ==="; \
        find /opt/bililive/tools -name '.gitkeep' -delete 2>/dev/null || true; \
    else \
        echo "=== 本地模式: 通过 sync-built-in-tools 下载 ==="; \
        /usr/bin/bililive-go --sync-built-in-tools-to-path /opt/bililive/tools || true; \
    fi

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME $OUTPUT_DIR

EXPOSE $PORT

WORKDIR ${WORKDIR}
ENTRYPOINT [ "sh" ]
CMD [ "/entrypoint.sh" ]
