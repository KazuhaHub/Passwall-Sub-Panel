import { Link } from 'react-router'

import type { ReviewBadge, RiskUserBasics, RiskUserSummary } from '@/api/riskCenter'
import { StatusBadge } from '@/components/common/StatusBadge'
import type { Tone } from '@/components/common/tones'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useUserUsage } from '@/query/traffic'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { formatGB } from '@/utils/riskSignals'
import { accountStateOf, serviceStateOf } from '@/utils/userAccess'
import { useRiskT } from '../useRiskT'

const U = 'admin:users.status.'

/** The Users page's words for the account axis, so the drawer and the Users
 *  table never name one state two ways. */
export function accountLabelKey(u: RiskUserBasics): string {
  switch (accountStateOf(u)) {
    case 'active': return `${U}account_active`
    case 'pending_approval': return `${U}pending_approval`
    case 'pending_delete': return `${U}pending_delete`
    case 'pending_email_verify': return `${U}pending_email_verify`
    default: return `${U}account_disabled`
  }
}

/**
 * The service state: the location holds by their own names (the detector's and an admin's), every other
 * state in the Users page's words. Keyed by the stored reason, not the state, because both holds are one
 * state (manual_suspended) there. The tone says how loud it is: a location hold is the alarm, any other
 * way the service is not running a notice, and a running service is the quiet dot.
 */
export function serviceChip(u: RiskUserBasics): { key: string; tone: Tone } {
  if (u.service_disabled_reason === 'geo_auto') return { key: `${U}geo_auto`, tone: 'danger' }
  if (u.service_disabled_reason === 'geo_anomaly') return { key: `${U}geo_manual`, tone: 'danger' }
  switch (serviceStateOf(u)) {
    case 'active': return { key: `${U}service_active`, tone: 'success' }
    case 'emergency_active': return { key: `${U}emergency_active`, tone: 'warning' }
    case 'traffic_exceeded': return { key: `${U}traffic_exhausted`, tone: 'warning' }
    case 'expired': return { key: `${U}expired`, tone: 'warning' }
    case 'blocked_client': return { key: `${U}blocked`, tone: 'warning' }
    case 'manual_suspended': return { key: `${U}service_suspended`, tone: 'warning' }
    default: return { key: `${U}service_unavailable`, tone: 'warning' }
  }
}

/** The review badges, most telling first: a dismissal that no longer holds
 *  outranks one that does, and trust stands beside either. */
export function ReviewBadges({ review }: { review: ReviewBadge }) {
  const { t } = useRiskT()
  const R = 'admin:risk_center.review.'
  return (
    <>
      {review.dismissed && (review.reopened
        ? <StatusBadge tone="warning" label={t(`${R}badge_reopened`)} />
        : review.lapsed
          ? <StatusBadge tone="warning" label={t(`${R}badge_lapsed`)} />
          : <Badge variant="outline">{t(`${R}badge_dismissed`)}</Badge>)}
      {review.trusted && <Badge variant="secondary">{t(`${R}badge_trusted`)}</Badge>}
    </>
  )
}

/**
 * Who the account is and where its service stands: names, group, the two access axes (said apart, so a
 * service hold never reads as the account being disabled), the review badges, the hold's time and the
 * message the user was given, and the period's usage with the way to its trend. The dialog's own title is the
 * fixed "risk details" caption (it is also what names the dialog while the summary is loading or has failed);
 * the account's name is the first line here.
 */
export default function DrawerHeader({ summary }: { summary: RiskUserSummary }) {
  const { t } = useRiskT()
  const scope = useQueryScope()
  const panelTz = useSiteStore(s => s.timezone)
  const u = summary.user
  // Period usage is not on the summary; a failed read leaves it "—" rather
  // than a zero that would read as "used nothing".
  const { data: usage } = useUserUsage(scope, u.id)
  const limit = u.traffic_limit_bytes > 0 ? formatGB(u.traffic_limit_bytes) : t('admin:users.detail.unlimited')
  const svc = serviceChip(u)
  const r = summary.review
  const account = accountStateOf(u)
  return (
    <div data-testid="risk-drawer-header" className="flex flex-col gap-2">
      <div className="min-w-0">
        <p className="text-xs text-muted-foreground">{t('admin:risk_center.drawer.title')}</p>
        <p className="text-base leading-snug font-medium break-all">{u.upn}</p>
        <p className="mt-0.5 flex flex-wrap gap-x-2 text-xs text-muted-foreground">
          {u.display_name && <span>{u.display_name}</span>}
          <span>{`#${u.id}`}</span>
          {u.group_name && <span>{u.group_name}</span>}
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5">
        <span className="inline-flex items-center gap-1.5 text-sm">
          <span className="text-xs text-muted-foreground">{t('admin:risk_center.drawer.axis_account')}</span>
          <StatusBadge tone={account === 'active' ? 'neutral' : 'warning'} label={t(accountLabelKey(u))} />
        </span>
        <span className="inline-flex items-center gap-1.5 text-sm">
          <span className="text-xs text-muted-foreground">{t('admin:risk_center.drawer.axis_service')}</span>
          <StatusBadge tone={svc.tone} label={t(svc.key)} />
        </span>
        <ReviewBadges review={{ dismissed: r.dismissed, reopened: r.reopened, lapsed: r.lapsed, trusted: r.trusted,
          escalated: r.escalated }} />
      </div>
      {u.service_disabled_reason && (
        <p className="text-[13px] text-muted-foreground">
          {t('admin:risk_center.drawer.suspended_since', { time: formatMsDualTz(u.service_disabled_at_ms, panelTz) })}
          {/* The message the user was shown, as they saw it. */}
          {u.service_disable_detail ? ` · ${u.service_disable_detail}` : ''}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-x-2 text-[13px]">
        <span className="text-xs text-muted-foreground">{t('admin:users.detail.period_used')}</span>
        <span className="tabular-nums">{`${usage ? formatGB(usage.period_used_bytes) : '—'} / ${limit}`}</span>
        <Button variant="link" size="sm" render={<Link to={`/admin/traffic?tab=trend&scope=user&user=${u.id}`} />}>
          {t('admin:risk_center.drawer.open_traffic')}
        </Button>
      </div>
    </div>
  )
}
