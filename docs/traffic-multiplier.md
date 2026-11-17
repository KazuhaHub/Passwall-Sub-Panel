# Per-server traffic multiplier

A server's **traffic factor** says how many bytes a user is charged for each byte
they move through it, in thousandths: `1000` is one-for-one (every server's
default), `1500` charges ×1.5, `500` charges ×0.5, and `0` makes the server free.
Range `0..100000` (×100). It is set on the server form, for every server kind, and
every change is written to the audit log as `server_traffic_factor_changed`.

## Granularity: the server, not the node or the line

A user holds one client counter per server. Every node, relay line, tunnel line and
VLESS route line on that server reports into the same counter, so the data cannot
tell them apart. The factor therefore belongs to the server that holds the user's
client — the node's own server. A relay or managed relay only forwards to it; a
tunnel line's exit server is not where the user is counted.

## Charged at booking

The poll weights each server's delta once, when it books it:

| Ledger | Holds |
|---|---|
| `users.lifetime_*`, `period_baseline_bytes`, `emergency_baseline_bytes` | **charged** bytes — what quota, suspension, the subscription header and mails read |
| `users.raw_lifetime_{up,down,total}_bytes` | raw bytes, display only |
| `psp_clients.lifetime_*`, `period_baseline_*` (and the retired `user_xui_clients`) | raw bytes, as before |
| `psp_clients.charged_lifetime_bytes` (and `user_xui_clients`) | that row's charged bytes |
| node counters, `node_traffic_snapshots` | raw bytes, unchanged |

`charged = round(raw × factor / 1000)`, up and down rounded separately so
`up + down = total` still holds. Changing a factor re-prices only traffic booked
afterwards; nothing already booked moves. If the factors cannot be read, the poll
cycle is abandoned before it books anything and the next cycle books the whole
delta at the right price.

With every factor at 1000 every figure is exactly what it was before the feature.

## Offline enforcement

The panel and node gates count **raw** bytes on their own counters, so PSP converts
the remaining charged quota into raw bytes for each server:

- **Passwall Node**: headroom = `floor(remaining charged × 1000 / factor)` — rounding
  down closes the gate no later than the charged ledger would. The advance grant for
  the next period is `limit × 1000 / factor`. Usage reported by agents but not yet
  booked is weighted by each client's own server. On a free server the gate is not
  armed (`nil`) while the user has quota left and is closed (`0`) once it is used up;
  no advance grant is scheduled for it, so an exhausted user stays closed until PSP
  is reachable after the period rolls over.
- **3X-UI / S-UI**: the period headroom goes through `domain.RawHeadroom` before it is
  rebased onto the panel counter (`domain.PanelQuotaCap`). Unlimited stays `0`, the
  exhausted sentinel stays `1` on every server, a free server with quota left gets
  `0` (no cap), and a small headroom never rounds down to `0`.

## Usage figures

- The user's usage, history and rollups are charged, as is the
  `Subscription-Userinfo` header.
- Admin per-server and per-node usage return raw figures plus `traffic_factor_milli`,
  `charged_lifetime_bytes` and `charged_period_bytes`, with a raw/charged toggle.
  Per-row period baselines are raw, so `charged_period_bytes` is the row's raw period
  at the row's lifetime average price: exact while the factor has not changed.
- A manual period-usage override (`SetPeriodUsage`) is distributed across the user's
  rows in proportion to their charged lifetime.
- The user portal shows a ×f badge on servers that are not one-for-one.

## Node names

A node, relay line or route line name may contain `{{ factor }}`. Subscriptions and
the user portal replace it with the server's multiplier (`×1.5`, `×1`, `×0`). Names
without it never change.

## Upgrade

`schema_upgrade_v4_3.go` (`migrateV4_3`) runs once at boot, before the first poll:
before 4.3 every byte was charged one-for-one, so raw user lifetime is seeded from
lifetime and each row's charged lifetime from its raw lifetime. It is recorded in
`schema_migrations`, because after 4.3 a charged ledger of 0 beside a raw lifetime
is a free server's history and must never be overwritten.
