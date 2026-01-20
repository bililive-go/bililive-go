# syntax=docker/dockerfile:1.7

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

RUN set -x; \
    case $(arch) in \
    aarch64) go_arch=arm64 ;; \
    arm*)    go_arch=arm   ;; \
    i386|i686) go_arch=386 ;; \
    x86_64)  go_arch=amd64 ;; \
    *) echo "Unsupported arch: $(arch)"; exit 1 ;; \
    esac; \
    echo "Downloading release ${tag} for arch ${go_arch}"; \
    cd /tmp && curl -sSLO "https://github.com/bililive-go/bililive-go/releases/download/${tag}/bililive-linux-${go_arch}.tar.gz" && \
    tar zxvf "bililive-linux-${go_arch}.tar.gz" "bililive-linux-${go_arch}" && \
    chmod +x "bililive-linux-${go_arch}" && \
    mv "./bililive-linux-${go_arch}" /usr/bin/bililive-go && \
    rm "./bililive-linux-${go_arch}.tar.gz"; \
    if [ "${tag}" != "$(/usr/bin/bililive-go --version 2>&1 | tr -d '\n')" ]; then exit 1; fi

# For local testing: copy pre-built binary from build context instead of downloading
# COPY bin/bililive-linux-amd64 /usr/bin/bililive-go
# RUN chmod +x /usr/bin/bililive-go

COPY config.docker.yml $CONF_DIR/config.yml

RUN --mount=type=cache,id=bililive-tools-$TARGETOS-$TARGETARCH$TARGETVARIANT,sharing=locked,target=/cache/bililive/tools \
    set -eux; \
    echo "Preparing bililive tools cache for bililive-tools-$TARGETOS-$TARGETARCH$TARGETVARIANT..."; \
    mkdir -p /opt/bililive/tools /cache/bililive/tools; \
    ls -lR /cache/bililive/tools || true; \
    /usr/bin/bililive-go --sync-built-in-tools-to-path /cache/bililive/tools || true; \
    cp -a /cache/bililive/tools/. /opt/bililive/tools/

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME $OUTPUT_DIR

EXPOSE $PORT

WORKDIR ${WORKDIR}
ENTRYPOINT [ "sh" ]
CMD [ "/entrypoint.sh" ]
