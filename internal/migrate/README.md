# internal/migrate

`psp migrate` subcommand — embedded one-shot side-by-side migration from
the legacy schema (≤ v2.5.x) to v3.0.0. Modelled on the Cloudreve `drive` →
`drive_v2` upgrade pattern: the v3.0.0 panel only knows about the new
database, so the old DB stays 100% intact as a permanent backup until the
operator decides to drop it.

**Invocation** (no separate binary; bundled into the main `psp` binary):

```bash
psp migrate --driver=sqlite|mysql --src=<SRC> --dst=<DST> [--dry-run]
```

See `docs/UPGRADE-v3.0.0.md` for full upgrade walkthrough including Docker.

## Lifecycle

This package's code is part of the v3.x binary and **stays there until the
v4.0.0 release**. At that point the legacy struct definitions and migration
logic here are replaced with the v3.x → v4 equivalents (and any user who
hadn't yet reached v3.x must first upgrade to v3.x before reaching v4).
Linear major upgrades only — see `docs/ARCHITECTURE.md §16`.

## What it does

- Reads the source DB (`--src`) — never writes to it.
- Creates the v3.0.0 schema on the destination DB (`--dst`) via the main
  program's `mysql.EnsureSchema`.
- Copies / transforms each table:

| Legacy table | v3.0.0 destination | Transform |
|---|---|---|
| `ui_settings` (one wide row) | `settings` (KV) | Splits ~40 fields into KV rows grouped by `type` (site / auth / sub / security / runtime / notice / notify). The migration also adds a `_migration` sentinel row at start so a crashed re-run is caught by the empty-dst guard. |
| `mail_settings.{expire_before_days, traffic_remain_percent}` | `settings.type='notify'` | Moves the two notify thresholds out of `mail_settings`. |
| `mail_settings` (other fields) | `mail_settings` (slimmed) | SMTP-connection subset only. |
| `saml_config` | `saml_settings` | Renamed; same fields. |
| `oidc_config` | `oidc_settings` | Renamed; same fields. |
| `xui_clients` | `user_xui_clients` | Renamed; `panel_name` dropped; `last_raw_*_bytes` seeded from the latest legacy `client_traffic_snapshots` row per (panel, inbound, email) so the first post-migration traffic poll does not double-count history. |
| `nodes` | `nodes` | `panel_name` column dropped. |
| `users` / `groups_` / `xui_panels` / `traffic_snapshots` / `node_traffic_snapshots` / `audit_log` / `sub_logs` / `sync_tasks` / `mail_templates` / `mail_sent` | same name in v3.0.0 | Copied verbatim. `users.period_baseline_bytes` backfilled from pre-period_start snapshot. |
| `client_traffic_snapshots` | (not copied) | Pre-v3.0.0 stored raw counters; v3.0.0 stores lifetime. Mixing the two would corrupt history graphs. The latest raw value per client is preserved on the new `user_xui_clients.last_raw_*` columns so live traffic accounting continues correctly. |
| `rule_sets` | (dropped) | Dead code — rule sets actually live in `config/rulesets/*.yaml`. |

## Safety guarantees

- The source DB is opened in read-only paths only; the migration never
  issues a write against `--src`.
- The destination DB must be empty (no rows in `settings`) — re-running
  over a half-migrated DB would double-insert. The program exits cleanly
  with a friendly error if it detects existing v3.0.0 data, including the
  `_migration` sentinel.
- Each table category is wrapped in its own statement; a failure on one
  category does not double-insert earlier writes on re-run (sentinel
  guards re-entry).

## On encrypted columns

AES-GCM-encrypted values (mail password, SAML SP key, OIDC client secret,
3X-UI panel API token / admin password) move as opaque ciphertext — this
code never decrypts or re-encrypts. The v3.0.0 panel decrypts at boot
using the `SecretKeyMaterial` already configured in your `config.yaml`.
Migration only succeeds end-to-end if that key matches what the legacy
panel was using; if not, the v3.0.0 panel will surface a clear "decrypt
secret" error on first boot and you fix `config.yaml` separately.
