import { useEffect, useMemo, useState, type FormEvent, type MouseEvent } from 'react'
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
  InputAdornment,
  Menu,
  MenuItem,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import SearchIcon from '@mui/icons-material/Search'
import RefreshIcon from '@mui/icons-material/Refresh'
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import EditIcon from '@mui/icons-material/EditOutlined'
import VisibilityIcon from '@mui/icons-material/Visibility'
import VisibilityOffIcon from '@mui/icons-material/VisibilityOff'
import { useTranslation } from 'react-i18next'

import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import UpgradeIcon from '@mui/icons-material/Upgrade'

import {
  createServer,
  deleteServer,
  listServers,
  testServer,
  updateServer,
  upgradePanel,
  upgradeXray,
  type Server,
} from '@/api/servers'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import {
  type FieldErrors,
  firstError,
  validateName,
  validateRequired,
  validateUrl,
} from '@/utils/validators'

type ProbeStatus = 'unknown' | 'checking' | 'ok' | 'fail' | 'unconfigured'

interface ProbeState {
  status: ProbeStatus
  error?: string
  inbound_count?: number
}

interface FormState {
  name: string
  url: string
  api_token: string
  username: string
  password: string
  remark: string
  change_api_token: boolean
  change_password: boolean
  show_api_token: boolean
  show_password: boolean
}

const EMPTY_FORM: FormState = {
  name: '', url: '', api_token: '', username: '', password: '', remark: '',
  change_api_token: false, change_password: false,
  show_api_token: false, show_password: false,
}

function credentialsConfigured(s: Server): boolean {
  return s.has_api_token || s.has_password
}

