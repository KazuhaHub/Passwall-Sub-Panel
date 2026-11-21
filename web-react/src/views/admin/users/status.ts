import type { User } from '@/api/types'
import type { Tone } from '@/components/common/tones'
import { accountStateOf, serviceStateOf } from '@/utils/userAccess'

/**
 * A user has two independent switches, and each is told on its own (domain/access.go AccessSnapshot):
 * the ACCOUNT (can they sign in to the panel) and the SERVICE (does their subscription work). An account
 * lock wins over everything; hard service holds win over emergency access; expiry and quota switch the
 * service off only, so such a user can still sign in. `label` and `explain` are keys of the admin namespace.
 */
export interface Axis {
  tone: Tone
  label: string
  explain: string
  /** The server's own words for why, when it gave any. */
  detail?: string
  vars?: Record<string, unknown>
}

export function accountAxis(u: User): Axis {
  switch (accountStateOf(u)) {
    case 'active':
      return { tone: 'success', label: 'users.status.account_active', explain: 'users.axis.account_active' }
    case 'pending_approval':
      return { tone: 'info', label: 'users.status.pending_approval', explain: 'users.axis.pending_approval' }
    case 'pending_email_verify':
      return { tone: 'info', label: 'users.status.pending_email_verify', explain: 'users.axis.pending_email_verify' }
    case 'pending_delete':
      return { tone: 'neutral', label: 'users.status.pending_delete', explain: 'users.axis.pending_delete' }
    default:
      return { tone: 'danger', label: 'users.status.account_disabled', explain: 'users.axis.account_disabled', detail: u.auto_disabled_reason || undefined }
  }
}

/** The two location holds are one state (manual_suspended) with two stored reasons; each is named as the risk center names it. */
function suspendedKeys(u: User): { label: string; explain: string } {
  switch (u.service_disabled_reason) {
    case 'geo_auto': return { label: 'users.status.geo_auto', explain: 'users.axis.suspended_geo_auto' }
    case 'geo_anomaly': return { label: 'users.status.geo_manual', explain: 'users.axis.suspended_geo_manual' }
    default: return { label: 'users.status.service_suspended', explain: 'users.axis.suspended_manual' }
  }
}

export function serviceAxis(u: User): Axis {
  const detail = u.service_disable_detail || undefined
  switch (serviceStateOf(u)) {
    case 'active':
      return { tone: 'success', label: 'users.status.service_active', explain: 'users.axis.service_active' }
    case 'emergency_active':
      return { tone: 'info', label: 'users.status.emergency_active', explain: 'users.axis.emergency_active' }
    case 'traffic_exceeded':
      return { tone: 'warning', label: 'users.status.traffic_exhausted', explain: 'users.axis.traffic_exceeded' }
    case 'expired':
      return { tone: 'danger', label: 'users.status.expired', explain: 'users.axis.expired' }
    case 'blocked_client':
      return { tone: 'danger', label: 'users.status.blocked', explain: 'users.axis.blocked_client', detail }
    case 'manual_suspended':
      return { tone: 'danger', ...suspendedKeys(u), detail }
    case 'account_disabled':
      return { tone: 'neutral', label: 'users.status.service_unavailable', explain: 'users.axis.service_unavailable' }
    default: {
      // A status this build does not know: say what the server said, never invent a reading for it.
      const raw = String(serviceStateOf(u))
      return { tone: 'neutral', label: raw, explain: '', detail }
    }
  }
}
