FROM alpine

ARG tag

ENV WORKDIR="/srv/bililive"
ENV OUTPUT_DIR="/srv/bililive" \
    CONF_DIR="/etc/bililive-go" \
    PORT=8080

ENV PUID=0 PGID=0 UMASK=022

RUN mkdir -p $OUTPUT_DIR && \
    mkdir -p $CONF_DIR && \
    apk update && \
    apk --no-cache add ffmpeg libc6-compat curl su-exec tzdata && \
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
