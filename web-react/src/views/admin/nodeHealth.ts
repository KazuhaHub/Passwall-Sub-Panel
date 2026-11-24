/**
 * The node health states the Go backend can write, and the wording key of each. The colour of each state is
 * decided where the states are drawn: nodes/shared/statusOf.ts (the nodes list) and dashboard/healthTone.ts.
 *
 * Extracted out of NodesView so a test can IMPORT this rather than scrape the
 * component's source — the same reason configSync.ts exists. A guard that
 * reads JSX text is one refactor away from silently passing on an empty match.
 *
 * WHY THIS FILE EXISTS AT ALL: until 2026-09-09 the palette had colours for
 * three states (panel_unreachable / inbound_missing / inbound_disabled) that
 * have had NO producer since v3.5, and no entry for `unreachable` — the one
 * state the data-plane probe actually writes. Unknown keys fall back to the
 * empty-string entry, so a node whose port was closed rendered as the grey
 * "not yet probed" dot. Down displayed as no signal, which is the precise
 * mirror of what the probe's own comment says the state exists to prevent.
 */

/** Every value domain.NodeHealthState can hold. Mirrors internal/domain/types.go. */
export const NODE_HEALTH_STATES = [
  '', // NodeHealthUnknown — never probed. Deliberately distinct from 'inconclusive'.
  'ok',
  'unreachable',
  'inconclusive',
  'panel_unreachable',
  'inbound_missing',
  'inbound_disabled',
] as const

export type NodeHealthState = (typeof NODE_HEALTH_STATES)[number]

/** States the health service actually writes today. The rest are vestigial. */
export const NODE_HEALTH_PRODUCED: readonly NodeHealthState[] = [
  '',
  'ok',
  'unreachable',
  'inconclusive',
]

/** i18n leaf under `nodes.health` for each state. */
export const NODE_HEALTH_KEY: Record<NodeHealthState, string> = {
  '': 'unknown',
  ok: 'ok',
  unreachable: 'unreachable',
  inconclusive: 'inconclusive',
  panel_unreachable: 'panel_unreachable',
  inbound_missing: 'inbound_missing',
  inbound_disabled: 'inbound_disabled',
}
