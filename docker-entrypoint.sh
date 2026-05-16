#!/bin/sh
# Container entrypoint: seed /app/config from baked-in defaults on first
# start, then exec the panel binary.
#
# Why this exists: docker-compose.yml bind-mounts ./config from the host into
# /app/config. Bind mounts (unlike named volumes) do NOT copy the image's
# pre-existing /app/config contents into an empty host directory — they just
# replace it with whatever the host has, which is nothing on first launch.
# Without seeding, templates/ and rulesets/ would be missing and subscription
# rendering would 500 the moment the first user tries to fetch a config.
#
# cp -rn = recursive, no-clobber: anything the user has already placed under
# /app/config (e.g. an edited config.yaml or a custom template) is preserved
# across restarts.
set -e

if [ -d /app/defaults ]; then
    cp -rn /app/defaults/. /app/config/ 2>/dev/null || true
fi

exec "$@"