export default function ServersView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])

  const [items, setItems] = useState<Server[]>([])
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(false)
  const [probeStates, setProbeStates] = useState<Record<number, ProbeState>>({})
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState<'test' | 'delete' | ''>('')
  const [singleTesting, setSingleTesting] = useState<number | null>(null)
  // Upgrade action state. menuAnchor + menuTarget power the kebab-menu
  // overlay (one global menu re-anchored per row). upgrading marks a panel
  // whose upgrade-panel / upgrade-xray request is in flight so we can
  // disable the corresponding menu item.
  const [menuAnchor, setMenuAnchor] = useState<HTMLElement | null>(null)
  const [menuTarget, setMenuTarget] = useState<Server | null>(null)
  const [upgrading, setUpgrading] = useState<number | null>(null)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<Server | null>(null)
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
  const [busy, setBusy] = useState(false)
  type ServerField = 'name' | 'url' | 'api_token' | 'password'
  const [fieldErr, setFieldErr] = useState<FieldErrors<ServerField>>({})

  // Free-text filter on name / URL. Small list, so a plain case-insensitive
  // substring match is enough. Remark is intentionally excluded — it's a
  // human-readable note, not an identifier worth searching on.
  const filteredItems = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return items
    return items.filter(s =>
      s.name.toLowerCase().includes(q) ||
      s.url.toLowerCase().includes(q),
    )
  }, [items, search])

  const selectedCount = selected.size
  // Header checkbox reflects the *visible* (filtered) rows so it can't claim
  // "all selected" while filtered-out rows sit unselected behind the search.
  const allChecked = filteredItems.length > 0 && filteredItems.every(s => selected.has(s.id))
  const someChecked = filteredItems.some(s => selected.has(s.id)) && !allChecked

  useEffect(() => { void load() }, [])

  async function load() {
    setLoading(true)
    try {
      const list = await listServers()
      setItems(list)
      setSelected(new Set())
      // Fire-and-forget probe of all items.
      void Promise.allSettled(list.map(s => probeServer(s)))
    } finally {
      setLoading(false)
    }
  }

  function stateFor(s: Server): ProbeState {
    return probeStates[s.id] ?? { status: credentialsConfigured(s) ? 'unknown' : 'unconfigured' }
  }

  async function probeServer(s: Server, notify = false) {
    if (!credentialsConfigured(s)) {
      setProbeStates(p => ({ ...p, [s.id]: { status: 'unconfigured' } }))
      if (notify) pushSnack(t('admin:servers.validate.credentials_required'), 'warning')
      return
    }
    setProbeStates(p => ({ ...p, [s.id]: { status: 'checking' } }))
    try {
      const r = await testServer(s.id)
      if (r.ok) {
        setProbeStates(p => ({ ...p, [s.id]: { status: 'ok', inbound_count: r.inbound_count } }))
        // Test piggybacks a version probe (v3.6.0-beta.2). Merge any returned
        // version fields back into the row so the Version column and the
        // top-of-page compat banner reflect the fresh probe without a
        // full list reload.
        if (r.panel_version !== undefined || r.compat_status) {
          setItems(prev => prev.map(it => it.id === s.id ? {
            ...it,
            panel_version: r.panel_version ?? it.panel_version,
            xray_version: r.xray_version ?? it.xray_version,
            version_checked_at: r.version_checked_at ?? it.version_checked_at,
            compat_status: r.compat_status ?? it.compat_status,
            compat_message: r.compat_message ?? it.compat_message,
          } : it))
        }
        if (notify) pushSnack(t('admin:servers.toast.test_ok', { count: r.inbound_count ?? 0 }), 'success')
      } else {
        setProbeStates(p => ({ ...p, [s.id]: { status: 'fail', error: r.error ?? 'unknown' } }))
        if (notify) pushSnack(r.error ?? 'unknown', 'error')
      }
    } catch (err) {
      const msg = (err as { response?: { data?: { error?: string } }; message?: string }).response?.data?.error
        ?? (err as { message?: string }).message
        ?? 'unknown'
      setProbeStates(p => ({ ...p, [s.id]: { status: 'fail', error: msg } }))
      // axios interceptor already toasts non-401 errors
    }
  }

  async function runTest(s: Server) {
    setSingleTesting(s.id)
    try { await probeServer(s, true) }
    finally { setSingleTesting(null) }
  }

  function openMenu(e: MouseEvent<HTMLElement>, s: Server) {
    setMenuAnchor(e.currentTarget)
    setMenuTarget(s)
  }

  function closeMenu() {
    setMenuAnchor(null)
    setMenuTarget(null)
  }

  async function runUpgradePanel(s: Server, force = false) {
    if (!force) {
      closeMenu()
      const ok = await confirm({
        title: t('admin:servers.confirm.upgrade_panel_title', { defaultValue: '升级 3X-UI 面板' }),
        message: t('admin:servers.confirm.upgrade_panel_message', {
          name: s.name,
          defaultValue: 'PSP 将先检查目标版本是否在已测试范围内,在范围内才会触发 {{name}} 的自升级。面板会重启,约 60 秒后 PSP 跑 smoke probe 验证。是否继续?',
        }),
        confirmText: t('admin:servers.action.upgrade', { defaultValue: '升级' }),
      })
      if (!ok) return
    }
    setUpgrading(s.id)
    try {
      const r = await upgradePanel(s.id, { force })
      pushSnack(
        t('admin:servers.toast.upgrade_panel_started', {
          target: r.target_version ?? '?',
          defaultValue: '已发起 3X-UI 升级到 {{target}},约 60 秒后 PSP 跑 smoke probe,结果写入 audit log',
        }),
        'success',
      )
    } catch (err) {
      const resp = (err as { response?: { status?: number; data?: {
        reason?: string
        latest_version?: string
        compat_status?: string
        psp_max_xui?: string
        can_force?: boolean
        error?: string
        message?: string
      } } }).response
      const body = resp?.data
      // 409 + reason:"untested_target" → admin can override. Pop a
      // second confirmation that spells out the risk before resending
      // with force=true. The structure matches PSP's two-step gate:
      // the first call gives admin a chance to see *why* it was
      // blocked; the second is the explicit "I accept the risk".
      if (resp?.status === 409 && body?.reason === 'untested_target' && body?.can_force && !force) {
        const proceed = await confirm({
          title: t('admin:servers.confirm.upgrade_force_title', { defaultValue: '目标版本超出已测试范围' }),
          message: t('admin:servers.confirm.upgrade_force_message', {
            latest: body.latest_version ?? '?',
            max: body.psp_max_xui || t('admin:servers.compat.not_loaded', { defaultValue: '未加载' }),
            status: body.compat_status ?? 'unknown',
            defaultValue: '目标版本 {{latest}} 状态为 {{status}}（PSP 当前测试最高 {{max}}）。强制升级可能因 schema 变更导致 PSP traffic poll 失败 —— PSP v3.5.1 修复的 v3.1.0 break 就是这类问题。确认强制升级吗?',
          }),
          destructive: true,
          confirmText: t('admin:servers.action.force_upgrade', { defaultValue: '强制升级' }),
        })
        if (proceed) {
          // Resend with force=true — recursive call lets the success
          // / error branches stay shared instead of duplicating them.
          return runUpgradePanel(s, true)
        }
        // admin declined → nothing further
        return
      }
      // Other errors (502 panel unreachable, etc.) — generic toast.
      const msg = body?.error ?? body?.message ?? (err as { message?: string }).message ?? 'unknown'
      pushSnack(msg, 'error')
    } finally {
      setUpgrading(null)
    }
  }

  async function runUpgradeXray(s: Server) {
    closeMenu()
    const ok = await confirm({
      title: t('admin:servers.confirm.upgrade_xray_title', { defaultValue: '升级 Xray (最新版)' }),
      message: t('admin:servers.confirm.upgrade_xray_message', {
        name: s.name,
        defaultValue: '在 {{name}} 上安装最新版 xray-core 二进制。面板自身不重启,只重启 xray 子进程。是否继续?',
      }),
      confirmText: t('admin:servers.action.upgrade', { defaultValue: '升级' }),
    })
    if (!ok) return
    setUpgrading(s.id)
    try {
      const r = await upgradeXray(s.id)
      pushSnack(
        t('admin:servers.toast.upgrade_xray_ok', {
          version: r.version ?? 'latest',
          defaultValue: 'Xray 已升级到 {{version}}',
        }),
        'success',
      )
      // The backend already refreshed UpdateVersion server-side, so
      // pull the row again so the Version column reflects new xray ver.
      void probeServer(s)
    } catch (err) {
      const msg = (err as { response?: { data?: { error?: string } }; message?: string }).response?.data?.error
        ?? (err as { message?: string }).message
        ?? 'unknown'
      pushSnack(msg, 'error')
    } finally {
      setUpgrading(null)
    }
  }

  function openCreate() {
    setEditing(null)
    setForm({ ...EMPTY_FORM, change_api_token: true, change_password: true })
    setFieldErr({})
    setDialogOpen(true)
  }

  function openEdit(s: Server) {
    setEditing(s)
    setForm({
      ...EMPTY_FORM,
      name: s.name,
      url: s.url,
      username: s.username ?? '',
      remark: s.remark ?? '',
    })
    setFieldErr({})
    setDialogOpen(true)
  }

  function validateForm(f: FormState, isEdit: boolean): FieldErrors<ServerField> {
    return {
      name: validateName(f.name, { required: true, max: 64 }),
      url: validateUrl(f.url, { required: true }),
      // On create, at least one credential is required (server enforces this
      // too — the panel won't probe without a token or login). On edit, both
      // fields are optional unless the admin explicitly toggled "change".
      api_token: !isEdit && !f.api_token && !f.password
        ? validateRequired('', 'validation.required')
        : '',
      password: !isEdit && !f.api_token && !f.password
        ? validateRequired('', 'validation.required')
        : '',
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    const errs = validateForm(form, !!editing)
    setFieldErr(errs)
    const firstKey = firstError(errs)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return }
    setBusy(true)
    try {
      if (editing) {
        const req: Record<string, string> = {
          url: form.url,
          name: form.name,
          username: form.username,
          remark: form.remark,
        }
        if (form.change_api_token) req.api_token = form.api_token
        if (form.change_password) req.password = form.password
        await updateServer(editing.id, req)
        pushSnack(t('admin:servers.toast.saved'), 'success')
      } else {
        await createServer({
          name: form.name, url: form.url,
          api_token: form.api_token || undefined,
          username: form.username || undefined,
          password: form.password || undefined,
          remark: form.remark || undefined,
        })
        pushSnack(t('admin:servers.toast.created'), 'success')
      }
      setDialogOpen(false)
      await load()
    } finally {
      setBusy(false)
    }
  }

  async function confirmDelete(s: Server) {
    const ok = await confirm({
      title: t('admin:servers.confirm.delete_title'),
      message: t('admin:servers.confirm.delete_message', { name: s.name }),
      destructive: true,
      confirmText: t('admin:servers.action.delete'),
    })
    if (!ok) return
    await deleteServer(s.id)
    pushSnack(t('admin:servers.toast.deleted'), 'success')
    await load()
  }

  async function batchRunTest() {
    const rows = items.filter(s => selected.has(s.id))
    if (!rows.length) return
    setBatchBusy('test')
    try {
      await Promise.allSettled(rows.map(s => probeServer(s)))
      pushSnack(t('admin:servers.toast.batch_tested', { count: rows.length }), 'success')
    } finally {
      setBatchBusy('')
    }
  }

  // runTestAll ignores selection and probes every server. Distinguished
  // from batchRunTest so admins don't have to "select all" first when they
  // just want a global sanity check.
  async function runTestAll() {
    if (!items.length) return
    setBatchBusy('test')
    try {
      await Promise.allSettled(items.map(s => probeServer(s)))
      pushSnack(t('admin:servers.toast.batch_tested', { count: items.length }), 'success')
    } finally {
      setBatchBusy('')
    }
  }

  async function batchDeleteServers() {
    const rows = items.filter(s => selected.has(s.id))
    if (!rows.length) return
    const names = rows.slice(0, 5).map(r => r.name).join('、')
    const suffix = rows.length > 5 ? ` +${rows.length - 5}` : ''
    const ok = await confirm({
      title: t('admin:servers.confirm.batch_delete_title'),
      message: t('admin:servers.confirm.batch_delete_message', { names, suffix }),
      destructive: true,
      confirmText: t('admin:servers.action.delete'),
    })
    if (!ok) return
    setBatchBusy('delete')
    try {
      const results = await Promise.allSettled(rows.map(r => deleteServer(r.id)))
      const okIds = rows.filter((_, i) => results[i].status === 'fulfilled').map(r => r.id)
      const failed = rows.length - okIds.length
      setItems(prev => prev.filter(s => !okIds.includes(s.id)))
      setSelected(new Set())
      if (failed > 0) {
        pushSnack(t('admin:servers.toast.batch_partial', { ok: okIds.length, fail: failed }), 'warning')
      } else {
        pushSnack(t('admin:servers.toast.batch_deleted', { count: okIds.length }), 'success')
      }
    } finally {
      setBatchBusy('')
    }
  }

  function toggleAll(checked: boolean) {
    // Only flip the currently-visible rows; preserve selection of rows
    // hidden by the active search filter.
    setSelected(prev => {
      const next = new Set(prev)
      filteredItems.forEach(s => { if (checked) next.add(s.id); else next.delete(s.id) })
      return next
    })
  }

  function toggleOne(id: number, checked: boolean) {
    setSelected(prev => {
      const next = new Set(prev)
      if (checked) next.add(id); else next.delete(id)
      return next
    })
  }

  // versionCell renders the 3X-UI + Xray version pair plus a compat badge
  // when the panel's reported version falls outside PSP's supported range.
  // Empty state ("never probed") shows an em-dash so it's visually distinct
  // from "probed and ok". Compat colors mirror Material's container roles
  // for consistency with statusBadge.
  function versionCell(s: Server) {
    if (!s.panel_version) {
      return <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>—</Typography>
    }
    let bg = md.tertiaryContainer
    let fg = md.onTertiaryContainer
    let label: string | null = null
    switch (s.compat_status) {
      case 'supported':
        // No badge — clean state. The version text alone suffices.
        break
      case 'too_old':
        bg = md.errorContainer
        fg = md.onErrorContainer
        label = t('admin:servers.compat.too_old', { defaultValue: '版本过低' })
        break
      case 'untested':
        bg = md.secondaryContainer
        fg = md.onSecondaryContainer
        label = t('admin:servers.compat.untested', { defaultValue: '未测试' })
        break
      case 'unknown':
      default:
        bg = md.surfaceContainerHighest
        fg = md.onSurfaceVariant
        label = t('admin:servers.compat.unknown', { defaultValue: '无法识别' })
    }
    const versionText = (
      <Box sx={{ display: 'flex', flexDirection: 'column', lineHeight: 1.3 }}>
        <Typography sx={{ fontSize: 13, fontWeight: 500 }}>3X-UI {s.panel_version}</Typography>
        {s.xray_version && (
          <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
            Xray {s.xray_version}
          </Typography>
        )}
      </Box>
    )
    const badge = label && (
      <Box sx={{
        display: 'inline-block', px: 1, py: 0.125,
        borderRadius: 1, fontSize: 11, fontWeight: 500,
        bgcolor: bg, color: fg, whiteSpace: 'nowrap', mt: 0.25,
      }}>
        {label}
      </Box>
    )
    const stacked = (
      <Box>
        {versionText}
        {badge}
      </Box>
    )
    if (s.compat_message) {
      return <Tooltip title={s.compat_message} placement="top"><span>{stacked}</span></Tooltip>
    }
    return stacked
  }

  // incompatibleServers powers the top banner. We surface only panels with
  // a definitive non-supported status — "unknown" stays out of the banner
  // because it usually means "never probed" or "probe failed transiently",
  // not a real compat issue worth a red banner.
  const incompatibleServers = useMemo(
    () => items.filter(s => s.compat_status === 'too_old' || s.compat_status === 'untested'),
    [items],
  )

  function statusBadge(s: Server) {
    const st = stateFor(s)
    let label: string
    let bg: string, fg: string
    switch (st.status) {
      case 'checking':
        label = t('admin:servers.status.checking')
        bg = md.secondaryContainer; fg = md.onSecondaryContainer
        break
      case 'ok':
        label = typeof st.inbound_count === 'number'
          ? t('admin:servers.status.ok_count', { count: st.inbound_count })
          : t('admin:servers.status.ok')
        bg = md.tertiaryContainer; fg = md.onTertiaryContainer
        break
      case 'fail':
        label = t('admin:servers.status.fail')
        bg = md.errorContainer; fg = md.onErrorContainer
        break
      case 'unconfigured':
        label = t('admin:servers.status.unconfigured')
        bg = md.surfaceContainerHighest; fg = md.onSurfaceVariant
        break
      default:
        label = t('admin:servers.status.unknown')
        bg = md.surfaceContainerHighest; fg = md.onSurfaceVariant
    }
    const chip = (
      <Box sx={{
        display: 'inline-block', px: 1.25, py: 0.25,
        borderRadius: 1, fontSize: 12, fontWeight: 500,
        bgcolor: bg, color: fg,
        // whiteSpace: nowrap stops the chip wrapping "Connected" / "(N)"
        // onto two lines when the column is narrow. The column itself
        // sets nowrap below so the chip width drives the cell width
        // rather than the other way around.
        whiteSpace: 'nowrap',
      }}>
        {label}
      </Box>
    )
    if (st.error) return <Tooltip title={st.error}><span>{chip}</span></Tooltip>
    return chip
  }

  return (
    <Box sx={{ p: 3 }}>
      {/* Header */}
      <Box sx={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', flexWrap: 'wrap', gap: 2, mb: 1 }}>
        <Box>
          <Typography variant="h4">{t('admin:servers.title')}</Typography>
          <Typography variant="body2" sx={{ mt: 0.5 }}>{t('admin:servers.subtitle')}</Typography>
        </Box>
        <Box sx={{ display: 'flex', gap: 1 }}>
          {/* "Test all" runs the connectivity probe across every server
              without requiring row selection — quick sanity check after a
              network change or panel update. */}
          <Button variant="outlined"
            startIcon={batchBusy === 'test' ? <CircularProgress size={14} /> : <RefreshIcon />}
            disabled={batchBusy !== '' || items.length === 0}
            onClick={runTestAll}>
            {t('admin:servers.test_all', { defaultValue: '测试全部' })}
          </Button>
          <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
            {t('admin:servers.create')}
          </Button>
        </Box>
      </Box>

      {/* Compat warning banner — only shown when at least one server's
          3X-UI version falls outside the supported range. "Unknown" panels
          (never probed / probe failing) deliberately stay out of this
          banner since they usually aren't a real compat issue. */}
      {incompatibleServers.length > 0 && (
        <Box sx={{
          mt: 2, p: 1.75, borderRadius: 2,
          bgcolor: md.errorContainer, color: md.onErrorContainer,
          display: 'flex', gap: 1.5, alignItems: 'flex-start',
        }}>
          <WarningAmberIcon fontSize="small" sx={{ mt: 0.25 }} />
          <Box>
            <Typography sx={{ fontSize: 13, fontWeight: 600 }}>
              {t('admin:servers.compat.banner_title', {
                count: incompatibleServers.length,
                defaultValue: '{{count}} 台服务器的 3X-UI 版本超出 PSP 兼容范围',
              })}
            </Typography>
            <Typography sx={{ fontSize: 12, mt: 0.25 }}>
              {incompatibleServers
                .map(s => `${s.name} (${s.panel_version ?? '?'}, ${s.compat_status ?? 'unknown'})`)
                .join('; ')}
            </Typography>
          </Box>
        </Box>
      )}

      {/* Selection toolbar */}
      {selectedCount > 0 && (
        <Box sx={{
          display: 'flex', alignItems: 'center', gap: 1, mt: 2, mb: 1,
          px: 2, py: 1, borderRadius: 9999,
          bgcolor: md.secondaryContainer, color: md.onSecondaryContainer,
          width: 'fit-content',
        }}>
          <Typography sx={{ fontSize: 13, fontWeight: 500, mr: 1 }}>
            {t('admin:servers.selection_count', { count: selectedCount })}
          </Typography>
          <Button
            size="small" variant="text"
            startIcon={batchBusy === 'test' ? <CircularProgress size={14} /> : <RefreshIcon />}
            disabled={batchBusy !== ''}
            onClick={batchRunTest}
            sx={{ color: 'inherit' }}
          >
            {t('admin:servers.batch_test')}
          </Button>
          <Button
            size="small" variant="text" color="error"
            startIcon={batchBusy === 'delete' ? <CircularProgress size={14} /> : <DeleteIcon />}
            disabled={batchBusy !== ''}
            onClick={batchDeleteServers}
          >
            {t('admin:servers.batch_delete')}
          </Button>
        </Box>
      )}

      {/* Search */}
      <Box sx={{ mt: 2 }}>
        <TextField
          size="small"
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder={t('admin:servers.search_placeholder', { defaultValue: '搜索名称 / URL' })}
          sx={{ width: 320, maxWidth: '100%' }}
          InputProps={{
            startAdornment: (
              <InputAdornment position="start">
                <SearchIcon fontSize="small" sx={{ color: md.onSurfaceVariant }} />
              </InputAdornment>
            ),
          }}
        />
      </Box>

      {/* Table */}
      <Card sx={{ mt: 2, bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
        <TableContainer>
          <Table>
            <TableHead>
              <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}` } }}>
                <TableCell padding="checkbox">
                  <Checkbox
                    indeterminate={someChecked}
                    checked={allChecked}
                    onChange={(_, c) => toggleAll(c)}
                  />
                </TableCell>
                <TableCell>{t('admin:servers.table.name')}</TableCell>
                <TableCell>{t('admin:servers.table.url')}</TableCell>
                <TableCell>{t('admin:servers.table.status')}</TableCell>
                <TableCell>{t('admin:servers.table.version', { defaultValue: '版本' })}</TableCell>
                <TableCell>{t('admin:servers.table.remark')}</TableCell>
                <TableCell align="right">{t('admin:servers.table.actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {loading && items.length === 0 && (
                <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6 }}>
                  <CircularProgress size={24} />
                </TableCell></TableRow>
              )}
              {!loading && items.length === 0 && (
                <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>
                  —
                </TableCell></TableRow>
              )}
              {!loading && items.length > 0 && filteredItems.length === 0 && (
                <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>
                  {t('admin:servers.no_match', { defaultValue: '没有匹配的服务器' })}
                </TableCell></TableRow>
              )}
              {filteredItems.map(s => (
                <TableRow
                  key={s.id}
                  hover
                  sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}
                >
                  <TableCell padding="checkbox">
                    <Checkbox
                      checked={selected.has(s.id)}
                      onChange={(_, c) => toggleOne(s.id, c)}
                    />
                  </TableCell>
                  <TableCell sx={{ fontWeight: 500, whiteSpace: 'nowrap' }}>{s.name}</TableCell>
                  {/* URL is the admin's reference, not subscription-critical
                      — clip aggressively so the table stays scannable and
                      keeps space for status / actions. Full URL shows in a
                      tooltip on hover for verification. */}
                  <TableCell sx={{
                    fontSize: 13, color: md.onSurfaceVariant,
                    maxWidth: 240, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                  }}>
                    <Tooltip title={s.url} placement="top">
                      <span>{s.url}</span>
                    </Tooltip>
                  </TableCell>
                  <TableCell sx={{ whiteSpace: 'nowrap' }}>{statusBadge(s)}</TableCell>
                  <TableCell sx={{ whiteSpace: 'nowrap' }}>{versionCell(s)}</TableCell>
                  <TableCell sx={{ color: md.onSurfaceVariant, fontSize: 13 }}>{s.remark || '—'}</TableCell>
                  <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                    <Button
                      size="small" variant="text"
                      startIcon={singleTesting === s.id ? <CircularProgress size={14} /> : <RefreshIcon />}
                      disabled={singleTesting !== null}
                      onClick={() => runTest(s)}
                    >
                      {t('admin:servers.action.test')}
                    </Button>
                    <IconButton size="small" onClick={() => openEdit(s)} aria-label={t('admin:servers.action.edit')}>
                      <EditIcon fontSize="small" />
                    </IconButton>
                    <IconButton size="small" onClick={() => confirmDelete(s)} aria-label={t('admin:servers.action.delete')} sx={{ color: md.error }}>
                      <DeleteIcon fontSize="small" />
                    </IconButton>
                    {/* Kebab menu hosts the destructive remote-upgrade
                        actions — kept out of the always-visible row
                        actions so an accidental click can't fire
                        something that restarts the remote panel. */}
                    <IconButton
                      size="small"
                      onClick={e => openMenu(e, s)}
                      disabled={upgrading === s.id}
                      aria-label={t('admin:servers.action.more', { defaultValue: '更多操作' })}
                    >
                      {upgrading === s.id ? <CircularProgress size={14} /> : <MoreVertIcon fontSize="small" />}
                    </IconButton>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      </Card>

      {/* Per-row kebab menu — hosts destructive remote-upgrade actions
          (panel + xray) so a stray click on the always-visible row
          buttons can't trigger a 3X-UI restart. Re-anchored per row,
          one global Menu component. */}
      <Menu
        anchorEl={menuAnchor}
        open={!!menuAnchor && !!menuTarget}
        onClose={closeMenu}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
      >
        <MenuItem onClick={() => menuTarget && runUpgradePanel(menuTarget)}>
          <UpgradeIcon fontSize="small" sx={{ mr: 1 }} />
          {t('admin:servers.action.upgrade_panel', { defaultValue: '升级 3X-UI 面板' })}
        </MenuItem>
        <MenuItem onClick={() => menuTarget && runUpgradeXray(menuTarget)}>
          <UpgradeIcon fontSize="small" sx={{ mr: 1 }} />
          {t('admin:servers.action.upgrade_xray', { defaultValue: '升级 Xray (最新)' })}
        </MenuItem>
      </Menu>

      {/* Create/Edit dialog */}
      <Dialog
        open={dialogOpen}
        onClose={() => !busy && setDialogOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }}
      >
        <DialogTitle>
          {editing ? t('admin:servers.edit_title', { name: editing.name }) : t('admin:servers.create')}
        </DialogTitle>
        <DialogContent>
          <Box component="form" id="server-form" onSubmit={submit} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            <Box>
              <TextField
                fullWidth required
                label={t('admin:servers.field.name')}
                placeholder={t('admin:servers.placeholder.name')}
                value={form.name}
                onChange={e => setForm({ ...form, name: e.target.value })}
                error={!!fieldErr.name}
                helperText={fieldErr.name ? t(`admin:${fieldErr.name}`) : t('admin:servers.hint.name')}
              />
            </Box>
            <TextField
              fullWidth required
              label={t('admin:servers.field.url')}
              placeholder={t('admin:servers.placeholder.url')}
              value={form.url}
              onChange={e => setForm({ ...form, url: e.target.value })}
              error={!!fieldErr.url}
              helperText={fieldErr.url ? t(`admin:${fieldErr.url}`) : ''}
              sx={{ '& input': { fontSize: 14 } }}
            />

            {/* API Token: in edit mode, default to "kept unchanged" with a Change link */}
            <SecretField
              label={t('admin:servers.field.api_token')}
              placeholder={t('admin:servers.placeholder.api_token')}
              value={form.api_token}
              show={form.show_api_token}
              onShow={v => setForm({ ...form, show_api_token: v })}
              onChange={v => setForm({ ...form, api_token: v })}
              edit={!!editing}
              changing={form.change_api_token}
              alreadyConfigured={!!editing?.has_api_token}
              onStartChange={() => setForm({ ...form, change_api_token: true })}
            />

            <TextField
              fullWidth
              label={t('admin:servers.field.username')}
              placeholder={t('admin:servers.placeholder.username')}
              value={form.username}
              onChange={e => setForm({ ...form, username: e.target.value })}
            />

            <SecretField
              label={t('admin:servers.field.password')}
              placeholder={t('admin:servers.placeholder.password')}
              value={form.password}
              show={form.show_password}
              onShow={v => setForm({ ...form, show_password: v })}
              onChange={v => setForm({ ...form, password: v })}
              edit={!!editing}
              changing={form.change_password}
              alreadyConfigured={!!editing?.has_password}
              onStartChange={() => setForm({ ...form, change_password: true })}
            />

            <TextField
              fullWidth
              label={t('admin:servers.field.remark')}
              value={form.remark}
              onChange={e => setForm({ ...form, remark: e.target.value })}
            />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setDialogOpen(false)} disabled={busy} variant="text">
            {t('common:actions.cancel')}
          </Button>
          <Button
            type="submit" form="server-form"
            variant="contained" disabled={busy}
            startIcon={busy ? <CircularProgress size={16} color="inherit" /> : null}
          >
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}

interface SecretFieldProps {
  label: string
  placeholder: string
  value: string
  show: boolean
  onShow: (v: boolean) => void
  onChange: (v: string) => void
  edit: boolean
  changing: boolean
  alreadyConfigured: boolean
  onStartChange: () => void
}

function SecretField(p: SecretFieldProps) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('admin')

  if (p.edit && !p.changing) {
    return (
      <Box>
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>{p.label}</Typography>
        <Box sx={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          gap: 1.5, height: 56, px: 1.75,
          borderRadius: 1.5, border: `1px solid ${md.outlineVariant}`,
        }}>
          <Typography variant="body2">
            {p.alreadyConfigured ? t('servers.hint.configured') : t('servers.hint.unconfigured')}
          </Typography>
          <Button size="small" variant="text" onClick={p.onStartChange}>
            {t('servers.hint.change')}
          </Button>
        </Box>
      </Box>
    )
  }
  return (
    <TextField
      fullWidth
      type={p.show ? 'text' : 'password'}
      label={p.label}
      placeholder={p.placeholder}
      value={p.value}
      onChange={e => p.onChange(e.target.value)}
      InputProps={{
        endAdornment: (
          <InputAdornment position="end">
            <IconButton size="small" onClick={() => p.onShow(!p.show)} aria-label="toggle visibility">
              {p.show ? <VisibilityOffIcon fontSize="small" /> : <VisibilityIcon fontSize="small" />}
            </IconButton>
          </InputAdornment>
        ),
      }}
    />
  )
}
