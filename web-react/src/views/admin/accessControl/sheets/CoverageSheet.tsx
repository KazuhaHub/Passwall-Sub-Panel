import { useMemo, useState } from 'react'
import { Alert, Box, Button, IconButton, Link, Menu, MenuItem, Skeleton, Typography, useTheme } from '@mui/material'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { DestNodeStatus } from '@/api/accessControl'
import AuditCollectDialog from '@/components/AuditCollectDialog'
import DestResolveDialog from '@/components/DestResolveDialog'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import NodePolicyStatusRow from '@/components/NodePolicyStatusRow'
import { useDestSettings, useDestStatus, useRetryDestAgent } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { collectingNodeCount, isNativeKind, NODE_FILTERS, NODE_STATE_FILTER, nodeStateKey } from '@/utils/accessControl'
import { reportDestError, reportRetry } from '../destErrors'
import type { NodeFilter } from '../accessParams'
import SheetDrawer from './SheetDrawer'

export interface CoverageSheetProps {
  open: boolean
  onClose: () => void
  /** The `node_state` filter, from the URL. */
  filter: NodeFilter | null
  onFilterChange: (next: NodeFilter | null) => void
  onOpenSettings: (focus?: 'apply' | 'refresh' | 'rules') => void
}

const C = 'admin:access_control.coverage.'

/**
 * S14: the whole fleet, one row per node, with where it stands, what to do next
 * and what it records. The rows are NodePolicyStatusRow, the one node-state
 * component; the recording mode is set in AuditCollectDialog and how IP rules
 * treat host names in DestResolveDialog (both shared with the servers page)
 * from a row's menu.
 */
