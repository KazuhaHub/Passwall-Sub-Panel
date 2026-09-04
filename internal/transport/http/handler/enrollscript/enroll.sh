#!/usr/bin/env bash
# Passwall Sub Panel — node enrollment.
#
# Prepares an ALREADY-INSTALLED 3X-UI panel for PSP and registers it, doing the
# three things that are reliably got wrong by hand:
#
#   * mints an admin-scoped, non-expiring API token (a narrower scope 403s, and
#     PSP treats 403 as a permanent failure)
#   * installs fail2ban and makes sure XUI_ENABLE_FAIL2BAN is not set to
#     something that looks enabled but is not — "1" disables enforcement
#   * reports every address this machine might be reachable at, and lets PSP
#     pick by probing, instead of asking a human which IP to type
#
# It does NOT install 3X-UI, and it does not touch inbounds or clients.
set -euo pipefail

PSP_BASE='__PSP_BASE__'
ENROLL_TOKEN='__ENROLL_TOKEN__'

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m warn\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m fail\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "run as root (needs to install fail2ban and read the 3X-UI database)"

say "Passwall Sub Panel — enrolling this node into ${PSP_BASE}"
echo "    This script will:"
echo "      1. locate the installed 3X-UI and read its port / base path"
echo "      2. install fail2ban if missing, and check XUI_ENABLE_FAIL2BAN"
echo "      3. create an admin-scoped API token for PSP"
echo "      4. register this node with PSP, which probes it back before saving"
echo "    It does not install 3X-UI and does not change your inbounds."
echo

# ---- 1. locate 3X-UI ----------------------------------------------------
command -v x-ui >/dev/null 2>&1 || die "x-ui not found — install 3X-UI first, then re-run this command"

XUI_DB=""
for c in /etc/x-ui/x-ui.db /usr/local/x-ui/bin/x-ui.db "${XUI_DB_FOLDER:-}/x-ui.db"; do
  [ -n "$c" ] && [ -f "$c" ] && { XUI_DB="$c"; break; }
done
[ -n "$XUI_DB" ] || die "could not find x-ui.db — is 3X-UI installed in a non-standard location?"

SETTINGS="$(x-ui setting -show 2>/dev/null || true)"
PORT="$(printf '%s\n' "$SETTINGS" | sed -n 's/^port: *//p' | head -1)"
BASE_PATH="$(printf '%s\n' "$SETTINGS" | sed -n 's/^webBasePath: *//p' | head -1)"
[ -n "$PORT" ] || die "could not read the panel port from 'x-ui setting -show'"
[ -n "$BASE_PATH" ] || BASE_PATH="/"

# https only when the panel actually has a cert configured; guessing wrong here
# makes every probe fail with a TLS error that looks like a network problem.
SCHEME="http"
printf '%s\n' "$SETTINGS" | grep -qi "not secure with SSL" || {
  printf '%s\n' "$SETTINGS" | grep -qiE 'certfile|keyfile' && SCHEME="https"
}
say "found 3X-UI on ${SCHEME} port ${PORT}, base path ${BASE_PATH}"

# ---- 2. fail2ban --------------------------------------------------------
# The concurrent-IP cap is stored and returns success whether or not the node
# can act on it, so this is invisible from PSP unless it is probed. Both gates
# are handled here.
F2B="unchanged"
if command -v fail2ban-client >/dev/null 2>&1; then
  say "fail2ban already installed"
  F2B="already-installed"
else
  say "installing fail2ban"
  if   command -v apt-get >/dev/null 2>&1; then DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq fail2ban >/dev/null
  elif command -v dnf     >/dev/null 2>&1; then dnf install -y -q fail2ban >/dev/null
  elif command -v yum     >/dev/null 2>&1; then yum install -y -q fail2ban >/dev/null
  elif command -v apk     >/dev/null 2>&1; then apk add --no-cache fail2ban >/dev/null
  else warn "no supported package manager found — install fail2ban yourself, or the IP limit will silently do nothing"
  fi
  if command -v fail2ban-client >/dev/null 2>&1; then F2B="installed"; else F2B="install-failed"; warn "fail2ban still not present"; fi
fi

# The trap: upstream accepts only the literal string "true". "1" reads as
# enabled to a person and disables enforcement entirely.
if [ -n "${XUI_ENABLE_FAIL2BAN+x}" ] && [ "${XUI_ENABLE_FAIL2BAN}" != "true" ]; then
  warn "XUI_ENABLE_FAIL2BAN is set to '${XUI_ENABLE_FAIL2BAN}', which DISABLES the IP limit."
  warn "Only the literal string 'true' enables it. Unset it in the x-ui service environment and restart x-ui."
  F2B="${F2B};env-var-disables-it"
