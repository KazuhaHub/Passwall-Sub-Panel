#!/usr/bin/env bash
#
# Starts an isolated third-party panel for the live adapter tests, mints a
# credential, and prints the environment those tests read.
#
# WHY A SCRIPT AND NOT A COMPOSE FILE. Neither panel is usable the moment its
# container is up, and both need things that are not obvious from the image:
#
#   - 3X-UI 3.8.x keeps API tokens in a dedicated `api_tokens` table (name,
#     scope, expiry) and only ever shows the plaintext once, so a token has to be
#     created THROUGH THE PANEL, which means a cookie session plus the
#     `X-CSRF-Token` header the login POST now requires.
#   - Its shared-client tests SKIP unless the panel already holds two inbounds,
#     and a skip is indistinguishable from a pass to anything reading the exit
#     code — see deploy/compat/check-go-results.mjs.
#   - Its traffic-floor matrix writes usage straight into the panel's SQLite
#     file, so it needs PSP_LIVE_XUI_DB and write access to the DIRECTORY, not
#     just the file: SQLite creates its journal beside it.
#   - S-UI serves the panel under a configurable webPath. On a fresh install that
#     is `/app/`, so the API is at `/app/apiv2/...`. Pointing the tests at the
#     bare origin produces a 404 on every call, which reads like a broken
#     adapter rather than a wrong base URL.
#
# A NOTE ON `[ cond ] && cmd`: under `set -e` that list returns 1 when cond is
# FALSE, which exits the script silently. Every wait loop below uses `if`, so a
# loop waits rather than dying on its first empty read.

# Everything here is pinned to a digest-carrying tag by the caller. Nothing
# touches the host's docker state beyond this case's own container and volume.
set -euo pipefail

RUNTIME="${PSP_CONTAINER_RUNTIME:-}"
if [ -z "$RUNTIME" ]; then
  for candidate in docker nerdctl podman; do
    if command -v "$candidate" >/dev/null 2>&1; then RUNTIME="$candidate"; break; fi
  done
fi
[ -n "$RUNTIME" ] || { echo "no container runtime found (docker/nerdctl/podman)" >&2; exit 2; }

SUDO=""
if ! "$RUNTIME" info >/dev/null 2>&1; then SUDO="sudo"; fi

# The privilege needed to READ a container's files is a different question from the
# one needed to TALK to the runtime. On a runner docker needs none, so $SUDO is
# empty, while the database and the -shm/-wal beside it belong to the container's
# root and refuse an unprivileged read — which surfaced as a panel that never
# published a webPath. The two are decided separately for that reason.
# Unconditional where sudo exists, NOT tied to $SUDO. Gating it on $SUDO got the
# two environments backwards: in the VM the runtime needs sudo so $SUDO is set and
# this would have been left empty, reading as an unprivileged user a database that
# belongs to root. sudo is passwordless on the runner and here.
DB_SUDO=""
if command -v sudo >/dev/null 2>&1; then DB_SUDO="sudo"; fi

THIRD_PARTY_IMAGE_3XUI="${PSP_LIVE_3XUI_IMAGE:-ghcr.io/mhsanaei/3x-ui:latest}"
THIRD_PARTY_IMAGE_SUI="${PSP_LIVE_SUI_IMAGE:-ghcr.io/alireza0/s-ui:latest}"
# A STABLE path, not a fresh mktemp per invocation: `env` and `down` have to find
# what `up` created, and a per-run temp directory silently made them look at an
# empty one — which reads as "the credential was never minted".
WORKDIR="${PSP_BACKEND_WORKDIR:-/tmp/psp-compat-backends}"
HOST="${PSP_BACKEND_HOST:-127.0.0.1}"

XUI_PORT="${PSP_LIVE_XUI_PORT:-2053}"
SUI_PORT="${PSP_LIVE_SUI_PORT:-2095}"
XUI_NAME=psp-compat-3xui
# The range a rendered node may use; published so a client outside the container
# can reach it.
NODE_PORT_RANGE="${PSP_LIVE_XUI_NODE_PORTS:-24443-24450}"
SUI_NAME=psp-compat-sui

