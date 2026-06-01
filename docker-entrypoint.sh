#!/bin/sh
# Self-healing ownership fix for the bind-mounted /app/config (and the named
# /app/data volume), then drop to a non-root user before running the panel.
#
# Why this exists: docker-compose bind-mounts ./config from the host. Docker
# never chowns a bind mount, so a freshly auto-created (or upgrade-leftover,
# root-era) ./config arrives root-owned and a non-root UID can't write
# config.yaml, seed rulesets/templates, or the geoip dir -> the panel
# crash-loops. Fixing that needs root once, so the image STARTS as root, this
# script repairs ownership, then irrevocably drops privileges via su-exec.
# (Same root-then-drop pattern as the official postgres/redis/gitea images.)
#
# PUID/PGID (default 10001) let an operator align the in-container UID with
# their host user so ./config stays editable on the host without sudo:
#   environment: { PUID: "1000", PGID: "1000" }   # = host `id -u` / `id -g`
set -e

PUID="${PUID:-10001}"
PGID="${PGID:-10001}"

if [ "$(id -u)" = "0" ]; then
    mkdir -p /app/config /app/data
    # Only chown entries NOT already owned by the target UID, so warm restarts
    # do ~no work and only genuinely root-owned (fresh/upgrade) files get
    # touched.
    find /app/config /app/data \! -uid "$PUID" -exec chown "$PUID:$PGID" {} + 2>/dev/null || true
    # Replace PID 1 with the panel running unprivileged. exec => the binary is
    # PID 1 and receives SIGTERM directly, preserving graceful drain. su-exec
    # takes a numeric UID:GID, so the target need not exist in /etc/passwd.
    exec su-exec "$PUID:$PGID" /app/psp "$@"
fi

# Already non-root (operator set compose `user:` / `docker run --user`): we
# cannot chown, so honor the supplied UID and run as-is.
exec /app/psp "$@"
