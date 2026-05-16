import { useEffect, useState, type FormEvent, type ChangeEvent } from 'react'
import {
  Box,
  Button,
  Card,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  InputBase,
  Pagination,
  Tab,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tabs,
  TextField,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import VisibilityIcon from '@mui/icons-material/Visibility'
import CleaningIcon from '@mui/icons-material/CleaningServices'
import SearchIcon from '@mui/icons-material/Search'
import { useTranslation } from 'react-i18next'

import { clearAudit, listAudit, type AuditEntry } from '@/api/audit'
import { clearSubLogs, getSubLogs, purgeSubLogs, type SubLog } from '@/api/subLogs'
import { getUISettings, putUISettings } from '@/api/settings'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useTabParam } from '@/hooks/useTabParam'

const PAGE_SIZE = 50

function formatDate(s?: string) {
  if (!s) return '-'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? '-' : d.toLocaleString()
}

function formatJson(s?: string) {
  if (!s) return ''
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}

export default function LogsView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])

  const [tab, setTab] = useTabParam<'sub' | 'audit'>('tab', 'sub', ['sub', 'audit'])

  // Sub logs
  const [subItems, setSubItems] = useState<SubLog[]>([])
  const [subTotal, setSubTotal] = useState(0)
  const [subPage, setSubPage] = useState(1)
  const [subLoading, setSubLoading] = useState(false)
  const [subDetailOpen, setSubDetailOpen] = useState(false)
  const [subDetail, setSubDetail] = useState<SubLog | null>(null)

  // Inline retention editor (mirrors settings.sub_log_retention_days)
  const [retentionDays, setRetentionDays] = useState<number | null>(null)
  const [retentionSavedDays, setRetentionSavedDays] = useState<number | null>(null)
  const [retentionSaving, setRetentionSaving] = useState(false)
  useEffect(() => {
    void (async () => {
      try {
        const s = await getUISettings()
        setRetentionDays(s.sub_log_retention_days)
        setRetentionSavedDays(s.sub_log_retention_days)
      } catch { /* toasted */ }
    })()
  }, [])
  async function saveRetention() {
    if (retentionDays === null) return
    setRetentionSaving(true)
    try {
      const s = await getUISettings()
      const updated = await putUISettings({ ...s, sub_log_retention_days: retentionDays })
      setRetentionSavedDays(updated.sub_log_retention_days)
      pushSnack(t('admin:logs.retention_saved'), 'success')
    } finally { setRetentionSaving(false) }
  }

  // Audit logs
  const [auditItems, setAuditItems] = useState<AuditEntry[]>([])
  const [auditTotal, setAuditTotal] = useState(0)
  const [auditPage, setAuditPage] = useState(1)
  const [auditLoading, setAuditLoading] = useState(false)
  const [actorFilter, setActorFilter] = useState('')
  const [actionFilter, setActionFilter] = useState('')
  const [auditDetailOpen, setAuditDetailOpen] = useState(false)
  const [auditDetail, setAuditDetail] = useState<AuditEntry | null>(null)

  useEffect(() => {
    if (tab === 'sub') void loadSub()
    else void loadAudit()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, subPage, auditPage])

  async function loadSub() {
    setSubLoading(true)
    try {
      const res = await getSubLogs({ page: subPage, page_size: PAGE_SIZE })
      setSubItems(res.items); setSubTotal(res.total)
    } finally { setSubLoading(false) }
  }

  async function loadAudit() {
    setAuditLoading(true)
    try {
      const res = await listAudit({
        page: auditPage, page_size: PAGE_SIZE,
        actor: actorFilter || undefined, action: actionFilter || undefined,
      })
      setAuditItems(res.items); setAuditTotal(res.total)
    } finally { setAuditLoading(false) }
  }

  async function clearSubAll() {
    const ok = await confirm({
      title: t('admin:logs.confirm.clear_sub_title'),
      message: t('admin:logs.confirm.clear_sub_message'),
      destructive: true,
      confirmText: t('admin:logs.clear_all'),
    })
    if (!ok) return
    await clearSubLogs()
    pushSnack(t('admin:logs.toast.cleared'), 'success')
    await loadSub()
  }

  async function purgeSubOld() {
    const r = await purgeSubLogs()
    pushSnack(t('admin:logs.toast.purged', { count: r.deleted }), 'success')
    await loadSub()
  }

  async function clearAuditAll() {
    const ok = await confirm({
      title: t('admin:logs.confirm.clear_audit_title'),
      message: t('admin:logs.confirm.clear_audit_message'),
      destructive: true,
      confirmText: t('admin:logs.clear_all'),
    })
    if (!ok) return
    await clearAudit()
    pushSnack(t('admin:logs.toast.cleared'), 'success')
    await loadAudit()
  }

  function onAuditFilter(e: FormEvent) { e.preventDefault(); setAuditPage(1); void loadAudit() }

  const subPages = Math.max(1, Math.ceil(subTotal / PAGE_SIZE))
  const auditPages = Math.max(1, Math.ceil(auditTotal / PAGE_SIZE))

  return (
    <Box sx={{ p: 3 }}>
      <Typography variant="h4" sx={{ mb: 2 }}>{t('admin:logs.title')}</Typography>
      <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ mb: 2, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Tab value="sub" label={t('admin:logs.tab_sub')} />
        <Tab value="audit" label={t('admin:logs.tab_audit')} />
      </Tabs>

      {tab === 'sub' && (
        <>
          <Box sx={{ display: 'flex', gap: 1, mb: 2, alignItems: 'center', flexWrap: 'wrap' }}>
            <Button variant="outlined" startIcon={<CleaningIcon />} onClick={purgeSubOld}>
              {t('admin:logs.purge_old')}
            </Button>
            <Button variant="outlined" color="error" startIcon={<DeleteIcon />} onClick={clearSubAll}>
              {t('admin:logs.clear_all')}
            </Button>
            <Box sx={{ flex: 1 }} />
            <TextField
              type="number" size="small"
              label={t('admin:logs.retention_label')}
              value={retentionDays ?? ''}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setRetentionDays(e.target.value === '' ? null : Number(e.target.value))}
              inputProps={{ min: 0 }}
              sx={{ width: 200 }}
            />
            <Button variant="contained" disabled={retentionSaving || retentionDays === null || retentionDays === retentionSavedDays}
              onClick={saveRetention}>
              {t('admin:logs.retention_save')}
            </Button>
          </Box>
          <Typography variant="body2" sx={{ mb: 2, color: md.onSurfaceVariant, fontSize: 12 }}>
            {t('admin:logs.retention_hint')}
          </Typography>
          <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
            <TableContainer>
              <Table>
                <TableHead>
                  <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                    <TableCell>{t('admin:logs.sub_table.id')}</TableCell>
                    <TableCell>{t('admin:logs.sub_table.user')}</TableCell>
                    <TableCell>{t('admin:logs.sub_table.ip')}</TableCell>
                    <TableCell>{t('admin:logs.sub_table.client_type')}</TableCell>
                    <TableCell>{t('admin:logs.sub_table.ua')}</TableCell>
                    <TableCell>{t('admin:logs.sub_table.at')}</TableCell>
                    <TableCell align="right" />
                  </TableRow>
                </TableHead>
                <TableBody>
                  {subLoading && subItems.length === 0 && (
                    <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6 }}><CircularProgress size={24} /></TableCell></TableRow>
                  )}
                  {!subLoading && subItems.length === 0 && (
                    <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
                  )}
                  {subItems.map(r => (
                    <TableRow key={r.id} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}>
                      <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{r.id}</TableCell>
                      <TableCell sx={{ fontWeight: 500 }}>{r.user_upn || `#${r.user_id}`}</TableCell>
                      <TableCell sx={{ fontSize: 13 }}>{r.ip}</TableCell>
                      <TableCell sx={{ fontSize: 13 }}>{r.client_type}</TableCell>
                      <TableCell sx={{ fontSize: 12, color: md.onSurfaceVariant, maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.ua}</TableCell>
                      <TableCell sx={{ fontSize: 13, whiteSpace: 'nowrap' }}>{formatDate(r.accessed_at)}</TableCell>
                      <TableCell align="right">
                        <Tooltip title={t('admin:logs.view_detail')}>
                          <IconButton size="small" onClick={() => { setSubDetail(r); setSubDetailOpen(true) }}>
                            <VisibilityIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableContainer>
            {subPages > 1 && (
              <Box sx={{ display: 'flex', justifyContent: 'center', py: 2, borderTop: `1px solid ${md.outlineVariant}` }}>
                <Pagination count={subPages} page={subPage} onChange={(_, p) => setSubPage(p)} shape="rounded" color="primary" />
              </Box>
            )}
          </Card>
        </>
      )}

      {tab === 'audit' && (
        <>
          <Box component="form" onSubmit={onAuditFilter} sx={{ display: 'flex', gap: 1.5, mb: 2, flexWrap: 'wrap', alignItems: 'center' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, height: 40, px: 2, borderRadius: 9999,
              bgcolor: md.surfaceContainer, color: md.onSurfaceVariant, width: 220 }}>
              <SearchIcon sx={{ fontSize: 18 }} />
              <InputBase placeholder={t('admin:logs.filter.actor')} value={actorFilter}
                onChange={e => setActorFilter(e.target.value)}
                sx={{ flex: 1, fontSize: 14, color: md.onSurface }} />
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, height: 40, px: 2, borderRadius: 9999,
              bgcolor: md.surfaceContainer, color: md.onSurfaceVariant, width: 220 }}>
              <SearchIcon sx={{ fontSize: 18 }} />
              <InputBase placeholder={t('admin:logs.filter.action')} value={actionFilter}
                onChange={e => setActionFilter(e.target.value)}
                sx={{ flex: 1, fontSize: 14, color: md.onSurface }} />
            </Box>
            <Button type="submit" variant="outlined">{t('common:search.placeholder')}</Button>
            <Box sx={{ flex: 1 }} />
            <Button variant="outlined" color="error" startIcon={<DeleteIcon />} onClick={clearAuditAll}>
              {t('admin:logs.clear_all')}
            </Button>
          </Box>
          <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
            <TableContainer>
              <Table>
                <TableHead>
                  <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                    <TableCell>{t('admin:logs.audit_table.id')}</TableCell>
                    <TableCell>{t('admin:logs.audit_table.actor')}</TableCell>
                    <TableCell>{t('admin:logs.audit_table.action')}</TableCell>
                    <TableCell>{t('admin:logs.audit_table.target')}</TableCell>
                    <TableCell>{t('admin:logs.audit_table.ip')}</TableCell>
                    <TableCell>{t('admin:logs.audit_table.at')}</TableCell>
                    <TableCell align="right" />
                  </TableRow>
                </TableHead>
                <TableBody>
                  {auditLoading && auditItems.length === 0 && (
                    <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6 }}><CircularProgress size={24} /></TableCell></TableRow>
                  )}
                  {!auditLoading && auditItems.length === 0 && (
                    <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
                  )}
                  {auditItems.map(r => (
                    <TableRow key={r.id} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}>
                      <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{r.id}</TableCell>
                      <TableCell sx={{ fontWeight: 500 }}>{r.actor}</TableCell>
                      <TableCell sx={{ fontSize: 13 }}>{r.action}</TableCell>
                      <TableCell sx={{ fontSize: 13 }}>{r.target}</TableCell>
                      <TableCell sx={{ fontSize: 13 }}>{r.ip}</TableCell>
                      <TableCell sx={{ fontSize: 13, whiteSpace: 'nowrap' }}>{formatDate(r.at)}</TableCell>
                      <TableCell align="right">
                        <Tooltip title={t('admin:logs.view_detail')}>
                          <IconButton size="small" onClick={() => { setAuditDetail(r); setAuditDetailOpen(true) }}>
                            <VisibilityIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableContainer>
            {auditPages > 1 && (
              <Box sx={{ display: 'flex', justifyContent: 'center', py: 2, borderTop: `1px solid ${md.outlineVariant}` }}>
                <Pagination count={auditPages} page={auditPage} onChange={(_, p) => setAuditPage(p)} shape="rounded" color="primary" />
              </Box>
            )}
          </Card>
        </>
      )}

      {/* Sub log detail */}
      <Dialog open={subDetailOpen} onClose={() => setSubDetailOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 600, maxWidth: '95vw' } }}>
        <DialogTitle>{t('admin:logs.view_detail')} #{subDetail?.id}</DialogTitle>
        <DialogContent>
          {subDetail && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, fontSize: 13 }}>
              <Row label="User" value={subDetail.user_upn || `#${subDetail.user_id}`} md={md} />
              <Row label="IP" value={subDetail.ip} mono md={md} />
              <Row label="Client" value={subDetail.client_type} md={md} />
              <Row label="UA" value={subDetail.ua} mono md={md} />
              <Row label="At" value={formatDate(subDetail.accessed_at)} md={md} />
            </Box>
          )}
        </DialogContent>
        <DialogActions>
          <Button variant="contained" onClick={() => setSubDetailOpen(false)}>{t('common:actions.ok')}</Button>
        </DialogActions>
      </Dialog>

      {/* Audit detail */}
      <Dialog open={auditDetailOpen} onClose={() => setAuditDetailOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 720, maxWidth: '95vw' } }}>
        <DialogTitle>{t('admin:logs.view_detail')} #{auditDetail?.id}</DialogTitle>
        <DialogContent>
          {auditDetail && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, fontSize: 13 }}>
              <Row label="Actor" value={auditDetail.actor} md={md} />
              <Row label="Action" value={auditDetail.action} mono md={md} />
              <Row label="Target" value={auditDetail.target} md={md} />
              <Row label="IP" value={auditDetail.ip} mono md={md} />
              <Row label="At" value={formatDate(auditDetail.at)} md={md} />
              {auditDetail.before_json && (
                <Box>
                  <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>{t('admin:logs.detail.before')}</Typography>
                  <Box component="pre" sx={{ p: 1.5, bgcolor: md.surfaceContainerHighest, borderRadius: 1.5, fontSize: 12, overflow: 'auto', maxHeight: 240, m: 0 }}>
                    {formatJson(auditDetail.before_json)}
                  </Box>
                </Box>
              )}
              {auditDetail.after_json && (
                <Box>
                  <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>{t('admin:logs.detail.after')}</Typography>
                  <Box component="pre" sx={{ p: 1.5, bgcolor: md.surfaceContainerHighest, borderRadius: 1.5, fontSize: 12, overflow: 'auto', maxHeight: 240, m: 0 }}>
                    {formatJson(auditDetail.after_json)}
                  </Box>
                </Box>
              )}
            </Box>
          )}
        </DialogContent>
        <DialogActions>
          <Button variant="contained" onClick={() => setAuditDetailOpen(false)}>{t('common:actions.ok')}</Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}

interface RowProps { label: string; value: string; mono?: boolean; md: { onSurfaceVariant: string } }
function Row({ label, value, mono, md }: RowProps) {
  return (
    <Box sx={{ display: 'flex', gap: 2, alignItems: 'baseline' }}>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, minWidth: 80, flexShrink: 0 }}>{label}</Typography>
      <Typography sx={{ fontSize: 13, fontFamily: mono ? 'monospace' : undefined, wordBreak: 'break-all' }}>{value}</Typography>
    </Box>
  )
}
