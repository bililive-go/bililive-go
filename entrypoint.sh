#!/bin/sh

HOME=/srv/bililive

chown -R ${PUID}:${PGID} ${HOME}

umask ${UMASK}

# Detect runtime architecture; on armv7/armhf skip gosu and run directly
ARCH_UNAME="$(uname -m 2>/dev/null || echo unknown)"
ARCH_DPKG="$(dpkg --print-architecture 2>/dev/null || echo unknown)"

case "${ARCH_UNAME}:${ARCH_DPKG}" in
	armv7l:armhf|armv6l:armhf|armv7l:unknown|armv6l:unknown)
		echo "[entrypoint] armv7/armhf detected (${ARCH_UNAME}/${ARCH_DPKG}), starting without gosu"
		exec /usr/bin/bililive-go -c /etc/bililive-go/config.yml
		;;
	*)
		exec gosu ${PUID}:${PGID} /usr/bin/bililive-go -c /etc/bililive-go/config.yml
		;;
esac
