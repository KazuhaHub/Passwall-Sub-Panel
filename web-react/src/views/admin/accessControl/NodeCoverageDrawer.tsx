import { useRef, useState } from 'react'
import { Alert, Box, Button, Drawer, DialogContent, DialogTitle, IconButton, Skeleton, Stack, Typography, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import { useLocation, useSearchParams } from 'react-router'
import type { DestinationStatus } from '@/api/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useRetryDestinationPolicy } from '@/query/accessControl'
import { nodeFilter, type NodeFilter } from '@/utils/accessControl'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { pushSnack } from '@/components/SnackbarHost'
import { destinationError } from './errors'
import { AsyncButton } from '@/components/AsyncButton'
import NodePolicyStatusRow from './NodePolicyStatusRow'
const P = 'admin:access_control.coverage.'
const filters = ['applied', 'pending', 'problem', 'upgrade', 'excluded'] as const
export default function NodeCoverageDrawer({ status, loading, failed, refreshing, onRetryRead, onClose, onLists, onSettings, applySeconds }: {
  status?: DestinationStatus; loading: boolean; failed: boolean; refreshing: boolean
  onRetryRead: () => Promise<unknown>; onClose: () => void
  onLists: () => void; onSettings: () => void; applySeconds?: number
}) {
  const { t, number } = useAccessTranslation(['admin', 'common'])
  const theme = useTheme()
  const [params, setParams] = useSearchParams()
  const location = useLocation()
  const raw = params.get('node_state')
  const filter: 'all' | NodeFilter = filters.find(filter => filter === raw) ?? 'all'
  const retry = useRetryDestinationPolicy(useQueryScope())
  const [error, setError] = useState('')
  const admission = useRef(false)
  const retryNode = async (agentId: string) => {
    if (admission.current) return
    admission.current = true; setError('')
    try { await retry.mutateAsync(agentId); pushSnack(t(`${P}retry_requested`), 'success') }
    catch (error) { setError(destinationError(error).error) }
    finally { admission.current = false }
  }
  const now = Date.now()
  const retryRead = <AsyncButton pending={refreshing} onClick={onRetryRead} sx={{ minHeight: 44 }}>{t('common:actions.retry')}</AsyncButton>
  return <Drawer open anchor="right" onClose={onClose} slotProps={{ paper: { role: 'dialog', 'aria-modal': true, 'aria-labelledby': 'coverage-title', sx: { width: { xs: '100vw', sm: 560 }, maxWidth: '100vw', bgcolor: theme.palette.md.surfaceContainerLow, borderTopLeftRadius: 16 } } }}>
    <Box sx={{ display: 'flex', alignItems: 'center' }}><DialogTitle id="coverage-title" sx={{ flex: 1, minWidth: 0 }}>{t(`${P}title`, { count: status?.nodes.length, n: status?.nodes.length ?? '—' })}</DialogTitle><IconButton aria-label={t('common:actions.close')} onClick={onClose} sx={{ mr: 2, width: 44, height: 44 }}><CloseIcon /></IconButton></Box>
    <DialogContent><Stack spacing={2}>
      <Typography variant="body2" color="text.secondary">{t(`${P}hint`)}</Typography>
      <KpiGrid>{filters.map(value => <KpiTile key={value} label={t(`${P}filter_${value}`)} value={status ? number(status.nodes.filter(node => nodeFilter(node, now) === value).length) : '—'} pressed={filter === value} onToggle={() => setParams(prev => { const next = new URLSearchParams(prev); if (filter === value) next.delete('node_state'); else next.set('node_state', value); return next }, { replace: true, state: location.state })} />)}</KpiGrid>
      {filter !== 'all' && <Button sx={{ minHeight: 44 }} onClick={() => setParams(prev => { const next = new URLSearchParams(prev); next.delete('node_state'); return next }, { replace: true, state: location.state })}>{t(`${P}clear_filter`)}</Button>}
      {error && <Alert severity="error">{error}</Alert>}
      {!status && (loading ? <Stack spacing={1} role="progressbar" aria-label={t(`${P}loading`)} aria-busy="true">{[0, 1, 2, 3, 4, 5].map(id => <Skeleton key={id} variant="rounded" height={72} />)}</Stack>
        : <Alert severity="error" action={retryRead}>{t(`${P}unavailable`)}</Alert>)}
      {status && failed && <Alert severity="warning" action={retryRead}>{t(`${P}read_stale`)}</Alert>}
      {status?.nodes.filter(node => filter === 'all' || nodeFilter(node, now) === filter).map(node => <NodePolicyStatusRow key={node.panel_id} node={node} now={now} onLists={onLists} onRetry={retryNode} retryPending={retry.isPending} />)}
      {status && !status.nodes.some(node => filter === 'all' || nodeFilter(node, now) === filter) && <Typography color="text.secondary">{t(`${P}empty`)}</Typography>}
      {applySeconds !== undefined && <Box><Typography variant="body2" color="text.secondary">{t(`${P}apply_interval`, { seconds: applySeconds })}</Typography><Button onClick={onSettings} sx={{ minHeight: 44 }}>{t(`${P}modify_interval`)}</Button></Box>}
    </Stack></DialogContent>
  </Drawer>
}
