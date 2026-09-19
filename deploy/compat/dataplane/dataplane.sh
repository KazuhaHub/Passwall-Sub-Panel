#!/usr/bin/env bash
#
# Drives PSP's dataplane: a candidate panel renders a subscription from a real
# third-party panel, a real client loads that render, and traffic moves.
#
# WHY A SCRIPT AND NOT A GO TEST. The chain crosses three processes that are not
# this repository's: a released panel image, a stock client build, and a target.
# Binding it to a Go test would mean either vendoring a client or hand-writing a
# config to stand in for the render — and a hand-written config proves the
# hand-writing, which is the one thing this layer exists to avoid.
#
# The client picks the node by default here, which is what a user does in their
# client's UI. Leaving it unselected is the more interesting state, and it is
# recorded in the README rather than silently configured away.
set -euo pipefail

PSP_URL="${PSP_DATA_URL:-http://127.0.0.1:8788}"
PANEL_URL="${PSP_DATA_PANEL_URL:?set PSP_DATA_PANEL_URL to the isolated 3X-UI origin}"
PANEL_TOKEN="${PSP_DATA_PANEL_TOKEN:?set PSP_DATA_PANEL_TOKEN}"
ADMIN_PASSWORD="${PSP_DATA_ADMIN_PASSWORD:?set PSP_DATA_ADMIN_PASSWORD to the bootstrap credential}"
CLIENT="${PSP_DATA_CLIENT:?set PSP_DATA_CLIENT to a sing-box binary (1.12+; 1.11 and older reject the rendered DNS schema)}"
WORKDIR="${PSP_DATA_WORKDIR:-/tmp/psp-dataplane}"

log() { printf '%s\n' "$*" >&2; }
api() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }

mkdir -p "$WORKDIR"

log "logging into the candidate panel"
TOKEN=$(curl -fsS -X POST -H 'Content-Type: application/json' \
  -d "{\"upn\":\"admin\",\"password\":\"$ADMIN_PASSWORD\"}" "$PSP_URL/api/auth/local/login" |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["access_token"])')

log "registering the real panel"
api -X POST -H 'Content-Type: application/json' \
  -d "{\"panel_type\":\"3xui\",\"name\":\"dataplane-xui\",\"url\":\"$PANEL_URL\",\"api_token\":\"$PANEL_TOKEN\"}" \
  "$PSP_URL/api/admin/servers" > "$WORKDIR/panel.json"

log "creating a node on it"
NODE_HOST=$(printf '%s' "$PANEL_URL" | sed -E 's#^https?://##; s#[:/].*$##')
python3 -c '
import json,sys
print(json.dumps({
  "panel_id":1,"display_name":"dataplane-node","server_address":sys.argv[1],"region":"test",
  "inbound":{"remark":"dataplane-inbound","enable":True,"port":24445,"protocol":"vless",
    "settings":json.dumps({"clients":[],"decryption":"none","fallbacks":[]}),
    "stream_settings":json.dumps({"network":"tcp","security":"none"}),
    "sniffing":json.dumps({"enabled":False,"destOverride":["http","tls"]}),
    "allocate":json.dumps({"strategy":"always","refresh":5,"concurrency":10})}}))' \
  "$NODE_HOST" > "$WORKDIR/node.json"
api -X POST -H 'Content-Type: application/json' -d "@$WORKDIR/node.json" "$PSP_URL/api/admin/nodes" > "$WORKDIR/node-resp.json"

