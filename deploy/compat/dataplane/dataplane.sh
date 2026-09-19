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
TARGET_PORT="${PSP_DATA_TARGET_PORT:-8080}"

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
python3 - "$WORKDIR/node-resp.json" >&2 <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print("node", d["id"], "inbound", d["inbound_id"], "sync", d.get("config_sync_state"))
PY

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

log "starting a target"
python3 -m http.server "$TARGET_PORT" > "$WORKDIR/target.log" 2>&1 &
TARGET_PID=$!
trap 'kill $TARGET_PID 2>/dev/null || true' EXIT

log "running the real client"
(cd "$WORKDIR" && "$CLIENT" run -c client.json > client.log 2>&1 &)
sleep 8

log "moving traffic through it"
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 \
  --proxy socks5h://127.0.0.1:7890 "http://127.0.0.1:$TARGET_PORT/" || true)
log "proxied request: HTTP $code"

if [ "$code" != "200" ]; then
  log "FAIL: traffic did not traverse the client"
  exit 1
fi
log "PASS: the rendered subscription loaded and carried a request"
