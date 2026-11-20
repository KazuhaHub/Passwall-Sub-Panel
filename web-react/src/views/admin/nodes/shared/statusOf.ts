import type { OrderRow } from '@/api/nodes'
import type { Tone } from '@/components/common/tones'

/**
 * The one status of a list row (design section 2.4). The server already decides publication (`published` and
 * `withheld`); the client adds only what the server does not fold in: a deleted entry server, the direct row's
 * config push and endpoint drift (they come from the node join), and health.
 */
export type StatusId =
  | 'entry_missing' | 'landing_disabled' | 'hidden_direct' | 'disabled' | 'waiting_entry' | 'egress' | 'probe'
  | 'source_guard' | 'cdn_unfit' | 'config_failed' | 'config_pending' | 'endpoint_drift'
  | 'unreachable' | 'server_unreachable' | 'inbound_missing' | 'inconclusive' | 'inbound_disabled'
  | 'ok' | 'unchecked'

export interface RowStatus {
  id: StatusId
  tone: Tone
  /** A quiet status is drawn as a dot with its words for assistive tech only; everything else is a badge. */
  quiet: boolean
}

/** What the node join adds for a direct row. Absent when the join has not loaded (those rows then show no node-level state). */
export interface NodeStatusFacts {
  config_sync_state?: string
  endpoint_in_sync?: boolean
  observed_protocol?: string
  observed_port?: number
}

const WITHHELD: Record<string, { id: StatusId; tone: Tone }> = {
  landing_disabled: { id: 'landing_disabled', tone: 'neutral' },
  hidden_direct: { id: 'hidden_direct', tone: 'neutral' },
  disabled: { id: 'disabled', tone: 'neutral' },
  waiting_entry: { id: 'waiting_entry', tone: 'info' },
  egress: { id: 'egress', tone: 'warning' },
  probe: { id: 'probe', tone: 'danger' },
  source_guard: { id: 'source_guard', tone: 'warning' },
  cdn_unfit: { id: 'cdn_unfit', tone: 'warning' },
}

const HEALTH: Record<string, { id: StatusId; tone: Tone }> = {
  unreachable: { id: 'unreachable', tone: 'danger' },
  panel_unreachable: { id: 'server_unreachable', tone: 'danger' },
  inbound_missing: { id: 'inbound_missing', tone: 'danger' },
  inconclusive: { id: 'inconclusive', tone: 'warning' },
  inbound_disabled: { id: 'inbound_disabled', tone: 'warning' },
}

/** Health states that read as "cannot be reached" for the KPI and the facet. */
export const UNREACHABLE_HEALTH = ['unreachable', 'panel_unreachable', 'inbound_missing'] as const

const loud = (id: StatusId, tone: Tone): RowStatus => ({ id, tone, quiet: false })

export function statusOf(row: OrderRow, node?: NodeStatusFacts): RowStatus {
  // A deleted entry server is shown even when the server still calls the line published: its forward row can
  // outlive the server (design gap G3).
  if (row.entry?.missing) return loud('entry_missing', 'danger')
  const withheld = row.withheld ? WITHHELD[row.withheld] : undefined
  if (withheld) return loud(withheld.id, withheld.tone)
  if (row.line_type === 'direct' && node) {
    // 'drift' is declared but has no producer (reconcile repairs it in the same call), so it is not a row state.
    if (node.config_sync_state === 'failed') return loud('config_failed', 'danger')
    if (node.config_sync_state === 'pending') return loud('config_pending', 'warning')
    const observed = !!node.observed_protocol || (node.observed_port ?? 0) > 0
    if (observed && node.endpoint_in_sync === false) return loud('endpoint_drift', 'warning')
  }
  const health = HEALTH[row.health?.state ?? '']
  if (health) return loud(health.id, health.tone)
  if (row.health?.state === 'ok') return { id: 'ok', tone: 'success', quiet: true }
  return { id: 'unchecked', tone: 'neutral', quiet: true }
}

/** The locale key (under `nodes.`) of a status's words: withheld reasons and the entry-missing case live under `withheld`. */
export function statusLabelKey(id: StatusId): string {
  switch (id) {
    case 'entry_missing': case 'landing_disabled': case 'hidden_direct': case 'disabled': case 'waiting_entry':
    case 'egress': case 'probe': case 'source_guard': case 'cdn_unfit':
      return `nodes.withheld.${id}`
    default:
      return `nodes.status.${id}`
  }
}

/** The key of the sentence that explains a status in the sheet; only a withheld reason has one. */
export function statusDetailKey(id: StatusId): string | null {
  return statusLabelKey(id).startsWith('nodes.withheld.') ? `nodes.withheld_detail.${id}` : null
}
