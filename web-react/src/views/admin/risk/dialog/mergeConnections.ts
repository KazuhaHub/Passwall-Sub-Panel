import type { ConnRegion, ConnectionRecord, LiveConnection } from '@/api/riskCenter'

/**
 * One (panel, node, source) the account connected from, merged across the
 * live snapshot and the connection history. `online` means the source is in
 * the snapshot; `first_seen_ms` and `count` come from the history only (0
 * for a source the detector has not sampled yet); `seen_at` is the upstream
 * panel's own clock for a live source (unix seconds, 0 otherwise).
 */
export interface MergedConnection {
  key: string
  panel_id: number
  panel_name: string
  node: string
  source_key: string
  ip: string
  exclusion: string
  region: ConnRegion | null
  online: boolean
  first_seen_ms: number
  last_seen_ms: number
  count: number
  seen_at: number
}

const keyOf = (c: { panel_id: number; node: string; source_key: string }) =>
  `${c.panel_id}|${c.node}|${c.source_key}`

/**
 * The drawer's one connection list: the live snapshot and the history as a
 * single list, keyed by (panel, node, source) — the node is part of the key,
 * because one source on two nodes of a panel is two connections.
 *
 * For a source in both, the live side wins for what is judged NOW (the
 * exclusion, the place, the address shown) and the history supplies what it
 * alone knows (first seen, samples). "Last seen" of a live source is the
 * snapshot's time on PSP's clock, never the upstream `seen_at`: that is the
 * panel's clock, and set against the history's PSP times a skewed panel
 * would reorder the list. Online entries come first, then the most recently
 * seen.
 */
export function mergeConnections(
  live: LiveConnection[], history: ConnectionRecord[], takenAt: string | null,
): MergedConnection[] {
  const takenMs = takenAt ? Date.parse(takenAt) || 0 : 0
  const byKey = new Map<string, MergedConnection>()
  for (const h of history) {
    byKey.set(keyOf(h), {
      key: keyOf(h), panel_id: h.panel_id, panel_name: h.panel_name, node: h.node, source_key: h.source_key,
      ip: h.ip, exclusion: h.exclusion, region: h.region, online: false,
      first_seen_ms: h.first_seen_ms, last_seen_ms: h.last_seen_ms, count: h.count, seen_at: 0,
    })
  }
  for (const c of live) {
    const k = keyOf(c)
    const h = byKey.get(k)
    byKey.set(k, {
      key: k, panel_id: c.panel_id, panel_name: c.panel_name || h?.panel_name || '', node: c.node,
      source_key: c.source_key, ip: c.ip || h?.ip || '', exclusion: c.exclusion, region: c.region, online: true,
      first_seen_ms: h?.first_seen_ms ?? 0, last_seen_ms: Math.max(h?.last_seen_ms ?? 0, takenMs),
      count: h?.count ?? 0, seen_at: c.seen_at,
    })
  }
  return [...byKey.values()].sort((a, b) =>
    Number(b.online) - Number(a.online) || b.last_seen_ms - a.last_seen_ms || a.key.localeCompare(b.key))
}
