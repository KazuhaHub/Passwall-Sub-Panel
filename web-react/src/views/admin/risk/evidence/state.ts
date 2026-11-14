import type { GeoAnomaly } from '@/api/geoAnomalies'
import type { AccessToneKind } from '@/utils/accessControl'

/**
 * Every state a detector row can be in: the seven the server stores (the
 * concurrent-location verdict and the four risk kinds share them), and
 * `not_computed` for a detector with no row at all, which is "not judged
 * yet", never blank and never clean.
 */
export type DetectorState = GeoAnomaly['state'] | 'not_computed'

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

/**
 * The row of the closed colour table a state is drawn in (the badge is a
 * ToneBadge, never a <Chip color>). Only `flagged` is actionable, and `suspect` is
 * the visible ramp, so neither may look like the other or like a clean row;
 * every state that is not a verdict is deliberately NOT green: `unknown` on a
 * fleet whose geo database has stopped working looks exactly like a clean
 * fleet if it is coloured like one. A clean verdict that was judged on a floor of
 * its sources (`complete` false) is "cannot be sure", not the green row.
 */
export function stateTone(state: DetectorState, complete?: boolean): AccessToneKind {
  switch (state) {
    case 'flagged': return 'failing'
    case 'suspect': return 'attention'
    case 'clean': return complete === false ? 'unknown' : 'ok'
    case 'exempt': return 'exempt'
    case 'disabled': return 'paused'
    case 'idle': return 'none'
    default: return 'unknown' // unknown, not_computed
  }
}
