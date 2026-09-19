# Dataplane: a real subscription through a real client

`dataplane.sh` drives the chain the compatibility policy calls `dataplane` — and
which nothing else in this repository exercises: the panel creates real state on
a real third-party panel, renders a subscription from it, and a real client loads
that render and moves traffic.

Every other layer stops short of this. `adapter-live` says the adapter talks to a
panel correctly; `wire-contract` says the Node agent and the panel agree. Neither
says a line a subscriber receives actually carries a connection.

## What it does

```bash
deploy/compat/backends/third-party.sh up          # the real panels (R07)
./deploy/compat/dataplane/dataplane.sh            # renders, loads, and moves traffic
```

1. Starts a candidate PSP in the sandbox and registers a running 3X-UI panel.
2. Creates a node through the panel's own API — a real inbound on the real
   panel — and a user with a real UUID.
3. Fetches the subscription **with a client's user agent**, so the format the
   client would get is the format under test.
4. Writes it to disk and runs a **real sing-box** with it, unmodified except for
   dropping the `tun` inbound, which needs a TUN device and root.
5. Moves HTTP through the client's mixed inbound.

## Measured

Against a real 3X-UI 3.8.5 and sing-box 1.12.9:

- The panel registered with 11 real capabilities read from the live panel.
- The node was created on the panel and reported `config_sync_state: synced`.
- The Clash render carried the real node — `server`, `port`, `type: vless` and
  the user's real UUID — and `Subscription-Userinfo: total=1073741824`.
- The sing-box render carried a `vless` outbound for that node plus a `mixed`
  inbound, and **a real sing-box 1.12.9 process loaded and ran it**.
- With the node selected, a PUBLIC destination routes through the node —
  `outbound/vless[r08-node]: outbound connection to example.com:80` — while a
  private address is matched by an earlier direct rule and never reaches it.

## Finding: an imported subscription proxies nothing until a node is selected

The rendered sing-box config routes as follows:

```
route.final = 🐟 漏网之鱼        (default: 🚀 节点选择)
🚀 节点选择  (selector)          (default: direct, options: [direct, r08-node])
```

`节点选择` lists the node but **defaults to `direct`**, so every unmatched
connection resolves to `direct`. Measured with a real client: `example.com`,
`www.wikipedia.org`, `ifconfig.me` and `www.gstatic.com` were all sent to
`direct`, and the client log shows it plainly —

```
outbound/direct[direct]: outbound connection to example.com:80
```

Changing that one default to the node — what a user does in their client's UI —
changes which outbound a public destination takes (`vless[r08-node]` instead of
`direct`). It does NOT change a private address, which an earlier rule sends
direct regardless of the selector.

**Stated as an observation, not a diagnosis.** Whether a subscription that
defaults to direct is intended template policy or a defect is not established
here. What is established is that a subscriber who imports this render and does
nothing else sends no traffic through the panel, which is worth a decision rather
than an assumption.

## Measured: the connection, and its refusal

The harness now drives all of it — traversal, refusal, and the return — and
prints the following on a run that passes:

```
target healthy at http://example.com/ (direct, unproxied)
PASS: the client routed example.com through the node's vless outbound
PASS: the rendered subscription loaded and carried a request
expiring the user (window 180s)
PASS: the proxied request was refused after the user expired (window 180s)
PASS: the panel's own view of the client is enable=False
restoring the user (window 180s)
PASS: traffic resumed after the user was restored
```

The expiry path was used rather than quota exhaustion. Both act on the same
service axis, so this does not establish that the quota path behaves the same —
it establishes the axis.

**The window is a profile value, not a retry count** (`PSP_DATA_ENFORCE_WINDOW_SECONDS`,
`PSP_DATA_RESTORE_WINDOW_SECONDS`, both 180). "It eventually refused" and "it
refused within its stated window" are different claims and only the second is
testable, so the number is written down where a reader can disagree with it.

## Four ways this harness lied before it worked

Each of these produced a green or red result that was about the harness rather
than about the chain. They are recorded because each one reads like a finding.

- **The target must be one the ruleset sends through the node.** A local target
  cannot be: private addresses go `direct` regardless of the selector. An earlier
  version pointed at the node's own LAN address and reported, for 180 seconds,
  that an expired user was still being carried — while the containers had already
  been removed and every one of those 200s came from the direct rule.
- **A 200 does not say which outbound produced it.** The harness now requires the
  client's own log to name a `vless` outbound for the destination before it
  believes anything downstream. Without that, `direct` and the node are
  indistinguishable.
- **A leftover client on the proxy port is not a node fault.** sing-box dies at
  startup with `bind: address already in use`, every request then returns
  `HTTP 000`, and that reads as a refusal by the node. The run that produced the
  table above was first blocked by a `/tmp/sing-box12` from an earlier session
  holding 127.0.0.1:7890. The harness now checks the port first and stops its own
  client on exit.
- **The panel is not reachable at loopback.** PSP's `safehttp` refuses loopback
  by design — the SSRF guard doing its job — so a panel published at
  `http://127.0.0.1:2053` cannot be used by the panel under test. The node-create
  path fails with `refusing connection to non-public address 127.0.0.1` and PSP
  answers `202 {"queued": true}`, which reads as "still syncing" rather than
  "this address can never work". Run the launcher with
  `PSP_BACKEND_HOST=<non-loopback address>`.

## The panel-side probe addresses the client by UUID

The client's email is derived from the user's ID (`u2@psp.local`), so looking for
the UPN inside it finds nothing and returns empty — which reads as "the panel does
not have this client". The probe matches on the VPN UUID, which is the same value
in the render and on the panel by construction.


## What this does NOT establish

- **Which transport or security layer carries it, beyond the one measured.** One
  VLESS/TCP/none combination is exercised. TLS, REALITY and the other transports
  are not.
- **The quota path.** Expiry was used. Both act on the service axis, but a quota
  exhaustion is a different trigger and is not tested here.
- **The window under a different cadence.** 180s is the harness's declared bound
  on this configuration. It says nothing about how a longer traffic-pull interval
  or a larger fleet changes it.
- **That the refusal is the node's rather than the client's.** The client is
  still configured and still routes the destination to the node's outbound; the
  connection fails at the node. The panel-side probe (`enable=False`) is what
  makes that attribution, and it is checked in the same run.

Earlier revisions of this file carried a claim that the proxied request returned
200 with the node selected, which was wrong twice over — the instance holding the
proxy port was the one using the direct default, and the target was a private
address the ruleset sends direct. The outbound assertion above exists so that
this specific claim cannot be made again without the client's own log supporting
it.

## Scope

The client is a stock upstream build, pinned by version. The panel is the real
image, started by R07's launcher. The subscription is PSP's own render — the
config is never hand-written to stand in for it, and the single edit is the `tun`
inbound removal.