# THE PANEL WRITE CAN BE QUEUED RATHER THAN DONE. PSP answers 202 {"queued":true}
# when the upstream call did not complete — the pool had no client for the panel
# yet, or the panel refused — and persists a sync task to retry it. The node has
# an id only once that task runs, so reading `id` straight out of the response is
# reading a field that is not there yet. This harness did exactly that and died
# with a KeyError, which named the symptom rather than the contract.
wait_node_id() {
  local name="$1" waited=0 id
  while :; do
    id=$(api "$PSP_URL/api/admin/nodes" |
      python3 -c "
import json,sys
want=sys.argv[1]
for n in json.load(sys.stdin).get('items',[]):
    if n.get('display_name')==want:
        print(n.get('id')); break
" "$name")
    if [ -n "$id" ]; then printf '%s' "$id"; return 0; fi
    if [ "$waited" -ge 120 ]; then
      log "the node $name never appeared; PSP answered $(head -c 120 "$WORKDIR/node-resp.json" 2>/dev/null)"
      return 1
    fi
    sleep 5; waited=$((waited + 5))
  done
}
NODE_ID=$(wait_node_id "dataplane-node")
log "node $NODE_ID appeared after the queued create"

log "creating a user"
api -X POST -H 'Content-Type: application/json' \
  -d '{"upn":"dataplane-user","display_name":"Dataplane","group_id":1,"traffic_limit_gb":1}' \
  "$PSP_URL/api/admin/users" > "$WORKDIR/user.json"
SUB_URL=$(python3 - "$WORKDIR/user.json" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["user"]["sub_url"])
PY
)

# A CLIENT'S USER AGENT, so the format under test is the format that client gets.
log "fetching the subscription as a client would"
curl -fsS --max-time 30 -H 'User-Agent: sing-box 1.12' "$SUB_URL" > "$WORKDIR/render.json"
python3 - "$WORKDIR/render.json" "$WORKDIR/client.json" <<'PY'
import json, sys
render = json.load(open(sys.argv[1]))
proxies = [o for o in render.get("outbounds", []) if o.get("type") in ("vless","vmess","trojan","shadowsocks")]
assert proxies, "the render carries no proxy entry; nothing to connect through"
print("rendered proxies:", [(p.get("tag"), p.get("server"), p.get("server_port")) for p in proxies])
# The only edit: a tun inbound needs a TUN device and root.
render["inbounds"] = [i for i in render.get("inbounds", []) if i.get("type") != "tun"]
# Selecting the node is what a user does in the client UI. Recorded, not hidden.
for out in render.get("outbounds", []):
    if out.get("type") == "selector" and any(o == proxies[0].get("tag") for o in out.get("outbounds", [])):
        out["default"] = proxies[0].get("tag")
json.dump(render, open(sys.argv[2], "w"), indent=1)
PY

if [ "$SABOTAGE" = "credentials" ]; then
  log "SABOTAGE: replacing the rendered credential with a wrong one"
  python3 - "$WORKDIR/client.json" <<'PY'
import json, sys
path = sys.argv[1]
render = json.load(open(path))
for out in render.get("outbounds", []):
    if out.get("type") == "vless":
        out["uuid"] = "00000000-0000-4000-8000-000000000000"
json.dump(render, open(path, "w"), indent=1)
PY
fi

# SABOTAGE: A DELIBERATE BREAK, SO THE ASSERTIONS CAN BE SHOWN TO FAIL.
#
# R08's completion criterion names three breaks that must each make the
# corresponding case fail — the real core closed, the rendered credentials wrong,
# the limit enforcement removed — and a harness whose green does not depend on any
# of them is a harness that would stay green through all three. These modes are
# how that is checked, and they exist for nothing else.
#
#   credentials     the render's credential is replaced with a wrong one, so the
#                   traversal below must NOT succeed
#   no-enforcement  the expiry is never pushed, so the refusal case must NOT pass
#
# `credentials` stands in for closing the core as well: both leave the node
# unable to carry the connection, which is the property the traversal assertion
# is supposed to be sensitive to.
SABOTAGE="${PSP_DATA_SABOTAGE:-}"
[ -z "$SABOTAGE" ] || log "SABOTAGE=$SABOTAGE — this run is EXPECTED to fail"

log "checking the target"
# THE TARGET MUST BE ONE THE RULESET SENDS THROUGH THE NODE.
#
# A LOCAL target cannot be: the render's ruleset sends private addresses direct
# regardless of the selector, so a request to 192.168.x.x or 127.0.0.1 succeeds
# whether or not the node is involved — and, worse, whether or not the node is
# even running. That is not hypothetical: an earlier version of this harness used
# the node's own LAN address as the target and reported "the expired user was
# still carried through the node" for 180s, when the containers had already been
# removed and every one of those 200s came from the direct rule. The README
# records the same mistake from a still earlier run, which is why the control
# below exists.
#
# The plan asks for a local target with fixed content so byte counts and request
# ids can be asserted. It cannot have one while the ruleset classifies local
# addresses as direct, and the traversal proof is worth more than the byte
# counting — so the trade is made explicitly here rather than quietly.
TARGET_URL="${PSP_DATA_TARGET_URL:-http://example.com/}"

