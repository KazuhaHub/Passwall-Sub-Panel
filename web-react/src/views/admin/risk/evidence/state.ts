import type { GeoAnomaly } from '@/api/geoAnomalies'

/**
 * Every state a detector row can be in: the seven the server stores (the
 * concurrent-location verdict and the four risk kinds share them), and
 * `not_computed` for a detector with no row at all, which is "not judged
 * yet", never blank and never clean.
 */
export type DetectorState = GeoAnomaly['state'] | 'not_computed'

/**
 * Colour carries meaning here, so it is assigned by what the operator should
 * DO rather than by how alarming the word sounds.
 *
 * Only `flagged` is actionable. `suspect` is the visible ramp and must look
 * different from both a flag and a clean row — hiding it would make the
 * eventual flag appear out of nowhere. Everything else means the detector is
 * not in a position to judge, and those states are deliberately NOT green:
 * `unknown` on a fleet whose geo database has stopped working looks exactly
 * like a clean fleet if it is coloured like one.
 */
export function stateColor(state: DetectorState): 'error' | 'warning' | 'success' | 'info' | 'default' {
  switch (state) {
    case 'flagged': return 'error'
    case 'suspect': return 'warning'
    case 'clean': return 'success'
    case 'unknown': return 'info'
    default: return 'default'   // exempt / disabled / idle / not_computed
  }
}

/**
 * The admin-namespace key naming a state, from the one vocabulary every risk
 * surface reads (`risk_center.state.*`): the Geo tab and the risk tab used to
 * carry a word each, and idle read "no connection" on one and "no data" on
 * the other.
 *
 * An exemption by an admin's trust (`code` "trusted", the geo reason code and
 * the two location kinds' risk code alike) reads as trust, not as exempt:
 * trust is a decision about the account, an exemption a setting's reach, and
 * one word for both would make a trusted account look like one a policy
 * happens to cover. Only the exempt state is renamed — the code alone does
 * not make another state a trust verdict.
 */
export function stateLabelKey(state: DetectorState, code?: string): string {
  if (state === 'exempt' && code === 'trusted') return 'risk_center.state.trusted'
  return `risk_center.state.${state}`
}