fi

# ---- 3. mint an API token ----------------------------------------------
# admin scope, no expiry. Stored as a hex SHA-256 of the plaintext, which is
# what upstream's MatchToken compares against.
API_TOKEN="psp-$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
TOKEN_HASH="$(printf '%s' "$API_TOKEN" | sha256sum | cut -d' ' -f1)"
TOKEN_NAME="psp-enroll"

# Replace any previous PSP token rather than accumulating one per re-run.
SQL="DELETE FROM api_tokens WHERE name='${TOKEN_NAME}';
     INSERT INTO api_tokens (name, token, enabled, created_at, scope, expires_at)
     VALUES ('${TOKEN_NAME}', '${TOKEN_HASH}', 1, 0, 'admin', 0);"

# python3 FIRST, deliberately. Its sqlite3 module is in the standard library, so
# this needs no package install — and installing one is the wrong thing to have
# on the critical path: a stale apt index or a minimal image would otherwise
# kill enrollment at the last step, after everything else had already succeeded.
# (Observed: an image whose mirror 404'd the sqlite3 .deb.)
if command -v python3 >/dev/null 2>&1; then
  python3 - "$XUI_DB" "$SQL" <<'PYEOF' || die "could not write the API token into the 3X-UI database"
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
con.executescript(sys.argv[2])
con.commit()
con.close()
PYEOF
elif command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "$XUI_DB" "$SQL" || die "could not write the API token into ${XUI_DB}"
else
  die "need python3 or sqlite3 to create the API token; install either and re-run"
fi
say "created an admin-scoped API token (no expiry)"

# ---- 4. collect candidate addresses -------------------------------------
# Everything this machine might be reachable at. PSP decides by probing; it
# also has the address it saw this callback arrive from, which is usually the
# right one but is wrong behind NAT — hence sending ours too.
ADDRS="$( { ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1
            ip -6 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1
            hostname -I 2>/dev/null | tr ' ' '\n'
          } | sed '/^$/d' | sort -u )"
ADDR_JSON="$(printf '%s\n' "$ADDRS" | awk 'NF{printf "%s\"%s\"", sep, $0; sep=","}')"

say "reporting addresses: $(printf '%s' "$ADDRS" | tr '\n' ' ')"

# ---- 5. register with PSP ------------------------------------------------
PAYLOAD="$(cat <<JSON
{"scheme":"${SCHEME}","port":${PORT},"base_path":"${BASE_PATH}","api_token":"${API_TOKEN}",
 "addresses":[${ADDR_JSON}],"hostname":"$(hostname 2>/dev/null || echo node)","fail2ban":"${F2B}"}
JSON
)"

say "registering with PSP"
HTTP_BODY="$(mktemp)"; trap 'rm -f "$HTTP_BODY"' EXIT
CODE="$(curl -sS -o "$HTTP_BODY" -w '%{http_code}' \
        -X POST "${PSP_BASE}/api/enroll/${ENROLL_TOKEN}" \
        -H 'Content-Type: application/json' \
        --data "$PAYLOAD" 2>/dev/null || true)"

if [ "$CODE" = "201" ]; then
  echo
  say "done — this node is registered"
  sed -e 's/^/    /' "$HTTP_BODY"; echo
  echo "    Next: open PSP → Nodes, pick this panel, and import or create an inbound."
  exit 0
fi

echo
warn "PSP did not accept the registration (HTTP ${CODE:-no response})"
# PSP's own reason, verbatim. Printed before the generic hints because it is
# the specific answer and the hints are guesses.
if [ -s "$HTTP_BODY" ]; then
  echo "    PSP said:" >&2
  sed -e 's/^/      /' "$HTTP_BODY" >&2
  echo >&2
fi
cat >&2 <<'HINT'
    The API token was created on this node and is still valid, so once the
    cause is fixed you can re-run with a fresh command from PSP.

    Most common causes, in order:
      * none of this machine's addresses are ones PSP is allowed to dial.
        PSP refuses loopback, link-local and documentation ranges, so a node
        with only 127.0.0.1 cannot be enrolled — give it a LAN or public
        address and make sure the panel listens on it
      * PSP cannot reach this machine on the panel port — check the firewall,
        and check the panel is not listening on 127.0.0.1 only
      * this node is behind NAT and none of the addresses above route back
      * the enrollment command expired (they are valid for 30 minutes)
HINT
exit 1
