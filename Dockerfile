ARG TARGETARCH

FROM alpine AS ffmpeg_amd64
RUN echo "Using ffmpeg for amd64"
COPY --from=mwader/static-ffmpeg:8.0 /ffmpeg /usr/local/bin/
COPY --from=mwader/static-ffmpeg:8.0 /ffprobe /usr/local/bin/

FROM alpine AS ffmpeg_arm64
RUN echo "Using ffmpeg for arm64"
COPY --from=mwader/static-ffmpeg:8.0 /ffmpeg /usr/local/bin/
COPY --from=mwader/static-ffmpeg:8.0 /ffprobe /usr/local/bin/

FROM alpine AS ffmpeg_arm
# FFmpeg will be installed later via Alpine packages (apk add ffmpeg) for arm architecture.
RUN echo "Using ffmpeg for arm"

FROM alpine AS ffmpeg_386
# FFmpeg will be installed later via Alpine packages (apk add ffmpeg) for 386 architecture.
RUN echo "Using ffmpeg for 386"

FROM ffmpeg_${TARGETARCH}
ARG TARGETARCH

ARG tag

ENV IS_DOCKER=true
ENV WORKDIR="/srv/bililive"
ENV OUTPUT_DIR="/srv/bililive" \
    CONF_DIR="/etc/bililive-go" \
    PORT=8080

ENV PUID=0 PGID=0 UMASK=022

RUN mkdir -p $OUTPUT_DIR && \
    mkdir -p $CONF_DIR && \
    apk update && \
    apk --no-cache add libc6-compat curl su-exec tzdata && \
    sh -c '\
        if [ "$TARGETARCH" = "amd64" ] || [ "$TARGETARCH" = "arm64" ]; then \
            echo "skip apk ffmpeg for $TARGETARCH"; \
        else \
            apk add --no-cache ffmpeg; \
        fi' && \
    cp -r -f /usr/share/zoneinfo/Asia/Shanghai /etc/localtime

RUN sh -c "case $(arch) in aarch64) go_arch=arm64 ;; arm*) go_arch=arm ;; i386|i686) go_arch=386 ;; x86_64) go_arch=amd64;; esac && \
    cd /tmp && curl -sSLO https://github.com/bililive-go/bililive-go/releases/download/$tag/bililive-linux-\${go_arch}.tar.gz && \
    tar zxvf bililive-linux-\${go_arch}.tar.gz bililive-linux-\${go_arch} && \
    chmod +x bililive-linux-\${go_arch} && \
    mv ./bililive-linux-\${go_arch} /usr/bin/bililive-go && \
    rm ./bililive-linux-\${go_arch}.tar.gz" && \
    sh -c "if [ $tag != $(/usr/bin/bililive-go --version | tr -d '\n') ]; then return 1; fi"

COPY config.docker.yml $CONF_DIR/config.yml

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME $OUTPUT_DIR

EXPOSE $PORT

WORKDIR ${WORKDIR}
ENTRYPOINT [ "sh" ]
CMD [ "/entrypoint.sh" ]
