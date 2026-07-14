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
| Inbound create/update/delete | Yes | Not yet |
| Client read/write and multi-inbound binding | Yes | Yes |
| Traffic and last-online polling | Yes | Yes |
| Status/version probe | Yes | Yes |
| Panel/core upgrade, web certificate, Reality scan | Yes | No |

The S-UI adapter uses token-authenticated `/apiv2` endpoints. Its inbound
objects are native sing-box configuration, while the existing structured node
editor still produces Xray-shaped settings. S-UI inbound writes are therefore
deliberately disabled until the editor and adapters share a canonical node
spec; this prevents lossy or destructive conversion. Existing S-UI inbounds
can be imported and then use the normal PSP client provisioning, traffic,
subscription, and reconciliation paths.
