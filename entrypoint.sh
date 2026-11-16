#!/bin/sh

HOME=/srv/bililive

chown -R ${PUID}:${PGID} ${HOME}
chown -R ${PUID}:${PGID} /etc/bililive-go
chown -R ${PUID}:${PGID} /opt/bililive

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
		# PUID 不在 /etc/passwd 中时，gosu 会把 HOME 设为不可写的 "/"，
		# 这里改为数据目录，避免子进程（如 node）找不到可用的主目录（#1241）
		if getent passwd "${PUID}" >/dev/null 2>&1; then
			exec gosu ${PUID}:${PGID} /usr/bin/bililive-go -c /etc/bililive-go/config.yml
		else
			exec gosu ${PUID}:${PGID} env HOME="${HOME}" /usr/bin/bililive-go -c /etc/bililive-go/config.yml
		fi
		;;
esac
