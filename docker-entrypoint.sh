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
    # RECURSIVE AND UNCONDITIONAL, BECAUSE THE CLEVER FORM DID NOTHING.
    # This used `find … ! -uid … -exec chown … +`, which reads better, skips work
    # on a warm restart and is what the flag means on GNU find — and on the
    # IMAGE'S OWN SHELL IT DOES NOT EXIST: the runtime is Alpine, BusyBox's find
    # has no -uid, and the error went into the stderr this line swallowed. So
    # every start repaired ownership of nothing, silently, and a fresh volume
    # stayed root-owned while the panel ran as PUID and could not write its own
    # data directory — visible only as the panel's own warnings about the files
    # it could not store.
    #
    # chown -R is the same job with no dialect to get wrong, and chowning
    # something the target already owns is a no-op, which is what the find was
    # optimizing for.
    #
    # A FAILURE IS STILL NOT FATAL — a read-only mount must not stop the panel
    # from starting — but it is no longer SILENT: an operator cannot tell a
    # repair that worked from one that never ran.
    chown -R "$PUID:$PGID" /app/config /app/data || \
        echo "passwall-sub-panel: could not repair ownership of /app/config or /app/data; the panel may be unable to write them" >&2
    # Replace PID 1 with the panel running unprivileged. exec => the binary is
    # PID 1 and receives SIGTERM directly, preserving graceful drain. su-exec
    # takes a numeric UID:GID, so the target need not exist in /etc/passwd.
    exec su-exec "$PUID:$PGID" /app/psp "$@"
fi

# Already non-root (operator set compose `user:` / `docker run --user`): we
# cannot chown, so honor the supplied UID and run as-is.
exec /app/psp "$@"
