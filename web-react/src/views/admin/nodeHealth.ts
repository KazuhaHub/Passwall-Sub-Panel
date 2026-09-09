/**
 * The node health states the Go backend can write, and how each one renders.
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

/**
 * Dot colour per state. `inconclusive` is amber and NEVER green: the probe ran
 * and could not decide, and a fleet whose probe has gone blind must not look
 * like a healthy one. It is also not red — an obfuscated UDP node is
 * unprobeable by design, and painting it as a failure would train the operator
 * to ignore the colour.
 */
export function nodeHealthColor(state: string, md: { error: string; outlineVariant: string }): string {
  switch (state) {
    case 'ok':
      return '#22c55e'
    case 'unreachable':
    case 'panel_unreachable':
      return md.error
    case 'inconclusive':
      return '#eab308'
    case 'inbound_missing':
      return '#f97316'
    case 'inbound_disabled':
      return '#9ca3af'
    default:
      return md.outlineVariant // '' — never probed
  }
}
