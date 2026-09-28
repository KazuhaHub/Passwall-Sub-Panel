import { Box, Button, Chip, CircularProgress, Tooltip, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import type { RiskUserSummary } from '@/api/riskCenter'
import { useConnectionHistory } from '@/query/riskCenter'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { AddressCell, JudgementChip, regionText } from '../LiveConnectionList'
import { LiveSnapshotHeader } from '../LiveConnectionsTab'
import { mergeConnections, type MergedConnection } from './mergeConnections'

/** One page of history is plenty for one account in a drawer; beyond it the
 *  list says it is capped rather than paging inside a side panel. */
const HISTORY_CAP = 200

function errorText(error: unknown): string {
  return isAxiosError(error) ? String(error.response?.data?.error ?? error.message) : String(error)
}

function Entry({ c }: { c: MergedConnection }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(s => s.timezone)
  const scanned = c.online && c.seen_at > 0
  const detail = [
    c.count > 0 ? t('admin:risk_center.drawer.seen_detail', { first: formatMsDualTz(c.first_seen_ms, panelTz), count: c.count }) : '',
    // The upstream's own time for a live source, in the Live tab's words:
    // the panel's scan that still saw it connected, on the panel's clock,
    // the same for every address still connected — not when this one was
    // last used.
    scanned ? t('admin:risk_center.live.still_connected', { time: formatMsDualTz(c.seen_at * 1000, panelTz) }) : '',
    scanned ? t('admin:risk_center.live.still_connected_hint') : '',
  ].filter(Boolean).join('\n')
  const lastSeen = <span>{t('admin:risk_center.drawer.last_seen', { time: formatMsDualTz(c.last_seen_ms, panelTz) })}</span>
  return (
    <Box data-testid="conn-entry" sx={{ py: 1, borderTop: `1px solid ${md.outlineVariant}`, display: 'flex', flexDirection: 'column', gap: 0.25 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Box sx={{ minWidth: 0 }}><AddressCell ip={c.ip} sourceKey={c.source_key} /></Box>
        {c.online && <Chip size="small" color="success" label={t('admin:risk_center.drawer.online')} />}
        <JudgementChip exclusion={c.exclusion} />
      </Box>
      <Box sx={{ fontSize: 12, color: md.onSurfaceVariant, display: 'flex', flexWrap: 'wrap', columnGap: 0.75 }}>
        <span>{regionText(c.region)}</span>
        <span>·</span>
        <span>{c.panel_name || `#${c.panel_id}`}</span>
        {/* The raw 3X-UI node id, explained as the Live tab's PanelCell
            explains it: PSP has no name for a node. */}
        {c.node && (
          <Tooltip title={t('admin:risk_center.live.node_hint')}>
            <Box component="span" sx={{ fontFamily: 'monospace', wordBreak: 'break-all' }}>{c.node}</Box>
          </Tooltip>
        )}
        <span>·</span>
        {/* The times behind "last seen" on the part of the line they are
            about, so the node's own tooltip never opens inside this one. */}
        {detail ? <Tooltip title={<Box sx={{ whiteSpace: 'pre-line' }}>{detail}</Box>}>{lastSeen}</Tooltip> : lastSeen}
      </Box>
    </Box>
  )
}

/**
 * 连接: the live snapshot and the connection history as ONE list, two lines
 * per source (the address and its judgement; where, through which panel,
 * last seen). No refresh here: the Live tab owns the fleet-wide, rationed
 * refresh, and the snapshot's own line says how old it is.
 */
export default function ConnectionsTab({ summary }: { summary: RiskUserSummary }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const history = useConnectionHistory(scope, {
    user_id: summary.user.id, page: 1, page_size: HISTORY_CAP, sort_by: 'last_seen', sort_dir: 'desc',
  })
  const live = summary.live.items[0]?.connections ?? []
  const merged = mergeConnections(live, history.data?.items ?? [], summary.live.snapshot.taken_at)
  const total = history.data?.total ?? 0
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <LiveSnapshotHeader view={summary.live} />
      {history.isPending && <CircularProgress size={18} />}
      {history.isError && (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
          <Typography sx={{ fontSize: 13, color: md.error }}>
            {t('admin:risk_center.load_failed', { error: errorText(history.error) })}
          </Typography>
          <Button size="small" onClick={() => void history.refetch()}>{t('admin:risk_center.retry')}</Button>
        </Box>
      )}
      {merged.length > 0 && <Box>{merged.map(c => <Entry key={c.key} c={c} />)}</Box>}
      {!history.isPending && !history.isError && merged.length === 0 && (
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('admin:risk_center.drawer.connections_empty')}</Typography>
      )}
      {total > HISTORY_CAP && (
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('admin:risk_center.drawer.history_capped', { shown: HISTORY_CAP, total })}
        </Typography>
      )}
    </Box>
  )
}
