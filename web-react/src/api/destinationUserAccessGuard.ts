import type { DestinationUserAccessView, DestinationUsagePage } from './accessControl'

const record = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const count = (v: unknown): v is number => typeof v === 'number' && Number.isInteger(v) && v >= 0
const id = (v: unknown): v is number => count(v) && Number.isSafeInteger(v) && v > 0
const instant = (v: unknown): v is number => count(v) && v > 0 && v <= 8.64e15
const text = (v: unknown) => typeof v === 'string' || v === null
const source = (v: unknown): v is string => typeof v === 'string' && /^[pg][1-9]\d*$/.test(v) && id(Number(v.slice(1)))

export function isDestinationUsagePage(v: unknown): v is DestinationUsagePage {
  if (!record(v) || !Array.isArray(v.items) || v.items.length > 20 ||
      !count(v.total_sites) || !count(v.total_count) || v.total_sites < v.items.length ||
      !v.items.every(row => record(row) && typeof row.site === 'string' && row.site.length > 0 && row.site.length <= 253 && count(row.count) && row.count > 0)) return false
  const losses = v.losses
  return record(losses) && losses.scope === 'panel' && losses.complete === false && count(losses.rows) && count(losses.events) && count(losses.unmatched)
}

// Old routers can return their SPA document with HTTP 200. Treat absent or
// malformed account telemetry as unavailable rather than inventing no hits.
export function isDestinationUserAccessView(v: unknown): v is DestinationUserAccessView {
  if (!record(v)) return false
  if (!(v.usage_available === null && v.usage_nodes === null)) {
    if (typeof v.usage_available !== 'boolean' || !Array.isArray(v.usage_nodes) ||
        !v.usage_nodes.every(p => record(p) && id(p.panel_id) && typeof p.name === 'string') ||
        v.usage_available !== (v.usage_nodes.length > 0) || !id(v.usage_retention_days) || v.usage_retention_days > 30) return false
  }
  const g = v.group, e = v.exemption
  if (g !== null && (!record(g) || !id(g.id) || typeof g.name !== 'string' ||
    !(g.mode === 'open' && g.stage === '' || g.mode === 'allowlist' && (g.stage === 'trial' || g.stage === 'enforce')))) return false
  if (e !== null && (!record(e) || !id(e.user_id) || !text(e.upn) || typeof e.reason !== 'string' || !id(e.created_by) ||
    !text(e.created_by_upn) || !instant(e.created_at) || !(e.expires_at === null || instant(e.expires_at)) || typeof e.expired !== 'boolean')) return false
  if (v.hits_available === null && v.recent_hits === null) return true // Stage-1c server.
  const h = v.recent_hits
  if (typeof v.hits_available !== 'boolean' || !record(h) || !id(h.days) || h.days > 7 || !Array.isArray(h.items)) return false
  const losses = h.losses
  if (!record(losses) || losses.scope !== 'panel' || losses.complete !== false || !count(losses.rows) || !count(losses.events) || !count(losses.unmatched)) return false
  return h.items.every(row => record(row) && source(row.source) && text(row.source_name) &&
    (row.action === 'block' || row.action === 'observe' && row.source.startsWith('p')) && count(row.count) && instant(row.last_at) &&
    Array.isArray(row.top_dests) && row.top_dests.length <= 3 && row.top_dests.every(d => record(d) && typeof d.dest === 'string' && d.dest.length > 0 && count(d.port) && d.port <= 65535 && count(d.count)) &&
    Array.isArray(row.panels) && row.panels.every(p => record(p) && id(p.panel_id) && text(p.name)))
}
