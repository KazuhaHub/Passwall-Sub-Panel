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
import { useCan } from '@/utils/permissions'

import { clearAudit, listAudit, type AuditEntry } from '@/api/audit'
import { clearSubLogs, getSubLogs, purgeSubLogs, type SubLog } from '@/api/subLogs'
import { clearEmailLogs, getEmailLogs, purgeEmailLogs, type EmailLog } from '@/api/emailLogs'
import { getUISettings, putUISettings } from '@/api/settings'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useTabParam } from '@/hooks/useTabParam'
import { useSiteStore } from '@/stores/site'

const PAGE_SIZE = 50

// formatDualTz renders the timestamp in the panel timezone first (the
// "system view" everything else in the panel reports against) with the
// browser-local rendering in parentheses. Falls back to a single value
// when the two timezones happen to be identical or panel tz is unset.
function formatDualTz(s: string | undefined, panelTz: string): string {
  if (!s) return '-'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return '-'
  let bz = ''
  try { bz = Intl.DateTimeFormat().resolvedOptions().timeZone } catch { bz = '' }
  const panelStr = panelTz
    ? d.toLocaleString(undefined, { timeZone: panelTz })
    : d.toLocaleString()
  if (!panelTz || panelTz === bz) return panelStr
  const browserStr = d.toLocaleString()
  return `${panelStr} (${browserStr})`
}

function formatJson(s?: string) {
  if (!s) return ''
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}

