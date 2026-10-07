import { useRef, useState } from 'react'
import { Alert, Box, Button, Drawer, DialogContent, DialogTitle, IconButton, Stack, Typography, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import PauseCircleOutlineIcon from '@mui/icons-material/PauseCircleOutlined'
import RemoveCircleOutlineIcon from '@mui/icons-material/RemoveCircleOutlineOutlined'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import { useLocation, useSearchParams } from 'react-router'
import type { DestinationStatus } from '@/api/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useRetryDestinationPolicy } from '@/query/accessControl'
import { fallbackState, nodeAccessTone, nodeFilter, type NodeFilter } from '@/utils/accessControl'
import { ToneBadge, stateTone } from '@/components/ToneBadge'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { pushSnack } from '@/components/SnackbarHost'
import { destinationError } from './errors'
import { AsyncButton } from '@/components/AsyncButton'
const P = 'admin:access_control.coverage.'
const filters = ['applied', 'pending', 'problem', 'upgrade', 'excluded'] as const
export default function NodeCoverageDrawer({ status, onClose }: { status?: DestinationStatus; onClose: () => void }) {
  const { t, dateTime, number } = useAccessTranslation(['admin', 'common'])
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
  return <Drawer open anchor="right" onClose={onClose} slotProps={{ paper: { role: 'dialog', 'aria-modal': true, 'aria-labelledby': 'coverage-title', sx: { width: { xs: '100vw', sm: 560 }, maxWidth: '100vw', bgcolor: theme.palette.md.surfaceContainerLow, borderTopLeftRadius: 16 } } }}>
    <Box sx={{ display: 'flex', alignItems: 'center' }}><DialogTitle id="coverage-title" sx={{ flex: 1, minWidth: 0 }}>{t(`${P}title`)}</DialogTitle><IconButton aria-label={t('common:actions.close')} onClick={onClose} sx={{ mr: 2, width: 44, height: 44 }}><CloseIcon /></IconButton></Box>
    <DialogContent><Stack spacing={2}>
      <Typography variant="body2" color="text.secondary">{t(`${P}hint`)}</Typography>
      <KpiGrid>{filters.map(value => <KpiTile key={value} label={t(`${P}filter_${value}`)} value={status ? number(status.nodes.filter(node => nodeFilter(node, now) === value).length) : '—'} pressed={filter === value} onToggle={() => setParams(prev => { const next = new URLSearchParams(prev); if (filter === value) next.delete('node_state'); else next.set('node_state', value); return next }, { replace: true, state: location.state })} />)}</KpiGrid>
      {filter !== 'all' && <Button onClick={() => setParams(prev => { const next = new URLSearchParams(prev); next.delete('node_state'); return next }, { replace: true, state: location.state })}>{t(`${P}clear_filter`)}</Button>}
      {error && <Alert severity="error">{error}</Alert>}
      {!status && <Alert severity="warning">{t(`${P}unavailable`)}</Alert>}
      {status?.nodes.filter(node => filter === 'all' || nodeFilter(node, now) === filter).map(node => {
        const colors = stateTone(theme, nodeAccessTone(node, now))
        const tone = node.state === 'paused' ? { ...colors, Icon: PauseCircleOutlineIcon } : node.state === 'unsupported_kind' ? { ...colors, Icon: RemoveCircleOutlineIcon } : colors
        const fallback = fallbackState(node)
        return <Box key={node.panel_id} sx={{ border: `1px solid ${theme.palette.md.outlineVariant}`, p: 2, borderRadius: 2 }}><Stack direction="row" sx={{ flexWrap: 'wrap', alignItems: 'center', gap: 1 }}><Typography sx={{ flex: 1, fontWeight: 600 }}>{node.panel_name}</Typography><ToneBadge tone={tone} label={t(`${P}state_${node.state}`)} /></Stack>
          <Typography variant="caption" color="text.secondary">{node.kind} · {node.engine ?? '—'} · {node.agent_version ?? '—'}</Typography>
          {node.applied_at !== null && <Typography variant="body2">{t(`${P}applied_at`, { time: dateTime(node.applied_at) })}</Typography>}
          {node.pending_since !== null && <Typography variant="body2">{t(`${P}pending_since`, { time: dateTime(node.pending_since) })}</Typography>}
          {node.over_limit && <Typography color="error">{t(`${P}over_limit`, { kind: node.over_limit.kind, used: node.over_limit.used, limit: node.over_limit.limit })}</Typography>}
          {node.sniffing_insufficient.map(listener => <Typography key={listener.listener} color="error">{t(`${P}sniffing`, { listener: listener.label })}</Typography>)}
          {fallback !== 'none' && <Alert sx={{ mt: 1 }} severity={fallback === 'exhausted' || fallback === 'stopping' ? 'error' : 'warning'}>{t(`${P}fallback_${fallback}`, { reason: node.fallback_reason })}</Alert>}
          {node.agent_id && node.kind === 'psp' && node.supports.policy && ['pending', 'rejected', 'over_limit', 'sniffing'].includes(node.state) && <AsyncButton pending={retry.isPending} onClick={() => retryNode(node.agent_id!)}>{t(`${P}retry`)}</AsyncButton>}
        </Box>
      })}
      {status && !status.nodes.some(node => filter === 'all' || nodeFilter(node, now) === filter) && <Typography color="text.secondary">{t(`${P}empty`)}</Typography>}
    </Stack></DialogContent>
  </Drawer>
}
