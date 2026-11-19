import { useMemo } from 'react'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import type { Alert, AlertSeverity, AlertType } from '@/api/alerts'
import { IconBell, IconError, IconSpinner, IconSuccess, IconShield, IconTime, IconUpgrade, IconWarning } from '@/lib/icons'
import { useAlerts } from '@/query/alerts'
import { riskCenterKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { spacedT } from '@/i18n/spacing'
import { IconButton } from '@/components/common/IconButton'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'

// In-app deep links per alert type. psp_upgrade is intentionally absent: it opens
// the GitHub releases page externally (handled in go()).
const ROUTE: Partial<Record<AlertType, string>> = {
  node_health: '/admin/nodes',
  cert_failed: '/admin/certs',
  cert_expiring: '/admin/certs',
  panel_upgrade: '/admin/servers',
  login_security: '/admin/logs',
  // A count, not accounts: the queue lists them, with the evidence beside each.
  // Filtered to the very accounts the count is of (needing action now), so "2"
  // never lands on a list of seven; the tab is named, not left to the page's
  // default, so the link does not depend on which tab it opens on.
  risk_queue: '/admin/risk?tab=queue&urgent=1',
}

const PSP_RELEASES_URL = 'https://github.com/KazuhaHub/passwall-sub-panel/releases'

const SEVERITY_RANK: Record<AlertSeverity, number> = { error: 0, warning: 1, info: 2 }

function TypeIcon({ type, severity, className }: { type: AlertType; severity: AlertSeverity; className?: string }) {
  switch (type) {
    case 'cert_failed':
      return <IconError className={className} />
    case 'cert_expiring':
      return <IconTime className={className} />
    case 'panel_upgrade':
    case 'psp_upgrade':
      return <IconUpgrade className={className} />
    case 'login_security':
    case 'risk_queue':
      return <IconShield className={className} />
    case 'node_health':
    default:
      return severity === 'error' ? <IconError className={className} /> : <IconWarning className={className} />
  }
}

const SEVERITY_INK: Record<AlertSeverity, string> = {
  error: 'text-destructive',
  warning: 'text-warning-ink',
  info: 'text-info',
}

// The count badge: white (or the dark page colour) on the tone, never tone-coloured
// text on the page. Warning is the one fill light enough to want dark text on both themes.
const SEVERITY_BADGE: Record<AlertSeverity, string> = {
  error: 'bg-destructive text-white dark:text-background',
  warning: 'bg-warning text-black',
  info: 'bg-info text-white dark:text-background',
}

/**
 * The top-bar bell: how many things need attention, and where each one is. Same data
 * (useAlerts), same copy and the same per-alert routes as the bell it replaces; only
 * what draws it changed. "No notifications" is a claim about the server, so a failed
 * request is shown as unavailable, never as an empty feed.
 */
export function NotificationBell() {
  const { t: rawT } = useTranslation(['admin', 'common'])
  // Keys with placeholders go through the spacing helper (copy rule 8).
  const t = useMemo(() => spacedT(rawT), [rawT])
  const navigate = useNavigate()
  const qc = useQueryClient()
  const scope = useQueryScope()
  const { data, isFetching, isError, refetch } = useAlerts(scope)

  // Sort a copy for display. Sorting the cached array in place would mutate what every
  // other observer of this query key reads.
  const alerts = useMemo(
    () => [...(data?.alerts ?? [])].sort((a, b) => SEVERITY_RANK[a.severity] - SEVERITY_RANK[b.severity]),
    [data],
  )
  const total = alerts.length
  const highest: AlertSeverity = alerts.some(a => a.severity === 'error')
    ? 'error'
    : alerts.some(a => a.severity === 'warning') ? 'warning' : 'info'

  const title = t('admin:alerts.bell_title', { defaultValue: '通知' })

  function alertTitle(a: Alert): string {
    const name = a.target_name || ''
    switch (a.type) {
      case 'node_health':
        return t('admin:alerts.title.node_health', { name, defaultValue: `节点 ${name} 异常` })
      case 'cert_failed':
        return t('admin:alerts.title.cert_failed', { name, defaultValue: `证书 ${name} 签发失败` })
      case 'cert_expiring':
        return a.severity === 'error'
          ? t('admin:alerts.title.cert_expired', { name, defaultValue: `证书 ${name} 已过期` })
          : t('admin:alerts.title.cert_expiring', { name, defaultValue: `证书 ${name} 即将到期` })
      case 'panel_upgrade':
        return t('admin:alerts.title.panel_upgrade', { name, version: a.latest_version, defaultValue: `${name}：3X-UI ${a.latest_version} 可升级` })
      case 'psp_upgrade':
        return t('admin:alerts.title.psp_upgrade', { version: a.latest_version, defaultValue: `面板新版本 ${a.latest_version} 可更新` })
      case 'login_security':
        return t('admin:alerts.title.login_security', { count: a.count, defaultValue: `近期发生 ${a.count} 次登录锁定` })
      case 'risk_queue':
        return t('admin:alerts.title.risk_queue', { count: a.count })
      default:
        return name
    }
  }

  function alertSecondary(a: Alert): string {
    switch (a.type) {
      case 'node_health':
        return [a.panel_name, t(`admin:nodes.health.${a.health_state}`, { defaultValue: a.health_state || '' })].filter(Boolean).join(' · ')
      case 'cert_failed':
        return a.last_error || ''
      case 'cert_expiring':
        return a.expire_at ? new Date(a.expire_at).toLocaleString() : ''
      case 'panel_upgrade':
      case 'psp_upgrade':
        return `${a.current_version || '?'} → ${a.latest_version || '?'}`
      default:
        return ''
    }
  }

  function go(a: Alert) {
    // PSP self-update has no in-app page: open the GitHub releases externally.
    if (a.type === 'psp_upgrade') {
      window.open(PSP_RELEASES_URL, '_blank', 'noopener,noreferrer')
      return
    }
    // The bell's count may be fresher than a queue page cached from a visit a minute
    // ago; landing on that page would show a list that disagrees with the number just
    // clicked. Marked stale first, so the queue refetches as it mounts.
    if (a.type === 'risk_queue') void qc.invalidateQueries({ queryKey: riskCenterKeys.queues(scope) })
    navigate(ROUTE[a.type] ?? '/admin/dashboard')
  }

  return (
    <DropdownMenu onOpenChange={open => { if (open) void refetch() /* refresh on open */ }}>
      <DropdownMenuTrigger
        render={<IconButton label={total > 0 ? `${title}, ${t('common:ui.shell.notifications_count', { count: total })}` : title} tooltip={title} className="relative" />}
      >
        <IconBell />
        {total > 0 && (
          <span
            aria-hidden
            className={cn(
              'absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] leading-none font-semibold tabular-nums',
              SEVERITY_BADGE[highest],
            )}
          >
            {total > 99 ? '99+' : total}
          </span>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-[30rem] w-[22.5rem] max-w-[90vw]">
        <div className="flex items-center justify-between px-2 py-1.5">
          <span className="text-sm font-semibold">{title}</span>
          {isFetching && <IconSpinner aria-hidden className="size-3.5 animate-spin text-muted-foreground" />}
        </div>
        <DropdownMenuSeparator />
        {total === 0 ? (
          <div className="flex items-center gap-2 px-2 py-6 text-sm text-muted-foreground">
            {/* "No notifications" is a claim about the server; a failed request is not
                evidence for it. Never present an outage as an empty feed. */}
            {isError ? (
              <>
                <IconError aria-hidden className="size-4 text-destructive" />
                {t('admin:alerts.unavailable', { defaultValue: '通知暂时不可用' })}
              </>
            ) : (
              <>
                <IconSuccess aria-hidden className="size-4 text-success" />
                {t('admin:alerts.empty', { defaultValue: '暂无通知' })}
              </>
            )}
          </div>
        ) : (
          alerts.map(a => (
            <DropdownMenuItem key={a.key} onClick={() => go(a)} className="items-start gap-2.5 py-2 whitespace-normal">
              <TypeIcon type={a.type} severity={a.severity} className={cn('mt-0.5', SEVERITY_INK[a.severity])} />
              <span className="min-w-0">
                <span className="block text-sm font-medium">{alertTitle(a)}</span>
                {alertSecondary(a) && <span className="block text-xs break-words text-muted-foreground">{alertSecondary(a)}</span>}
              </span>
            </DropdownMenuItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