# THE TARGET IS PROVED UP BEFORE IT IS PROVED REACHABLE THROUGH THE PROXY, and
# unproxied. Without this a proxied failure is attributable to either the node
# or a dead target, which the plan requires to be distinguishable.
direct=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 "$TARGET_URL" || true)
if [ "$direct" != "200" ]; then
  log "FAIL: the target does not answer directly (HTTP $direct at $TARGET_URL); nothing below could be attributed to the node"
  exit 1
fi
log "target healthy at $TARGET_URL (direct, unproxied)"

# THE PROXY PORT MUST BE OURS, AND A LEFTOVER CLIENT IS NOT A NODE FAULT.
#
# A sing-box from an earlier run keeps 127.0.0.1:7890, the next instance dies at
# startup with `bind: address already in use`, and every request then comes back
# HTTP 000 — which reads as "the node refused" and is the opposite of what
# happened. The README already records this misreading from an earlier run: the
# instance holding the port was the one using the direct default, and the selected
# instance had exited at bind time. So the port is checked first, and this
# harness's own client is tracked and stopped on exit rather than left behind for
# the next run to trip over.
CLIENT_PID=""
stop_client() {
  [ -n "$CLIENT_PID" ] || return 0
  kill -0 "$CLIENT_PID" 2>/dev/null || return 0
  kill "$CLIENT_PID" 2>/dev/null || true
}
trap stop_client EXIT

if ss -ltn 2>/dev/null | grep -q ':7890 '; then
  log "FAIL: 127.0.0.1:7890 is already bound, so the client below cannot start and every request would read as a node refusal"
  ss -ltnp 2>/dev/null | grep ':7890 ' >&2 || true
  exit 1
fi

log "running the real client"
# `exec` in a subshell, so `$!` names sing-box itself: that is the pid the cleanup
# has to stop, and a wrapper would leave the port held by an orphan.
(cd "$WORKDIR" && exec "$CLIENT" run -c client.json > client.log 2>&1) &
CLIENT_PID=$!
sleep 8
kill -0 "$CLIENT_PID" 2>/dev/null || { log "FAIL: the client exited during startup; see $WORKDIR/client.log"; tail -5 "$WORKDIR/client.log" >&2; exit 1; }

log "moving traffic through it"
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 \
  --proxy socks5h://127.0.0.1:7890 "$TARGET_URL" || true)
log "proxied request: HTTP $code"

if [ "$code" != "200" ]; then
  log "FAIL: traffic did not traverse the client"
  exit 1
fi

# THE 200 PROVES NOTHING ON ITS OWN. A request answered by the direct rule looks
# exactly like one answered through the node, and misreading that is the mistake
# this file's README already records once. So the client's OWN log has to name a
# vless outbound — the node — for this destination before anything below is
# allowed to mean anything.
TARGET_HOST=$(printf '%s' "$TARGET_URL" | sed -E 's#^https?://##; s#[:/].*$##')
if ! grep -q "outbound/vless\[[^]]*\]: outbound connection to $TARGET_HOST" "$WORKDIR/client.log"; then
  log "FAIL: the request was answered, but not through the node's outbound; the client log says:"
  grep -oE 'outbound/[a-z0-9-]+\[[^]]*\]' "$WORKDIR/client.log" | sort -u | tail -5 >&2 || true
  exit 1
fi
log "PASS: the client routed $TARGET_HOST through the node's vless outbound"
log "PASS: the rendered subscription loaded and carried a request"

# ---------------------------------------------------------------- enforcement

# THE WINDOW IS A PROFILE VALUE, NOT A RETRY COUNT. The plan requires the
# effective window to be written down rather than retried until something
# happens, because "it eventually refused" and "it refused within its stated
# window" are different claims and only the second is testable. PSP reconciles on
# its own schedule, so the harness polls the PROXIED REQUEST — the thing a
# subscriber experiences — rather than any internal state.
ENFORCE_WINDOW_SECONDS="${PSP_DATA_ENFORCE_WINDOW_SECONDS:-180}"
RESTORE_WINDOW_SECONDS="${PSP_DATA_RESTORE_WINDOW_SECONDS:-180}"