export default function CoverageSheet({ open, onClose, filter, onFilterChange, onOpenSettings }: CoverageSheetProps) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const navigate = useNavigate()
  const scope = useQueryScope()
  const status = useDestStatus(scope, { enabled: open })
  const settings = useDestSettings(scope, { enabled: open })
  const retry = useRetryDestAgent(scope)
  const [menu, setMenu] = useState<{ el: HTMLElement; node: DestNodeStatus } | null>(null)
  const [recordingFor, setRecordingFor] = useState<DestNodeStatus | null>(null)
  const [resolveFor, setResolveFor] = useState<DestNodeStatus | null>(null)
  const readAt = status.dataUpdatedAt
  const now = useMemo(() => Date.now(), [readAt]) // eslint-disable-line react-hooks/exhaustive-deps

  const nodes = useMemo(() => status.data?.nodes ?? [], [status.data])
  const counts = useMemo(() => {
    const c: Record<NodeFilter, number> = { applied: 0, pending: 0, problem: 0, upgrade: 0, inactive: 0 }
    for (const n of nodes) c[NODE_STATE_FILTER[nodeStateKey(n, now)]]++
    return c
  }, [nodes, now])
  const shown = filter ? nodes.filter(n => NODE_STATE_FILTER[nodeStateKey(n, now)] === filter) : nodes

  const doRetry = async (agentId: string) => {
    try {
      reportRetry(t, await retry.mutateAsync(agentId))
    } catch (err) {
      reportDestError(t, err)
    }
  }
  const toServer = (n: DestNodeStatus) => void navigate(`/admin/servers?q=${encodeURIComponent(n.panel_name)}`)
  const release = status.data?.geosite_release
  const recordingCount = collectingNodeCount(status.data)

  return (
    <SheetDrawer open={open} onClose={onClose} headingId="coverage-heading"
      title={t(`${C}title`, { n: status.data ? nodes.length : '…' })}>
      {status.isPending && [0, 1, 2, 3, 4, 5].map(i => <Skeleton key={i} variant="rounded" height={64} sx={{ mb: 1 }} />)}
      {status.isError && (
        <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void status.refetch()}>{t('admin:access_control.retry')}</Button>}>
          {t('admin:access_control.verdict.read_failed')}
        </Alert>
      )}
      {status.data && (
        <>
          <Box data-testid="recording-note" sx={{ display: 'flex', gap: 1, alignItems: 'flex-start', p: 1.25, mb: 1, borderRadius: 2, bgcolor: md.surfaceContainerHigh }}>
            <InfoOutlinedIcon aria-hidden sx={{ fontSize: 18, mt: '2px', color: md.onSurfaceVariant }} />
            <Typography variant="body2">
              {recordingCount > 0
                ? t(`${C}recording_note`, { count: recordingCount, days: settings.data?.effective.dest_hit_retention_days ?? '…' })
                : t(`${C}recording_none`)}
            </Typography>
          </Box>
          <KpiGrid>
            {NODE_FILTERS.map(f => (
              <KpiTile key={f} label={t(`${C}filter.${f}`)} value={counts[f]} pressed={filter === f}
                onToggle={() => onFilterChange(filter === f ? null : f)} />
            ))}
          </KpiGrid>
          {shown.length === 0 ? (
            <Box sx={{ p: 2, borderRadius: 2, bgcolor: md.surfaceContainerHigh, display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
              <Typography variant="body2" sx={{ flex: 1 }}>{t(`${C}empty`)}</Typography>
              {filter && <Button size="small" onClick={() => onFilterChange(null)}>{t(`${C}clear_filter`)}</Button>}
            </Box>
          ) : (
            <Box sx={{ borderRadius: 3, border: `1px solid ${md.outlineVariant}`, bgcolor: md.surfaceContainerLow, px: 1.5 }}>
              {shown.map((n, i) => {
                const oldData = !!n.rules_data_version && !!release && n.rules_data_version !== release
                return (
                  <Box key={n.agent_id || n.panel_id} sx={{ borderTop: i ? `1px solid ${md.outlineVariant}` : 'none', display: 'flex', gap: 0.5 }}>
                    <Box sx={{ flex: 1, minWidth: 0 }}>
                      <NodePolicyStatusRow node={n} variant="row" now={now}
                        onRetry={doRetry}
                        onOpenInbound={id => void navigate(`/admin/nodes?inbound=${id}`)}
                        onUpgrade={() => toServer(n)}
                        onOpenLists={() => onClose()}
                        extra={(oldData || n.geoip_unavailable) ? (
                          <Box sx={{ mt: 0.25 }}>
                            {oldData && <Typography variant="body2" sx={{ color: md.onSurface }}>{t(`${C}old_data`, { version: n.rules_data_version })}</Typography>}
                            {n.geoip_unavailable && <Typography variant="body2" sx={{ color: md.onSurface }}>{t(`${C}geoip_unavailable`)}</Typography>}
                          </Box>
                        ) : undefined} />
                    </Box>
                    <IconButton size="small" aria-label={t(`${C}row_menu`, { name: n.panel_name })} sx={{ alignSelf: 'flex-start', mt: 0.75, minWidth: 44, minHeight: 44 }}
                      onClick={e => setMenu({ el: e.currentTarget, node: n })}>
                      <MoreVertIcon />
                    </IconButton>
                  </Box>
                )
              })}
            </Box>
          )}
          <Typography variant="body2" sx={{ mt: 2, color: md.onSurfaceVariant }}>
            {t(`${C}apply_line`, { seconds: settings.data?.effective.dest_policy_apply_min_seconds ?? '…' })}{' '}
            <Link component="button" type="button" underline="always" onClick={() => onOpenSettings('apply')}
              sx={{ font: 'inherit', verticalAlign: 'baseline' }}>
              {t(`${C}edit`)}
            </Link>
          </Typography>
        </>
      )}
      <Menu anchorEl={menu?.el} open={menu !== null} onClose={() => setMenu(null)}>
        {menu && isNativeKind(menu.node.kind) && menu.node.state !== 'unsupported_kind' && (
          <MenuItem onClick={() => { const n = menu.node; setMenu(null); setRecordingFor(n) }}>{t(`${C}set_recording`)}</MenuItem>
        )}
        {menu && isNativeKind(menu.node.kind) && menu.node.state !== 'unsupported_kind' && (
          <MenuItem onClick={() => { const n = menu.node; setMenu(null); setResolveFor(n) }}>{t(`${C}set_resolve`)}</MenuItem>
        )}
        <MenuItem disabled={menu?.node.kind !== 'psp'} onClick={() => { const n = menu!.node; setMenu(null); toServer(n) }}>{t('admin:access_control.node_action.open_server')}</MenuItem>
        <MenuItem onClick={() => { const n = menu!.node; setMenu(null); void navigate(`/admin/node-issues?agent=${encodeURIComponent(n.agent_id)}`) }}>
          {t('admin:access_control.node_action.node_issues')}
        </MenuItem>
      </Menu>
      <AuditCollectDialog node={recordingFor} onClose={() => setRecordingFor(null)} />
      <DestResolveDialog node={resolveFor} onClose={() => setResolveFor(null)} />
    </SheetDrawer>
  )
}
