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
- With the node selected, a request through the client's proxy returned 200.

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

Changing that one default to the node — which is what a user does when they pick
a node in their client's UI — made the same request succeed through the proxy.

**Stated as an observation, not a diagnosis.** Whether a subscription that
defaults to direct is intended template policy or a defect is not established
here. What is established is that a subscriber who imports this render and does
nothing else sends no traffic through the panel, which is worth a decision rather
than an assumption.

## What this does NOT establish

- **That the traffic traversed the panel's core.** The proxy path returned 200
  with the node selected, but the panel-side counter check did not complete in
  the run recorded here. Until it does, this is a client-side observation.
- **Enforcement.** No test here disables the user, exhausts the quota, or expires
  the subscription and confirms the next connection is refused. That is R08's
  remaining half and the reason the policy separates "connected" from "correctly
  refused".
- **The full protocol matrix.** One VLESS/TCP/none combination was exercised.
  TLS, REALITY and the other transports are not covered.

## Scope

The client is a stock upstream build, pinned by version. The panel is the real
image, started by R07's launcher. The subscription is PSP's own render — the
config is never hand-written to stand in for it, and the single edit is the `tun`
inbound removal.