proxied_code() {
  curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
    --proxy socks5h://127.0.0.1:7890 "$TARGET_URL" || true
}

proxied_attempt() {
  local want="$1" window="$2" waited=0 got=""
  while [ "$waited" -le "$window" ]; do
    got=$(proxied_code)
    if [ "$got" = "$want" ]; then printf '%s' "$got"; return 0; fi
    sleep 5; waited=$((waited + 5))
  done
  printf '%s' "$got"
  return 1
}

# THE PANEL'S OWN VIEW, not PSP's. "PSP believes the user is expired" and "the
# panel stopped serving them" are different statements, and only the second is
# what a subscriber feels.
#
# Matched by the VPN UUID the render actually carries, NOT by the user's UPN. The
# client's email is derived from the user's ID (`u2@psp.local`), so a probe
# looking for the UPN in it finds nothing and prints "" — which reads as "the
# panel does not have the client" rather than "this looked in the wrong field".
# The UUID is the same value on both sides by construction.
PANEL_CLIENT_UUID=$(python3 - "$WORKDIR/render.json" <<'PY'
import json, sys
render = json.load(open(sys.argv[1]))
for out in render.get("outbounds", []):
    if out.get("type") == "vless" and out.get("uuid"):
        print(out["uuid"]); break
PY
)
[ -n "$PANEL_CLIENT_UUID" ] || { log "FAIL: the render carries no vless UUID to look up on the panel"; exit 1; }

panel_client_enabled() {
  curl -fsS -H "Authorization: Bearer $PANEL_TOKEN" \
    "$PANEL_URL/panel/api/inbounds/list" 2>/dev/null |
    python3 -c "
import json,sys
want=sys.argv[1]
for ib in json.load(sys.stdin).get('obj') or []:
    settings = ib.get('settings') or '{}'
    if isinstance(settings, str):
        settings = json.loads(settings)
    for c in settings.get('clients') or []:
        if c.get('id') == want:
            print(c.get('enable')); raise SystemExit(0)
print('')
" "$PANEL_CLIENT_UUID"
}

USER_ID=$(python3 - "$WORKDIR/user.json" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["user"]["id"])
PY
)

if [ "$SABOTAGE" = "no-enforcement" ]; then
  # The limit enforcement is never applied, so nothing below has any reason to
  # refuse. A harness that passes here is asserting something it did not do.
  log "SABOTAGE: NOT expiring the user; the refusal case below must fail"
else
  log "expiring the user (window ${ENFORCE_WINDOW_SECONDS}s)"
  api -X PUT -H 'Content-Type: application/json' \
    -d '{"expire_at":"2020-01-01T00:00:00Z"}' \
    "$PSP_URL/api/admin/users/$USER_ID" > "$WORKDIR/expire.json"
fi

if ! proxied_attempt "000" "$ENFORCE_WINDOW_SECONDS" >/dev/null; then
  log "FAIL: an expired user was still carried through the node after ${ENFORCE_WINDOW_SECONDS}s"
  exit 1
fi
log "PASS: the proxied request was refused after the user expired (window ${ENFORCE_WINDOW_SECONDS}s)"

enabled=$(panel_client_enabled)
if [ "$enabled" != "False" ]; then
  log "FAIL: the panel still reports the client as enabled=${enabled:-<absent>}"
  exit 1
fi
log "PASS: the panel's own view of the client is enable=False"

log "restoring the user (window ${RESTORE_WINDOW_SECONDS}s)"
api -X PUT -H 'Content-Type: application/json' \
  -d '{"clear_expire":true}' \
  "$PSP_URL/api/admin/users/$USER_ID" > "$WORKDIR/restore.json"

if ! proxied_attempt "200" "$RESTORE_WINDOW_SECONDS" >/dev/null; then
  log "FAIL: traffic did not resume within ${RESTORE_WINDOW_SECONDS}s of restoring the user"
  exit 1
fi
log "PASS: traffic resumed after the user was restored"

log "PASS: the chain carried traffic, refused it once the user expired, and carried it again once restored"
