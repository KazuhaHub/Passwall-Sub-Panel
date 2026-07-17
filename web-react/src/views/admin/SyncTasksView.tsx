import { useEffect, useMemo, useState } from 'react'
import {
  Box,
  Button,
  Card,
  Checkbox,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  MenuItem,
  Select,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import RefreshIcon from '@mui/icons-material/Refresh'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import VisibilityIcon from '@mui/icons-material/Visibility'
import CloseIcon from '@mui/icons-material/Close'
import ReplayIcon from '@mui/icons-material/Replay'
import CleaningIcon from '@mui/icons-material/CleaningServices'
import { useTranslation } from 'react-i18next'
import { useCan } from '@/utils/permissions'
import { formatDualTz } from '@/utils/datetime'
import { allSettledLimited } from '@/utils/promises'
import { useSiteStore } from '@/stores/site'

import {
  cancelSyncTask,
  listSyncTasks,
  purgeFinishedSyncTasks,
  retrySyncTask,
  type SyncTaskListParams,
} from '@/api/syncTasks'
import type { SyncTask, SyncTaskStatus, SyncTaskType } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import PageHeader from '@/components/PageHeader'

function initialPageSize(): number {
  try {
    const raw = localStorage.getItem('psp_page_size')
    const n = raw ? parseInt(raw, 10) : 25
    return Number.isFinite(n) && n > 0 ? n : 25
  } catch { return 25 }
}

const STATUSES: SyncTaskStatus[] = ['pending', 'running', 'succeeded', 'canceled']
const TYPES: SyncTaskType[] = [
  'user_delete', 'user_resync', 'user_push_config',
  'node_create', 'node_delete', 'node_set_enabled', 'node_update',
]

function idOf(r: SyncTask) { return r.id ?? r.ID ?? 0 }
function typeOf(r: SyncTask): SyncTaskType | '' { return (r.type ?? r.Type ?? '') as SyncTaskType | '' }
function statusOf(r: SyncTask): SyncTaskStatus | '' { return (r.status ?? r.Status ?? '') as SyncTaskStatus | '' }
function summaryOf(r: SyncTask) { return r.summary ?? r.Summary ?? '' }
function targetOf(r: SyncTask) {
  const ty = r.target_type ?? r.TargetType ?? ''
  const id = r.target_id ?? r.TargetID ?? ''
  return `${ty}#${id}`
}
function attemptsOf(r: SyncTask) { return r.attempts ?? r.Attempts ?? 0 }
function nextRunOf(r: SyncTask) { return r.next_run_at ?? r.NextRunAt }
function lastErrorOf(r: SyncTask) { return r.last_error ?? r.LastError ?? '' }

export default function SyncTasksView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  const canConfig = useCan('config.write')
  // Admin view: times in panel tz (+ browser tz disclosed when they differ).
  const panelTz = useSiteStore(s => s.timezone)

  const [items, setItems] = useState<SyncTask[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState<number>(initialPageSize)
  function setPageSizePersist(n: number) {
    setPageSize(n)
    try { localStorage.setItem('psp_page_size', String(n)) } catch { /* ignore */ }
    setPage(1)
  }
  const [loading, setLoading] = useState(false)
  const [statusFilter, setStatusFilter] = useState<SyncTaskStatus | ''>('')
  const [typeFilter, setTypeFilter] = useState<SyncTaskType | ''>('')
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState<'retry' | 'cancel' | ''>('')

  const [detailOpen, setDetailOpen] = useState(false)
  const [detail, setDetail] = useState<SyncTask | null>(null)

  const pendingCount = useMemo(() => items.filter(r => statusOf(r) === 'pending').length, [items])

  useEffect(() => { void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, pageSize, statusFilter, typeFilter])

  async function load() {
    setLoading(true)
    try {
      const params: SyncTaskListParams = { page, page_size: pageSize }
      if (statusFilter) params.status = statusFilter
      if (typeFilter) params.type = typeFilter
      const res = await listSyncTasks(params)
      setItems(res.items); setTotal(res.total); setSelected(new Set())
    } finally { setLoading(false) }
  }

  function toggleAll(checked: boolean) {
    setSelected(checked ? new Set(items.map(idOf)) : new Set())
  }
  function toggleOne(id: number, checked: boolean) {
    setSelected(prev => {
      const next = new Set(prev)
      if (checked) next.add(id); else next.delete(id)
      return next
    })
  }

  async function retry(r: SyncTask) { await retrySyncTask(idOf(r)); await load() }
  async function cancel(r: SyncTask) { await cancelSyncTask(idOf(r)); await load() }

  async function batchRetry() {
    const rows = items.filter(r => selected.has(idOf(r)))
    if (!rows.length) return
    setBatchBusy('retry')
    try {
      const results = await allSettledLimited(rows, r => retrySyncTask(idOf(r)))
      const failed = results.filter(r => r.status === 'rejected').length
      if (failed > 0) pushSnack(t('admin:sync_tasks.toast.batch_partial', { ok: rows.length - failed, fail: failed }), 'warning')
      else pushSnack(t('admin:sync_tasks.toast.batch_retried', { count: rows.length }), 'success')
      await load()
    } finally { setBatchBusy('') }
  }

  async function batchCancel() {
    const rows = items.filter(r => selected.has(idOf(r)))
    if (!rows.length) return
    const ok = await confirm({
      title: t('admin:sync_tasks.confirm.batch_cancel_title'),
      message: t('admin:sync_tasks.confirm.batch_cancel_message', { count: rows.length }),
      destructive: true,
    })
    if (!ok) return
    setBatchBusy('cancel')
    try {
      const results = await allSettledLimited(rows, r => cancelSyncTask(idOf(r)))
      const failed = results.filter(r => r.status === 'rejected').length
      if (failed > 0) pushSnack(t('admin:sync_tasks.toast.batch_partial', { ok: rows.length - failed, fail: failed }), 'warning')
      else pushSnack(t('admin:sync_tasks.toast.batch_canceled', { count: rows.length }), 'success')
      await load()
    } finally { setBatchBusy('') }
  }

  async function purge() {
    const ok = await confirm({
      title: t('admin:sync_tasks.confirm.purge_title'),
      message: t('admin:sync_tasks.confirm.purge_message'),
      destructive: true,
      confirmText: t('admin:sync_tasks.purge'),
    })
    if (!ok) return
    const res = await purgeFinishedSyncTasks()
    pushSnack(t('admin:sync_tasks.toast.purged', { count: res.deleted }), 'success')
    await load()
  }

  function statusBadge(s: SyncTaskStatus | '') {
    let bg = md.surfaceContainerHighest, fg = md.onSurfaceVariant
    if (s === 'succeeded') { bg = md.tertiaryContainer; fg = md.onTertiaryContainer }
    else if (s === 'running') { bg = md.secondaryContainer; fg = md.onSecondaryContainer }
    else if (s === 'pending') { bg = md.primaryContainer; fg = md.onPrimaryContainer }
    else if (s === 'canceled') { bg = md.surfaceContainerHighest; fg = md.onSurfaceVariant }
    return <Box sx={{
      display: 'inline-block', px: 1.25, py: 0.25,
      borderRadius: 1, fontSize: 12, fontWeight: 500,
      bgcolor: bg, color: fg, whiteSpace: 'nowrap',
    }}>{s ? t(`admin:sync_tasks.status.${s}`) : '-'}</Box>
  }

  const allChecked = items.length > 0 && items.every(r => selected.has(idOf(r)))
  const someChecked = selected.size > 0 && !allChecked

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader
        title={t('admin:sync_tasks.title')}
        subtitle={t('admin:sync_tasks.pending_count', { count: pendingCount })}
        actions={
          <>
            {canConfig && <Button variant="outlined" color="error" startIcon={<CleaningIcon />} onClick={purge}>{t('admin:sync_tasks.purge')}</Button>}
            <Button variant="contained" startIcon={<RefreshIcon />} onClick={() => load()}>
              {t('admin:sync_tasks.refresh')}
            </Button>
          </>
        }
      />
      <Box sx={{ display: 'flex', gap: 1.5, mb: 2, flexWrap: 'wrap', alignItems: 'center' }}>
        <Select size="small" value={statusFilter} displayEmpty
          onChange={e => { setStatusFilter(e.target.value as SyncTaskStatus | ''); setPage(1) }}
          sx={{ minWidth: 150, height: 40 }}>
          <MenuItem value="">{t('admin:sync_tasks.filter_status')}</MenuItem>
          {STATUSES.map(s => <MenuItem key={s} value={s}>{t(`admin:sync_tasks.status.${s}`)}</MenuItem>)}
        </Select>
        <Select size="small" value={typeFilter} displayEmpty
          onChange={e => { setTypeFilter(e.target.value as SyncTaskType | ''); setPage(1) }}
          sx={{ minWidth: 180, height: 40 }}>
          <MenuItem value="">{t('admin:sync_tasks.filter_type')}</MenuItem>
          {TYPES.map(ty => <MenuItem key={ty} value={ty}>{t(`admin:sync_tasks.type.${ty}`)}</MenuItem>)}
        </Select>
        {selected.size > 0 && (
          <Box sx={{
            display: 'flex', alignItems: 'center', gap: 1, ml: 1, px: 2, py: 1,
            borderRadius: 9999, bgcolor: md.secondaryContainer, color: md.onSecondaryContainer,
          }}>
            <Typography sx={{ fontSize: 13, fontWeight: 500, mr: 1 }}>
              {t('admin:sync_tasks.selection_count', { count: selected.size })}
            </Typography>
            <Button size="small" variant="text" sx={{ color: 'inherit' }}
              startIcon={batchBusy === 'retry' ? <CircularProgress size={14} /> : <ReplayIcon />}
              disabled={batchBusy !== ''} onClick={batchRetry}>
              {t('admin:sync_tasks.batch_retry')}
            </Button>
            <Button size="small" variant="text" color="error"
              startIcon={batchBusy === 'cancel' ? <CircularProgress size={14} /> : <CloseIcon />}
              disabled={batchBusy !== ''} onClick={batchCancel}>
              {t('admin:sync_tasks.batch_cancel')}
            </Button>
          </Box>
        )}
      </Box>
      <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
        <TableContainer>
          <Table>
            <TableHead>
              <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                <TableCell padding="checkbox">
                  <Checkbox indeterminate={someChecked} checked={allChecked}
                    onChange={(_, c) => toggleAll(c)} disabled={items.length === 0} />
                </TableCell>
                <TableCell>{t('admin:sync_tasks.table.id')}</TableCell>
                <TableCell>{t('admin:sync_tasks.table.status')}</TableCell>
                <TableCell>{t('admin:sync_tasks.table.type')}</TableCell>
                <TableCell>{t('admin:sync_tasks.table.target')}</TableCell>
                <TableCell>{t('admin:sync_tasks.table.summary')}</TableCell>
                <TableCell align="right">{t('admin:sync_tasks.table.attempts')}</TableCell>
                <TableCell>{t('admin:sync_tasks.table.next_run')}</TableCell>
                <TableCell>{t('admin:sync_tasks.table.error')}</TableCell>
                <TableCell align="right">{t('admin:sync_tasks.table.actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {loading && items.length === 0 && (
                <TableRow><TableCell colSpan={10} sx={{ textAlign: 'center', py: 6 }}><CircularProgress size={24} /></TableCell></TableRow>
              )}
              {!loading && items.length === 0 && (
                <TableRow><TableCell colSpan={10} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
              )}
              {items.map(r => {
                const id = idOf(r); const ty = typeOf(r); const err = lastErrorOf(r)
                return (
                  <TableRow key={id} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                    <TableCell padding="checkbox">
                      <Checkbox checked={selected.has(id)} onChange={(_, c) => toggleOne(id, c)} />
                    </TableCell>
                    <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{id}</TableCell>
                    <TableCell>{statusBadge(statusOf(r))}</TableCell>
                    <TableCell sx={{ fontSize: 13 }}>{ty ? t(`admin:sync_tasks.type.${ty}`) : '-'}</TableCell>
                    <TableCell sx={{ fontSize: 13 }}>{targetOf(r)}</TableCell>
                    <TableCell sx={{ fontSize: 13, maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis' }}>{summaryOf(r)}</TableCell>
                    <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums' }}>{attemptsOf(r)}</TableCell>
                    <TableCell sx={{ fontSize: 13 }}>{formatDualTz(nextRunOf(r), panelTz)}</TableCell>
                    <TableCell sx={{ fontSize: 12, color: md.error, maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                      <Tooltip title={err}><span>{err}</span></Tooltip>
                    </TableCell>
                    <TableCell align="right">
                      <Tooltip title={t('admin:sync_tasks.action.detail')}>
                        <IconButton size="small" onClick={() => { setDetail(r); setDetailOpen(true) }}>
                          <VisibilityIcon fontSize="small" />
                        </IconButton>
                      </Tooltip>
                      <Tooltip title={t('admin:sync_tasks.action.retry')}>
                        <IconButton size="small" onClick={() => retry(r)}><ReplayIcon fontSize="small" /></IconButton>
                      </Tooltip>
                      <Tooltip title={t('admin:sync_tasks.action.cancel')}>
                        <IconButton size="small" onClick={() => cancel(r)} sx={{ color: md.error }}>
                          <CloseIcon fontSize="small" />
                        </IconButton>
                      </Tooltip>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </TableContainer>
        <PagedTableFooter
          total={total} page={page} pageSize={pageSize}
          onPageChange={setPage} onPageSizeChange={setPageSizePersist}
        />
      </Card>
      {/* Detail dialog */}
      <Dialog open={detailOpen} onClose={() => setDetailOpen(false)}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 720, maxWidth: '95vw' } }
        }}>
        <DialogTitle>{t('admin:sync_tasks.detail_title')} #{detail && idOf(detail)}</DialogTitle>
        <DialogContent>
          {detail && (
            <Box component="pre" sx={{
              p: 2, bgcolor: md.surfaceContainerHighest, borderRadius: 1.5,
              fontSize: 13, lineHeight: 1.55, color: md.onSurface,
              // <pre> defaults to the browser's old Courier New on Windows,
              // which looks cramped and ragged. Stack modern monospaces so we
              // pick whatever the OS / app actually ships first.
              fontFamily: `ui-monospace, "SF Mono", "Cascadia Code", "JetBrains Mono", Menlo, Consolas, "Liberation Mono", monospace`,
              overflow: 'auto', maxHeight: 520, m: 0,
              tabSize: 2,
            }}>
              {JSON.stringify(detail, null, 2)}
            </Box>
          )}
        </DialogContent>
        <DialogActions>
          <DeleteIcon sx={{ display: 'none' }} />
          <Button variant="contained" onClick={() => setDetailOpen(false)}>{t('common:actions.ok')}</Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
