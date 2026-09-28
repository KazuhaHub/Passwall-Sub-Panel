import {
  LOCATION_SOURCES, type AttentionEntry, type QueueRow, type ReviewBadge, type RiskUserSummary,
} from '@/api/riskCenter'
import type { ServiceStatus } from '@/api/types'
import { serviceStateOf } from '@/utils/userAccess'

/**
 * The account an action is about, as much as the action matrix needs and no
 * more. Built from a drawer summary or from a queue row, so the drawer's
 * action bar and the queue's row menu decide from the same facts — and the
 * menu needs no fetch to decide.
 */
export interface RiskSubject {
  id: number
  upn: string
  /** The access decision's service state (what the Users page shows). */
  service_state: ServiceStatus
  /** The hold's stored reason; '' when the service axis carries none. */
  service_disabled_reason: string
  attention: AttentionEntry[]
  review: ReviewBadge
}

export function subjectOfSummary(s: RiskUserSummary): RiskSubject {
  const r = s.review
  return {
    id: s.user.id, upn: s.user.upn, service_state: serviceStateOf(s.user),
    service_disabled_reason: s.user.service_disabled_reason ?? '',
    attention: s.attention,
    review: { dismissed: r.dismissed, reopened: r.reopened, lapsed: r.lapsed, trusted: r.trusted, escalated: r.escalated },
  }
}

export function subjectOfRow(r: QueueRow): RiskSubject {
  return {
    id: r.user_id, upn: r.upn, service_state: r.service_state,
    service_disabled_reason: r.service_disabled_reason ?? '',
    attention: r.sources, review: r.review,
  }
}

export type RiskActionKind = 'pause' | 'convert_manual' | 'resume' | 'dismiss' | 'redismiss' | 'undismiss'
  | 'trust' | 'untrust'

/** The service states a pause applies to: the account is being served. */
const SERVING: readonly string[] = ['active', 'emergency_active']

/**
 * The holds the risk center may lift: the detector's own, an admin's geo
 * hold, and the Users page's manual pause. Any other (a blocked client, the
 * quota, expiry, a disabled account) belongs to the Users page, whose rules
 * for it the risk center does not repeat.
 */
export const RESUMABLE_REASONS: readonly string[] = ['geo_auto', 'geo_anomaly', 'service_manual']

const has = (s: RiskSubject, source: string) => s.attention.some(a => a.source === source)

/** Suspended by a hold nobody here may lift: the drawer says so and links to
 *  the Users page instead of offering an action. */
export function otherHold(s: RiskSubject): boolean {
  return !SERVING.includes(s.service_state) && !RESUMABLE_REASONS.includes(s.service_disabled_reason)
}

/**
 * The reason a pause from the risk center records. geo_anomaly exists to
 * count the location detector's false positives, so only a pause the
 * location evidence prompted (geo, or the detector's own hold) may carry it;
 * any other is the Users page's manual pause.
 */
export function pauseReason(s: RiskSubject): 'geo_anomaly' | 'service_manual' {
  return has(s, 'geo') || has(s, 'geo_auto') ? 'geo_anomaly' : 'service_manual'
}

/** The label key naming a liftable hold, in the Users page's words. */
export function holdLabelKey(reason: string): string {
  switch (reason) {
    case 'geo_auto': return 'admin:users.status.geo_auto'
    case 'geo_anomaly': return 'admin:users.status.geo_manual'
    default: return 'admin:users.status.service_suspended'
  }
}

/**
 * The levels a dismissal accepts: every one the admin saw, the detector's
 * hold included although it has no chip of its own. A worse level on the
 * server by now is a 409 `changed`, so nobody accepts an escalation they never
 * saw.
 */
export function expectedLevels(s: RiskSubject): Record<string, string> {
  return Object.fromEntries(s.attention.map(a => [a.source, a.level]))
}

/**
 * THE action matrix, in display order (§3.6). `where` differs in one row
 * only: the row menu offers trust only where trust changes something — a
 * location source or the detector's hold — since a devices-only row would
 * read trust as the cure for a device count; the drawer, which shows every
 * detector, offers it to any untrusted account.
 */
export function availableActions(s: RiskSubject, where: 'drawer' | 'menu'): RiskActionKind[] {
  const out: RiskActionKind[] = []
  const r = s.review
  if (SERVING.includes(s.service_state)) out.push('pause')
  if (s.service_disabled_reason === 'geo_auto') out.push('convert_manual')
  if (RESUMABLE_REASONS.includes(s.service_disabled_reason)) out.push('resume')
  if (s.attention.length > 0 && (!r.dismissed || r.reopened || r.lapsed)) {
    // A dismissal that no longer holds is dismissed AGAIN: the words say the
    // admin is accepting something new, not repeating a no-op.
    out.push(r.dismissed ? 'redismiss' : 'dismiss')
  }
  if (r.dismissed) out.push('undismiss')
  if (!r.trusted && (where === 'drawer' || LOCATION_SOURCES.some(src => has(s, src)) || has(s, 'geo_auto'))) {
    out.push('trust')
  }
  if (r.trusted) out.push('untrust')
  return out
}
