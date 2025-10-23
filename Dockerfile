ARG TARGETARCH

FROM ubuntu:22.04 AS ffmpeg_amd64
RUN echo "Using static-ffmpeg for amd64"
COPY --from=mwader/static-ffmpeg:8.0 /ffmpeg /usr/local/bin/
COPY --from=mwader/static-ffmpeg:8.0 /ffprobe /usr/local/bin/

FROM ubuntu:22.04 AS ffmpeg_arm64
RUN echo "Using static-ffmpeg for arm64"
COPY --from=mwader/static-ffmpeg:8.0 /ffmpeg /usr/local/bin/
COPY --from=mwader/static-ffmpeg:8.0 /ffprobe /usr/local/bin/

FROM ubuntu:22.04 AS ffmpeg_arm
# FFmpeg will be installed later via Ubuntu packages (apt-get install ffmpeg) for arm architecture.
RUN echo "Using ffmpeg for arm"

FROM ubuntu:22.04 AS ffmpeg_386
# FFmpeg will be installed later via Ubuntu packages (apt-get install ffmpeg) for 386 architecture.
RUN echo "Using ffmpeg for 386"

FROM ffmpeg_${TARGETARCH}
ARG TARGETARCH

# 控制是否使用本地已编译好的二进制；默认为从发布页下载
ARG USE_LOCAL=false
ARG tag

ENV IS_DOCKER=true
ENV WORKDIR="/srv/bililive"
ENV OUTPUT_DIR="/srv/bililive" \
    CONF_DIR="/etc/bililive-go" \
    PORT=8080

ENV PUID=0 PGID=0 UMASK=022

RUN mkdir -p $OUTPUT_DIR && \
    mkdir -p $CONF_DIR && \
    apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    curl \
    gosu \
    tzdata \
    ca-certificates && \
    sh -c '\
    if [ "$TARGETARCH" = "amd64" ] || [ "$TARGETARCH" = "arm64" ]; then \
    echo "skip apt ffmpeg for $TARGETARCH"; \
    else \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ffmpeg; \
    fi' && \
    cp -r -f /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# 使用本地产物或从发布页下载对应架构的二进制（使用 BuildKit 挂载本地 bin/，不会写入镜像层）
RUN --mount=type=bind,source=bin,target=/tmp/localbin,ro \
    set -eux; \
    case $(arch) in \
    aarch64) go_arch=arm64 ;; \
    arm*)    go_arch=arm   ;; \
    i386|i686) go_arch=386 ;; \
    x86_64)  go_arch=amd64 ;; \
    *) echo "Unsupported arch: $(arch)"; exit 1 ;; \
    esac; \
    if [ "${USE_LOCAL}" = "true" ] && [ -f "/tmp/localbin/bililive-linux-${go_arch}" ]; then \
    echo "Using local binary: /tmp/localbin/bililive-linux-${go_arch}"; \
    install -m 0755 "/tmp/localbin/bililive-linux-${go_arch}" /usr/bin/bililive-go; \
    else \
    echo "Downloading release ${tag} for arch ${go_arch}"; \
    cd /tmp && curl -sSLO "https://github.com/bililive-go/bililive-go/releases/download/${tag}/bililive-linux-${go_arch}.tar.gz" && \
    tar zxvf "bililive-linux-${go_arch}.tar.gz" "bililive-linux-${go_arch}" && \
    chmod +x "bililive-linux-${go_arch}" && \
    mv "./bililive-linux-${go_arch}" /usr/bin/bililive-go && \
    rm "./bililive-linux-${go_arch}.tar.gz"; \
    fi; \
    if [ "${USE_LOCAL}" != "true" ]; then \
    if [ "${tag}" != "$(/usr/bin/bililive-go --version | tr -d '\n')" ]; then exit 1; fi; \
    fi

COPY config.docker.yml $CONF_DIR/config.yml

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME $OUTPUT_DIR

EXPOSE $PORT

WORKDIR ${WORKDIR}
ENTRYPOINT [ "sh" ]
CMD [ "/entrypoint.sh" ]
