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

With the launcher publishing the node's port, the chain completes:

| | Result |
| --- | --- |
| Baseline, through the node | `example.com -> HTTP 200` over `outbound/vless[r08-node3]` |
| Panel-side counter after it | `inb 3 r08-i3 port 24447 | inb up 110` |
| After expiring the user | `attempt 1 -> HTTP 000`, `attempt 2 -> HTTP 000` |
| Panel-side after expiring | `u2@psp.local enable False`, `expiryTime 1789801199000` |

The port stays open at the transport layer while the handshake is refused, which
is the correct shape: the panel refuses at the protocol, not by dropping the
listener.

The expiry path was used rather than quota exhaustion. Both act on the same
service axis, so this does not establish that the quota path behaves the same —
it establishes the axis.

## What this does NOT establish

- **That the traffic traversed the panel's core.** An earlier reading of this run
  claimed the proxied request returned 200 with the node selected. That was
  wrong twice over: the instance holding the proxy port was still the one using
  the direct default — the selected instance had failed to bind and exited — and
  the target was a private address, which the ruleset sends direct regardless of
  the selector. Corrected here because the claim was in a PR body.
  What is established: with the selected config running, a public destination
  routes to `vless[r08-node]`. What is not: that the connection completed through
  the panel, because the panel no longer holds PSP's node — the launcher was
  restarted afterwards and rebuilt the panel's volume, which removed the inbound
  PSP had created on it.
- **The quota path.** Expiry was used. Both act on the service axis, but a quota
  exhaustion is a different trigger and is not tested here.
- **The full protocol matrix.** One VLESS/TCP/none combination was exercised.
  TLS, REALITY and the other transports are not covered.

## Scope

The client is a stock upstream build, pinned by version. The panel is the real
image, started by R07's launcher. The subscription is PSP's own render — the
config is never hand-written to stand in for it, and the single edit is the `tun`
inbound removal.
