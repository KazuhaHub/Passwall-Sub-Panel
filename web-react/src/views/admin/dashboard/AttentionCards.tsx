import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { DashboardExpiringRow, DashboardNodeAlert } from '@/api/dashboard'
import { StatusBadge } from '@/components/common/StatusBadge'
import { StatusDot } from '@/components/common/StatusDot'
import type { Tone } from '@/components/common/tones'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { IconChevronRight, IconServer, IconTime } from '@/lib/icons'
import { formatDualDate } from '@/utils/datetime'
import { useAdminT } from '../traffic/useAdminT'
import { healthTone } from './healthTone'

const MS_PER_DAY = 86400000

/** One card of the "needs attention" row: a title, a link to the page where it is resolved, and either the rows or a quiet all-clear. */
function AttentionCard({ icon, title, to, loading, empty, emptyLabel, children }: {
  icon: ReactNode
  title: string
  to: string
  loading: boolean
  empty: boolean
  emptyLabel: string
  children: ReactNode
}) {
  const t = useAdminT()
  return (
    <Card size="sm" className="min-w-0">
      <CardHeader>
        <CardTitle className="flex min-w-0 items-center gap-2 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-muted-foreground">{icon}<span className="min-w-0 truncate">{title}</span></CardTitle>
        <CardAction>
          <Link to={to} className="inline-flex min-h-8 items-center gap-1 rounded text-sm text-brand outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50">
            {t('dashboard.traffic.view_all')}<IconChevronRight className="size-3.5" aria-hidden />
          </Link>
        </CardAction>
      </CardHeader>
      <CardContent>
        {loading ? <CardSkeleton lines={2} />
          : empty ? <StatusDot tone="success" label={emptyLabel} className="py-1 text-muted-foreground" />
            : <ul className="divide-y">{children}</ul>}
      </CardContent>
    </Card>
  )
}

/** Nodes the health probe is not happy with. Each row says why in words, not only a colour. */
export function NodeAlertsCard({ alerts, loading }: { alerts: readonly DashboardNodeAlert[]; loading: boolean }) {
  const t = useAdminT()
  return (
    <AttentionCard
      icon={<IconServer aria-hidden />}
      title={t('dashboard.alerts.node_health')}
      to="/admin/nodes"
      loading={loading}
      empty={alerts.length === 0}
      emptyLabel={t('dashboard.alerts.node_health_empty')}
    >
      {alerts.map(n => {
        const state = n.health_state || ''
        const label = t(`nodes.health.${state === '' ? 'unknown' : state}`, { defaultValue: state })
        return (
          <li key={n.id} className="flex min-w-0 items-center gap-3 py-2">
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium" title={n.display_name}>{n.display_name}</div>
              <div className="truncate text-xs text-muted-foreground" title={n.panel_name}>{n.panel_name}</div>
            </div>
            <StatusBadge tone={healthTone(state)} label={label} className="shrink-0" />
          </li>
        )
      })}
    </AttentionCard>
  )
}

/** Accounts that expire within the week; the closer the date, the louder the badge. */
export function ExpiringCard({ users, loading, panelTz }: { users: readonly DashboardExpiringRow[]; loading: boolean; panelTz: string }) {
  const t = useAdminT()
  return (
    <AttentionCard
      icon={<IconTime aria-hidden />}
      title={t('dashboard.alerts.expiring_soon')}
      to="/admin/users"
      loading={loading}
      empty={users.length === 0}
      emptyLabel={t('dashboard.alerts.expiring_empty')}
    >
      {users.map(u => {
        const days = Math.ceil((new Date(u.expire_at).getTime() - Date.now()) / MS_PER_DAY)
        const tone: Tone = days <= 1 ? 'danger' : days <= 3 ? 'warning' : 'neutral'
        const name = u.display_name || u.upn
        return (
          <li key={u.id} className="flex min-w-0 items-center gap-3 py-2">
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium" title={name}>{name}</div>
              <div className="truncate text-xs text-muted-foreground tabular-nums">{formatDualDate(u.expire_at, panelTz)}</div>
            </div>
            <StatusBadge
              tone={tone}
              // Always shown as a badge here, not a dot: this row exists to say how soon.
              quiet={false}
              label={days <= 0 ? t('dashboard.alerts.expiring_today') : t('dashboard.alerts.expiring_in_days', { days })}
              className="shrink-0"
            />
          </li>
        )
      })}
    </AttentionCard>
  )
}
