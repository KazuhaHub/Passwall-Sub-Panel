import { isAxiosError } from 'axios'

import type { RiskUserSummary } from '@/api/riskCenter'
import { EmptyState } from '@/components/feedback/EmptyState'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { StatusBadge } from '@/components/common/StatusBadge'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useConnectionHistory } from '@/query/riskCenter'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { AddressCell, JudgementChip, regionText } from '../LiveConnectionList'
import { LiveSnapshotHeader } from '../LiveConnectionsTab'
import { useRiskT } from '../useRiskT'
import { mergeConnections, type MergedConnection } from './mergeConnections'

/** One page of history is plenty for one account in a drawer; beyond it the
 *  list says it is capped rather than paging inside a side panel. */
const HISTORY_CAP = 200

function errorText(error: unknown): string {
  return isAxiosError(error) ? String(error.response?.data?.error ?? error.message) : String(error)
}

function Entry({ c }: { c: MergedConnection }) {
  const { t } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  const scanned = c.online && c.seen_at > 0
  const detail = [
    c.count > 0 ? t('admin:risk_center.drawer.seen_detail', { first: formatMsDualTz(c.first_seen_ms, panelTz), count: c.count }) : '',
    // The upstream's own time for a live source, in the Live tab's words: the scan that still saw it
    // connected, on the server's clock, the same for every address still connected — not when this one
    // was last used.
    scanned ? t('admin:risk_center.live.still_connected', { time: formatMsDualTz(c.seen_at * 1000, panelTz) }) : '',
    scanned ? t('admin:risk_center.live.still_connected_hint') : '',
  ].filter(Boolean).join('\n')
  const lastSeen = t('admin:risk_center.drawer.last_seen', { time: formatMsDualTz(c.last_seen_ms, panelTz) })
  return (
    <li data-testid="conn-entry" className="flex flex-col gap-0.5 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="min-w-0"><AddressCell ip={c.ip} sourceKey={c.source_key} /></div>
        {c.online && <StatusBadge tone="success" label={t('admin:risk_center.drawer.online')} />}
        <JudgementChip exclusion={c.exclusion} />
      </div>
      <div className="flex flex-wrap gap-x-2 text-xs text-muted-foreground">
        <span>{regionText(c.region)}</span>
        <span>·</span>
        <span>{c.panel_name || `#${c.panel_id}`}</span>
        {/* The raw 3X-UI node id, explained as the Live tab's PanelCell explains it: PSP has no name for a node. */}
        {c.node && (
          <Tooltip>
            <TooltipTrigger render={<span className="font-mono break-all" />}>{c.node}</TooltipTrigger>
            <TooltipContent>{t('admin:risk_center.live.node_hint')}</TooltipContent>
          </Tooltip>
        )}
        <span>·</span>
        {/* The times behind "last seen" on the part of the line they are about. */}
        {detail
          ? (
            <Tooltip>
              <TooltipTrigger render={<span />}>{lastSeen}</TooltipTrigger>
              <TooltipContent className="whitespace-pre-line">{detail}</TooltipContent>
            </Tooltip>
          )
          : <span>{lastSeen}</span>}
      </div>
    </li>
  )
}

/**
 * 连接: the live snapshot and the connection history as ONE list, two lines
 * per source (the address and its judgement; where, through which panel,
 * last seen). No refresh here: the Live tab owns the fleet-wide, rationed
 * refresh, and the snapshot's own line says how old it is.
 */
export default function ConnectionsTab({ summary }: { summary: RiskUserSummary }) {
  const { t } = useRiskT()
  const scope = useQueryScope()
  const history = useConnectionHistory(scope, {
    user_id: summary.user.id, page: 1, page_size: HISTORY_CAP, sort_by: 'last_seen', sort_dir: 'desc',
  })
  const live = summary.live.items[0]?.connections ?? []
  const merged = mergeConnections(live, history.data?.items ?? [], summary.live.snapshot.taken_at)
  const total = history.data?.total ?? 0
  return (
    <div className="flex flex-col gap-2">
      <LiveSnapshotHeader view={summary.live} />
      {history.isPending && <CardSkeleton lines={3} />}
      {history.isError && (
        <div className="flex flex-wrap items-center gap-2">
          <p className="text-[13px] text-destructive">{t('admin:risk_center.load_failed', { error: errorText(history.error) })}</p>
          <Button variant="outline" size="sm" onClick={() => void history.refetch()}>{t('admin:risk_center.retry')}</Button>
        </div>
      )}
      {merged.length > 0 && <ul className="divide-y">{merged.map(c => <Entry key={c.key} c={c} />)}</ul>}
      {!history.isPending && !history.isError && merged.length === 0 && (
        <EmptyState compact title={t('admin:risk_center.drawer.connections_empty')} />
      )}
      {total > HISTORY_CAP && (
        <p className="text-xs text-muted-foreground">{t('admin:risk_center.drawer.history_capped', { shown: HISTORY_CAP, total })}</p>
      )}
    </div>
  )
}