export default function LogsView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  const canConfig = useCan('config.write')
  const panelTz = useSiteStore(s => s.timezone)

  const [tab, setTab] = useTabParam<'sub' | 'audit' | 'email'>('tab', 'sub', ['sub', 'audit', 'email'])

  // Sub logs
  const [subItems, setSubItems] = useState<SubLog[]>([])
  const [subTotal, setSubTotal] = useState(0)
  const [subPage, setSubPage] = useState(1)
  const [subLoading, setSubLoading] = useState(false)
  const [subSearch, setSubSearch] = useState('')
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
  const [auditSearch, setAuditSearch] = useState('')
  const [auditDetailOpen, setAuditDetailOpen] = useState(false)
  const [auditDetail, setAuditDetail] = useState<AuditEntry | null>(null)

  // Email logs — successful outbound notifications recorded by the
  // mailer service (mail_sent table). Same pagination + clear/purge
  // pattern as sub logs; retention is admin-tunable separately under
  // notify settings (MailSentRetentionDays, default 30 days).
  const [emailItems, setEmailItems] = useState<EmailLog[]>([])
  const [emailTotal, setEmailTotal] = useState(0)
  const [emailPage, setEmailPage] = useState(1)
  const [emailLoading, setEmailLoading] = useState(false)
  const [emailSearch, setEmailSearch] = useState('')
  const [emailDetailOpen, setEmailDetailOpen] = useState(false)
  const [emailDetail, setEmailDetail] = useState<EmailLog | null>(null)
  const [emailRetentionDays, setEmailRetentionDays] = useState<number | null>(null)
  const [emailRetentionSavedDays, setEmailRetentionSavedDays] = useState<number | null>(null)
  const [emailRetentionSaving, setEmailRetentionSaving] = useState(false)
  useEffect(() => {
    void (async () => {
      try {
        const s = await getUISettings()
        setEmailRetentionDays(s.mail_sent_retention_days)
        setEmailRetentionSavedDays(s.mail_sent_retention_days)
      } catch { /* toasted */ }
    })()
  }, [])
  async function saveEmailRetention() {
    if (emailRetentionDays === null) return
    setEmailRetentionSaving(true)
    try {
      const s = await getUISettings()
      const updated = await putUISettings({ ...s, mail_sent_retention_days: emailRetentionDays })
      setEmailRetentionSavedDays(updated.mail_sent_retention_days)
      pushSnack(t('admin:logs.retention_saved'), 'success')
    } finally { setEmailRetentionSaving(false) }
  }

  useEffect(() => {
    if (tab === 'sub') void loadSub()
    else if (tab === 'audit') void loadAudit()
    else void loadEmail()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, subPage, auditPage, emailPage])

  async function loadSub() {
    setSubLoading(true)
    try {
      const res = await getSubLogs({ page: subPage, page_size: PAGE_SIZE, search: subSearch || undefined })
      setSubItems(res.items); setSubTotal(res.total)
    } finally { setSubLoading(false) }
  }

  async function loadAudit() {
    setAuditLoading(true)
    try {
      const res = await listAudit({
        page: auditPage, page_size: PAGE_SIZE,
        search: auditSearch || undefined,
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
  function onSubFilter(e: FormEvent) { e.preventDefault(); setSubPage(1); void loadSub() }
  function onEmailFilter(e: FormEvent) { e.preventDefault(); setEmailPage(1); void loadEmail() }

  async function loadEmail() {
    setEmailLoading(true)
    try {
      const res = await getEmailLogs({ page: emailPage, page_size: PAGE_SIZE, search: emailSearch || undefined })
      setEmailItems(res.items); setEmailTotal(res.total)
    } finally { setEmailLoading(false) }
  }

  async function clearEmailAll() {
    const ok = await confirm({
      title: t('admin:logs.confirm.clear_email_title'),
      message: t('admin:logs.confirm.clear_email_message'),
      destructive: true,
      confirmText: t('admin:logs.clear_all'),
    })
    if (!ok) return
    await clearEmailLogs()
    pushSnack(t('admin:logs.toast.cleared'), 'success')
    await loadEmail()
  }

  async function purgeEmailOld() {
    const r = await purgeEmailLogs()
    pushSnack(t('admin:logs.toast.purged', { count: r.deleted }), 'success')
    await loadEmail()
  }

  const subPages = Math.max(1, Math.ceil(subTotal / PAGE_SIZE))
  const auditPages = Math.max(1, Math.ceil(auditTotal / PAGE_SIZE))
  const emailPages = Math.max(1, Math.ceil(emailTotal / PAGE_SIZE))

  return (
    <Box sx={{ p: 3 }}>
      <Typography variant="h4" sx={{ mb: 2 }}>{t('admin:logs.title')}</Typography>
      <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ mb: 2, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Tab value="sub" label={t('admin:logs.tab_sub')} />
        <Tab value="audit" label={t('admin:logs.tab_audit')} />
        <Tab value="email" label={t('admin:logs.tab_email')} />
      </Tabs>

      {tab === 'sub' && (
        <>
          <Box component="form" onSubmit={onSubFilter} sx={{ display: 'flex', gap: 1.5, mb: 2, alignItems: 'center', flexWrap: 'wrap' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, height: 40, px: 2, borderRadius: 9999,
              bgcolor: md.surfaceContainer, color: md.onSurfaceVariant, width: 320, maxWidth: '100%' }}>
              <SearchIcon sx={{ fontSize: 18 }} />
              <InputBase placeholder={t('admin:logs.filter.sub_search', { defaultValue: '搜索 用户 / IP / UA / 客户端' })} value={subSearch}
                onChange={e => setSubSearch(e.target.value)}
                sx={{ flex: 1, fontSize: 14, color: md.onSurface }} />
            </Box>
            <Button type="submit" variant="outlined">{t('common:search.placeholder')}</Button>
          </Box>
          <Box sx={{ display: 'flex', gap: 1, mb: 2, alignItems: 'center', flexWrap: 'wrap' }}>
            {canConfig && <>
              <Button variant="outlined" startIcon={<CleaningIcon />} onClick={purgeSubOld}>
                {t('admin:logs.purge_old')}
              </Button>
              <Button variant="outlined" color="error" startIcon={<DeleteIcon />} onClick={clearSubAll}>
                {t('admin:logs.clear_all')}
              </Button>
            </>}
            <Box sx={{ flex: 1 }} />
            {canConfig && <>
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
            </>}
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
                      <TableCell sx={{ fontSize: 13, whiteSpace: 'nowrap' }}>{formatDualTz(r.accessed_at, panelTz)}</TableCell>
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
              bgcolor: md.surfaceContainer, color: md.onSurfaceVariant, width: 320, maxWidth: '100%' }}>
              <SearchIcon sx={{ fontSize: 18 }} />
              <InputBase placeholder={t('admin:logs.filter.audit_search', { defaultValue: '搜索 操作者 / 动作 / 对象' })} value={auditSearch}
                onChange={e => setAuditSearch(e.target.value)}
                sx={{ flex: 1, fontSize: 14, color: md.onSurface }} />
            </Box>
            <Button type="submit" variant="outlined">{t('common:search.placeholder')}</Button>
            <Box sx={{ flex: 1 }} />
            {canConfig && <Button variant="outlined" color="error" startIcon={<DeleteIcon />} onClick={clearAuditAll}>
              {t('admin:logs.clear_all')}
            </Button>}
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
                      <TableCell sx={{ fontSize: 13, whiteSpace: 'nowrap' }}>{formatDualTz(r.at, panelTz)}</TableCell>
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

      {tab === 'email' && (
        <>
          <Box component="form" onSubmit={onEmailFilter} sx={{ display: 'flex', gap: 1.5, mb: 2, alignItems: 'center', flexWrap: 'wrap' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, height: 40, px: 2, borderRadius: 9999,
              bgcolor: md.surfaceContainer, color: md.onSurfaceVariant, width: 320, maxWidth: '100%' }}>
              <SearchIcon sx={{ fontSize: 18 }} />
              <InputBase placeholder={t('admin:logs.filter.email_search', { defaultValue: '搜索 用户 / 收件人 / 类型' })} value={emailSearch}
                onChange={e => setEmailSearch(e.target.value)}
                sx={{ flex: 1, fontSize: 14, color: md.onSurface }} />
            </Box>
            <Button type="submit" variant="outlined">{t('common:search.placeholder')}</Button>
          </Box>
          <Box sx={{ display: 'flex', gap: 1, mb: 2, alignItems: 'center', flexWrap: 'wrap' }}>
            {canConfig && <>
              <Button variant="outlined" startIcon={<CleaningIcon />} onClick={purgeEmailOld}>
                {t('admin:logs.purge_old')}
              </Button>
              <Button variant="outlined" color="error" startIcon={<DeleteIcon />} onClick={clearEmailAll}>
                {t('admin:logs.clear_all')}
              </Button>
            </>}
            <Box sx={{ flex: 1 }} />
            {canConfig && <>
              <TextField
                type="number" size="small"
                label={t('admin:logs.retention_label')}
                value={emailRetentionDays ?? ''}
                onChange={(e: ChangeEvent<HTMLInputElement>) => setEmailRetentionDays(e.target.value === '' ? null : Number(e.target.value))}
                inputProps={{ min: 0 }}
                sx={{ width: 200 }}
              />
              <Button variant="contained"
                disabled={emailRetentionSaving || emailRetentionDays === null || emailRetentionDays === emailRetentionSavedDays}
                onClick={saveEmailRetention}>
                {t('admin:logs.retention_save')}
              </Button>
            </>}
          </Box>
          <Typography variant="body2" sx={{ mb: 2, color: md.onSurfaceVariant, fontSize: 12 }}>
            {t('admin:logs.retention_hint')}
          </Typography>
          <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
            <TableContainer>
              <Table>
                <TableHead>
                  <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                    <TableCell>{t('admin:logs.email_table.id')}</TableCell>
                    <TableCell>{t('admin:logs.email_table.user')}</TableCell>
                    <TableCell>{t('admin:logs.email_table.to')}</TableCell>
                    <TableCell>{t('admin:logs.email_table.kind')}</TableCell>
                    <TableCell>{t('admin:logs.email_table.at')}</TableCell>
                    <TableCell align="right" />
                  </TableRow>
                </TableHead>
                <TableBody>
                  {emailLoading && emailItems.length === 0 && (
                    <TableRow><TableCell colSpan={6} sx={{ textAlign: 'center', py: 6 }}><CircularProgress size={24} /></TableCell></TableRow>
                  )}
                  {!emailLoading && emailItems.length === 0 && (
                    <TableRow><TableCell colSpan={6} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
                  )}
                  {emailItems.map(r => (
                    <TableRow key={r.id} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}>
                      <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{r.id}</TableCell>
                      <TableCell sx={{ fontWeight: 500 }}>{r.user_upn || `#${r.user_id}`}</TableCell>
                      <TableCell sx={{ fontSize: 13 }}>{r.to_email}</TableCell>
                      <TableCell sx={{ fontSize: 13 }}>{r.kind}</TableCell>
                      <TableCell sx={{ fontSize: 13, whiteSpace: 'nowrap' }}>{formatDualTz(r.sent_at, panelTz)}</TableCell>
                      <TableCell align="right">
                        <Tooltip title={t('admin:logs.view_detail')}>
                          <IconButton size="small" onClick={() => { setEmailDetail(r); setEmailDetailOpen(true) }}>
                            <VisibilityIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableContainer>
            {emailPages > 1 && (
              <Box sx={{ display: 'flex', justifyContent: 'center', py: 2, borderTop: `1px solid ${md.outlineVariant}` }}>
                <Pagination count={emailPages} page={emailPage} onChange={(_, p) => setEmailPage(p)} shape="rounded" color="primary" />
              </Box>
            )}
          </Card>
        </>
      )}

      {/* Email log detail */}
      <Dialog open={emailDetailOpen} onClose={() => setEmailDetailOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 600, maxWidth: '95vw' } }}>
        <DialogTitle>{t('admin:logs.view_detail')} #{emailDetail?.id}</DialogTitle>
        <DialogContent>
          {emailDetail && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, fontSize: 13 }}>
              <Row label="User" value={emailDetail.user_upn || `#${emailDetail.user_id}`} md={md} />
              <Row label="To" value={emailDetail.to_email} mono md={md} />
              <Row label="Kind" value={emailDetail.kind} md={md} />
              <Row label="Window" value={emailDetail.window_key} mono md={md} />
              <Row label="Sent at" value={formatDualTz(emailDetail.sent_at, panelTz)} md={md} />
            </Box>
          )}
        </DialogContent>
        <DialogActions>
          <Button variant="contained" onClick={() => setEmailDetailOpen(false)}>{t('common:actions.ok')}</Button>
        </DialogActions>
      </Dialog>

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
              <Row label="At" value={formatDualTz(subDetail.accessed_at, panelTz)} md={md} />
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
              <Row label="At" value={formatDualTz(auditDetail.at, panelTz)} md={md} />
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
