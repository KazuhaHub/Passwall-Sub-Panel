import { useEffect, useRef, useState, type FormEvent, type ChangeEvent } from 'react'
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
  MenuItem,
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
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import VisibilityIcon from '@mui/icons-material/Visibility'
import CleaningIcon from '@mui/icons-material/CleaningServices'
import SearchIcon from '@mui/icons-material/Search'
import { useTranslation } from 'react-i18next'
import { useCan } from '@/utils/permissions'

import PageHeader from '@/components/PageHeader'
import { clearAudit, listAudit, type AuditEntry } from '@/api/audit'
import { listAuthEvents, type AuthEvent } from '@/api/authEvents'
import { clearSubLogs, getSubLogs, purgeSubLogs, type SubLog } from '@/api/subLogs'
import { formatRegion } from '@/utils/geo'
import { clearEmailLogs, getEmailLogs, purgeEmailLogs, type EmailLog } from '@/api/emailLogs'
import { getUISettings, putUISettings } from '@/api/settings'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import { useTabParam } from '@/hooks/useTabParam'
import { formatDualTz } from '@/utils/datetime'
import CertEventsTab from './CertEventsTab'
import { useSiteStore } from '@/stores/site'

// Initial page size pulled from the shared psp_page_size key so the
// admin's "100/page" preference set on Users carries to the Logs tabs
// too. Per-tab pageSize state below tracks runtime changes.
function initialPageSize(): number {
  try {
    const raw = localStorage.getItem('psp_page_size')
    const n = raw ? parseInt(raw, 10) : 25
    return Number.isFinite(n) && n > 0 ? n : 25
  } catch { return 25 }
}
function persistPageSize(n: number) {
  try { localStorage.setItem('psp_page_size', String(n)) } catch { /* ignore */ }
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

  const [tab, setTab] = useTabParam<'sub' | 'audit' | 'auth' | 'email' | 'certs'>('tab', 'sub', ['sub', 'audit', 'auth', 'email', 'certs'])

  // Sub logs
  const [subItems, setSubItems] = useState<SubLog[]>([])
  const [subTotal, setSubTotal] = useState(0)
  const [subPage, setSubPage] = useState(1)
  const [subPageSize, setSubPageSize] = useState<number>(initialPageSize)
  function setSubPageSizePersist(n: number) { setSubPageSize(n); persistPageSize(n); setSubPage(1) }
  const [subLoading, setSubLoading] = useState(false)
  const [subSearch, setSubSearch] = useState('')
  // appliedSearch is what loads/pagination key off; subSearch is just the live
  // input. Splitting them means paging doesn't pick up a half-typed term, and
  // submitting drives a single effect-triggered reload (no stale-page flash).
  const [subAppliedSearch, setSubAppliedSearch] = useState('')
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
  const [auditPageSize, setAuditPageSize] = useState<number>(initialPageSize)
  function setAuditPageSizePersist(n: number) { setAuditPageSize(n); persistPageSize(n); setAuditPage(1) }
  const [auditLoading, setAuditLoading] = useState(false)
  const [auditSearch, setAuditSearch] = useState('')
  const [auditAppliedSearch, setAuditAppliedSearch] = useState('')
  const [auditDetailOpen, setAuditDetailOpen] = useState(false)
  const [auditDetail, setAuditDetail] = useState<AuditEntry | null>(null)

  // Auth events — logins across local / saml / oidc, success + failure.
  const [authItems, setAuthItems] = useState<AuthEvent[]>([])
  const [authTotal, setAuthTotal] = useState(0)
  const [authPage, setAuthPage] = useState(1)
  const [authPageSize, setAuthPageSize] = useState<number>(initialPageSize)
  function setAuthPageSizePersist(n: number) { setAuthPageSize(n); persistPageSize(n); setAuthPage(1) }
  const [authLoading, setAuthLoading] = useState(false)
  const [authSearch, setAuthSearch] = useState('')
  const [authAppliedSearch, setAuthAppliedSearch] = useState('')
  const [authMethod, setAuthMethod] = useState<'' | AuthEvent['method']>('')
  const [authOutcome, setAuthOutcome] = useState<'' | AuthEvent['outcome']>('')

  // Email logs — successful outbound notifications recorded by the
  // mailer service (mail_sent table). Same pagination + clear/purge
  // pattern as sub logs; retention is admin-tunable separately under
  // notify settings (MailSentRetentionDays, default 30 days).
  const [emailItems, setEmailItems] = useState<EmailLog[]>([])
  const [emailTotal, setEmailTotal] = useState(0)
  const [emailPage, setEmailPage] = useState(1)
  const [emailPageSize, setEmailPageSize] = useState<number>(initialPageSize)
  function setEmailPageSizePersist(n: number) { setEmailPageSize(n); persistPageSize(n); setEmailPage(1) }
  const [emailLoading, setEmailLoading] = useState(false)
  const [emailSearch, setEmailSearch] = useState('')
  const [emailAppliedSearch, setEmailAppliedSearch] = useState('')
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
    else if (tab === 'auth') void loadAuth()
    else void loadEmail()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab,
    subPage, subPageSize, subAppliedSearch,
    auditPage, auditPageSize, auditAppliedSearch,
    authPage, authPageSize, authAppliedSearch, authMethod, authOutcome,
    emailPage, emailPageSize, emailAppliedSearch,
  ])

  // Last-wins guards: connecting Pagination / filter submit fires overlapping
  // loads within one tab; a slow earlier page must not overwrite the newer one.
  const subSeq = useRef(0)
  const auditSeq = useRef(0)
  const authSeq = useRef(0)
  const emailSeq = useRef(0)

  async function loadSub() {
    const seq = ++subSeq.current
    setSubLoading(true)
    try {
      const res = await getSubLogs({ page: subPage, page_size: subPageSize, search: subAppliedSearch || undefined })
      if (seq !== subSeq.current) return
      setSubItems(res.items); setSubTotal(res.total)
    } finally { if (seq === subSeq.current) setSubLoading(false) }
  }

  async function loadAudit() {
    const seq = ++auditSeq.current
    setAuditLoading(true)
    try {
      const res = await listAudit({
        page: auditPage, page_size: auditPageSize,
        search: auditAppliedSearch || undefined,
      })
      if (seq !== auditSeq.current) return
      setAuditItems(res.items); setAuditTotal(res.total)
    } finally { if (seq === auditSeq.current) setAuditLoading(false) }
  }

  async function loadAuth() {
    const seq = ++authSeq.current
    setAuthLoading(true)
    try {
      const res = await listAuthEvents({
        page: authPage, page_size: authPageSize,
        search: authAppliedSearch || undefined,
        method: authMethod || undefined,
        outcome: authOutcome || undefined,
      })
      if (seq !== authSeq.current) return
      setAuthItems(res.items); setAuthTotal(res.total)
    } finally { if (seq === authSeq.current) setAuthLoading(false) }
  }
  function onAuthFilter(e: FormEvent) { e.preventDefault(); setAuthPage(1); setAuthAppliedSearch(authSearch) }

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

  // Submitting a filter resets to page 1 and applies the typed term; the
  // single effect above (keyed on page + appliedSearch) does the one reload.
  function onAuditFilter(e: FormEvent) { e.preventDefault(); setAuditPage(1); setAuditAppliedSearch(auditSearch) }
  function onSubFilter(e: FormEvent) { e.preventDefault(); setSubPage(1); setSubAppliedSearch(subSearch) }
  function onEmailFilter(e: FormEvent) { e.preventDefault(); setEmailPage(1); setEmailAppliedSearch(emailSearch) }

  async function loadEmail() {
    const seq = ++emailSeq.current
    setEmailLoading(true)
    try {
      const res = await getEmailLogs({ page: emailPage, page_size: emailPageSize, search: emailAppliedSearch || undefined })
      if (seq !== emailSeq.current) return
      setEmailItems(res.items); setEmailTotal(res.total)
    } finally { if (seq === emailSeq.current) setEmailLoading(false) }
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

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader title={t('admin:logs.title')} />
      <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ mb: 2, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Tab value="sub" label={t('admin:logs.tab_sub')} />
        <Tab value="audit" label={t('admin:logs.tab_audit')} />
        <Tab value="auth" label={t('admin:logs.tab_auth', { defaultValue: '认证日志' })} />
        <Tab value="email" label={t('admin:logs.tab_email')} />
        <Tab value="certs" label={t('admin:logs.tab_certs')} />
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
                sx={{ width: 200 }}
                slotProps={{
                  htmlInput: { min: 0 }
                }}
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
                      <TableCell sx={{ fontSize: 13 }}>
                        {r.ip}
                        {formatRegion(r.region) && (
                          <Box sx={{ fontSize: 11, color: md.onSurfaceVariant, mt: 0.25 }}
                            title={t('logs.region_hint', { defaultValue: '地区由离线 IP 库估算；城市级仅供参考' })}>{formatRegion(r.region)}</Box>
                        )}
                      </TableCell>
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
            <PagedTableFooter
              total={subTotal} page={subPage} pageSize={subPageSize}
              onPageChange={setSubPage} onPageSizeChange={setSubPageSizePersist}
            />
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
                      <TableCell sx={{ fontSize: 13 }}>
                        {r.ip}
                        {formatRegion(r.region) && (
                          <Box sx={{ fontSize: 11, color: md.onSurfaceVariant, mt: 0.25 }}
                            title={t('logs.region_hint', { defaultValue: '地区由离线 IP 库估算；城市级仅供参考' })}>{formatRegion(r.region)}</Box>
                        )}
                      </TableCell>
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
            <PagedTableFooter
              total={auditTotal} page={auditPage} pageSize={auditPageSize}
              onPageChange={setAuditPage} onPageSizeChange={setAuditPageSizePersist}
            />
          </Card>
        </>
      )}
      {tab === 'auth' && (
        <>
          <Box component="form" onSubmit={onAuthFilter} sx={{ display: 'flex', gap: 1.5, mb: 2, flexWrap: 'wrap', alignItems: 'center' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, height: 40, px: 2, borderRadius: 9999,
              bgcolor: md.surfaceContainer, color: md.onSurfaceVariant, width: 300, maxWidth: '100%' }}>
              <SearchIcon sx={{ fontSize: 18 }} />
              <InputBase placeholder={t('admin:logs.filter.auth_search', { defaultValue: '搜索 用户 / IP / 原因' })} value={authSearch}
                onChange={e => setAuthSearch(e.target.value)}
                sx={{ flex: 1, fontSize: 14, color: md.onSurface }} />
            </Box>
            <TextField select size="small" label={t('admin:logs.auth_table.method', { defaultValue: '方法' })}
              value={authMethod} onChange={e => { setAuthPage(1); setAuthMethod(e.target.value as '' | AuthEvent['method']) }}
              sx={{ width: 130 }}>
              <MenuItem value="">{t('admin:logs.auth_filter_all', { defaultValue: '全部' })}</MenuItem>
              <MenuItem value="local">Local</MenuItem>
              <MenuItem value="saml">SAML</MenuItem>
              <MenuItem value="oidc">OIDC</MenuItem>
            </TextField>
            <TextField select size="small" label={t('admin:logs.auth_table.outcome', { defaultValue: '结果' })}
              value={authOutcome} onChange={e => { setAuthPage(1); setAuthOutcome(e.target.value as '' | AuthEvent['outcome']) }}
              sx={{ width: 130 }}>
              <MenuItem value="">{t('admin:logs.auth_filter_all', { defaultValue: '全部' })}</MenuItem>
              <MenuItem value="success">{t('admin:logs.auth_outcome.success', { defaultValue: '成功' })}</MenuItem>
              <MenuItem value="failure">{t('admin:logs.auth_outcome.failure', { defaultValue: '失败' })}</MenuItem>
            </TextField>
            <Button type="submit" variant="outlined">{t('common:search.placeholder')}</Button>
          </Box>
          <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
            <TableContainer>
              <Table>
                <TableHead>
                  <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                    <TableCell>{t('admin:logs.auth_table.at', { defaultValue: '时间' })}</TableCell>
                    <TableCell>{t('admin:logs.auth_table.upn', { defaultValue: '用户' })}</TableCell>
                    <TableCell>{t('admin:logs.auth_table.method', { defaultValue: '方法' })}</TableCell>
                    <TableCell>{t('admin:logs.auth_table.outcome', { defaultValue: '结果' })}</TableCell>
                    <TableCell>{t('admin:logs.auth_table.ip', { defaultValue: 'IP / 地区' })}</TableCell>
                    <TableCell>{t('admin:logs.auth_table.reason', { defaultValue: '原因' })}</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {authLoading && authItems.length === 0 && (
                    <TableRow><TableCell colSpan={6} sx={{ textAlign: 'center', py: 6 }}><CircularProgress size={24} /></TableCell></TableRow>
                  )}
                  {!authLoading && authItems.length === 0 && (
                    <TableRow><TableCell colSpan={6} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
                  )}
                  {authItems.map(r => (
                    <TableRow key={r.id} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}>
                      <TableCell sx={{ fontSize: 13, whiteSpace: 'nowrap' }}>{formatDualTz(r.at, panelTz)}</TableCell>
                      <TableCell sx={{ fontWeight: 500 }} title={r.ua || ''}>{r.upn || '—'}</TableCell>
                      <TableCell sx={{ fontSize: 13, textTransform: 'uppercase' }}>{r.method}</TableCell>
                      <TableCell>
                        <Box component="span" sx={{
                          fontSize: 12, fontWeight: 600, px: 1, py: 0.25, borderRadius: 1,
                          color: r.outcome === 'success' ? '#1b5e20' : '#b00020',
                          bgcolor: r.outcome === 'success' ? 'rgba(46,125,50,0.12)' : 'rgba(176,0,32,0.12)',
                        }}>
                          {r.outcome === 'success'
                            ? t('admin:logs.auth_outcome.success', { defaultValue: '成功' })
                            : t('admin:logs.auth_outcome.failure', { defaultValue: '失败' })}
                        </Box>
                      </TableCell>
                      <TableCell sx={{ fontSize: 13 }}>
                        {r.ip}
                        {formatRegion(r.region) && (
                          <Box sx={{ fontSize: 11, color: md.onSurfaceVariant, mt: 0.25 }}
                            title={t('logs.region_hint', { defaultValue: '地区由离线 IP 库估算；城市级仅供参考' })}>{formatRegion(r.region)}</Box>
                        )}
                      </TableCell>
                      <TableCell sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{r.reason || '—'}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableContainer>
            <PagedTableFooter
              total={authTotal} page={authPage} pageSize={authPageSize}
              onPageChange={setAuthPage} onPageSizeChange={setAuthPageSizePersist}
            />
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
                sx={{ width: 200 }}
                slotProps={{
                  htmlInput: { min: 0 }
                }}
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
            <PagedTableFooter
              total={emailTotal} page={emailPage} pageSize={emailPageSize}
              onPageChange={setEmailPage} onPageSizeChange={setEmailPageSizePersist}
            />
          </Card>
        </>
      )}
      {tab === 'certs' && <CertEventsTab />}
      {/* Email log detail */}
      <Dialog open={emailDetailOpen} onClose={() => setEmailDetailOpen(false)}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 600, maxWidth: '95vw' } }
        }}>
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
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 600, maxWidth: '95vw' } }
        }}>
        <DialogTitle>{t('admin:logs.view_detail')} #{subDetail?.id}</DialogTitle>
        <DialogContent>
          {subDetail && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, fontSize: 13 }}>
              <Row label="User" value={subDetail.user_upn || `#${subDetail.user_id}`} md={md} />
              <Row label="IP" value={subDetail.ip} mono md={md} />
              {formatRegion(subDetail.region) && <Row label="Region" value={formatRegion(subDetail.region)} md={md} />}
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
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 720, maxWidth: '95vw' } }
        }}>
        <DialogTitle>{t('admin:logs.view_detail')} #{auditDetail?.id}</DialogTitle>
        <DialogContent>
          {auditDetail && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, fontSize: 13 }}>
              <Row label="Actor" value={auditDetail.actor} md={md} />
              <Row label="Action" value={auditDetail.action} mono md={md} />
              <Row label="Target" value={auditDetail.target} md={md} />
              <Row label="IP" value={auditDetail.ip} mono md={md} />
              {formatRegion(auditDetail.region) && <Row label="Region" value={formatRegion(auditDetail.region)} md={md} />}
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
  );
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