log() { printf '%s\n' "$*" >&2; }

# wait_for polls the panel's own authenticated surface with a bounded deadline.
# A fixed sleep would either be too short on a cold runner or slow on a warm one,
# and "the container is up" is not "the panel answers".
wait_for() {
  local url="$1" deadline="$2" started now
  started=$(date +%s)
  while :; do
    if curl -fsS -o /dev/null --max-time 3 "$url" 2>/dev/null; then return 0; fi
    now=$(date +%s)
    if [ $((now - started)) -ge "$deadline" ]; then
      log "timed out after ${deadline}s waiting for $url"
      return 1
    fi
    sleep 2
  done
}

# ---------------------------------------------------------------- 3X-UI

xui_token() {
  # Cookie session first: 3.8.x refuses a login POST without the CSRF header,
  # and the header is only obtainable once the session cookie is set.
  local jar="$WORKDIR/xui.cookies" csrf body
  curl -sS -o /dev/null -c "$jar" "$XUI_ORIGIN/"
  csrf=$(curl -sS -b "$jar" -c "$jar" "$XUI_ORIGIN/csrf-token" | python3 -c 'import json,sys;print(json.load(sys.stdin)["obj"])')
  curl -sS -o /dev/null -b "$jar" -c "$jar" -X POST \
    -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/x-www-form-urlencoded' \
    -d 'username=admin&password=admin' "$XUI_ORIGIN/login"
  csrf=$(curl -sS -b "$jar" -c "$jar" "$XUI_ORIGIN/csrf-token" | python3 -c 'import json,sys;print(json.load(sys.stdin)["obj"])')
  body=$(curl -sS -b "$jar" -X POST -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' \
    -d '{"name":"psp-compat","scope":"admin"}' "$XUI_ORIGIN/panel/api/setting/apiTokens/create")
  printf '%s' "$body" | python3 -c 'import json,sys;print(json.load(sys.stdin)["obj"]["token"])'
}

xui_seed() {
  # The shared-client tests skip below two inbounds, and a skip is not a pass.
  local token="$1" i port
  for i in a b; do
    if [ "$i" = a ]; then port=24443; else port=24444; fi
    curl -sS -o /dev/null -X POST -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
      -d "{\"up\":0,\"down\":0,\"total\":0,\"remark\":\"psp-compat-$i\",\"enable\":true,\"expiryTime\":0,\"listen\":\"\",\"port\":$port,\"protocol\":\"vless\",\"settings\":\"{\\\"clients\\\":[],\\\"decryption\\\":\\\"none\\\",\\\"fallbacks\\\":[]}\",\"streamSettings\":\"{\\\"network\\\":\\\"tcp\\\",\\\"security\\\":\\\"none\\\"}\",\"sniffing\":\"{\\\"enabled\\\":false,\\\"destOverride\\\":[\\\"http\\\",\\\"tls\\\"]}\",\"allocate\":\"{\\\"strategy\\\":\\\"always\\\",\\\"refresh\\\":5,\\\"concurrency\\\":10}\"}" \
      "$XUI_ORIGIN/panel/api/inbounds/add"
  done
  # The floor matrix writes usage directly; SQLite needs the DIRECTORY writable
  # to create its journal beside the file.
  chmod 777 "$WORKDIR/3xui" 2>/dev/null || true
  chmod 666 "$WORKDIR/3xui/x-ui.db"* 2>/dev/null || true
}

# ---------------------------------------------------------------- S-UI

sui_token() {
  # S-UI keeps tokens in its own `tokens` table and has no mint endpoint the
  # panel itself uses for first-run; the row is inserted directly. `expiry` is
  # in SECONDS (PSP's own expiry fields are milliseconds), and 0 means no expiry.
  local token
  token=$(python3 -c 'import secrets;print(secrets.token_urlsafe(32))')
  # python3, not sqlite3: the runner does not reliably have the sqlite3 CLI, and
  # the script already depends on python3. A missing tool here presented as "the
  # panel never published a webPath", which names the symptom and not the cause.
  $DB_SUDO python3 - "$WORKDIR/sui/s-ui.db" "$token" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
db.execute("delete from tokens")
db.execute("insert into tokens (desc, token, expiry, user_id) values ('psp-compat', ?, 0, 1)", (sys.argv[2],))
db.commit()
db.close()
PY
  printf '%s' "$token"
}

# ---------------------------------------------------------------- commands

# THE TRAFFIC-FLOOR MATRIX WRITES THIS DATABASE, and writing it needs the
# DIRECTORY.
#
# The panel runs as its own root, so x-ui.db and the folder holding it belong to
# uid 0. The matrix seeds accumulated usage with a plain UPDATE, and SQLite
# creates its journal beside the file — so an unwritable folder fails the open
# itself, reported as `unable to open database file: out of memory (14)`, which
# reads like a corrupt database rather than a permission on the folder. Handing
# the folder to the invoking user is what makes the difference; root inside the
# container keeps writing it either way.
#
# Measured as passing before this existed, but only from a root shell — the VM's
# harness ran as root, where the folder was writable all along. On a runner it
# is not, which is why the failure appeared only once the tests stopped skipping.
hand_over_xui_db() {
  if [ -n "$DB_SUDO" ]; then
    $DB_SUDO chown -R "$(id -u):$(id -g)" "$WORKDIR/3xui"
  elif [ ! -w "$WORKDIR/3xui" ]; then
    log "WARNING: $WORKDIR/3xui is not writable by $(id -un) and no sudo is available to hand it over; TestLive_XUITrafficFloorMatrix will fail to seed"
  fi
}

up() {
  rm -rf "$WORKDIR"
  mkdir -p "$WORKDIR/3xui" "$WORKDIR/sui"
  log "workdir: $WORKDIR"

  log "pulling $THIRD_PARTY_IMAGE_3XUI"
  $SUDO "$RUNTIME" pull "$THIRD_PARTY_IMAGE_3XUI" >/dev/null
  $SUDO "$RUNTIME" rm -f "$XUI_NAME" >/dev/null 2>&1 || true
  # THE NODE PORT RANGE IS PUBLISHED TOO. Only the panel port would leave every
  # inbound the panel creates — and therefore every subscription rendered from
  # it — unreachable from outside the container, so a dataplane test would fail
  # with connection refused against a node that is correctly configured.
  # 3X-UI restarts its core when an inbound is added, so the range has to be
  # published before the node exists.
  $SUDO "$RUNTIME" run -d --name "$XUI_NAME" -p "$XUI_PORT:2053" -p "$NODE_PORT_RANGE:$NODE_PORT_RANGE" -v "$WORKDIR/3xui:/etc/x-ui" "$THIRD_PARTY_IMAGE_3XUI" >/dev/null
  XUI_ORIGIN="http://$HOST:$XUI_PORT"
  wait_for "$XUI_ORIGIN/" 120
  log "3X-UI ready at $XUI_ORIGIN"
  local token; token=$(xui_token)
  xui_seed "$token"
  printf '%s' "$token" > "$WORKDIR/3xui.token"
  hand_over_xui_db

  log "pulling $THIRD_PARTY_IMAGE_SUI"
  $SUDO "$RUNTIME" pull "$THIRD_PARTY_IMAGE_SUI" >/dev/null
  $SUDO "$RUNTIME" rm -f "$SUI_NAME" >/dev/null 2>&1 || true
  $SUDO "$RUNTIME" run -d --name "$SUI_NAME" -p "$SUI_PORT:2095" -v "$WORKDIR/sui:/app/db" "$THIRD_PARTY_IMAGE_SUI" >/dev/null
  # The panel is NOT reachable at the bare origin: it serves under webPath, so
  # waiting there would 404 until the deadline however healthy the panel is. Wait
  # for the database to exist first — that is what says the process started —
  # then read webPath out of it and wait on the real base.
  local waited=0
  while [ ! -f "$WORKDIR/sui/s-ui.db" ]; do
    if [ "$waited" -ge 120 ]; then log "S-UI never created its database"; return 1; fi
    sleep 2; waited=$((waited + 2))
  done
  # webPath is looked up rather than assumed: a fresh install uses /app/, and
  # guessing the bare origin 404s every call.
  #
  # The database is owned by the CONTAINER's root, so whether this shell can read
  # it depends on whether the runtime needed sudo at all — on a runner, docker
  # does not, $SUDO is empty, and the read is refused. The privilege is therefore
  # decided from the file rather than from the runtime.
  # ALWAYS with the privilege, not conditionally. A condition on the main database
  # file is not enough: SQLite also needs the -shm and -wal beside it, and those
  # are the container's root's too. Deciding from the one file that happens to be
  # readable is how this failed twice while looking fixed. sudo is present here
  # and on the runner; where it is not, the runtime already needed it anyway.
  local web_path=""
  while [ -z "$web_path" ]; do
    if [ "$waited" -ge 150 ]; then log "S-UI never published a webPath"; return 1; fi
    # THE PATH IS AN ARGUMENT, NOT AN ENVIRONMENT VARIABLE. sudo resets the
    # environment, so an exported SUI_DB never reaches the interpreter and the
    # read dies with a KeyError that the redirect above hides — which is how this
    # presented as a panel that never published a webPath.
    web_path=$($DB_SUDO python3 - "$WORKDIR/sui/s-ui.db" <<'PY' 2>/dev/null || true
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
row = db.execute("select value from settings where key='webPath'").fetchone()
print(row[0] if row else "")
PY
)
    if [ -n "$web_path" ]; then break; fi
    sleep 2; waited=$((waited + 2))
  done
  local sui_base="http://$HOST:$SUI_PORT${web_path%/}"
  wait_for "$sui_base/" 60
  log "S-UI ready at $sui_base"
  local sui_tok; sui_tok=$(sui_token)
  $SUDO "$RUNTIME" restart "$SUI_NAME" >/dev/null
  sleep 8
  printf '%s' "$sui_tok" > "$WORKDIR/sui.token"
  printf '%s' "$sui_base" > "$WORKDIR/sui.base"
}

down() {
  # Only this case's resources. A global prune would take other work with it.
  for name in "$XUI_NAME" "$SUI_NAME"; do
    $SUDO "$RUNTIME" rm -f "$name" >/dev/null 2>&1 || true
  done
  log "removed $XUI_NAME and $SUI_NAME (workdir $WORKDIR left in place)"
}

# `env` IS MEANT TO BE EVALUATED, and only `export` survives that for a child.
#
# The documented consumer is `eval "$(third-party.sh env)"` (see the README), and
# what it feeds — `go test` — is a CHILD process, so it inherits the EXPORTED
# environment only. A bare `NAME=value` sets a shell variable and stops there:
# every check the evaluating shell can make about itself passes, the tests see
# nothing, and the suite answers with SKIPs that read as passes to anything
# reading an exit code. That is the failure check-go-results.mjs exists to
# refuse, so it was reported rather than hidden -- but only after a whole
# isolated-backends job had run and measured nothing.
sq() {
  # `%q` is bash's "quote this so it can be read back as shell input", which is
  # precisely this function's job. Hand-rolling it does not work: the
  # `${v//\'/...}` idiom produced a literal `\'\\'\'` here rather than the
  # `'\''` it is supposed to, and the resulting eval died with "unexpected EOF
  # while looking for matching `'`".
  printf '%q' "$1"
}

env_out() {
  printf 'export PSP_LIVE_XUI_URL=%s\n' "$(sq "http://$HOST:$XUI_PORT")"
  printf 'export PSP_LIVE_XUI_TOKEN=%s\n' "$(sq "$(cat "$WORKDIR/3xui.token")")"
  printf 'export PSP_LIVE_XUI_DB=%s\n' "$(sq "$WORKDIR/3xui/x-ui.db")"
  printf 'export PSP_LIVE_SUI_URL=%s\n' "$(sq "$(cat "$WORKDIR/sui.base")")"
  printf 'export PSP_LIVE_SUI_TOKEN=%s\n' "$(sq "$(cat "$WORKDIR/sui.token")")"
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  env) env_out ;;
  *) echo "usage: $0 {up|env|down}" >&2; exit 2 ;;
esac
