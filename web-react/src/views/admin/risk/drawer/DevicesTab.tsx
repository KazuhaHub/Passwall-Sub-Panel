import { Link } from 'react-router'

import type { RiskUserSummary, UserDevice } from '@/api/riskCenter'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { EmptyState } from '@/components/feedback/EmptyState'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { useRiskT } from '../useRiskT'

function Entry({ d }: { d: UserDevice }) {
  const { t } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  // The declared device by its label, else its digest's 4-character prefix;
  // a client that declared nothing is named by what it sent.
  const name = d.label || (d.device_id4 ? `#${d.device_id4}` : d.ua || '—')
  // Full UA and the span the fetches cover, first to last.
  const detail = [d.ua, `${formatMsDualTz(d.first_at_ms, panelTz)} – ${formatMsDualTz(d.last_at_ms, panelTz)}`]
    .filter(Boolean).join('\n')
  return (
    <li data-testid="device-entry" className="flex flex-col gap-0.5 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium break-all">{name}</span>
        {d.label && d.device_id4 && <span className="font-mono text-[11px] text-muted-foreground">{`#${d.device_id4}`}</span>}
        {d.client_type && <Badge variant="outline">{d.client_type}</Badge>}
        <span className="text-xs text-muted-foreground">{t('admin:risk_center.drawer.device_fetches', { count: d.fetches })}</span>
      </div>
      <Tooltip>
        <TooltipTrigger render={<div className="flex flex-wrap gap-x-2 text-xs text-muted-foreground" />}>
          <span className="font-mono break-all">
            {d.sources.join(', ') || '—'}
            {d.sources_more > 0 ? ` ${t('admin:risk_center.drawer.sources_more', { count: d.sources_more })}` : ''}
          </span>
          <span>·</span>
          <span>{t('admin:risk_center.drawer.last_seen', { time: formatMsDualTz(d.last_at_ms, panelTz) })}</span>
        </TooltipTrigger>
        <TooltipContent className="break-all whitespace-pre-line">{detail}</TooltipContent>
      </Tooltip>
    </li>
  )
}

/**
 * 设备: the clients behind the account's subscription fetches in the device window, one two-line entry
 * each (what it is and how often it fetched; from where and when last). An unreadable sub log says so
 * rather than passing for "no devices", and the full log is one exact link away.
 */
export default function DevicesTab({ summary }: { summary: RiskUserSummary }) {
  const { t } = useRiskT()
  const u = summary.user
  const logs = `/admin/logs?tab=sub&user_id=${u.id}&upn=${encodeURIComponent(u.upn)}`
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-muted-foreground">{t('admin:risk_center.drawer.devices_intro', { hours: summary.device_window_hours })}</p>
      {summary.devices_unavailable
        ? <p className="text-[13px] text-destructive">{t('admin:risk_center.drawer.devices_unavailable')}</p>
        : summary.devices.length === 0
          ? <EmptyState compact title={t('admin:risk_center.drawer.devices_empty')} />
          : <ul className="divide-y">{summary.devices.map((d, i) => <Entry key={`${d.device_id4}/${d.ua}/${i}`} d={d} />)}</ul>}
      <Button variant="link" size="sm" className="self-start" render={<Link to={logs} />}>
        {t('admin:risk_center.drawer.open_sub_logs')}
      </Button>
    </div>
  )
}
