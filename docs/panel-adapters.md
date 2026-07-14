# Panel adapters

Passwall Sub Panel routes upstream panel operations through a vendor-neutral
adapter registry. Persisted rows select an adapter with `xui_panels.kind`;
legacy rows with an empty kind are treated as `3xui`.

## Contracts

- `ports.PanelClient` is the data-plane contract used by node, client,
  traffic, reconcile, and rendering services.
- `ports.CapabilityProvider` declares which operations are safe to expose in
  the API and UI.
- `ports.PanelUpdater`, `ports.CoreUpdater`, `ports.WebCertProvider`, and
  `ports.RealityScanner` are optional capabilities. Adapters do not implement
  unrelated vendor operations just to satisfy one large interface.
- `adapters/panel.Registry` maps a stable `domain.PanelKind` to a constructor.
- `adapters/panel.Pool` can hold different adapter implementations at the same
  time and routes calls by the local panel ID.

Adding another panel implementation requires:

1. Add a stable `domain.PanelKind` value.
2. Implement `ports.PanelClient` in `internal/adapters/<kind>` and return an
   accurate capability list. Unsupported required compatibility methods must
   wrap `ports.ErrPanelCapabilityUnsupported`.
3. Register the constructor in `app.Build`.
4. Add the type to the admin server selector and API TypeScript union.
5. Add contract tests around authentication, response envelopes, read
   normalization, client binding, and every advertised write capability.

Adapter constructors must validate local configuration but must not perform
network I/O. Connectivity belongs to the normal test/probe flow, which keeps
startup deterministic and lets the pool replace an adapter atomically.

## Current support

| Capability | 3X-UI | S-UI |
| --- | --- | --- |
| Inbound read/import | Yes | Yes |
| Inbound create/update/delete | Yes | Yes |
| Per-inbound enable/disable | Yes | No (not represented by S-UI) |
| Client read/write and multi-inbound binding | Yes | Yes |
| Traffic and last-online polling | Yes | Yes |
| Status/version probe | Yes | Yes |
| Panel/core upgrade, web certificate, Reality scan | Yes | No |

The S-UI adapter uses token-authenticated `/apiv2` endpoints. The admin API
continues accepting its historical Xray-shaped inbound payload for backward
compatibility, but `internal/pkg/nodespec` decodes the structured fields into a
vendor-neutral model before the S-UI adapter emits native sing-box objects.
The supported write subset is VLESS, VMess, Trojan, Shadowsocks 2022, and
Hysteria 2 with TCP, WebSocket, gRPC, HTTPUpgrade, or HTTP transport and TLS or
VLESS REALITY where applicable. XHTTP and the raw advanced JSON editor remain
3X-UI-only so adapter conversion never silently discards vendor-specific
options.

Modern sing-box performs sniffing through route actions rather than persisted
per-inbound fields. The S-UI editor therefore hides PSP's Xray-specific
per-inbound sniffing and socket/transport controls that have no native mapping.
The structured editor submits neutral defaults, while the adapter rejects
modelled non-default unsupported values instead of silently dropping them.

S-UI stores TLS independently from inbounds. PSP creates a uniquely named,
per-inbound TLS row, swaps it atomically on update, and only garbage-collects
unreferenced TLS rows carrying the `PSP:` prefix. Hand-managed or shared S-UI
TLS records are never deleted. S-UI has no persisted per-inbound enable flag,
so the capability is not advertised and the admin UI disables that switch.
