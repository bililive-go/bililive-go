#!/bin/sh

HOME=/srv/bililive

chown -R ${PUID}:${PGID} ${HOME}

umask ${UMASK}

exec gosu ${PUID}:${PGID} /usr/bin/bililive-go -c /etc/bililive-go/config.yml
