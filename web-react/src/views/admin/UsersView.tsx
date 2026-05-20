import { useEffect, useMemo, useState, type FormEvent, type MouseEvent } from 'react'
import {
  Autocomplete,
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
  InputBase,
  ListItemIcon,
  ListItemText,
  Menu,
  MenuItem,
  Pagination,
  Popover,
  Select,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
  alpha,
  useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import RefreshIcon from '@mui/icons-material/Refresh'
import SearchIcon from '@mui/icons-material/Search'
import EditIcon from '@mui/icons-material/EditOutlined'
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import ToggleOnIcon from '@mui/icons-material/ToggleOn'
import ToggleOffIcon from '@mui/icons-material/ToggleOff'
import RestartAltIcon from '@mui/icons-material/RestartAlt'
import VisibilityIcon from '@mui/icons-material/Visibility'
import VisibilityOffIcon from '@mui/icons-material/VisibilityOff'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import KeyIcon from '@mui/icons-material/VpnKey'
import LockResetIcon from '@mui/icons-material/LockReset'
import CasinoIcon from '@mui/icons-material/Casino'
import RuleIcon from '@mui/icons-material/Rule'
import EmergencyIcon from '@mui/icons-material/MedicalServices'
import LinkOffIcon from '@mui/icons-material/LinkOff'
import SyncIcon from '@mui/icons-material/Sync'
import HelpOutlineIcon from '@mui/icons-material/HelpOutline'
import { useTranslation } from 'react-i18next'

import {
  createUser,
  deleteUser,
  getUserRules,
  listUsers,
  resetCredentials,
  resetEmergencyUsage,
  resetPassword,
  setEnabled,
  unlinkSSO,
  updateUser,
  updateUserRules,
} from '@/api/users'
import type { UpdateUserRequest } from '@/api/users'
import { listGroups } from '@/api/groups'
import { runReconcile } from '@/api/reconcile'
import { setUserTraffic, topTraffic, type TrafficRow } from '@/api/traffic'
import type { Group, ResetPeriod, Role, User } from '@/api/types'
import type { ReconcileReport } from '@/api/reconcile'
import { useAuthStore } from '@/stores/auth'
import { useCan } from '@/utils/permissions'
import { useSiteStore } from '@/stores/site'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import {
  type FieldErrors,
  firstError,
  validateEmail,
  validateGroupId,
  validateName,
  validateNonNegativeNumber,
  validatePassword,
  validateRequired,
} from '@/utils/validators'

const PAGE_SIZE = 50

interface CreateForm {
  upn: string
  email: string
  display_name: string
  password: string
  group_id: number | ''
  // Mirrors the edit dialog: a date is interpreted as end-of-day in the panel
  // timezone server-side (sent as expire_date), so create and edit anchor the
  // same way. 'permanent' sends no expiry at all.
  expire_mode: 'date' | 'permanent'
  expire_date: string
  traffic_limit_gb: number
  traffic_reset_period: ResetPeriod
  remark: string
  show_password: boolean
}

interface EditForm {
  display_name: string
  email: string
  group_id: number | ''
  role: Role
  expire_mode: 'date' | 'permanent'
  expire_at: string
  traffic_limit_gb: number
  traffic_reset_period: ResetPeriod
  remark: string
  // Period-used edit: initialised from the latest poll snapshot. If the
  // admin actually moves it (epsilon check), submitEdit calls setUserTraffic
  // to push a synthetic snapshot baseline so the next poll's delta starts
  // from this value.
  period_used_gb: number
  period_used_initial: number
  emergency_used_count: number
}

const EMPTY_CREATE: CreateForm = {
  upn: '', email: '', display_name: '', password: '',
  group_id: '', expire_mode: 'date', expire_date: '', traffic_limit_gb: 0,
  traffic_reset_period: 'monthly', remark: '', show_password: false,
}

// dateInNDays formats "n days from now" as a local YYYY-MM-DD string for the
// create dialog's default. The exact day is resolved against the panel
// timezone server-side; this is just the picker's pre-fill.
function dateInNDays(n: number): string {
  const d = new Date(Date.now() + n * 86400000)
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${d.getFullYear()}-${m}-${day}`
}

const EMPTY_EDIT: EditForm = {
  display_name: '', email: '', group_id: '', role: 'user',
  expire_mode: 'date', expire_at: '', traffic_limit_gb: 0,
  traffic_reset_period: 'monthly', remark: '',
  period_used_gb: 0, period_used_initial: 0, emergency_used_count: 0,
}

function bytesToGB(b: number) { return Math.round((b / 1024 / 1024 / 1024) * 100) / 100 }
function isExpired(u: User) { return !!u.expire_at && new Date(u.expire_at).getTime() < Date.now() }
function canQuickRenew(u: User) { return !!u.expire_at && u.auto_disabled_reason !== 'pending_delete' }
function canSelect(u: User) { return u.auto_disabled_reason !== 'pending_delete' }
function renewedExpireAt(u: User, days: number) {
  const now = Date.now()
  const current = u.expire_at ? new Date(u.expire_at).getTime() : 0
  const base = Number.isFinite(current) && current > now ? current : now
  return new Date(base + days * 86400000).toISOString()
}

type BatchKind = 'enable' | 'disable' | 'renew' | 'delete' | 'emergency' | 'unlink_sso' | ''

export default function UsersView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  // Expiry dates are set/shown in the panel timezone. Surface a hint only
  // when the admin's browser is in a different zone, so a picked "5/30"
  // isn't silently read as the browser's 5/30.
  const panelTz = useSiteStore(s => s.timezone)
  const browserTz = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone } catch { return '' } })()
  const expireTzDiffers = !!panelTz && panelTz !== browserTz
  const auth = useAuthStore()
  // Elevated user actions (act on admin/operator accounts) are admin-only;
  // operators may only manage role=user targets. canManageUser mirrors the
  // backend's ensureOperatorAllowed guard so we don't show buttons that 403.
  const canElevate = useCan('users.elevate')
  const canManageUser = (target: User) => canElevate || target.role === 'user'

  const [items, setItems] = useState<User[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [search, setSearch] = useState('')
  const [groupFilter, setGroupFilter] = useState<number | ''>('')
  const [groups, setGroups] = useState<Group[]>([])
  const [loading, setLoading] = useState(false)
  const [reconcileBusy, setReconcileBusy] = useState(false)

  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState<BatchKind>('')

  // Dialogs
  const [createOpen, setCreateOpen] = useState(false)
  const [createBusy, setCreateBusy] = useState(false)
  const [createForm, setCreateForm] = useState<CreateForm>(EMPTY_CREATE)
  type CreateField = 'upn' | 'email' | 'display_name' | 'password' | 'group_id' | 'expire_date' | 'traffic_limit_gb'
  const [createErr, setCreateErr] = useState<FieldErrors<CreateField>>({})

  const [resultOpen, setResultOpen] = useState(false)
  const [resultUser, setResultUser] = useState<User | null>(null)
  const [resultPassword, setResultPassword] = useState('')

  const [editOpen, setEditOpen] = useState(false)
  const [editBusy, setEditBusy] = useState(false)
  const [editing, setEditing] = useState<User | null>(null)
  const [editForm, setEditForm] = useState<EditForm>(EMPTY_EDIT)
  type EditField = 'display_name' | 'email' | 'group_id' | 'expire_at' | 'traffic_limit_gb' | 'period_used_gb'
  const [editErr, setEditErr] = useState<FieldErrors<EditField>>({})

  const [reasonOpen, setReasonOpen] = useState(false)
  const [reasonUser, setReasonUser] = useState<User | null>(null)
  const [reasonText, setReasonText] = useState('')
  const [reasonBatch, setReasonBatch] = useState<{ enable: boolean } | null>(null)
  // Reset-password dialog. Admin can either type a password or click the
  // dice button to fill a server-style random one without hitting submit
  // yet — that way they can copy it from the field before confirming.
  const [pwdResetOpen, setPwdResetOpen] = useState(false)
  const [pwdResetUser, setPwdResetUser] = useState<User | null>(null)
  const [pwdResetValue, setPwdResetValue] = useState('')
  const [pwdResetShow, setPwdResetShow] = useState(false)
  const [pwdResetBusy, setPwdResetBusy] = useState(false)
  const [pwdResetError, setPwdResetError] = useState('')
  // Anchor for the "?" popover next to the role field. Toggled by the
  // help icon button so admins don't have to guess what each role can
  // actually do.
  const [roleHelpAnchor, setRoleHelpAnchor] = useState<HTMLElement | null>(null)

  const [rulesOpen, setRulesOpen] = useState(false)
  const [rulesUser, setRulesUser] = useState<User | null>(null)
  const [rulesText, setRulesText] = useState('')
  const [rulesSaved, setRulesSaved] = useState('')
  const [rulesBusy, setRulesBusy] = useState(false)

  const [reconcileOpen, setReconcileOpen] = useState(false)
  const [reconcileReport, setReconcileReport] = useState<ReconcileReport | null>(null)
  // Collapsed by default — the summary "Scanned N, fixed M" sits at the
  // top of the result dialog and admin can expand the per-issue list via
  // a "show details" button left of OK. Mirrors the spec from v2.2.5:
  // detail toggle lives in DialogActions, not the body.
  const [reconcileDetailsOpen, setReconcileDetailsOpen] = useState(false)

  // Per-row More menu
  const [moreAnchor, setMoreAnchor] = useState<HTMLElement | null>(null)
  const [moreUser, setMoreUser] = useState<User | null>(null)
  // Batch More menu
  const [batchMoreAnchor, setBatchMoreAnchor] = useState<HTMLElement | null>(null)

  // Per-user current-period usage. Loaded alongside the user list via
  // /admin/traffic/top so each row can display "used / limit".
  const [usageMap, setUsageMap] = useState<Map<number, TrafficRow>>(new Map())

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const groupNameMap = useMemo(() => new Map(groups.map(g => [g.id, g.name])), [groups])
  const selectableIds = items.filter(canSelect).map(u => u.id)
  const allChecked = selectableIds.length > 0 && selectableIds.every(id => selected.has(id))
  const someChecked = selected.size > 0 && !allChecked
  const selectedRows = items.filter(u => selected.has(u.id))

  useEffect(() => { void loadGroups() }, [])
  useEffect(() => { void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, groupFilter])

  async function loadGroups() {
    try { const res = await listGroups(); setGroups(res.items) } catch { /* toast */ }
  }

  async function load() {
    setLoading(true)
    try {
      const res = await listUsers({
        page, page_size: PAGE_SIZE,
        search: search || undefined,
        group_id: groupFilter === '' ? undefined : groupFilter,
      })
      setItems(res.items)
      setTotal(res.total)
      setSelected(new Set())
      // Best-effort fetch of current-period usage for each user. The traffic
      // top endpoint already iterates every user, so requesting a large limit
      // gives us "used bytes" for everyone in one round-trip.
      try {
        const rows = await topTraffic(1000)
        setUsageMap(new Map(rows.map(r => [r.user_id, r])))
      } catch { /* table just won't show usage; not fatal */ }
    } finally {
      setLoading(false)
    }
  }

  function onSearchSubmit(e: FormEvent) { e.preventDefault(); setPage(1); void load() }

  function toggleAll(checked: boolean) { setSelected(checked ? new Set(selectableIds) : new Set()) }
  function toggleOne(id: number, checked: boolean) {
    setSelected(prev => {
      const next = new Set(prev)
      if (checked) next.add(id); else next.delete(id)
      return next
    })
  }

  // ---- Create ----
  function openCreate() {
    setCreateForm({ ...EMPTY_CREATE, group_id: groups[0]?.id ?? '', expire_date: dateInNDays(30) })
    setCreateErr({})
    setCreateOpen(true)
  }

  function validateCreate(f: CreateForm): FieldErrors<CreateField> {
    return {
      // UPN doubles as login + sub-token salt — must look like an email to
      // route password-reset and emergency-access mails correctly.
      upn: validateEmail(f.upn, { required: true }),
      email: validateEmail(f.email),
      display_name: validateName(f.display_name, { max: 64 }),
      // Initial password is optional (server auto-generates when empty), but
      // if the admin types one in we hold it to the floor.
      password: f.password ? validatePassword(f.password, { strong: true }) : '',
      group_id: validateGroupId(f.group_id, { required: true }),
      expire_date: f.expire_mode === 'date' ? validateRequired(f.expire_date) : '',
      traffic_limit_gb: validateNonNegativeNumber(f.traffic_limit_gb),
    }
  }

  async function submitCreate(e: FormEvent) {
    e.preventDefault()
    const errs = validateCreate(createForm)
    setCreateErr(errs)
    const firstKey = firstError(errs)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return }
    setCreateBusy(true)
    try {
      const res = await createUser({
        upn: createForm.upn,
        email: createForm.email || undefined,
        display_name: createForm.display_name || undefined,
        password: createForm.password || undefined,
        // validateGroupId above proves this is a number; the FormState type
        // keeps the '' option for the empty-select state only.
        group_id: createForm.group_id as number,
        // Send the picked calendar day; the backend resolves it to end-of-day
        // in the panel timezone (same path as edit). Permanent → omit entirely.
        expire_date: createForm.expire_mode === 'date' ? createForm.expire_date : undefined,
        traffic_limit_gb: createForm.traffic_limit_gb,
        traffic_reset_period: createForm.traffic_reset_period,
        remark: createForm.remark || undefined,
      })
      setCreateOpen(false)
      setResultUser(res.user); setResultPassword(res.initial_password); setResultOpen(true)
      await load()
    } finally { setCreateBusy(false) }
  }

  // ---- Edit ----
  function openEdit(u: User) {
    setEditing(u)
    const usedGB = bytesToGB(usageMap.get(u.id)?.period_used_bytes ?? 0)
    setEditForm({
      display_name: u.display_name ?? '', email: u.email ?? '',
      group_id: u.group_id, role: u.role,
      expire_mode: u.expire_at ? 'date' : 'permanent',
      // Prefill from the panel-timezone calendar day the backend computed,
      // not the raw instant — slicing the UTC date off expire_at would show
      // the wrong day in zones west of UTC.
      expire_at: u.expire_date ?? '',
      traffic_limit_gb: bytesToGB(u.traffic_limit_bytes),
      traffic_reset_period: u.traffic_reset_period,
      remark: u.remark ?? '',
      period_used_gb: usedGB,
      period_used_initial: usedGB,
      emergency_used_count: u.emergency_used_count,
    })
    setEditErr({})
    setEditOpen(true)
  }

  function validateEdit(f: EditForm): FieldErrors<EditField> {
    return {
      display_name: validateName(f.display_name, { max: 64 }),
      email: validateEmail(f.email),
      group_id: validateGroupId(f.group_id, { required: true }),
      // Date mode requires a value; permanent mode skips the check entirely.
      expire_at: f.expire_mode === 'date' ? validateRequired(f.expire_at) : '',
      // GB fields persist as int64 bytes downstream, so the UI happily
      // takes decimals (1.23 GB is the natural way to show 1320 MB of
      // accrued usage — integer-only would reject the field whenever a
      // real user has period usage).
      traffic_limit_gb: validateNonNegativeNumber(f.traffic_limit_gb),
      period_used_gb: validateNonNegativeNumber(f.period_used_gb),
    }
  }

  async function submitEdit(e: FormEvent) {
    e.preventDefault()
    if (!editing) return
    const errs = validateEdit(editForm)
    setEditErr(errs)
    const firstKey = firstError(errs)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return }
    if (editing.id === auth.userId && editing.role === 'admin' && editForm.role === 'user') {
      const ok = await confirm({
        title: t('admin:users.confirm.demote_title'),
        message: t('admin:users.validate.demote_self'),
        destructive: true,
      })
      if (!ok) return
    }
    setEditBusy(true)
    try {
      const req: UpdateUserRequest = {
        group_id: editForm.group_id as number, email: editForm.email,
        traffic_limit_gb: editForm.traffic_limit_gb,
        traffic_reset_period: editForm.traffic_reset_period,
        remark: editForm.remark, display_name: editForm.display_name,
        role: editForm.role,
      }
      if (editForm.expire_mode === 'permanent') req.clear_expire = true
      // Send the bare YYYY-MM-DD; the backend anchors it to end-of-day in
      // the panel timezone so the chosen day can't drift with the browser tz.
      else if (editForm.expire_at) req.expire_date = editForm.expire_at
      await updateUser(editing.id, req)
      // Period-used edit: only push when admin actually moved it. Sub-1MB
      // jitter is treated as no-op so refreshing the dialog without changing
      // anything doesn't synthesise a baseline snapshot.
      if (Math.abs(editForm.period_used_gb - editForm.period_used_initial) > 0.001) {
        await setUserTraffic(editing.id, editForm.period_used_gb)
      }
      if (editing.id === auth.userId) auth.setDisplayName(editForm.display_name || '')
      pushSnack(t('admin:users.toast.saved'), 'success')
      setEditOpen(false)
      await load()
    } finally { setEditBusy(false) }
  }

  // ---- Single delete ----
  async function confirmDelete(u: User) {
    const ok = await confirm({
      title: t('admin:users.confirm.delete_title'),
      message: t('admin:users.confirm.delete_message', { upn: u.upn }),
      destructive: true,
      confirmText: t('admin:users.action.delete'),
    })
    if (!ok) return
    await deleteUser(u.id)
    setItems(prev => prev.filter(x => x.id !== u.id))
    setTotal(t => Math.max(0, t - 1))
    pushSnack(t('admin:users.toast.deleted'), 'success')
  }

  // ---- Toggle enabled (single + batch) ----
  function openReason(u: User) {
    setReasonUser(u); setReasonBatch(null); setReasonText(''); setReasonOpen(true)
  }
  function openBatchReason(enable: boolean) {
    setReasonUser(null); setReasonBatch({ enable }); setReasonText(''); setReasonOpen(true)
  }
  async function submitReason() {
    setReasonOpen(false)
    if (reasonBatch) {
      const enable = reasonBatch.enable
      const rows = selectedRows
      if (rows.length === 0) return
      setBatchBusy(enable ? 'enable' : 'disable')
      try {
        const results = await Promise.allSettled(rows.map(r => setEnabled(r.id, enable, reasonText.trim() || undefined)))
        const failed = results.filter(r => r.status === 'rejected').length
        if (failed > 0) {
          pushSnack(t(enable ? 'admin:users.batch.enable_partial' : 'admin:users.batch.disable_partial', { ok: rows.length - failed, fail: failed }), 'warning')
        } else {
          pushSnack(t(enable ? 'admin:users.batch.enabled_count' : 'admin:users.batch.disabled_count', { count: rows.length }), 'success')
        }
        await load()
      } finally { setBatchBusy('') }
      return
    }
    if (!reasonUser) return
    const enabling = !reasonUser.enabled
    await setEnabled(reasonUser.id, enabling, reasonText.trim() || undefined)
    pushSnack(t(enabling ? 'admin:users.toast.enabled' : 'admin:users.toast.disabled'), 'success')
    await load()
  }

  // ---- Quick renew (single + batch) ----
  async function quickRenew(u: User) {
    if (!canQuickRenew(u)) { pushSnack(t('admin:users.toast.renew_permanent_skip'), 'info'); return }
    await updateUser(u.id, { expire_at: renewedExpireAt(u, 30) })
    pushSnack(t('admin:users.toast.renewed', { days: 30 }), 'success')
    await load()
  }
  async function batchQuickRenew() {
    const renewable = selectedRows.filter(canQuickRenew)
    if (renewable.length === 0) { pushSnack(t('admin:users.batch.no_renewable'), 'info'); return }
    setBatchBusy('renew')
    try {
      const results = await Promise.allSettled(
        renewable.map(r => updateUser(r.id, { expire_at: renewedExpireAt(r, 30) })),
      )
      const failed = results.filter(r => r.status === 'rejected').length
      const skipped = selectedRows.length - renewable.length
      if (failed > 0) {
        pushSnack(t('admin:users.batch.renewed_partial', { ok: renewable.length - failed, fail: failed }), 'warning')
      } else if (skipped > 0) {
        pushSnack(t('admin:users.batch.renewed_with_skip', { count: renewable.length, skipped }), 'success')
      } else {
        pushSnack(t('admin:users.toast.renewed', { days: 30 }), 'success')
      }
      await load()
    } finally { setBatchBusy('') }
  }

  // ---- Batch unlink SSO ----
  async function batchUnlinkSSO() {
    setBatchMoreAnchor(null)
    // Only SSO-bound rows are eligible — skip rows already on local so
    // the count surfaced in the confirm dialog matches what'll actually
    // get touched. UnlinkSSO server-side rejects local rows with
    // ErrValidation, but we'd rather hide the noise than rely on it.
    const eligible = selectedRows.filter(r => r.sso_provider && r.sso_provider !== 'local')
    if (eligible.length === 0) {
      pushSnack(t('admin:users.batch.unlink_sso_none_eligible', { defaultValue: '所选用户中没有 SSO 绑定的账号' }), 'info')
      return
    }
    const names = eligible.slice(0, 5).map(r => r.display_name || r.upn).join('、')
    const suffix = eligible.length > 5 ? ` +${eligible.length - 5}` : ''
    const ok = await confirm({
      title: t('admin:users.confirm.batch_unlink_sso_title'),
      message: t('admin:users.confirm.batch_unlink_sso_message', { names, suffix, count: eligible.length }),
      destructive: true,
      confirmText: t('admin:users.batch.unlink_sso'),
    })
    if (!ok) return
    setBatchBusy('unlink_sso')
    try {
      const results = await Promise.allSettled(eligible.map(r => unlinkSSO(r.id)))
      const failed = results.filter(r => r.status === 'rejected').length
      await load()
      setSelected(new Set())
      if (failed > 0) {
        pushSnack(t('admin:users.batch.unlink_sso_partial', {
          ok: eligible.length - failed, fail: failed,
          defaultValue: '已解除 {{ok}} 个，{{fail}} 个失败',
        }), 'warning')
      } else {
        pushSnack(t('admin:users.batch.unlink_sso_done', {
          count: eligible.length,
          defaultValue: '已解除 {{count}} 个 SSO 绑定',
        }), 'success')
      }
    } finally { setBatchBusy('') }
  }

  // ---- Batch delete ----
  async function batchDelete() {
    const rows = selectedRows
    if (rows.length === 0) return
    const names = rows.slice(0, 5).map(r => r.display_name || r.upn).join('、')
    const suffix = rows.length > 5 ? ` +${rows.length - 5}` : ''
    const ok = await confirm({
      title: t('admin:users.confirm.batch_delete_title'),
      message: t('admin:users.confirm.batch_delete_message', { names, suffix }),
      destructive: true,
      confirmText: t('admin:users.action.delete'),
    })
    if (!ok) return
    setBatchBusy('delete')
    try {
      const results = await Promise.allSettled(rows.map(r => deleteUser(r.id)))
      const okIds = rows.filter((_, i) => results[i].status === 'fulfilled').map(r => r.id)
      const failed = rows.length - okIds.length
      setItems(prev => prev.filter(x => !okIds.includes(x.id)))
      setTotal(t => Math.max(0, t - okIds.length))
      setSelected(new Set())
      if (failed > 0) pushSnack(t('admin:users.batch.delete_partial', { ok: okIds.length, fail: failed }), 'warning')
      else pushSnack(t('admin:users.batch.deleted_count', { count: okIds.length }), 'success')
    } finally { setBatchBusy('') }
  }

  // ---- More menu actions ----
  function openMore(e: MouseEvent<HTMLElement>, u: User) {
    setMoreAnchor(e.currentTarget); setMoreUser(u)
  }
  function closeMore() { setMoreAnchor(null); setMoreUser(null) }

  async function copy(text: string) {
    try { await navigator.clipboard.writeText(text); pushSnack(t('admin:users.toast.copied'), 'success') }
    catch { /* ignore */ }
  }

  async function actionResetCredentials(u: User) {
    closeMore()
    const ok = await confirm({
      title: t('admin:users.confirm.reset_credentials_title'),
      message: t('admin:users.confirm.reset_credentials_message', { upn: u.upn }),
      destructive: true,
      confirmText: t('admin:users.more_menu.reset_credentials'),
    })
    if (!ok) return
    const res = await resetCredentials(u.id)
    pushSnack(t('admin:users.credentials.result', { uuid: res.uuid }), 'success')
    await load()
  }

  async function actionUnlinkSSO(u: User) {
    closeMore()
    const ok = await confirm({
      title: t('admin:users.confirm.unlink_sso_title'),
      message: t('admin:users.confirm.unlink_sso_message', {
        upn: u.upn, provider: u.sso_provider,
      }),
      destructive: true,
      confirmText: t('admin:users.more_menu.unlink_sso'),
    })
    if (!ok) return
    await unlinkSSO(u.id)
    pushSnack(t('admin:users.toast.sso_unlinked'), 'success')
    await load()
  }

  function actionResetPassword(u: User) {
    closeMore()
    setPwdResetUser(u)
    setPwdResetValue('')
    setPwdResetShow(false)
    setPwdResetError('')
    setPwdResetOpen(true)
  }

  // Random-fill: client-side generator so the admin sees the value
  // immediately and can edit it before submitting. Format mirrors the
  // server's idgen.NewPassword (alnum, mixed case, 12 chars) so a manual
  // edit feels consistent.
  function genRandomPassword(): string {
    const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
    const arr = new Uint32Array(12)
    crypto.getRandomValues(arr)
    let out = ''
    for (let i = 0; i < arr.length; i++) out += alphabet[arr[i] % alphabet.length]
    return out
  }

  async function submitPwdReset() {
    if (!pwdResetUser) return
    const v = pwdResetValue.trim()
    // Server validates too; mirror the rule locally for instant feedback.
    if (v && !/^(?=.{8,})(?=.*[A-Za-z])(?=.*\d)/.test(v)) {
      setPwdResetError(t('admin:validation.password_strength'))
      return
    }
    setPwdResetBusy(true)
    try {
      const res = await resetPassword(pwdResetUser.id, v || undefined)
      setPwdResetOpen(false)
      // Surface the final password (server-echoed) in the same result
      // dialog used by create-user so the copy + dismiss flow is identical.
      setResultUser(pwdResetUser)
      setResultPassword(res.password)
      setResultOpen(true)
    } finally { setPwdResetBusy(false) }
  }

  async function actionResetEmergency(u: User) {
    closeMore()
    await resetEmergencyUsage(u.id)
    pushSnack(t('admin:users.credentials.emergency_reset'), 'success')
    await load()
  }

  async function batchResetEmergency() {
    setBatchMoreAnchor(null)
    const rows = selectedRows
    if (rows.length === 0) return
    setBatchBusy('emergency')
    try {
      const results = await Promise.allSettled(rows.map(r => resetEmergencyUsage(r.id)))
      const failed = results.filter(r => r.status === 'rejected').length
      if (failed > 0) pushSnack(t('admin:users.batch.emergency_partial', { ok: rows.length - failed, fail: failed }), 'warning')
      else pushSnack(t('admin:users.batch.emergency_count', { count: rows.length }), 'success')
      await load()
    } finally { setBatchBusy('') }
  }

  // ---- Personal rules ----
  async function openRules(u: User) {
    closeMore()
    setRulesUser(u); setRulesOpen(true); setRulesBusy(true); setRulesText(''); setRulesSaved('')
    try {
      const text = await getUserRules(u.id)
      setRulesText(text); setRulesSaved(text)
    } finally { setRulesBusy(false) }
  }

  async function saveRules() {
    if (!rulesUser) return
    setRulesBusy(true)
    try {
      const text = rulesText.trim()
      await updateUserRules(rulesUser.id, text)
      setRulesText(text); setRulesSaved(text)
      pushSnack(t('admin:users.rules.saved'), 'success')
      setRulesOpen(false)
    } finally { setRulesBusy(false) }
  }

  // ---- Reconcile ----
  async function triggerReconcile() {
    setReconcileBusy(true)
    try {
      const report = await runReconcile()
      setReconcileReport(report)
      setReconcileDetailsOpen(false) // reset every run; admin reveals via button
      if ((report.fixed ?? 0) > 0 || (report.issues?.length ?? 0) > 0) {
        // Show dialog whenever something happened (fix OR unfixed issue),
        // not just when there are unfixed issues — admin wants to see
        // "what got fixed" in the detail view, not just leftovers.
        setReconcileOpen(true)
      } else {
        const msg = t('admin:users.reconcile.summary_no_fix', { scanned: report.scanned })
        pushSnack(msg, 'success')
      }
      await load()
    } finally { setReconcileBusy(false) }
  }

  // ---- Cells ----
  function badge(label: string, bg: string, fg: string) {
    return <Box sx={{
      display: 'inline-block', px: 1.25, py: 0.25,
      borderRadius: 1, fontSize: 12, fontWeight: 500,
      bgcolor: bg, color: fg, whiteSpace: 'nowrap',
    }}>{label}</Box>
  }

  function expireBadge(u: User) {
    if (!u.expire_at) return badge(t('admin:users.status.permanent'), md.surfaceContainerHighest, md.onSurfaceVariant)
    const expire = new Date(u.expire_at)
    const diffDays = Math.ceil((expire.getTime() - Date.now()) / 86400000)
    if (diffDays < 0) return badge(t('admin:users.status.expired_days', { days: Math.abs(diffDays) }), md.errorContainer, md.onErrorContainer)
    if (diffDays === 0) return badge(t('admin:users.status.today'), md.errorContainer, md.onErrorContainer)
    if (diffDays <= 7) return badge(t('admin:users.status.expiring_soon', { days: diffDays }), md.tertiaryContainer, md.onTertiaryContainer)
    // Show the panel-timezone calendar day (the authoritative one), falling
    // back to the browser-local render only if the backend didn't supply it.
    return <Typography sx={{ fontSize: 13, color: md.onSurface, fontVariantNumeric: 'tabular-nums' }}>{u.expire_date ?? expire.toLocaleDateString()}</Typography>
  }

  function statusBadge(u: User) {
    // Highest priority: active emergency window. The user is technically
    // enabled but in a special "burning the emergency budget" state — admins
    // need to spot these to know who's about to be cut off when the window
    // expires. Uses tertiaryContainer (same green family) since it's
    // self-service / not punitive.
    const emergencyActive = u.emergency_until && new Date(u.emergency_until).getTime() > Date.now()
    if (emergencyActive) {
      return badge(t('admin:users.status.emergency_active'), md.tertiaryContainer, md.onTertiaryContainer)
    }
    if (u.enabled) {
      // Even when `enabled` is still true the admin needs to see at-a-glance
      // when the user is actually in a bad state — common cases:
      //   • Traffic poll hasn't fired yet but periodUsed already >= limit
      //   • Past ExpireAt but auto-disable hasn't caught up (5min cron)
      // Surface those here so the table tells the truth without waiting for
      // the next poll cycle. Limit/expire columns already show the numbers;
      // this gives the status column matching semantics.
      const periodUsed = usageMap.get(u.id)?.period_used_bytes ?? 0
      if (u.traffic_limit_bytes > 0 && periodUsed >= u.traffic_limit_bytes) {
        return badge(t('admin:users.status.traffic_exhausted'), md.tertiaryContainer, md.onTertiaryContainer)
      }
      if (u.expire_at && new Date(u.expire_at).getTime() < Date.now()) {
        return badge(t('admin:users.status.expired'), md.errorContainer, md.onErrorContainer)
      }
      return badge(t('admin:users.status.enabled'), md.tertiaryContainer, md.onTertiaryContainer)
    }
    // Distinguish disabled-by-reason so admin can scan the table and see WHY
    // a user is offline without opening the edit dialog. Traffic-exhausted
    // is the most common case (auto-disabled when periodUsed >= limit) and
    // gets its own copy "已用尽" so it doesn't read like a punitive action.
    switch (u.auto_disabled_reason) {
      case 'traffic_exceeded':
        return badge(t('admin:users.status.traffic_exhausted'), md.tertiaryContainer, md.onTertiaryContainer)
      case 'expired':
        return badge(t('admin:users.status.expired'), md.errorContainer, md.onErrorContainer)
      case 'blocked_client':
        return badge(t('admin:users.status.blocked'), md.errorContainer, md.onErrorContainer)
      case 'pending_approval':
        return badge(t('admin:users.status.pending_approval'), md.surfaceContainerHighest, md.onSurfaceVariant)
      case 'pending_delete':
        return badge(t('admin:users.status.pending_delete'), md.surfaceContainerHighest, md.onSurfaceVariant)
      case 'manual':
        return badge(t('admin:users.status.manual_disabled'), md.errorContainer, md.onErrorContainer)
      default:
        return badge(t('admin:users.status.disabled'), md.errorContainer, md.onErrorContainer)
    }
  }

  function roleBadge(role: Role) {
    switch (role) {
      case 'admin':
        return badge(t('admin:users.role.admin'), md.primaryContainer, md.onPrimaryContainer)
      case 'operator':
        return badge(t('admin:users.role.operator'), md.secondaryContainer, md.onSecondaryContainer)
      default:
        return badge(t('admin:users.role.user'), md.surfaceContainerHighest, md.onSurfaceVariant)
    }
  }

  // SSO binding chip: "saml" / "oidc" / hidden for local. Tooltip carries
  // the full provider + subject so admins can verify which IdP / NameID
  // the row is bound to before clicking Unlink.
  function ssoBadge(u: User) {
    const provider = u.sso_provider || 'local'
    if (provider === 'local') return null
    const protocol = provider.split(':', 1)[0]
    const label = protocol.toUpperCase()
    const tooltip = `${provider}${u.sso_subject ? `  (${u.sso_subject})` : ''}`
    return (
      <Tooltip title={tooltip}>
        <Box component="span" sx={{
          display: 'inline-block',
          px: 0.75, py: 0,
          borderRadius: 999,
          fontSize: 10, fontWeight: 600,
          letterSpacing: 0.4,
          bgcolor: md.tertiaryContainer,
          color: md.onTertiaryContainer,
        }}>{label}</Box>
      </Tooltip>
    )
  }

  function trafficCell(u: User) {
    const limitGB = bytesToGB(u.traffic_limit_bytes)
    const usedGB = bytesToGB(usageMap.get(u.id)?.period_used_bytes ?? 0)
    if (limitGB === 0) {
      // Unlimited — still surface usage so admin sees what's flowing.
      return (
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, fontVariantNumeric: 'tabular-nums' }}>
          {usedGB} GB / {t('admin:users.status.unlimited')}
        </Typography>
      )
    }
    const overLimit = usedGB >= limitGB
    return (
      <Typography sx={{
        fontSize: 13, fontVariantNumeric: 'tabular-nums',
        color: overLimit ? md.error : 'inherit',
        fontWeight: overLimit ? 500 : 400,
      }}>
        {usedGB} / {limitGB} GB
      </Typography>
    )
  }

  return (
    <Box sx={{ p: 3 }}>
      {/* Header */}
      <Box sx={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', flexWrap: 'wrap', gap: 2, mb: 2 }}>
        <Typography variant="h4">{t('admin:users.title')}</Typography>
        <Box sx={{ display: 'flex', gap: 1 }}>
          <Button variant="outlined" startIcon={reconcileBusy ? <CircularProgress size={14} /> : <SyncIcon />}
            onClick={triggerReconcile} disabled={reconcileBusy}>
            {t('admin:users.reconcile.trigger')}
          </Button>
          <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
            {t('admin:users.create')}
          </Button>
        </Box>
      </Box>

      {/* Toolbar */}
      <Box sx={{ display: 'flex', gap: 1.5, mb: 2, flexWrap: 'wrap', alignItems: 'center' }}>
        <Box component="form" onSubmit={onSearchSubmit}
          sx={{ display: 'flex', alignItems: 'center', gap: 1, height: 40, px: 2, borderRadius: 9999,
            bgcolor: md.surfaceContainer, color: md.onSurfaceVariant, width: 320, maxWidth: '100%' }}>
          <SearchIcon sx={{ fontSize: 20 }} />
          <InputBase placeholder={t('admin:users.search_placeholder')} value={search}
            onChange={e => setSearch(e.target.value)}
            sx={{ flex: 1, fontSize: 14, color: md.onSurface }} />
        </Box>
        <Select size="small" value={groupFilter} displayEmpty
          onChange={e => { setGroupFilter(e.target.value as number | ''); setPage(1) }}
          sx={{ minWidth: 160, height: 40, '& .MuiSelect-select': { py: 1 } }}>
          <MenuItem value="">{t('admin:users.filter_group_all')}</MenuItem>
          {groups.map(g => <MenuItem key={g.id} value={g.id}>{g.name}</MenuItem>)}
        </Select>
        <Button variant="outlined" startIcon={<RefreshIcon />} onClick={() => load()} disabled={loading}>
          {t('admin:users.refresh')}
        </Button>
      </Box>

      {/* Batch toolbar */}
      {selected.size > 0 && (
        <Box sx={{
          display: 'flex', alignItems: 'center', gap: 1, mb: 2, px: 2, py: 1,
          borderRadius: 9999, bgcolor: md.secondaryContainer, color: md.onSecondaryContainer,
          flexWrap: 'wrap',
        }}>
          <Typography sx={{ fontSize: 13, fontWeight: 500, mr: 1 }}>
            {t('admin:users.batch.selected', { count: selected.size })}
          </Typography>
          <Button size="small" variant="text" sx={{ color: 'inherit' }}
            startIcon={batchBusy === 'enable' ? <CircularProgress size={14} /> : <ToggleOnIcon />}
            disabled={batchBusy !== ''} onClick={() => openBatchReason(true)}>
            {t('admin:users.batch.enable')}
          </Button>
          <Button size="small" variant="text" sx={{ color: 'inherit' }}
            startIcon={batchBusy === 'disable' ? <CircularProgress size={14} /> : <ToggleOffIcon />}
            disabled={batchBusy !== ''} onClick={() => openBatchReason(false)}>
            {t('admin:users.batch.disable')}
          </Button>
          <Button size="small" variant="text" sx={{ color: 'inherit' }}
            startIcon={batchBusy === 'renew' ? <CircularProgress size={14} /> : <RestartAltIcon />}
            disabled={batchBusy !== ''} onClick={batchQuickRenew}>
            {t('admin:users.batch.renew')}
          </Button>
          <Button size="small" variant="text" color="error"
            startIcon={batchBusy === 'delete' ? <CircularProgress size={14} /> : <DeleteIcon />}
            disabled={batchBusy !== ''} onClick={batchDelete}>
            {t('admin:users.batch.delete')}
          </Button>
          <Button size="small" variant="text" sx={{ color: 'inherit' }}
            startIcon={<MoreVertIcon />}
            disabled={batchBusy !== ''}
            onClick={(e) => setBatchMoreAnchor(e.currentTarget)}>
            {t('admin:users.batch.more')}
          </Button>
          <Menu anchorEl={batchMoreAnchor} open={!!batchMoreAnchor} onClose={() => setBatchMoreAnchor(null)}>
            <MenuItem onClick={batchResetEmergency}>
              <ListItemIcon><EmergencyIcon fontSize="small" /></ListItemIcon>
              <ListItemText>{t('admin:users.more_menu.reset_emergency')}</ListItemText>
            </MenuItem>
            <MenuItem onClick={batchUnlinkSSO}>
              <ListItemIcon><LinkOffIcon fontSize="small" /></ListItemIcon>
              <ListItemText>{t('admin:users.batch.unlink_sso')}</ListItemText>
            </MenuItem>
          </Menu>
        </Box>
      )}

      {/* Table */}
      <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
        <TableContainer>
          <Table>
            <TableHead>
              <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                <TableCell padding="checkbox">
                  <Checkbox indeterminate={someChecked} checked={allChecked}
                    onChange={(_, c) => toggleAll(c)}
                    disabled={selectableIds.length === 0} />
                </TableCell>
                <TableCell>{t('admin:users.table.upn')}</TableCell>
                <TableCell>{t('admin:users.table.display_name')}</TableCell>
                <TableCell>{t('admin:users.table.group')}</TableCell>
                <TableCell>{t('admin:users.table.role')}</TableCell>
                <TableCell>{t('admin:users.table.traffic')}</TableCell>
                <TableCell>{t('admin:users.table.expire')}</TableCell>
                <TableCell>{t('admin:users.table.status')}</TableCell>
                <TableCell align="right">{t('admin:users.table.actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {loading && items.length === 0 && (
                <TableRow><TableCell colSpan={9} sx={{ textAlign: 'center', py: 6 }}>
                  <CircularProgress size={24} />
                </TableCell></TableRow>
              )}
              {!loading && items.length === 0 && (
                <TableRow><TableCell colSpan={9} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
              )}
              {items.map(u => (
                <TableRow key={u.id} hover sx={{
                  '& td': { borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' },
                  opacity: u.enabled && !isExpired(u) ? 1 : 0.65,
                }}>
                  <TableCell padding="checkbox">
                    <Checkbox checked={selected.has(u.id)} disabled={!canSelect(u)}
                      onChange={(_, c) => toggleOne(u.id, c)} />
                  </TableCell>
                  <TableCell sx={{ fontWeight: 500 }}>
                    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.75 }}>
                      <span>{u.upn}</span>
                      {ssoBadge(u)}
                    </Box>
                  </TableCell>
                  <TableCell sx={{ color: md.onSurfaceVariant, fontSize: 13 }}>{u.display_name || '—'}</TableCell>
                  <TableCell sx={{ fontSize: 13 }}>{groupNameMap.get(u.group_id) || u.group_id}</TableCell>
                  <TableCell>{roleBadge(u.role)}</TableCell>
                  <TableCell>{trafficCell(u)}</TableCell>
                  <TableCell>{expireBadge(u)}</TableCell>
                  <TableCell>{statusBadge(u)}</TableCell>
                  <TableCell align="right">
                    <Tooltip title={t('admin:users.action.edit')}>
                      <IconButton size="small" onClick={() => openEdit(u)}><EditIcon fontSize="small" /></IconButton>
                    </Tooltip>
                    <Tooltip title={t('admin:users.action.renew')}>
                      <span>
                        <IconButton size="small" onClick={() => quickRenew(u)} disabled={!canQuickRenew(u)}>
                          <RestartAltIcon fontSize="small" />
                        </IconButton>
                      </span>
                    </Tooltip>
                    {canManageUser(u) && <>
                    <Tooltip title={t(u.enabled ? 'admin:users.action.disable' : 'admin:users.action.enable')}>
                      <IconButton size="small" onClick={() => openReason(u)}>
                        {u.enabled
                          ? <ToggleOnIcon fontSize="small" sx={{ color: md.primary }} />
                          : <ToggleOffIcon fontSize="small" />}
                      </IconButton>
                    </Tooltip>
                    <Tooltip title={t('admin:users.action.delete')}>
                      <IconButton size="small" onClick={() => confirmDelete(u)} sx={{ color: md.error }}>
                        <DeleteIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                    <IconButton size="small" onClick={(e) => openMore(e, u)}>
                      <MoreVertIcon fontSize="small" />
                    </IconButton>
                    </>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>

        {totalPages > 1 && (
          <Box sx={{ display: 'flex', justifyContent: 'center', py: 2, borderTop: `1px solid ${md.outlineVariant}` }}>
            <Pagination count={totalPages} page={page} onChange={(_, p) => setPage(p)} shape="rounded" color="primary" />
          </Box>
        )}
      </Card>

      {/* Per-row More menu */}
      <Menu anchorEl={moreAnchor} open={!!moreAnchor} onClose={closeMore}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}>
        <MenuItem onClick={() => { if (moreUser) copy(moreUser.sub_url); closeMore() }}>
          <ListItemIcon><ContentCopyIcon fontSize="small" /></ListItemIcon>
          <ListItemText>{t('admin:users.more_menu.copy_sub')}</ListItemText>
        </MenuItem>
        <MenuItem onClick={() => moreUser && openRules(moreUser)}>
          <ListItemIcon><RuleIcon fontSize="small" /></ListItemIcon>
          <ListItemText>{t('admin:users.more_menu.personal_rules')}</ListItemText>
        </MenuItem>
        <MenuItem onClick={() => moreUser && actionResetPassword(moreUser)}>
          <ListItemIcon><LockResetIcon fontSize="small" /></ListItemIcon>
          <ListItemText>{t('admin:users.more_menu.reset_password', { defaultValue: '重置密码' })}</ListItemText>
        </MenuItem>
        <MenuItem onClick={() => moreUser && actionResetCredentials(moreUser)}>
          <ListItemIcon><KeyIcon fontSize="small" /></ListItemIcon>
          <ListItemText>{t('admin:users.more_menu.reset_credentials')}</ListItemText>
        </MenuItem>
        <MenuItem onClick={() => moreUser && actionResetEmergency(moreUser)}>
          <ListItemIcon><EmergencyIcon fontSize="small" /></ListItemIcon>
          <ListItemText>{t('admin:users.more_menu.reset_emergency')}</ListItemText>
        </MenuItem>
        <MenuItem
          onClick={() => moreUser && actionUnlinkSSO(moreUser)}
          disabled={!moreUser || !moreUser.sso_provider || moreUser.sso_provider === 'local'}>
          <ListItemIcon><LinkOffIcon fontSize="small" /></ListItemIcon>
          <ListItemText>{t('admin:users.more_menu.unlink_sso')}</ListItemText>
        </MenuItem>
      </Menu>

      {/* Create dialog */}
      <Dialog open={createOpen} onClose={() => !createBusy && setCreateOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }}>
        <DialogTitle>{t('admin:users.create')}</DialogTitle>
        <DialogContent>
          <Box component="form" id="create-form" onSubmit={submitCreate} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            <TextField required fullWidth label={t('admin:users.field.upn')}
              value={createForm.upn} onChange={e => setCreateForm({ ...createForm, upn: e.target.value })}
              error={!!createErr.upn}
              helperText={createErr.upn ? t(`admin:${createErr.upn}`) : ''} />
            <TextField fullWidth label={t('admin:users.field.email')}
              value={createForm.email} onChange={e => setCreateForm({ ...createForm, email: e.target.value })}
              error={!!createErr.email}
              helperText={createErr.email ? t(`admin:${createErr.email}`) : ''} />
            <TextField fullWidth label={t('admin:users.field.display_name')}
              value={createForm.display_name} onChange={e => setCreateForm({ ...createForm, display_name: e.target.value })}
              error={!!createErr.display_name}
              helperText={createErr.display_name ? t(`admin:${createErr.display_name}`) : ''} />
            <TextField fullWidth type={createForm.show_password ? 'text' : 'password'}
              label={t('admin:users.field.password')}
              value={createForm.password} onChange={e => setCreateForm({ ...createForm, password: e.target.value })}
              error={!!createErr.password}
              helperText={createErr.password ? t(`admin:${createErr.password}`) : ''}
              InputProps={{ endAdornment: (
                <InputAdornment position="end">
                  <IconButton size="small" onClick={() => setCreateForm({ ...createForm, show_password: !createForm.show_password })}>
                    {createForm.show_password ? <VisibilityOffIcon fontSize="small" /> : <VisibilityIcon fontSize="small" />}
                  </IconButton>
                </InputAdornment>
              ) }} />
            <Autocomplete
              size="small" fullWidth
              options={groups}
              getOptionLabel={(g) => g.name}
              isOptionEqualToValue={(a, b) => a.id === b.id}
              value={groups.find(g => g.id === createForm.group_id) ?? null}
              onChange={(_, v) => setCreateForm({ ...createForm, group_id: v ? v.id : '' })}
              renderInput={(params) => (
                <TextField {...params} required label={t('admin:users.field.group')}
                  error={!!createErr.group_id}
                  helperText={createErr.group_id ? t(`admin:${createErr.group_id}`) : ''} />
              )} />
            <Box sx={{ display: 'flex', gap: 1.5, alignItems: 'flex-start' }}>
              <TextField select size="small" label={t('admin:users.field.expire_at')}
                value={createForm.expire_mode}
                onChange={e => setCreateForm({ ...createForm, expire_mode: e.target.value as 'date' | 'permanent' })}
                sx={{ minWidth: 180 }}>
                <MenuItem value="date">{t('admin:users.field.expire_mode_date')}</MenuItem>
                <MenuItem value="permanent">{t('admin:users.field.expire_mode_permanent')}</MenuItem>
              </TextField>
              {createForm.expire_mode === 'date' && (
                <TextField type="date" required size="small" label={t('admin:users.field.expire_at')}
                  value={createForm.expire_date}
                  onChange={e => setCreateForm({ ...createForm, expire_date: e.target.value })}
                  error={!!createErr.expire_date}
                  helperText={createErr.expire_date
                    ? t(`admin:${createErr.expire_date}`)
                    : (expireTzDiffers ? t('admin:users.field.expire_tz_hint', { tz: panelTz }) : '')}
                  sx={{ flex: 1 }} InputLabelProps={{ shrink: true }} />
              )}
            </Box>
            <TextField fullWidth type="number" label={t('admin:users.field.traffic_limit_gb')}
              value={createForm.traffic_limit_gb}
              onChange={e => setCreateForm({ ...createForm, traffic_limit_gb: Number(e.target.value) })}
              inputProps={{ min: 0 }}
              error={!!createErr.traffic_limit_gb}
              helperText={createErr.traffic_limit_gb ? t(`admin:${createErr.traffic_limit_gb}`) : ''} />
            <TextField select size="small" fullWidth label={t('admin:users.field.traffic_reset_period')}
              value={createForm.traffic_reset_period}
              onChange={e => setCreateForm({ ...createForm, traffic_reset_period: e.target.value as ResetPeriod })}>
              <MenuItem value="never">{t('admin:users.reset_period.never')}</MenuItem>
              <MenuItem value="monthly">{t('admin:users.reset_period.monthly')}</MenuItem>
              <MenuItem value="quarterly">{t('admin:users.reset_period.quarterly')}</MenuItem>
              <MenuItem value="yearly">{t('admin:users.reset_period.yearly')}</MenuItem>
            </TextField>
            <TextField fullWidth label={t('admin:users.field.remark')}
              value={createForm.remark} onChange={e => setCreateForm({ ...createForm, remark: e.target.value })} />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCreateOpen(false)} disabled={createBusy} variant="text">{t('common:actions.cancel')}</Button>
          <Button type="submit" form="create-form" variant="contained" disabled={createBusy}
            startIcon={createBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>

      {/* Reset-password dialog */}
      <Dialog open={pwdResetOpen} onClose={() => !pwdResetBusy && setPwdResetOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 460, maxWidth: '90vw' } }}>
        <DialogTitle>
          {t('admin:users.reset_password.title', { defaultValue: '重置登录密码' })}
          {pwdResetUser ? ` — ${pwdResetUser.upn}` : ''}
        </DialogTitle>
        <DialogContent>
          <Typography variant="body2" sx={{ mb: 2, color: md.onSurfaceVariant }}>
            {t('admin:users.reset_password.hint', {
              defaultValue: '留空将生成随机密码；也可以点击右侧骰子按钮自动填充后再手动修改。',
            })}
          </Typography>
          <TextField
            fullWidth autoFocus
            type={pwdResetShow ? 'text' : 'password'}
            label={t('admin:users.reset_password.field', { defaultValue: '新密码（留空 = 随机）' })}
            value={pwdResetValue}
            onChange={e => { setPwdResetValue(e.target.value); if (pwdResetError) setPwdResetError('') }}
            error={!!pwdResetError}
            helperText={pwdResetError || ' '}
            InputProps={{
              endAdornment: (
                <InputAdornment position="end">
                  <Tooltip title={t('admin:users.reset_password.gen', { defaultValue: '随机生成' })}>
                    <IconButton size="small" onClick={() => { setPwdResetValue(genRandomPassword()); setPwdResetShow(true); setPwdResetError('') }}>
                      <CasinoIcon fontSize="small" />
                    </IconButton>
                  </Tooltip>
                  <IconButton size="small" onClick={() => setPwdResetShow(s => !s)}>
                    {pwdResetShow ? <VisibilityOffIcon fontSize="small" /> : <VisibilityIcon fontSize="small" />}
                  </IconButton>
                </InputAdornment>
              ),
            }}
          />
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setPwdResetOpen(false)} disabled={pwdResetBusy} variant="text">
            {t('common:actions.cancel')}
          </Button>
          <Button onClick={submitPwdReset} disabled={pwdResetBusy} variant="contained"
            startIcon={pwdResetBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>

      {/* Result dialog */}
      <Dialog open={resultOpen} onClose={() => setResultOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }}>
        <DialogTitle>{t('admin:users.result_title')}</DialogTitle>
        <DialogContent>
          <Typography variant="body2" sx={{ mb: 2 }}>
            {resultUser && t('admin:users.result.intro', { upn: resultUser.upn })}
          </Typography>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            <Box>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>
                {t('admin:users.result.password_label')}
              </Typography>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, p: 1.5,
                bgcolor: md.surfaceContainerHighest, borderRadius: 1.5,
                fontSize: 14 }}>
                <Box sx={{ flex: 1, wordBreak: 'break-all' }}>{resultPassword}</Box>
                <IconButton size="small" onClick={() => copy(resultPassword)}>
                  <ContentCopyIcon fontSize="small" />
                </IconButton>
              </Box>
            </Box>
            {resultUser && (
              <Box>
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>
                  {t('admin:users.result.sub_url_label')}
                </Typography>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, p: 1.5,
                  bgcolor: md.surfaceContainerHighest, borderRadius: 1.5,
                  fontSize: 12 }}>
                  <Box sx={{ flex: 1, wordBreak: 'break-all' }}>{resultUser.sub_url}</Box>
                  <IconButton size="small" onClick={() => copy(resultUser.sub_url)}>
                    <ContentCopyIcon fontSize="small" />
                  </IconButton>
                </Box>
              </Box>
            )}
          </Box>
        </DialogContent>
        <DialogActions>
          <Button variant="contained" onClick={() => setResultOpen(false)}>{t('common:actions.ok')}</Button>
        </DialogActions>
      </Dialog>

      {/* Edit dialog */}
      <Dialog open={editOpen} onClose={() => !editBusy && setEditOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 540, maxWidth: '90vw' } }}>
        <DialogTitle>{t('admin:users.edit_title')} — {editing?.upn}</DialogTitle>
        <DialogContent>
          <Box component="form" id="edit-form" onSubmit={submitEdit} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            <TextField fullWidth label={t('admin:users.field.display_name')}
              value={editForm.display_name} onChange={e => setEditForm({ ...editForm, display_name: e.target.value })}
              error={!!editErr.display_name}
              helperText={editErr.display_name ? t(`admin:${editErr.display_name}`) : ''} />
            <TextField fullWidth label={t('admin:users.field.email')}
              value={editForm.email} onChange={e => setEditForm({ ...editForm, email: e.target.value })}
              error={!!editErr.email}
              helperText={editErr.email ? t(`admin:${editErr.email}`) : ''} />
            <Autocomplete
              size="small" fullWidth
              options={groups}
              getOptionLabel={(g) => g.name}
              isOptionEqualToValue={(a, b) => a.id === b.id}
              value={groups.find(g => g.id === editForm.group_id) ?? null}
              onChange={(_, v) => setEditForm({ ...editForm, group_id: v ? v.id : '' })}
              renderInput={(params) => (
                <TextField {...params} required label={t('admin:users.field.group')}
                  error={!!editErr.group_id}
                  helperText={editErr.group_id ? t(`admin:${editErr.group_id}`) : ''} />
              )} />
            <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 1 }}>
              <TextField select size="small" fullWidth label={t('admin:users.field.role')}
                value={editForm.role}
                // Operators can only assign role=user — backend enforces the
                // same rule but disabling the field avoids a confusing 403
                // after submit. Admins see all three options.
                disabled={auth.role === 'operator'}
                helperText={auth.role === 'operator' ? t('admin:users.role.operator_locked', { defaultValue: '运营人员不能调整角色' }) : ''}
                onChange={e => setEditForm({ ...editForm, role: e.target.value as Role })}>
                <MenuItem value="user">{t('admin:users.role.user')}</MenuItem>
                <MenuItem value="operator">{t('admin:users.role.operator')}</MenuItem>
                <MenuItem value="admin">{t('admin:users.role.admin')}</MenuItem>
              </TextField>
              {/* Help button reveals a per-role capability cheat sheet.
                  Two roles is OK to remember; three already has admins
                  asking "what can operator actually do?" — short table
                  beats a wiki link. */}
              <Tooltip title={t('admin:users.role.help_tooltip', { defaultValue: '角色权限说明' })}>
                <IconButton size="small" sx={{ mt: 0.5 }}
                  onClick={(e: MouseEvent<HTMLElement>) => setRoleHelpAnchor(e.currentTarget)}>
                  <HelpOutlineIcon fontSize="small" />
                </IconButton>
              </Tooltip>
              <Popover
                open={!!roleHelpAnchor}
                anchorEl={roleHelpAnchor}
                onClose={() => setRoleHelpAnchor(null)}
                anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
                transformOrigin={{ vertical: 'top', horizontal: 'right' }}
                PaperProps={{ sx: { p: 2, maxWidth: 360, bgcolor: md.surfaceContainerHigh } }}>
                <Typography sx={{ fontWeight: 500, mb: 1 }}>{t('admin:users.role.help_title', { defaultValue: '角色权限速览' })}</Typography>
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25, fontSize: 13 }}>
                  <Box>
                    <Typography component="span" sx={{ fontSize: 13, fontWeight: 600, color: md.onPrimaryContainer }}>
                      {t('admin:users.role.admin')}
                    </Typography>
                    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: 0.25 }}>
                      {t('admin:users.role.help_admin', { defaultValue: '完整权限：服务器/SSO/邮件 SMTP/规则模板/审计清空，可创建任何角色。' })}
                    </Typography>
                  </Box>
                  <Box>
                    <Typography component="span" sx={{ fontSize: 13, fontWeight: 600, color: md.onSecondaryContainer }}>
                      {t('admin:users.role.operator')}
                    </Typography>
                    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: 0.25 }}>
                      {t('admin:users.role.help_operator', { defaultValue: '日常运营：用户 CRUD、流量、紧急访问、节点开关、审计读、同步任务。不能改服务器/系统设置/SSO/邮件 SMTP，不能调整角色，不能动 admin/operator 账号。' })}
                    </Typography>
                  </Box>
                  <Box>
                    <Typography component="span" sx={{ fontSize: 13, fontWeight: 600, color: md.onSurfaceVariant }}>
                      {t('admin:users.role.user')}
                    </Typography>
                    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: 0.25 }}>
                      {t('admin:users.role.help_user', { defaultValue: '普通用户：仅可访问 /user/me 自助页（订阅、用量、紧急访问、改密码、个人规则）。' })}
                    </Typography>
                  </Box>
                </Box>
              </Popover>
            </Box>
            <Box sx={{ display: 'flex', gap: 1.5, alignItems: 'flex-start' }}>
              <TextField select size="small" label={t('admin:users.field.expire_at')}
                value={editForm.expire_mode}
                onChange={e => setEditForm({ ...editForm, expire_mode: e.target.value as 'date' | 'permanent' })}
                sx={{ minWidth: 180 }}>
                <MenuItem value="date">{t('admin:users.field.expire_mode_date')}</MenuItem>
                <MenuItem value="permanent">{t('admin:users.field.expire_mode_permanent')}</MenuItem>
              </TextField>
              {editForm.expire_mode === 'date' && (
                <TextField type="date" required size="small" label={t('admin:users.field.expire_at')}
                  value={editForm.expire_at}
                  onChange={e => setEditForm({ ...editForm, expire_at: e.target.value })}
                  error={!!editErr.expire_at}
                  helperText={editErr.expire_at
                    ? t(`admin:${editErr.expire_at}`)
                    : (expireTzDiffers ? t('admin:users.field.expire_tz_hint', { tz: panelTz }) : '')}
                  sx={{ flex: 1 }} InputLabelProps={{ shrink: true }} />
              )}
            </Box>
            <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
              <TextField type="number" label={t('admin:users.field.traffic_limit_gb')}
                value={editForm.traffic_limit_gb}
                onChange={e => setEditForm({ ...editForm, traffic_limit_gb: Number(e.target.value) })}
                inputProps={{ min: 0, step: 1 }}
                error={!!editErr.traffic_limit_gb}
                helperText={editErr.traffic_limit_gb ? t(`admin:${editErr.traffic_limit_gb}`) : ''}
                sx={{ flex: '1 1 200px' }} />
              <TextField type="number" label={t('admin:users.field.period_used_gb')}
                value={editForm.period_used_gb}
                onChange={e => setEditForm({ ...editForm, period_used_gb: Number(e.target.value) })}
                inputProps={{ min: 0, step: 0.01 }}
                error={!!editErr.period_used_gb}
                helperText={editErr.period_used_gb ? t(`admin:${editErr.period_used_gb}`) : ''}
                sx={{ flex: '1 1 200px' }} />
            </Box>
            <TextField select size="small" fullWidth label={t('admin:users.field.traffic_reset_period')}
              value={editForm.traffic_reset_period}
              onChange={e => setEditForm({ ...editForm, traffic_reset_period: e.target.value as ResetPeriod })}>
              <MenuItem value="never">{t('admin:users.reset_period.never')}</MenuItem>
              <MenuItem value="monthly">{t('admin:users.reset_period.monthly')}</MenuItem>
              <MenuItem value="quarterly">{t('admin:users.reset_period.quarterly')}</MenuItem>
              <MenuItem value="yearly">{t('admin:users.reset_period.yearly')}</MenuItem>
            </TextField>
            {(() => {
              // Compute active-window details (only meaningful when the user
              // is mid-window). The list table only shows the headline "紧急
              // 访问中" badge; the admin can open this dialog to see剩余时长
              // and 已用/配额 numbers without an extra round-trip.
              const until = editing?.emergency_until ? new Date(editing.emergency_until) : null
              const active = until && until.getTime() > Date.now()
              const remainingMs = until ? until.getTime() - Date.now() : 0
              const remainingHours = Math.max(0, Math.ceil(remainingMs / 3600000))
              const usedBytes = editing?.emergency_used_bytes ?? 0
              const quotaBytes = editing?.emergency_quota_bytes ?? 0
              return (
                <Box sx={{
                  display: 'flex', flexDirection: 'column', gap: 1,
                  p: 1.5, borderRadius: 2,
                  border: `1px solid ${md.outlineVariant}`,
                  bgcolor: md.surfaceContainer,
                }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
                    <EmergencyIcon sx={{ color: md.onSurfaceVariant, fontSize: 20 }} />
                    <Typography sx={{ flex: 1, fontSize: 13 }}>
                      {t('admin:users.field.emergency_used_count', { count: editForm.emergency_used_count })}
                    </Typography>
                    <Button size="small" variant="outlined"
                      disabled={(editForm.emergency_used_count === 0 && !active) || editBusy}
                      onClick={async () => {
                        if (!editing) return
                        await resetEmergencyUsage(editing.id)
                        setEditForm({ ...editForm, emergency_used_count: 0 })
                        // Reflect the cleared window locally so the panel
                        // below disappears immediately without a refetch.
                        setEditing({ ...editing, emergency_until: null, emergency_used_bytes: 0 })
                        pushSnack(t('admin:users.credentials.emergency_reset'), 'success')
                      }}>
                      {t('admin:users.more_menu.reset_emergency')}
                    </Button>
                  </Box>
                  {active && (
                    <Box sx={{ pl: 4.25, display: 'flex', flexDirection: 'column', gap: 0.5 }}>
                      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                        {t('admin:users.field.emergency_window_until', {
                          time: until!.toLocaleString(),
                          hours: remainingHours,
                          defaultValue: `生效至 ${until!.toLocaleString()}（剩余 ${remainingHours} 小时）`,
                        })}
                      </Typography>
                      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, fontVariantNumeric: 'tabular-nums' }}>
                        {quotaBytes > 0
                          ? t('admin:users.field.emergency_window_used_with_quota', {
                              used: bytesToGB(usedBytes),
                              quota: bytesToGB(quotaBytes),
                              defaultValue: `本窗口已用 ${bytesToGB(usedBytes)} / ${bytesToGB(quotaBytes)} GB`,
                            })
                          : t('admin:users.field.emergency_window_used_unlimited', {
                              used: bytesToGB(usedBytes),
                              defaultValue: `本窗口已用 ${bytesToGB(usedBytes)} GB（不限量）`,
                            })}
                      </Typography>
                    </Box>
                  )}
                </Box>
              )
            })()}
            <TextField fullWidth label={t('admin:users.field.remark')}
              value={editForm.remark} onChange={e => setEditForm({ ...editForm, remark: e.target.value })} />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setEditOpen(false)} disabled={editBusy} variant="text">{t('common:actions.cancel')}</Button>
          <Button type="submit" form="edit-form" variant="contained" disabled={editBusy}
            startIcon={editBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>

      {/* Reason dialog (single + batch) */}
      <Dialog open={reasonOpen} onClose={() => setReasonOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 480, maxWidth: '90vw' } }}>
        <DialogTitle>
          {(reasonBatch?.enable ?? !reasonUser?.enabled)
            ? t('admin:users.reason.enable_title')
            : t('admin:users.reason.disable_title')}
          {reasonUser ? ` — ${reasonUser.upn}` : reasonBatch ? ` (${selected.size})` : ''}
        </DialogTitle>
        <DialogContent>
          <TextField fullWidth multiline minRows={3} autoFocus
            value={reasonText} onChange={e => setReasonText(e.target.value)}
            placeholder={
              (reasonBatch?.enable ?? !reasonUser?.enabled)
                ? t('admin:users.reason.enable_placeholder')
                : t('admin:users.reason.disable_placeholder')
            } />
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setReasonOpen(false)} variant="text">{t('common:actions.cancel')}</Button>
          <Button onClick={submitReason} variant="contained"
            sx={(reasonBatch?.enable ?? !reasonUser?.enabled)
              ? undefined
              : { bgcolor: md.error, color: md.onError, '&:hover': { bgcolor: alpha(md.error, 0.9) } }}>
            {(reasonBatch?.enable ?? !reasonUser?.enabled)
              ? t('admin:users.action.enable')
              : t('admin:users.action.disable')}
          </Button>
        </DialogActions>
      </Dialog>

      {/* Personal rules dialog */}
      <Dialog open={rulesOpen} onClose={() => !rulesBusy && setRulesOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 600, maxWidth: '95vw' } }}>
        <DialogTitle>
          {rulesUser && t('admin:users.rules.title', { upn: rulesUser.upn })}
        </DialogTitle>
        <DialogContent>
          {rulesBusy
            ? <Box sx={{ display: 'grid', placeItems: 'center', py: 4 }}><CircularProgress size={24} /></Box>
            : <TextField fullWidth multiline minRows={10} maxRows={20}
                value={rulesText} onChange={e => setRulesText(e.target.value)}
                placeholder={t('admin:users.rules.placeholder')}
                sx={{ '& textarea': { fontSize: 13 } }} />}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setRulesText(rulesSaved)}
            disabled={rulesBusy || rulesText.trim() === rulesSaved.trim()} variant="text">
            {t('admin:users.rules.reset')}
          </Button>
          <Button onClick={() => setRulesOpen(false)} disabled={rulesBusy} variant="text">
            {t('common:actions.cancel')}
          </Button>
          <Button onClick={saveRules} disabled={rulesBusy || rulesText.trim() === rulesSaved.trim()}
            variant="contained"
            startIcon={rulesBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('admin:users.rules.save')}
          </Button>
        </DialogActions>
      </Dialog>

      {/* Reconcile result dialog (only when issues exist) */}
      <Dialog open={reconcileOpen} onClose={() => setReconcileOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 640, maxWidth: '95vw' } }}>
        <DialogTitle>{t('admin:users.reconcile.result_title')}</DialogTitle>
        <DialogContent>
          {reconcileReport && (
            <>
              <Typography variant="body2" sx={{ mb: 2 }}>
                {reconcileReport.fixed > 0
                  ? t('admin:users.reconcile.summary_fixed', { scanned: reconcileReport.scanned, fixed: reconcileReport.fixed })
                  : t('admin:users.reconcile.summary_no_fix', { scanned: reconcileReport.scanned })}
              </Typography>
              {reconcileDetailsOpen && (reconcileReport.issues?.length ?? 0) > 0 && (() => {
                const fixed = (reconcileReport.issues ?? []).filter(i => i.fixed)
                const unfixed = (reconcileReport.issues ?? []).filter(i => !i.fixed)
                const renderItem = (i: { panel_name?: string; client_email?: string; code?: string; detail?: string }, key: number) => (
                  <li key={key}>
                    {i.panel_name && <strong>[{i.panel_name}]</strong>} {i.client_email || '(node)'}
                    {' — '}
                    <code style={{ fontSize: 12 }}>{i.code}</code>
                    {i.detail ? `: ${i.detail}` : ''}
                  </li>
                )
                return (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
                    {fixed.length > 0 && (
                      <Box>
                        <Typography sx={{ fontWeight: 500, mb: 0.5, fontSize: 13 }}>
                          {t('admin:users.reconcile.fixed_section', { defaultValue: '已修复' })}（{fixed.length}）
                        </Typography>
                        <Box component="ul" sx={{ pl: 2, m: 0, '& li': { fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 } }}>
                          {fixed.map((i, idx) => renderItem(i, idx))}
                        </Box>
                      </Box>
                    )}
                    {unfixed.length > 0 && (
                      <Box>
                        <Typography sx={{ fontWeight: 500, mb: 0.5, fontSize: 13, color: md.error }}>
                          {t('admin:users.reconcile.unfixed_section', { defaultValue: '未能修复' })}（{unfixed.length}）
                        </Typography>
                        <Box component="ul" sx={{ pl: 2, m: 0, '& li': { fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 } }}>
                          {unfixed.map((i, idx) => renderItem(i, idx))}
                        </Box>
                      </Box>
                    )}
                  </Box>
                )
              })()}
            </>
          )}
        </DialogContent>
        <DialogActions>
          {reconcileReport && (reconcileReport.issues?.length ?? 0) > 0 && (
            <Button variant="text" onClick={() => setReconcileDetailsOpen(v => !v)}>
              {reconcileDetailsOpen
                ? t('admin:users.reconcile.hide_details', { defaultValue: '隐藏详情' })
                : t('admin:users.reconcile.show_details', { defaultValue: '显示详情' })}
            </Button>
          )}
          <Button variant="contained" onClick={() => setReconcileOpen(false)}>{t('common:actions.ok')}</Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}
