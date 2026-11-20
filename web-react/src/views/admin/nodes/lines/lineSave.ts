import { getNode, updateNodeMetadata } from '@/api/nodes'
import type { Node, RelayLine } from '@/api/types'
import type { TunnelConfirmOptions } from '@/utils/tunnelConfirm'
import { factorTextToMilli } from '@/utils/trafficFactor'
import { draftOf, wireLine, type LineDraft } from './lineModel'

/** What one editor or menu action does to a node's lines (design 4.4.5, 4.4.7, 5.1). */
export type LineChange =
  /** Replace the line with this id, or append it when the draft has none. */
  | { kind: 'save'; draft: LineDraft }
  /** Enable or disable lines; several at once is the bulk bar's, one write per node. */
  | { kind: 'toggle'; lineIds: readonly string[]; enabled: boolean }
  | { kind: 'remove'; lineId: string }
  /** The direct line: its switch and, when it was edited, its multiplier. */
  | { kind: 'direct'; hidden: boolean; factor?: { text: string; milli: number | null } }

/**
 * The request body for a change, built on the node AS READ A MOMENT AGO.
 *
 * `relays` is a full replace on the server, and `hide_direct` is applied only when `relays` is present, so every
 * line save re-sends the rest of the lines and the node's own switch. `flow` is assigned on every metadata save,
 * so it is sent with the node's current value. Nothing else goes: the name, address, region, tags and exit are
 * the General and Exit tabs' (an empty or absent field means keep), and `sort_order` is never sent, because a
 * node's position is written only by the order endpoint.
 */
export function bodyFor(fresh: Node, change: LineChange) {
  const lines = (fresh.relays ?? []).map(l => wireLine(draftOf(l)))
  let relays: RelayLine[]
  switch (change.kind) {
    case 'save': {
      const next = wireLine(change.draft)
      const at = change.draft.id ? lines.findIndex(l => l.id === change.draft.id) : -1
      relays = at >= 0 ? lines.map((l, i) => (i === at ? next : l)) : [...lines, next]
      break
    }
    case 'toggle': relays = lines.map(l => (l.id && change.lineIds.includes(l.id) ? { ...l, enabled: change.enabled } : l)); break
    case 'remove': relays = lines.filter(l => l.id !== change.lineId); break
    default: relays = lines
  }
  const milli = change.kind === 'direct' && change.factor ? (change.factor.milli ?? factorTextToMilli(change.factor.text)) : null
  return {
    flow: fresh.flow ?? '',
    relays,
    hide_direct: change.kind === 'direct' ? change.hidden : !!fresh.hide_direct,
    ...(milli !== null ? { traffic_factor_milli: milli } : {}),
  }
}

/**
 * Reads the node again, applies one change to its lines and writes them back. The read narrows the window in
 * which another administrator's edit of a different line of the same node would be overwritten (the endpoint
 * has no precondition; design gap G7), it does not close it.
 *
 * A tunnel line that is created or removed makes the server ask for confirmation first; `confirm.present` shows
 * its plan inside the caller's own dialog, so the person answers one dialog, not two. Declining rejects with the
 * server's refusal and nothing is written. `addedId` is the id the server gave a line this save appended.
 */
export async function saveLineChange(nodeId: number, change: LineChange, confirm?: TunnelConfirmOptions): Promise<{ saved: Node; before: Node; addedId?: string }> {
  const { node: before } = await getNode(nodeId, { signal: confirm?.signal })
  const saved = await updateNodeMetadata(nodeId, bodyFor(before, change), confirm)
  const known = new Set((before.relays ?? []).map(l => l.id))
  const addedId = change.kind === 'save' && !change.draft.id ? (saved.relays ?? []).find(l => l.id && !known.has(l.id))?.id : undefined
  return { saved, before, addedId }
}
