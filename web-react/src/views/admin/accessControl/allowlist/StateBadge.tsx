import { useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { DestState } from '@/api/accessControl'
import ToneBadge from '@/components/ToneBadge'
import { accessTone, type AccessToneKind } from '@/utils/accessControl'

const TONE: Record<DestState, AccessToneKind> = { report: 'would_deny', on: 'allowlist_enforced', off: 'none' }

/**
 * A group's allowlist state as a badge: report-only (the flask: what it WOULD
 * do), on (the lock) and off with its lists kept (quiet). The day counts from
 * the day the state began; a state with no known start is written without one.
 */
export default function StateBadge({ state, day, scope = 'allowlist' }: {
  state: DestState
  day: number | null
  /** Where the badge is read: on the allowlist page the state alone, on the groups page prefixed with what it is ("Allowlist · on"). */
  scope?: 'allowlist' | 'group' | 'group-badge'
}) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const group = scope === 'group' && state !== 'off'
  const label = scope === 'group-badge'
    ? t(`admin:groups.destination.${state === 'report' ? 'badge_report' : 'badge_on'}`)
    : group
    ? state === 'report' && day !== null
      ? t('admin:groups.destination.report', { days: day })
      : t(`admin:groups.destination.${state === 'report' ? 'report_nodays' : 'on'}`)
    : state === 'report' && day !== null
      ? t('admin:access_control.allowlist.state.report_day', { days: day })
      : t(`admin:access_control.allowlist.state.${state}`)
  return <ToneBadge tone={accessTone(theme, TONE[state])} label={label} testId="allowlist-state" data={state} />
}
