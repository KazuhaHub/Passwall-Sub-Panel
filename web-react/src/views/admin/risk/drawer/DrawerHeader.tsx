import { Box, Button, Chip, IconButton, Typography, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { ReviewBadge, RiskUserBasics, RiskUserSummary } from '@/api/riskCenter'
import { useUserUsage } from '@/query/traffic'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { formatGB } from '@/utils/riskSignals'
import { accountStateOf, serviceStateOf } from '@/utils/userAccess'

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
 * The service chip: the location holds by their own names (the detector's
 * and an admin's), every other state in the Users page's words. Keyed by the
 * stored reason, not the state, because both holds are one state
 * (manual_suspended) there.
 */
export function serviceChip(u: RiskUserBasics): { key: string; color: 'error' | 'warning' | 'default' } {
  if (u.service_disabled_reason === 'geo_auto') return { key: `${U}geo_auto`, color: 'error' }
  if (u.service_disabled_reason === 'geo_anomaly') return { key: `${U}geo_manual`, color: 'error' }
  switch (serviceStateOf(u)) {
    case 'active': return { key: `${U}service_active`, color: 'default' }
    case 'emergency_active': return { key: `${U}emergency_active`, color: 'warning' }
    case 'traffic_exceeded': return { key: `${U}traffic_exhausted`, color: 'warning' }
    case 'expired': return { key: `${U}expired`, color: 'warning' }
    case 'blocked_client': return { key: `${U}blocked`, color: 'warning' }
    case 'manual_suspended': return { key: `${U}service_suspended`, color: 'warning' }
    default: return { key: `${U}service_unavailable`, color: 'warning' }
  }
}

/** The review badges, most telling first: a dismissal that no longer holds
 *  outranks one that does, and trust stands beside either. */
export function ReviewBadges({ review }: { review: ReviewBadge }) {
  const { t } = useTranslation(['admin'])
  const R = 'admin:risk_center.review.'
  return (
    <>
      {review.dismissed && (review.reopened
        ? <Chip size="small" color="warning" label={t(`${R}badge_reopened`)} />
        : review.lapsed
          ? <Chip size="small" color="warning" label={t(`${R}badge_lapsed`)} />
          : <Chip size="small" variant="outlined" label={t(`${R}badge_dismissed`)} />)}
      {review.trusted && <Chip size="small" variant="outlined" color="primary" label={t(`${R}badge_trusted`)} />}
    </>
  )
}

/**
 * Who the account is and where its service stands: names, group, the two
 * access axes, the review badges, the hold's time and the message the user
 * was given, and the period's usage with the way to its trend.
 */
export default function DrawerHeader({ summary, headingId, onClose, disabled = false }: {
  summary: RiskUserSummary
  headingId: string
  onClose: () => void
  disabled?: boolean
}) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const panelTz = useSiteStore(s => s.timezone)
  const u = summary.user
  // Period usage is not on the summary; a failed read leaves it "—" rather
  // than a zero that would read as "used nothing".
  const { data: usage } = useUserUsage(scope, u.id)
  const limit = u.traffic_limit_bytes > 0 ? formatGB(u.traffic_limit_bytes) : t('admin:users.detail.unlimited')
  const svc = serviceChip(u)
  const r = summary.review
  return (
    <Box data-testid="risk-drawer-header" sx={{ p: 2.5, pb: 1.5, display: 'flex', flexDirection: 'column', gap: 0.75 }}>
      <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 1 }}>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('admin:risk_center.drawer.title')}</Typography>
          <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1, flexWrap: 'wrap' }}>
            <Typography id={headingId} variant="h6" component="h2" sx={{ fontWeight: 700, wordBreak: 'break-all' }}>
              {u.upn}
            </Typography>
            {u.display_name && <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{u.display_name}</Typography>}
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{`#${u.id}`}</Typography>
            {u.group_name && <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{u.group_name}</Typography>}
          </Box>
        </Box>
        <IconButton disabled={disabled} onClick={onClose} aria-label={t('admin:risk_center.drawer.close')} sx={{ minWidth: 44, minHeight: 44 }}><CloseIcon /></IconButton>
      </Box>
      <Box sx={{ display: 'flex', gap: 0.75, flexWrap: 'wrap', alignItems: 'center' }}>
        <Chip size="small" variant="outlined" label={t(accountLabelKey(u))} />
        <Chip size="small" color={svc.color} variant={svc.color === 'default' ? 'outlined' : 'filled'} label={t(svc.key)} />
        <ReviewBadges review={{ dismissed: r.dismissed, reopened: r.reopened, lapsed: r.lapsed, trusted: r.trusted,
          escalated: r.escalated }} />
      </Box>
      {u.service_disabled_reason && (
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
          {t('admin:risk_center.drawer.suspended_since', { time: formatMsDualTz(u.service_disabled_at_ms, panelTz) })}
          {/* The message the user was shown, as they saw it. */}
          {u.service_disable_detail ? ` · ${u.service_disable_detail}` : ''}
        </Typography>
      )}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', fontSize: 13 }}>
        <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('admin:users.detail.period_used')}
        </Typography>
        <span>{`${usage ? formatGB(usage.period_used_bytes) : '—'} / ${limit}`}</span>
        <Button disabled={disabled} size="small" component={RouterLink} to={`/admin/traffic?tab=trend&scope=user&user=${u.id}`}
          sx={{ textTransform: 'none', minWidth: 44, minHeight: 44 }}>
          {t('admin:risk_center.drawer.open_traffic')}
        </Button>
      </Box>
    </Box>
  )
}
