import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type MouseEvent } from 'react'
import { NativeAgentUpgradeDialog } from './NativeAgentUpgradeDialog'
import { Link as RouterLink } from 'react-router'
import {
	Alert,
  Box,
  Button,
  Card,
  Checkbox,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControl,
  FormControlLabel,
  IconButton,
  InputAdornment,
  InputLabel,
  Menu,
  MenuItem,
  Select,
  Step,
  StepLabel,
  Stepper,
  Switch,
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
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import EditIcon from '@mui/icons-material/EditOutlined'
import VisibilityIcon from '@mui/icons-material/Visibility'
import VisibilityOffIcon from '@mui/icons-material/VisibilityOff'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import VpnKeyIcon from '@mui/icons-material/VpnKeyOutlined'
import DownloadIcon from '@mui/icons-material/DownloadOutlined'
import { useTranslation } from 'react-i18next'
import { allSettledLimited } from '@/utils/promises'

import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import SystemUpdateIcon from '@mui/icons-material/SystemUpdateAlt'
import UpgradeIcon from '@mui/icons-material/Upgrade'

import {
  createServer,
  createNativeInstallScript,
  createNativeInstallationFiles,
  deleteServer,
	getNativeAgentStatus,
	getNativeInstallation,
	importNativeCredential,
	listCoreReleases,
  listServers,
  listXrayVersions,
	rotateNativeCredential,
	selectCore,
  testServer,
  updateServer,
  upgradePanel,
  upgradePreview,
  upgradeXray,
	type Server,
	type NativeServerProvisioning,
	type NativeAgentStatus,
	type NativeCoreEngine,
  type NativeInstallationSelection,
  type NativeInstallationFiles,
  type PanelCapability,
  type PanelType,
	type CoreRelease,
  type UpgradePreviewResult,
  type XUIAuthMethod,
  type UpdateServerRequest,
} from '@/api/servers'
import { confirm } from '@/components/ConfirmHost'
import PageHeader from '@/components/PageHeader'
import { pushSnack } from '@/components/SnackbarHost'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import { SortableTableCell } from '@/components/SortableTableCell'
import { usePaged } from '@/hooks/usePaged'
import { ipCapBadgeTone, type IPCapTone } from '@/utils/capabilities'
import { copyToClipboard } from '@/utils/clipboard'
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
  panel_type: PanelType
  name: string
  url: string
  api_token: string
  username: string
  password: string
  remark: string
  auth_method: XUIAuthMethod
  insecure_https: boolean
  change_api_token: boolean
  change_password: boolean
  show_api_token: boolean
  show_password: boolean
}

const EMPTY_FORM: FormState = {
  name: '', url: '', api_token: '', username: '', password: '', remark: '',
  panel_type: 'psp',
  auth_method: 'token', insecure_https: false,
  change_api_token: false, change_password: false,
  show_api_token: false, show_password: false,
}

const DEFAULT_INSTALLATION: NativeInstallationSelection = { method: 'linux', os: 'linux', arch: 'amd64' }

function credentialsConfigured(s: Server): boolean {
	return s.panel_type === 'psp' || s.has_api_token || s.has_password
}

function hasCapability(s: Server | null, capability: PanelCapability): boolean {
  return !!s?.capabilities?.includes(capability)
}

export default function ServersView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t, i18n } = useTranslation(['admin', 'common'])

  const [search, setSearch] = useState('')
  const [probeStates, setProbeStates] = useState<Record<number, ProbeState>>({})
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState<'test' | 'delete' | 'upgrade_xray' | 'upgrade_panel' | ''>('')
  const [singleTesting, setSingleTesting] = useState<number | null>(null)
  // Upgrade action state. menuAnchor + menuTarget power the kebab-menu
  // overlay (one global menu re-anchored per row). upgrading marks a panel
  // whose upgrade-panel / upgrade-xray request is in flight so we can
  // disable the corresponding menu item.
  const [menuAnchor, setMenuAnchor] = useState<HTMLElement | null>(null)
  const [menuTarget, setMenuTarget] = useState<Server | null>(null)
  const [upgrading, setUpgrading] = useState<number | null>(null)
  // Core selection dialog state. Legacy 3X-UI lazy-loads its Xray list and
  // may use "latest"; native PSP nodes load the shared audited engine catalog,
  // require an exact version, and fail closed if the catalog is unavailable.
  const [coreDialogTarget, setCoreDialogTarget] = useState<Server | null>(null)
  const [coreVersionPick, setCoreVersionPick] = useState<string>('')
  const [legacyXrayVersions, setLegacyXrayVersions] = useState<string[]>([])
	const [coreReleases, setCoreReleases] = useState<CoreRelease[]>([])
	const [coreEnginePick, setCoreEnginePick] = useState<NativeCoreEngine>('xray')
  const [coreVersionsLoading, setCoreVersionsLoading] = useState(false)
	const nativeCoreDialog = coreDialogTarget?.panel_type === 'psp'
	const selectedCoreRelease = coreReleases.find(release => release.engine === coreEnginePick && release.version === coreVersionPick)
	const selectableCoreVersions = nativeCoreDialog
		? coreReleases.filter(release => release.engine === coreEnginePick).map(release => release.version)
		: legacyXrayVersions

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<Server | null>(null)
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
	const [busy, setBusy] = useState(false)
	const [nativeInstallationTarget, setNativeInstallationTarget] = useState<Server | null>(null)
	const [nativeUpgradeTarget, setNativeUpgradeTarget] = useState<Server | null>(null)
	const [nativeProvisioning, setNativeProvisioning] = useState<NativeServerProvisioning | null>(null)
  const [nativeCreationFlow, setNativeCreationFlow] = useState(false)
  const [installationSelection, setInstallationSelection] = useState<NativeInstallationSelection>(DEFAULT_INSTALLATION)
  const nativeInstallationIntent = useRef(0)
  type ServerField = 'name' | 'url' | 'api_token' | 'password'
  const [fieldErr, setFieldErr] = useState<FieldErrors<ServerField>>({})

  // Paged server list. Substring search now lives on the backend
  // (matches name/url/remark/username case-insensitively).
  const fetchServers = useCallback(
    async (req: { page: number; page_size: number; keyword: string; sort_by: string; sort_dir: 'asc' | 'desc' }, signal: AbortSignal) => {
      const res = await listServers({
        page: req.page,
        page_size: req.page_size,
        keyword: req.keyword || undefined,
        sort_by: req.sort_by || undefined,
        sort_dir: req.sort_dir,
      }, signal)
      return {
        items: res.items,
        total: res.total,
        page: res.page ?? req.page,
        page_size: res.page_size ?? req.page_size,
      }
    },
    [],
  )
  const paged = usePaged<Server>(fetchServers, { defaultSortBy: 'id', defaultSortDir: 'asc' })
  const { items, total, loading, page, pageSize, sortBy, sortDir, setPage, setPageSize, setKeyword, setSort, refresh, mutateItems } = paged
  // pageIdsKey is a stable string keyed off the set of row IDs on the
  // current page. Crucial guard for the side-effect hooks below: each
  // probe response calls mutateItems() to merge fresh version data
  // back into the row, which produces a new `items` reference — if the
  // effect depended on `items` directly we'd loop forever (probe →
  // mutate → effect → probe → ...). Deriving from IDs only means
  // content updates don't retrigger us; the effect fires exactly once
  // per page / search / sort change, which is what we want.
  const pageIdsKey = useMemo(() => items.map(s => s.id).join('|'), [items])
  useEffect(() => {
    void allSettledLimited(items, s => probeServer(s))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pageIdsKey])
  // Reset selection on page change — selection state is per-id and
  // shouldn't carry batch actions onto rows admin can no longer see.
  useEffect(() => { setSelected(new Set()) }, [pageIdsKey])

  const selectedCount = selected.size
	const selectedCanUpgradeCore = items.some(s => selected.has(s.id) && s.panel_type !== 'psp' && s.capabilities?.includes('core.upgrade'))
  const selectedCanUpgradePanel = items.some(s => selected.has(s.id) && s.capabilities?.includes('panel.upgrade'))
  // Header checkbox reflects the *visible* page only.
  const allChecked = items.length > 0 && items.every(s => selected.has(s.id))
  const someChecked = items.some(s => selected.has(s.id)) && !allChecked

  function load() { refresh() }

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
        // The IP-cap probe rides the same click, so an admin who just
        // installed fail2ban sees the badge clear here rather than after the
        // next traffic-poll tick. Absent means the probe produced nothing usable
        // and the stored state was deliberately left alone — keep the old
        // value rather than blanking the badge on a blip.
        if (r.panel_version !== undefined || r.compat_status || r.ip_limit_enforcement) {
          mutateItems(prev => prev.map(it => it.id === s.id ? {
            ...it,
            panel_version: r.panel_version ?? it.panel_version,
            xray_version: r.xray_version ?? it.xray_version,
						core_engine: r.core_engine ?? it.core_engine,
						core_version: r.core_version ?? it.core_version,
            version_checked_at: r.version_checked_at ?? it.version_checked_at,
            compat_status: r.compat_status ?? it.compat_status,
            compat_message: r.compat_message ?? it.compat_message,
            ip_limit_enforcement: r.ip_limit_enforcement ?? it.ip_limit_enforcement,
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
    // Set BEFORE the await confirm() so the kebab menu's
    // `disabled={upgrading === s.id}` immediately blocks a double-click
    // from stacking two confirm dialogs. Pre-v3.6.0-beta.6 the set
    // happened after the await — admin double-clicking ⋮ → "升级面板"
    // during the confirm modal could fire two upgrade POSTs.
    setUpgrading(s.id)
    try {
      if (!force) {
        closeMenu()
        // Read-only pre-flight: target version + tested-range check + advisory,
        // so the confirm dialog warns about breaking changes (especially ones
        // that also restart/upgrade the bundled Xray) BEFORE anything fires. If
        // the preview itself fails (panel unreachable), fall through to a generic
        // confirm — UpgradePanel's own gate still protects the actual fire.
        let preview: UpgradePreviewResult | null = null
        try {
          preview = await upgradePreview(s.id)
        } catch {
          preview = null
        }
        if (preview?.already_latest) {
          // Nothing to upgrade — report and skip the confirm + the fire entirely.
          pushSnack(
            t('admin:servers.toast.upgrade_panel_already_latest', {
              version: preview.current_version ?? '',
              defaultValue: '已是最新版（{{version}}），无需升级',
            }),
            'success',
          )
          return
        }
        const advisory = preview?.advisory
        const lines = [
          t('admin:servers.confirm.upgrade_panel_message', {
            name: s.name,
            defaultValue: 'Passwall Panel 将先检查目标版本是否在已测试范围内，在范围内才会触发 {{name}} 的自升级。面板会重启，约 60 秒后 Passwall Panel 跑 smoke probe 验证。是否继续？',
          }),
        ]
        if (preview?.target_version) {
          lines.push(t('admin:servers.confirm.upgrade_target', {
            target: preview.target_version,
            defaultValue: '目标版本：{{target}}',
          }))
        }
        if (advisory?.text) {
          lines.push('⚠️ ' + advisory.text)
        }
        if (advisory?.affects_xray) {
          lines.push(t('admin:servers.confirm.upgrade_affects_xray', {
            defaultValue: '此升级会同时重启并升级内置 Xray-core，升级前请确认相关 inbound 与新版兼容。',
          }))
        }
        const ok = await confirm({
          title: t('admin:servers.confirm.upgrade_panel_title', { defaultValue: '升级 3X-UI 面板' }),
          message: lines.join('\n\n'),
          confirmText: t('admin:servers.action.upgrade', { defaultValue: '升级' }),
          // A "warning" advisory (or any breaking change flagged for Xray) makes
          // the confirm destructive-styled so the admin slows down.
          destructive: advisory?.severity === 'warning' || advisory?.affects_xray,
        })
        if (!ok) return // finally clears setUpgrading
      }
      const r = await upgradePanel(s.id, { force })
      if (r.already_latest) {
        pushSnack(
          t('admin:servers.toast.upgrade_panel_already_latest', {
            version: r.current_version ?? '',
            defaultValue: '已是最新版（{{version}}），无需升级',
          }),
          'success',
        )
      } else {
        pushSnack(
          t('admin:servers.toast.upgrade_panel_started', {
            target: r.target_version ?? '?',
            defaultValue: '已发起 3X-UI 升级到 {{target}}，约 60 秒后 Passwall Panel 跑 smoke probe，结果写入 audit log',
          }),
          'success',
        )
      }
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
            defaultValue: '即将升级到 {{latest}}。当前 Passwall Panel 已验证的最高 3X-UI 版本为 {{max}}。强制升级可能因协议或字段变更导致 traffic poll、reconcile 等关键流程失败。建议先升级 Passwall Panel 至支持该 3X-UI 版本的发行，再升级面板。是否仍要强制升级？',
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

  // openCoreDialog replaces the v3.6.0-beta.7 simple confirm flow. The
  // dialog hosts a version dropdown so admin can pin a specific xray-core
  // tag (or keep "latest"); version list is lazy-loaded from the panel's
  // own /server/getXrayVersion the moment the dialog opens.
  async function openCoreDialog(s: Server) {
    closeMenu()
    setCoreDialogTarget(s)
		setCoreVersionPick('') // "" = latest for legacy 3X-UI only
		setCoreEnginePick('xray')
    setLegacyXrayVersions([])
		setCoreReleases([])
    setCoreVersionsLoading(true)
    try {
			if (s.panel_type === 'psp') {
				const releases = await listCoreReleases(s.id)
				setCoreReleases(releases)
				const engine = s.desired_core_engine ?? s.core_engine ?? 'xray'
				setCoreEnginePick(engine)
				const selected = releases.find(release => release.engine === engine && release.version === s.desired_core_version)
					?? releases.find(release => release.engine === engine && release.tier === 'recommended')
					?? releases.find(release => release.engine === engine)
				setCoreVersionPick(selected?.version ?? '')
			} else {
				const result = await listXrayVersions(s.id)
				setLegacyXrayVersions(result.versions)
				setCoreReleases(result.releases)
			}
    } catch {
      // Panel unreachable / endpoint failed — dialog still opens with
      // just the "latest" pseudo-option so admin can still upgrade.
      setLegacyXrayVersions([])
			setCoreReleases([])
    } finally {
      setCoreVersionsLoading(false)
    }
  }

  function closeCoreDialog() {
    setCoreDialogTarget(null)
		setCoreReleases([])
  }

	function changeCoreEngine(engine: NativeCoreEngine) {
		setCoreEnginePick(engine)
		const release = coreReleases.find(item => item.engine === engine && item.tier === 'recommended')
			?? coreReleases.find(item => item.engine === engine)
		setCoreVersionPick(release?.version ?? '')
	}

  async function submitCoreSelection() {
    const s = coreDialogTarget
    if (!s) return
    setUpgrading(s.id)
    try {
			const selectedRelease = coreReleases.find(release => release.engine === coreEnginePick && release.version === coreVersionPick)
			let confirmRestricted = false
			if (s.panel_type === 'psp') {
				if (!coreVersionPick) return
				if (selectedRelease?.requires_confirmation) {
					const accepted = await confirm({
						title: t('admin:servers.confirm.restricted_core_title'),
						message: t('admin:servers.confirm.restricted_core_message', {
							version: selectedRelease.version,
							summary: i18n.language.startsWith('zh') ? selectedRelease.summary.zh_cn : selectedRelease.summary.en,
						}),
						confirmText: t('admin:servers.confirm.restricted_core_confirm'),
					})
					if (!accepted) return
					confirmRestricted = true
				}
			}
			const r = s.panel_type === 'psp'
				? await selectCore(s.id, coreEnginePick, coreVersionPick, { confirmRestricted })
				: await upgradeXray(s.id, coreVersionPick || undefined, { confirmRestricted })
			if (s.panel_type === 'psp') {
				mutateItems(previous => previous.map(item => item.id === s.id ? {
					...item,
					desired_core_engine: coreEnginePick,
					desired_core_version: coreVersionPick,
				} : item))
			}
      pushSnack(
				s.panel_type === 'psp'
					? t('admin:servers.toast.core_intent_saved', { engine: coreEnginePick === 'sing-box' ? 'sing-box' : 'Xray', version: r.version })
					: t('admin:servers.toast.upgrade_xray_ok', { version: r.version ?? 'latest' }),
        'success',
      )
      // Backend already refreshed UpdateVersion server-side; probe to
      // pull the latest snapshot into the items list.
			if (s.panel_type !== 'psp') void probeServer(s)
      closeCoreDialog()
    } catch (err) {
      const msg = (err as { response?: { data?: { error?: string } }; message?: string }).response?.data?.error
        ?? (err as { message?: string }).message
        ?? 'unknown'
      pushSnack(msg, 'error')
    } finally {
      setUpgrading(null)
    }
  }

	async function rotateCredential(s: Server) {
		const installationIntent = nativeInstallationIntent.current
		closeMenu()
		const accepted = await confirm({
			title: t('admin:servers.native.rotate_title'),
			message: t('admin:servers.native.rotate_warning', { name: s.name }),
			destructive: true,
			confirmText: t('admin:servers.native.rotate_confirm'),
		})
		if (!accepted) return
		setUpgrading(s.id)
		try {
			const provisioned = await rotateNativeCredential(s.id)
      // The server may have committed this rotation after the administrator
      // closed or switched installation dialogs. Keep that newer UI intent;
      // its credential can be retrieved later by explicitly reopening the node.
			if (installationIntent === nativeInstallationIntent.current) openNativeInstallation(provisioned.server, provisioned, nativeCreationFlow)
			pushSnack(t('admin:servers.native.rotated'), 'success')
		} catch (err) {
			const message = (err as { response?: { data?: { error?: string } }; message?: string }).response?.data?.error
				?? (err as { message?: string }).message
				?? 'unknown'
			pushSnack(message, 'error')
		} finally {
			setUpgrading(null)
		}
	}

  function openNativeInstallation(server: Server, provisioned: NativeServerProvisioning | null = null, creating = false) {
    nativeInstallationIntent.current += 1
    closeMenu()
    setNativeProvisioning(provisioned)
    setNativeInstallationTarget(server)
    setNativeCreationFlow(creating)
    if (!creating) setDialogOpen(false)
  }

  function closeNativeInstallation() {
    nativeInstallationIntent.current += 1
    setNativeInstallationTarget(null)
    setNativeProvisioning(null)
    if (nativeCreationFlow) setDialogOpen(false)
    setNativeCreationFlow(false)
  }

  function openCreate() {
    closeNativeInstallation()
    setEditing(null)
    setInstallationSelection(DEFAULT_INSTALLATION)
    setForm({ ...EMPTY_FORM, change_api_token: true, change_password: true })
    setFieldErr({})
    setDialogOpen(true)
  }

  function openEdit(s: Server) {
    closeNativeInstallation()
    setEditing(s)
    setForm({
      ...EMPTY_FORM,
      name: s.name,
      panel_type: s.panel_type ?? '3xui',
      url: s.url,
      username: s.username ?? '',
      remark: s.remark ?? '',
      auth_method: s.auth_method ?? 'token',
      insecure_https: !!s.insecure_https,
    })
    setFieldErr({})
    setDialogOpen(true)
  }

	function validateForm(f: FormState, isEdit: boolean): FieldErrors<ServerField> {
		if (f.panel_type === 'psp') {
			return { name: validateName(f.name, { required: true, max: 64 }), url: '', api_token: '', password: '' }
		}
    const tokenRequired = f.panel_type === 'sui' || f.auth_method === 'token'
    const tokenConfigured = isEdit && !!editing?.has_api_token && !f.change_api_token
    const passwordConfigured = isEdit && !!editing?.has_password && !f.change_password
    return {
      name: validateName(f.name, { required: true, max: 64 }),
      url: validateUrl(f.url, { required: true }),
      // Validate the credential selected by auth_method. S-UI exposes only
      // token-authenticated /apiv2, so a stale 3X-UI password must never make
      // this form appear valid after switching the adapter kind.
      api_token: tokenRequired && !f.api_token && !tokenConfigured
        ? validateRequired('', 'validation.required') : '',
      password: !tokenRequired && !f.password && !passwordConfigured
        ? validateRequired('', 'validation.required') : '',
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
		let continueInstallation = false
		if (editing) {
			const req: UpdateServerRequest = editing.panel_type === 'psp'
				? { name: form.name, remark: form.remark }
				: {
					panel_type: form.panel_type,
					url: form.url,
					name: form.name,
					username: form.username,
					remark: form.remark,
					auth_method: form.auth_method,
					insecure_https: form.insecure_https,
				}
			if (editing.panel_type !== 'psp' && form.change_api_token) req.api_token = form.api_token
			if (editing.panel_type !== 'psp' && form.change_password) req.password = form.password
        const saved = await updateServer(editing.id, req)
        mutateItems(prev => prev.map(server => server.id === saved.id ? saved : server))
        pushSnack(t('admin:servers.toast.saved'), 'success')
      } else {
			const created = await createServer(form.panel_type === 'psp'
				? { name: form.name, panel_type: 'psp', remark: form.remark || undefined }
				: {
					name: form.name, url: form.url,
					panel_type: form.panel_type,
					api_token: form.api_token || undefined,
					username: form.username || undefined,
					password: form.password || undefined,
					remark: form.remark || undefined,
					auth_method: form.auth_method,
					insecure_https: form.insecure_https,
				})
			if ('server' in created) {
          openNativeInstallation(created.server, created, true)
          continueInstallation = true
        }
        pushSnack(t('admin:servers.toast.created'), 'success')
      }
      if (!continueInstallation) setDialogOpen(false)
      if (!editing) refresh()
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
      await allSettledLimited(rows, s => probeServer(s))
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
      await allSettledLimited(items, s => probeServer(s))
      pushSnack(t('admin:servers.toast.batch_tested', { count: items.length }), 'success')
    } finally {
      setBatchBusy('')
    }
  }

  // batchUpgradeXray fires installXray("latest") in parallel across the
  // selected panels. Latest-only by design — per-panel version pinning
  // stays in the single-row Upgrade Xray dialog because different
  // panels may surface different version lists, and admin asking for
  // "v25.10.31 on all" can't always be satisfied. Each panel's 3X-UI
  // resolves "latest" independently, so the post-upgrade xray versions
  // may differ by a patch but all sit at each panel's known latest.
  async function batchUpgradeXray() {
		const rows = items.filter(s => selected.has(s.id) && s.panel_type !== 'psp' && s.capabilities?.includes('core.upgrade'))
    if (!rows.length) return
    const ok = await confirm({
      title: t('admin:servers.confirm.batch_upgrade_xray_title', { defaultValue: '批量升级 Xray' }),
      message: t('admin:servers.confirm.batch_upgrade_xray_message', {
        count: rows.length,
        defaultValue: '在 {{count}} 台服务器上同时安装最新版 xray-core。每台 3X-UI 各自解析自己的最新版本，可能存在小幅差异。面板自身不重启，只重启 xray 子进程。是否继续？',
      }),
      confirmText: t('admin:servers.action.upgrade', { defaultValue: '升级' }),
    })
    if (!ok) return
    setBatchBusy('upgrade_xray')
    try {
      const results = await allSettledLimited(rows, r => upgradeXray(r.id))
      const success = results.filter(r => r.status === 'fulfilled').length
      const failed = rows.length - success
      // Refresh version snapshots for the rows that succeeded so the
      // Version column reflects the new xray-core tag without admin
      // having to click Test individually.
      void allSettledLimited(
        rows.filter((_, i) => results[i].status === 'fulfilled'),
        s => probeServer(s),
      )
      if (failed > 0) {
        pushSnack(
          t('admin:servers.toast.batch_upgrade_xray_partial', {
            ok: success, fail: failed,
            defaultValue: '已升级 {{ok}} 台 Xray，失败 {{fail}} 台',
          }),
          'warning',
        )
      } else {
        pushSnack(
          t('admin:servers.toast.batch_upgrade_xray_ok', {
            count: success,
            defaultValue: '已升级 {{count}} 台 Xray 到最新版',
          }),
          'success',
        )
      }
    } finally {
      setBatchBusy('')
    }
  }

  // batchUpgradePanel fires UpdatePanel("latest") across the selected panels.
  // UNLIKE batchUpgradeXray, a 3X-UI panel upgrade restarts the whole panel
  // process — every user on that panel drops for ~30-60s — and a new 3X-UI
  // release can schema-break the management API (docs/3xui-compat.md warns
  // against blind batch panel upgrades). So this batch path deliberately does
  // NOT force: PSP's compat gate still blocks any panel whose target version
  // is outside the tested range, and those are reported as "blocked" rather
  // than overridden. Forcing an out-of-range upgrade stays a per-panel
  // decision in the ⋮ menu (runUpgradePanel's two-step confirm), where the
  // friction is appropriate. The clean rollout: deploy a PSP build whose
  // max_tested_xui covers the target, then the gate passes for every panel
  // and this one click upgrades the whole fleet.
  async function batchUpgradePanel() {
    const rows = items.filter(s => selected.has(s.id) && s.capabilities?.includes('panel.upgrade'))
    if (!rows.length) return
    const ok = await confirm({
      title: t('admin:servers.confirm.batch_upgrade_panel_title', { defaultValue: '批量升级 3X-UI 面板' }),
      message: t('admin:servers.confirm.batch_upgrade_panel_message', {
        count: rows.length,
        defaultValue: '将对 {{count}} 台面板触发 3X-UI 自升级到最新版。每台面板会重启，其上所有用户断连约 30–60 秒；约 60 秒后各自跑 smoke probe，结果写入 audit log。目标版本超出 Passwall Panel 已测试范围的面板会被拦截（不强制）——如需强制，请用单台 ⋮ 菜单逐台确认。是否继续？',
      }),
      confirmText: t('admin:servers.action.upgrade', { defaultValue: '升级' }),
    })
    if (!ok) return
    setBatchBusy('upgrade_panel')
    try {
      const results = await allSettledLimited(rows, r => upgradePanel(r.id))
      let initiated = 0
      let alreadyLatest = 0
      let blocked = 0
      let failed = 0
      results.forEach(res => {
        if (res.status === 'fulfilled') {
          if (res.value?.already_latest) alreadyLatest++
          else initiated++
          return
        }
        const resp = (res.reason as { response?: { status?: number; data?: { reason?: string } } }).response
        if (resp?.status === 409 && resp.data?.reason === 'untested_target') blocked++
        else failed++
      })
      if (blocked === 0 && failed === 0) {
        pushSnack(
          alreadyLatest > 0
            ? t('admin:servers.toast.batch_upgrade_panel_ok_latest', {
                count: initiated, latest: alreadyLatest,
                defaultValue: '已发起 {{count}} 台 3X-UI 升级；{{latest}} 台已是最新、跳过',
              })
            : t('admin:servers.toast.batch_upgrade_panel_ok', {
                count: initiated,
                defaultValue: '已发起 {{count}} 台 3X-UI 升级，约 60 秒后各自跑 smoke probe（结果见 audit log）',
              }),
          'success',
        )
      } else {
        pushSnack(
          t('admin:servers.toast.batch_upgrade_panel_partial', {
            ok: initiated, latest: alreadyLatest, blocked, fail: failed,
            defaultValue: '已发起 {{ok}} 台；{{latest}} 台已是最新；{{blocked}} 台超出已测范围被拦截（可逐台强制）；{{fail}} 台失败',
          }),
          'warning',
        )
      }
      // Panel upgrade restarts each selected panel (disruptive — drops every
      // user on it). Clear the selection on completion so an accidental
      // immediate re-click can't re-fire it without fresh intent, mirroring
      // batchDeleteServers. (batchUpgradeXray/batchRunTest keep their selection
      // — they're non-disruptive and re-running them is harmless.)
      setSelected(new Set())
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
      const results = await allSettledLimited(rows, r => deleteServer(r.id))
      const okIds = rows.filter((_, i) => results[i].status === 'fulfilled').map(r => r.id)
      const failed = rows.length - okIds.length
      setSelected(new Set())
      if (failed > 0) {
        pushSnack(t('admin:servers.toast.batch_partial', { ok: okIds.length, fail: failed }), 'warning')
      } else {
        pushSnack(t('admin:servers.toast.batch_deleted', { count: okIds.length }), 'success')
      }
      refresh()
    } finally {
      setBatchBusy('')
    }
  }

  function toggleAll(checked: boolean) {
    // Only flips the currently-visible page; selection on other pages
    // (preserved on page change) is not affected.
    setSelected(prev => {
      const next = new Set(prev)
      items.forEach(s => { if (checked) next.add(s.id); else next.delete(s.id) })
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
		if (!s.panel_version && (s.panel_type !== 'psp' || !s.desired_core_version)) {
      return <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>—</Typography>
    }
		const pendingCoreSelection = s.panel_type === 'psp' && !!s.desired_core_version &&
			(s.desired_core_engine !== s.core_engine || s.desired_core_version !== s.core_version)
    let bg = md.tertiaryContainer
    let fg = md.onTertiaryContainer
    let label: string | null = null
		if (s.panel_type !== 'psp') {
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
		}
    const versionText = (
      <Box sx={{ display: 'flex', flexDirection: 'column', lineHeight: 1.3 }}>
        <Typography sx={{ fontSize: 13, fontWeight: 500 }}>
					{s.panel_type === 'sui' ? 'S-UI' : s.panel_type === 'psp' ? 'Passwall Node' : '3X-UI'} {s.panel_version ?? ''}
        </Typography>
        {(s.panel_type === 'psp' ? s.core_version : s.xray_version) && (
          <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
						{s.panel_type === 'psp'
							? `${s.core_engine === 'sing-box' ? 'sing-box' : 'Xray'} ${s.core_version}`
							: `Xray ${s.xray_version}`}
          </Typography>
        )}
				{pendingCoreSelection && (
					<Typography sx={{ fontSize: 11, color: md.tertiary }}>
						{t('admin:servers.field.core_pending', {
							engine: s.desired_core_engine === 'sing-box' ? 'sing-box' : 'Xray',
							version: s.desired_core_version,
						})}
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
    // Update-available chip lives inside the Version column so the
    // target version is visible at a glance (the previous red-dot on
    // the ⋮ kebab told admin "something is new" but they had to hover
    // to learn what — too vague to act on). Tertiary-container coloring
    // keeps it informational, not alarming.
    const updateChip = s.update_available && s.latest_xui_version && (
      <Box sx={{
        display: 'inline-block', px: 1, py: 0.125,
        borderRadius: 1, fontSize: 11, fontWeight: 500,
        bgcolor: md.tertiaryContainer, color: md.onTertiaryContainer,
        whiteSpace: 'nowrap', mt: 0.25, ml: badge ? 0.5 : 0,
      }}>
        {t('admin:servers.update_available_chip', {
          latest: s.latest_xui_version,
          defaultValue: '可升级 → {{latest}}',
        })}
      </Box>
    )
    const stacked = (
      <Box>
        {versionText}
        {badge}
        {updateChip}
      </Box>
    )
    if (s.compat_message) {
      return <Tooltip title={s.compat_message} placement="top"><span>{stacked}</span></Tooltip>
    }
    return stacked
  }

  // Compat-warning banners split by KIND because the operator's next
  // action differs: too_old means "upgrade the panel ASAP" (panel can
  // no longer be talked to reliably); untested means "Passwall Panel
  // hasn't verified this 3X-UI version yet — may work, may not". Same
  // visual treatment (error vs warning container) reinforces the
  // distinction. "unknown" stays out — usually "never probed" or a
  // transient probe failure, not a real compat issue.
  // Compat banners filter on the current page only — surfacing rows
  // from invisible pages would be misleading. Banners only fire when
  // the admin's current view contains an offending row.
  const panelsTooOld = items.filter(s => s.compat_status === 'too_old')
  const panelsUntested = items.filter(s => s.compat_status === 'untested')
  // Reused for both banners — render "name (vX.Y.Z)" joined with 、
  // so admin can see exactly which panels triggered the warning without
  // a separate hover.
  function panelsList(panels: Server[]): string {
    return panels.map(s => `${s.name} (v${s.panel_version ?? '?'})`).join('、')
  }

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

  // ipCapBadge says whether a concurrent-IP cap pushed to this node is acted
  // on. It sits under the connection status because that is the question it
  // answers: the panel is reachable, the write succeeds, and the limit still
  // does nothing.
  //
  // Two states render NOTHING, for opposite reasons. "enforced" is the clean
  // case and needs no decoration, mirroring compat_status 'supported'. And
  // "unsupported" — a panel older than 3.7.0, which has no way to tell us —
  // would otherwise become a permanent badge on every older node, un-actionable
  // and therefore quickly unread; the version cell beside it already says the
  // panel is old.
  //
  // "unknown" DOES render, greyed. It means a panel that should have answered
  // did not, so the detector itself is broken — and a broken detector that
  // looks like a clean fleet is the exact failure this whole probe exists to
  // prevent.
  function ipCapBadge(s: Server) {
    const tone = ipCapBadgeTone(s.ip_limit_enforcement)
    if (tone === 'none') return null
    const palette: Record<Exclude<IPCapTone, 'none'>, [string, string]> = {
      error: [md.errorContainer, md.onErrorContainer],
      info: [md.secondaryContainer, md.onSecondaryContainer],
      neutral: [md.surfaceContainerHighest, md.onSurfaceVariant],
    }
    const [bg, fg] = palette[tone]
    let label: string, tip: string
    switch (s.ip_limit_enforcement) {
      case 'disabled':
        label = t('admin:servers.ip_cap.dead')
        tip = t('admin:servers.ip_cap.disabled_tip')
        break
      case 'not_installed':
        label = t('admin:servers.ip_cap.dead')
        tip = t('admin:servers.ip_cap.not_installed_tip')
        break
      case 'disconnect_only':
        label = t('admin:servers.ip_cap.disconnect_only')
        tip = t('admin:servers.ip_cap.disconnect_only_tip')
        break
      default:
        label = t('admin:servers.ip_cap.unknown')
        tip = t('admin:servers.ip_cap.unknown_tip')
    }
    return (
      <Tooltip title={tip} placement="top">
        <Box sx={{
          display: 'inline-block', px: 1, py: 0.125, mt: 0.5,
          borderRadius: 1, fontSize: 11, fontWeight: 500,
          bgcolor: bg, color: fg, whiteSpace: 'nowrap',
        }}>
          {label}
        </Box>
      </Tooltip>
    )
  }

  return (
    <Box sx={{ p: 3 }}>
      {/* Header */}
      <PageHeader
        title={t('admin:servers.title')}
        subtitle={t('admin:servers.subtitle')}
        actions={
          <>
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
          </>
        }
      />
      {/* Compat banners — split by severity (too_old = error, can't be
          relied on; untested = warning, may still work). "Unknown" panels
          (never probed / probe failing transiently) stay out of both
          banners because they usually aren't a real compat issue. */}
      {/* too_old banner ── error severity. Panel can't be relied on,
          admin action is "upgrade the panel ASAP". */}
      {panelsTooOld.length > 0 && (
        <Box sx={{
          mt: 2, p: 1.75, borderRadius: 2,
          bgcolor: md.errorContainer, color: md.onErrorContainer,
          display: 'flex', gap: 1.5, alignItems: 'flex-start',
        }}>
          <WarningAmberIcon fontSize="small" sx={{ mt: 0.25 }} />
          <Box>
            <Typography sx={{ fontSize: 13, fontWeight: 600 }}>
              {t('admin:servers.compat.banner_title_too_old', { count: panelsTooOld.length })}
            </Typography>
            <Typography sx={{ fontSize: 12, mt: 0.5, lineHeight: 1.5 }}>
              {t('admin:servers.compat.banner_body_too_old', {
                panels: panelsList(panelsTooOld),
              })}
            </Typography>
          </Box>
        </Box>
      )}
      {/* untested banner ── warning severity. Panel may work, may not;
          admin action is "check if Passwall Panel has a newer release,
          or report the new 3X-UI version for verification". */}
      {panelsUntested.length > 0 && (
        <Box sx={{
          mt: 2, p: 1.75, borderRadius: 2,
          bgcolor: md.secondaryContainer, color: md.onSecondaryContainer,
          display: 'flex', gap: 1.5, alignItems: 'flex-start',
        }}>
          <WarningAmberIcon fontSize="small" sx={{ mt: 0.25 }} />
          <Box>
            <Typography sx={{ fontSize: 13, fontWeight: 600 }}>
              {t('admin:servers.compat.banner_title_untested', { count: panelsUntested.length })}
            </Typography>
            <Typography sx={{ fontSize: 12, mt: 0.5, lineHeight: 1.5 }}>
              {t('admin:servers.compat.banner_body_untested', {
                panels: panelsList(panelsUntested),
              })}
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
            size="small" variant="text"
            startIcon={batchBusy === 'upgrade_xray' ? <CircularProgress size={14} /> : <SystemUpdateIcon />}
            disabled={batchBusy !== '' || !selectedCanUpgradeCore}
            onClick={batchUpgradeXray}
            sx={{ color: 'inherit' }}
          >
            {t('admin:servers.batch_upgrade_xray', { defaultValue: '批量升级 Xray' })}
          </Button>
          <Button
            size="small" variant="text"
            startIcon={batchBusy === 'upgrade_panel' ? <CircularProgress size={14} /> : <UpgradeIcon />}
            disabled={batchBusy !== '' || !selectedCanUpgradePanel}
            onClick={batchUpgradePanel}
            sx={{ color: 'inherit' }}
          >
            {t('admin:servers.batch_upgrade_panel', { defaultValue: '批量升级 3X-UI' })}
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
      {/* Search — Enter to commit so admin can type without firing a
          request per keystroke. */}
      <Box component="form" onSubmit={(e: FormEvent) => { e.preventDefault(); setKeyword(search) }}
        sx={{ mt: 2 }}>
        <TextField
          size="small"
          value={search}
          onChange={e => setSearch(e.target.value)}
          onBlur={() => setKeyword(search)}
          placeholder={t('admin:servers.search_placeholder', { defaultValue: '搜索名称 / URL' })}
          sx={{ width: 320, maxWidth: '100%' }}
          slotProps={{
            input: {
              startAdornment: (
                <InputAdornment position="start">
                  <SearchIcon fontSize="small" sx={{ color: md.onSurfaceVariant }} />
                </InputAdornment>
              ),
            }
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
                <SortableTableCell column="name" activeColumn={sortBy} activeDir={sortDir} onSort={setSort}>
                  {t('admin:servers.table.name')}
                </SortableTableCell>
                <SortableTableCell column="url" activeColumn={sortBy} activeDir={sortDir} onSort={setSort}>
                  {t('admin:servers.table.url')}
                </SortableTableCell>
                <TableCell>{t('admin:servers.table.status')}</TableCell>
                <SortableTableCell column="panel_version" activeColumn={sortBy} activeDir={sortDir} onSort={setSort}>
                  {t('admin:servers.table.version', { defaultValue: '版本' })}
                </SortableTableCell>
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
              {items.map(s => (
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
                  <TableCell sx={{ whiteSpace: 'nowrap' }}>
                    <Box>{statusBadge(s)}</Box>
                    {ipCapBadge(s)}
                  </TableCell>
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
                        something that restarts the remote panel. The
                        "update available" hint lives in the Version
                        column (see versionCell), not on this button,
                        so the kebab stays neutral. */}
                    {(s.panel_type === 'psp' || s.capabilities?.includes('panel.upgrade') || s.capabilities?.includes('core.upgrade')) && <IconButton
                      size="small"
                      onClick={e => openMenu(e, s)}
                      disabled={upgrading === s.id}
                      aria-label={t('admin:servers.action.more', { defaultValue: '更多操作' })}
                    >
                      {upgrading === s.id ? <CircularProgress size={14} /> : <MoreVertIcon fontSize="small" />}
                    </IconButton>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
        <PagedTableFooter
          total={total} page={page} pageSize={pageSize}
          onPageChange={setPage} onPageSizeChange={setPageSize}
        />
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
        {menuTarget?.panel_type === 'psp' && <MenuItem onClick={() => openNativeInstallation(menuTarget)}>
          <DownloadIcon fontSize="small" sx={{ mr: 1 }} />
          {t('admin:servers.action.install_node')}
        </MenuItem>}
        {menuTarget?.panel_type === 'psp' && <MenuItem onClick={() => { setNativeUpgradeTarget(menuTarget); closeMenu() }}>
          <UpgradeIcon fontSize="small" sx={{ mr: 1 }} />
          {t('admin:servers.agent_upgrade.action')}
        </MenuItem>}
        {hasCapability(menuTarget, 'panel.upgrade') && <MenuItem onClick={() => menuTarget && runUpgradePanel(menuTarget)}>
          <SystemUpdateIcon fontSize="small" sx={{ mr: 1 }} />
          {t('admin:servers.action.upgrade_panel', { defaultValue: '升级 3X-UI 面板（最新）' })}
        </MenuItem>}
        {hasCapability(menuTarget, 'core.upgrade') && <MenuItem onClick={() => menuTarget && openCoreDialog(menuTarget)}>
          <SystemUpdateIcon fontSize="small" sx={{ mr: 1 }} />
						{menuTarget?.panel_type === 'psp'
							? t('admin:servers.action.select_core')
							: t('admin:servers.action.upgrade_xray')}
        </MenuItem>}
		{menuTarget?.panel_type === 'psp' && <MenuItem onClick={() => rotateCredential(menuTarget)}>
			<VpnKeyIcon fontSize="small" sx={{ mr: 1 }} />
			{t('admin:servers.action.rotate_node_credential')}
		</MenuItem>}
      </Menu>
      {/* Legacy 3X-UI upgrades Xray in place; native PSP nodes select an
          audited engine + exact version and converge asynchronously. */}
      <Dialog
        open={!!coreDialogTarget}
        onClose={() => upgrading === null && closeCoreDialog()}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 480, maxWidth: '90vw' } }
        }}
      >
				<DialogTitle>{t(nativeCoreDialog ? 'admin:servers.confirm.select_core_title' : 'admin:servers.confirm.upgrade_xray_title')}</DialogTitle>
        <DialogContent>
          {coreDialogTarget && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, pt: 1 }}>
              <Typography variant="body2">
								{t(nativeCoreDialog
									? 'admin:servers.confirm.select_core_message'
									: 'admin:servers.confirm.upgrade_xray_message', { name: coreDialogTarget.name })}
              </Typography>
							{nativeCoreDialog && (
								<FormControl fullWidth size="small" disabled={coreVersionsLoading || upgrading !== null}>
									<InputLabel id="core-engine-select">{t('admin:servers.field.core_engine')}</InputLabel>
									<Select
										labelId="core-engine-select"
										value={coreEnginePick}
										label={t('admin:servers.field.core_engine')}
										onChange={event => changeCoreEngine(event.target.value as NativeCoreEngine)}
									>
										<MenuItem value="xray">Xray</MenuItem>
										<MenuItem value="sing-box">sing-box</MenuItem>
									</Select>
								</FormControl>
							)}
              <FormControl fullWidth size="small" disabled={coreVersionsLoading || upgrading !== null}>
                <InputLabel id="xray-version-select">
                  {t('admin:servers.field.xray_version', { defaultValue: '目标版本' })}
                </InputLabel>
                <Select
                  labelId="xray-version-select"
                  value={coreVersionPick}
                  label={t('admin:servers.field.xray_version', { defaultValue: '目标版本' })}
                  onChange={e => setCoreVersionPick(e.target.value)}
                >
									{!nativeCoreDialog && <MenuItem value="">
										{t('admin:servers.field.xray_version_latest')}
									</MenuItem>}
									{selectableCoreVersions.map(v => {
										const release = coreReleases.find(item => item.engine === coreEnginePick && item.version === v)
                    return (
                      <MenuItem key={v} value={v}>
                        {v}{release ? ` · ${t(`admin:servers.core_tier.${release.tier}`)}` : ''}
                      </MenuItem>
                    )
                  })}
                </Select>
              </FormControl>
              {coreVersionsLoading && (
                <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>
                  {t('admin:servers.field.xray_version_loading', { defaultValue: '正在加载可用版本…' })}
                </Typography>
              )}
							{nativeCoreDialog && !coreVersionsLoading && coreReleases.length === 0 && (
								<Typography variant="caption" sx={{ color: md.error }}>
									{t('admin:servers.field.core_catalog_unavailable')}
								</Typography>
							)}
							{nativeCoreDialog && selectedCoreRelease && (
								<Box sx={{ p: 1.5, borderRadius: 2, bgcolor: md.surfaceContainerHighest }}>
									<Typography variant="body2" sx={{ fontWeight: 600 }}>
										{t(`admin:servers.core_tier.${selectedCoreRelease.tier}`)}
									</Typography>
									<Typography variant="body2" sx={{ mt: 0.5 }}>
										{i18n.language.startsWith('zh') ? selectedCoreRelease.summary.zh_cn : selectedCoreRelease.summary.en}
									</Typography>
									<Typography variant="caption" component="div" sx={{ mt: 1, color: md.onSurfaceVariant }}>
										{t('admin:servers.field.core_reality_matrix', {
											xray: t(`admin:servers.core_support.${selectedCoreRelease.reality.xray}`),
											mihomo: t(`admin:servers.core_support.${selectedCoreRelease.reality.mihomo}`),
											singbox: t(`admin:servers.core_support.${selectedCoreRelease.reality.sing_box}`),
											uri: t(`admin:servers.core_support.${selectedCoreRelease.reality.uri_list}`),
										})}
									</Typography>
									<Typography variant="caption" component="div" sx={{ color: md.onSurfaceVariant }}>
										{t('admin:servers.field.core_evidence', {
											config: selectedCoreRelease.evidence.config_tested ? t('admin:servers.field.core_evidence_yes') : t('admin:servers.field.core_evidence_no'),
											handshake: selectedCoreRelease.evidence.handshake_tested ? t('admin:servers.field.core_evidence_yes') : t('admin:servers.field.core_evidence_no'),
										})}
									</Typography>
									{selectedCoreRelease.evidence.handshakes?.length ? (
										<Typography variant="caption" component="div" sx={{ color: md.onSurfaceVariant }}>
											{t('admin:servers.field.core_handshake_title')}{' '}
											{selectedCoreRelease.evidence.handshakes.map((handshake, index) => (
												<Box component="span" key={`${handshake.client}-${handshake.version}-${handshake.profile}`} title={`${handshake.profile}; ${handshake.platform}`}>
													{index > 0 ? ' · ' : ''}
													{t(`admin:servers.core_client.${handshake.client}`)} {handshake.version}{' '}
													{t(`admin:servers.core_handshake_result.${handshake.result}`)}
												</Box>
											))}
										</Typography>
									) : null}
								</Box>
							)}
            </Box>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={closeCoreDialog} disabled={upgrading !== null} variant="text">
            {t('common:actions.cancel')}
          </Button>
          <Button
            onClick={submitCoreSelection}
            variant="contained"
				disabled={upgrading !== null || (nativeCoreDialog && (coreVersionsLoading || !coreVersionPick))}
            startIcon={upgrading !== null ? <CircularProgress size={16} color="inherit" /> : null}
          >
            {nativeCoreDialog
              ? t('admin:servers.action.select_core')
              : t('admin:servers.action.upgrade', { defaultValue: '升级' })}
          </Button>
        </DialogActions>
      </Dialog>
      {/* Create/Edit dialog */}
      <Dialog
        open={dialogOpen}
        onClose={() => !busy && (nativeCreationFlow ? closeNativeInstallation() : setDialogOpen(false))}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: nativeCreationFlow ? 760 : 600, maxWidth: '94vw' } }
        }}
      >
        <DialogTitle>
          {nativeCreationFlow
            ? t('admin:servers.native.install_title', { name: nativeInstallationTarget?.name ?? '' })
            : editing ? t('admin:servers.edit_title', { name: editing.name }) : t('admin:servers.create')}
          {!editing && form.panel_type === 'psp' && <Stepper activeStep={nativeCreationFlow ? 1 : 0} sx={{ mt: 2 }}>
            <Step><StepLabel>{t('admin:servers.native.step_details')}</StepLabel></Step>
            <Step><StepLabel>{t('admin:servers.native.step_install')}</StepLabel></Step>
          </Stepper>}
        </DialogTitle>
        {nativeCreationFlow ? <NativeInstallationDialog
          embedded server={nativeInstallationTarget} initialProvisioning={nativeProvisioning}
          initialSelection={installationSelection} onClose={closeNativeInstallation}
          onRotate={server => void rotateCredential(server)} rotating={upgrading === nativeInstallationTarget?.id}
        /> : <>
        <DialogContent>
          <Box component="form" id="server-form" onSubmit={submit} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            <TextField select fullWidth
              label={t('admin:servers.field.panel_type', { defaultValue: '服务器类型' })}
              value={form.panel_type}
              disabled={!!editing || busy}
              onChange={e => {
                const panelType = e.target.value as PanelType
                setForm(prev => panelType === 'sui'
                  ? {
                      ...prev, panel_type: panelType, auth_method: 'token', username: '', password: '',
                      change_password: false, show_password: false,
                    }
                  : panelType === 'psp'
                    ? {
                        ...prev, panel_type: panelType, url: '', api_token: '', username: '', password: '',
                        auth_method: '', insecure_https: false, change_api_token: false, change_password: false,
                      }
                    : { ...prev, panel_type: panelType, auth_method: prev.auth_method || 'token' })
                setFieldErr(prev => ({ ...prev, api_token: '', password: '' }))
              }}>
              <MenuItem value="psp">Passwall Node</MenuItem>
              <MenuItem value="3xui">3X-UI</MenuItem>
              <MenuItem value="sui">S-UI</MenuItem>
            </TextField>
            <TextField
              fullWidth required
              label={t('admin:servers.field.name')}
              placeholder={t('admin:servers.placeholder.name')}
              value={form.name}
              onChange={e => setForm({ ...form, name: e.target.value })}
              error={!!fieldErr.name}
              helperText={fieldErr.name ? t(`admin:${fieldErr.name}`) : t('admin:servers.hint.name')}
            />

            {form.panel_type === 'psp' ? (
              <>
              <Alert severity="info">
                {t('admin:servers.native.outbound_hint')}
              </Alert>
              {!editing && <NativeInstallationMethodFields selection={installationSelection} onChange={setInstallationSelection} disabled={busy} />}
              </>
            ) : <>
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
              <TextField select fullWidth
                label={t('admin:servers.field.auth_method', { defaultValue: '认证方式' })}
                value={form.auth_method}
                onChange={e => setForm({ ...form, auth_method: e.target.value as XUIAuthMethod })}>
                <MenuItem value="token">{t('admin:servers.auth_method.token', { defaultValue: 'API Token' })}</MenuItem>
                {form.panel_type === '3xui' && <MenuItem value="password">{t('admin:servers.auth_method.password', { defaultValue: '账户密码' })}</MenuItem>}
              </TextField>

              {form.auth_method === 'token' ? (
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
                  error={!!fieldErr.api_token}
                  helperText={fieldErr.api_token ? t(`admin:${fieldErr.api_token}`) : ''}
                />
              ) : <>
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
                  error={!!fieldErr.password}
                  helperText={fieldErr.password ? t(`admin:${fieldErr.password}`) : ''}
                />
              </>}

              <Box>
                <FormControlLabel sx={{
                  ml: 0,
                  '& .MuiFormControlLabel-label': { ml: 1, color: md.error },
                }}
                  label={t('admin:servers.field.insecure_https', { defaultValue: '允许不安全的 HTTPS（危险！）' })}
                  control={<Switch checked={form.insecure_https}
                    onChange={(_, checked) => setForm({ ...form, insecure_https: checked })} />} />
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, ml: 0.25, mt: 0.75 }}>
                  {t('admin:servers.hint.insecure_https', { defaultValue: '面板使用自签名 / 域名不匹配证书时开启。仅对该面板生效，不影响 SSRF 防护。' })}
                </Typography>
              </Box>
            </>}

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
            {t(!editing && form.panel_type === 'psp' ? 'admin:servers.native.create_continue' : 'common:actions.ok')}
          </Button>
        </DialogActions>
        </>}
      </Dialog>
      <NativeAgentUpgradeDialog server={nativeUpgradeTarget} onClose={() => { setNativeUpgradeTarget(null); refresh() }} />
      <NativeInstallationDialog
        server={nativeCreationFlow ? null : nativeInstallationTarget}
        initialProvisioning={nativeCreationFlow ? null : nativeProvisioning}
        onClose={closeNativeInstallation}
        onRotate={server => void rotateCredential(server)}
        rotating={upgrading === nativeInstallationTarget?.id}
      />
    </Box>
  );
}

function NativeInstallationMethodFields({ selection, onChange, disabled = false }: {
  selection: NativeInstallationSelection
  onChange: (selection: NativeInstallationSelection) => void
  disabled?: boolean
}) {
  const { t } = useTranslation('admin')
  return <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
    <TextField select fullWidth label={t('admin:servers.native.method_label')} value={selection.method} disabled={disabled}
      onChange={event => onChange({ ...selection, method: event.target.value as NativeInstallationSelection['method'] })}>
      {(['linux', 'docker', 'manual'] as const).map(method => <MenuItem key={method} value={method}>{t(`admin:servers.native.method.${method}`)}</MenuItem>)}
    </TextField>
    {selection.method === 'manual' && <Box sx={{ display: 'flex', flexDirection: { xs: 'column', sm: 'row' }, gap: 1.5 }}>
      <TextField select fullWidth label={t('admin:servers.native.platform_label')} value={selection.os} disabled={disabled}
        onChange={event => onChange({ ...selection, os: event.target.value as NativeInstallationSelection['os'] })}>
        {(['linux', 'darwin', 'windows'] as const).map(os => <MenuItem key={os} value={os}>{t(`admin:servers.native.platform.${os}`)}</MenuItem>)}
      </TextField>
      <TextField select fullWidth label={t('admin:servers.native.architecture')} value={selection.arch} disabled={disabled}
        onChange={event => onChange({ ...selection, arch: event.target.value as NativeInstallationSelection['arch'] })}>
        <MenuItem value="amd64">amd64 (x86_64)</MenuItem>
        <MenuItem value="arm64">arm64 (aarch64)</MenuItem>
      </TextField>
    </Box>}
    <Alert severity="info" sx={{ whiteSpace: 'pre-line' }}>
      {t(`admin:servers.native.method_hint.${selection.method}`)}
      <Typography variant="body2" sx={{ mt: 1, whiteSpace: 'pre-line' }}>{t(`admin:servers.native.method_steps.${selection.method}`)}</Typography>
    </Alert>
  </Box>
}

export function isNodeReleaseVersion(version: string): boolean {
  if (!/^v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.test(version)) return false
  const prerelease = version.slice(version.indexOf('-') + 1)
  return !version.includes('-') || prerelease.split('.').every(segment => !/^0\d+$/.test(segment))
}

function installationErrorMessage(error: unknown, fallback: string): string {
  const err = error as { response?: { data?: unknown }; message?: string } | null | undefined
  let data = err?.response?.data
  if (typeof data === 'string') {
    try { data = JSON.parse(data) as unknown } catch { return fallback }
  }
  if (data && typeof data === 'object' && 'error' in data && typeof data.error === 'string') return data.error
  return err?.message ?? fallback
}

function isCredentialUnavailable(error: unknown): boolean {
  const err = error as { response?: { status?: number; data?: { code?: string } } } | null | undefined
  return err?.response?.status === 409 && err.response.data?.code === 'node_credential_unavailable'
}

function downloadInstallationFile(name: string, content: string, type = 'text/plain;charset=utf-8') {
  const url = URL.createObjectURL(new Blob([content], { type }))
  try {
    const link = document.createElement('a')
    link.href = url
    link.download = name
    document.body.appendChild(link)
    link.click()
    link.remove()
  } finally { URL.revokeObjectURL(url) }
}

interface NativeInstallationDialogProps {
  server: Server | null
  initialProvisioning: NativeServerProvisioning | null
  onClose: () => void
  onRotate: (server: Server) => void
  rotating?: boolean
  embedded?: boolean
  initialSelection?: NativeInstallationSelection
}

// Installation secrets live only in this open administrator dialog. Reopening
// reads the same identity/credential; rotation is an explicit, separate action.
export function NativeInstallationDialog({ server, initialProvisioning, onClose, onRotate, rotating = false, embedded = false, initialSelection = DEFAULT_INSTALLATION }: NativeInstallationDialogProps) {
  const { t } = useTranslation(['admin', 'common'])
  const md = useTheme().palette.md
  const [provisioning, setProvisioning] = useState<NativeServerProvisioning | null>(null)
  const [installationLoading, setInstallationLoading] = useState(false)
  const [installationError, setInstallationError] = useState('')
  const [credentialUnavailable, setCredentialUnavailable] = useState(false)
  const [oldCredential, setOldCredential] = useState('')
  const [credentialBusy, setCredentialBusy] = useState(false)
  const [credentialError, setCredentialError] = useState('')
  const [version, setVersion] = useState('')
  const [scriptBusy, setScriptBusy] = useState<'copy' | 'download' | ''>('')
  const [scriptError, setScriptError] = useState('')
  const [selection, setSelection] = useState<NativeInstallationSelection>(initialSelection)
  const [files, setFiles] = useState<NativeInstallationFiles | null>(null)
  const [filesBusy, setFilesBusy] = useState(false)
  const [filesError, setFilesError] = useState('')
  const [status, setStatus] = useState<NativeAgentStatus | null>(null)
  const [statusError, setStatusError] = useState('')
  const [installationReload, setInstallationReload] = useState(0)
  const credentialRequest = useRef<AbortController | null>(null)
  const scriptRequest = useRef<AbortController | null>(null)
  const filesRequest = useRef<AbortController | null>(null)
  const serverID = server?.id
  const versionValid = isNodeReleaseVersion(version.trim())

  useEffect(() => {
    const controller = new AbortController()
    setProvisioning(serverID === undefined ? null : initialProvisioning)
    setInstallationError('')
    setCredentialUnavailable(false)
    setOldCredential('')
    setCredentialError('')
    setCredentialBusy(false)
    setVersion('')
    setSelection(initialSelection)
    setFiles(null)
    setFilesBusy(false)
    setFilesError('')
    setScriptError('')
    setScriptBusy('')
    setInstallationLoading(serverID !== undefined && !initialProvisioning)
    if (serverID !== undefined && !initialProvisioning) {
      void getNativeInstallation(serverID, controller.signal).then(data => {
        if (!controller.signal.aborted) setProvisioning(data)
      }).catch(error => {
        if (controller.signal.aborted) return
        if (isCredentialUnavailable(error)) setCredentialUnavailable(true)
        else setInstallationError(installationErrorMessage(error, t('admin:servers.native.installation_failed')))
      }).finally(() => {
        if (!controller.signal.aborted) setInstallationLoading(false)
      })
    }
    return () => {
      controller.abort()
      credentialRequest.current?.abort()
      scriptRequest.current?.abort()
      filesRequest.current?.abort()
    }
    // Request lifetime is scoped to this installation identity, not translated labels.
  }, [serverID, initialProvisioning, installationReload])

  useEffect(() => {
    setStatus(null)
    setStatusError('')
    if (serverID === undefined) return
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    async function poll() {
      try {
        const next = await getNativeAgentStatus(serverID!, controller.signal)
        if (!controller.signal.aborted) {
          setStatus(next)
          setStatusError('')
        }
      } catch (error) {
        if (!controller.signal.aborted) setStatusError(installationErrorMessage(error, t('admin:servers.native.status_failed')))
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(() => void poll(), 4000)
      }
    }
    void poll()
    return () => { controller.abort(); if (timer !== undefined) clearTimeout(timer) }
  }, [serverID])

  async function saveOldCredential() {
    if (!server || !oldCredential.trim() || credentialBusy) return
    const controller = new AbortController()
    credentialRequest.current = controller
    setCredentialBusy(true)
    setCredentialError('')
    try {
      await importNativeCredential(server.id, oldCredential.trim(), controller.signal)
      const data = await getNativeInstallation(server.id, controller.signal)
      if (!controller.signal.aborted) {
        setProvisioning(data)
        setCredentialUnavailable(false)
        setOldCredential('')
      }
    } catch (error) {
      if (!controller.signal.aborted) setCredentialError(installationErrorMessage(error, t('admin:servers.native.import_failed')))
    } finally {
      if (!controller.signal.aborted) setCredentialBusy(false)
    }
  }

  async function deliverScript(action: 'copy' | 'download') {
    if (!server || !provisioning || !versionValid || scriptBusy) return
    const controller = new AbortController()
    scriptRequest.current = controller
    setScriptBusy(action)
    setScriptError('')
    try {
      const script = await createNativeInstallScript(server.id, version.trim(), controller.signal)
      if (controller.signal.aborted) return
      if (typeof script !== 'string' || !script.trim()) throw new Error(t('admin:servers.native.script_failed'))
      if (action === 'copy') await copyToClipboard(script)
      else downloadInstallationFile(`passwall-node-install-${provisioning.agent_id}.sh`, script, 'text/x-shellscript;charset=utf-8')
    } catch (error) {
      if (!controller.signal.aborted) setScriptError(installationErrorMessage(error, t('admin:servers.native.script_failed')))
    } finally {
      if (!controller.signal.aborted) setScriptBusy('')
    }
  }

  // A changed method/platform/version invalidates every in-flight secret
  // response. Reuse the server identity, not an old installer's returned files.
  function invalidateMaterials() {
    scriptRequest.current?.abort()
    filesRequest.current?.abort()
    setScriptBusy('')
    setScriptError('')
    setFiles(null)
    setFilesBusy(false)
    setFilesError('')
  }

  async function generateFiles() {
    if (!server || !provisioning || !versionValid || selection.method === 'linux' || filesBusy || rotating) return
    const controller = new AbortController()
    filesRequest.current = controller
    setFilesBusy(true)
    setFilesError('')
    setFiles(null)
    try {
      const result = await createNativeInstallationFiles(server.id, version.trim(), selection, controller.signal)
      if (controller.signal.aborted) return
      if (result.method !== selection.method ||
        (selection.method === 'manual' ? result.os !== selection.os || result.arch !== selection.arch : result.os !== 'linux') ||
        !Array.isArray(result.files) || !result.files.length ||
        result.files.some(file => !/^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/.test(file.name) || typeof file.content !== 'string') ||
        !Array.isArray(result.steps)) throw new Error(t('admin:servers.native.files_failed'))
      setFiles(result)
    } catch (error) {
      if (!controller.signal.aborted) setFilesError(installationErrorMessage(error, t('admin:servers.native.files_failed')))
    } finally {
      if (!controller.signal.aborted) setFilesBusy(false)
    }
  }

  const statusSeverity = status?.state === 'running' ? 'success' : status?.state === 'error' ? 'error' : status?.state === 'offline' ? 'warning' : 'info'

  const content = <>
    <DialogContent sx={{ display: 'flex', flexDirection: 'column', gap: 2, pt: '12px !important' }}>
      <Alert severity="warning">{t('admin:servers.native.private_warning')}</Alert>
      <NativeInstallationMethodFields selection={selection} disabled={rotating} onChange={next => { invalidateMaterials(); setSelection(next) }} />
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
        <Typography variant="subtitle1">{t('admin:servers.native.status_title')}</Typography>
        {statusError && <Alert severity="warning">{t('admin:servers.native.status_stale')} {statusError}</Alert>}
        <Alert severity={statusSeverity}>
          {t(`admin:servers.native.agent_status.${status?.state ?? 'checking'}`)}
          {status?.last_seen && <Typography variant="body2">{t('admin:servers.native.last_seen', { time: status.last_seen })}</Typography>}
          {status && <Typography variant="body2">{t('admin:servers.native.configured_nodes', { count: status.configured_nodes })}</Typography>}
          {status?.core_state && <Typography variant="body2">{t('admin:servers.native.core_state', { state: status.core_state })}</Typography>}
        </Alert>
        <Typography variant="body2">{t('admin:servers.native.heartbeat_hint')}</Typography>
      </Box>
      {installationLoading && <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}><CircularProgress size={18} />{t('admin:servers.native.installation_loading')}</Box>}
      {installationError && <Alert severity="error" action={<Button color="inherit" onClick={() => setInstallationReload(value => value + 1)}>{t('common:actions.retry')}</Button>}>{installationError}</Alert>}
      {credentialUnavailable && <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
        <Alert severity="warning">{t('admin:servers.native.legacy_credential_hint')}</Alert>
        <TextField label={t('admin:servers.native.old_credential')} type="password" value={oldCredential}
          autoComplete="off" onChange={event => setOldCredential(event.target.value)} disabled={credentialBusy} fullWidth />
        {credentialError && <Alert severity="error">{credentialError}</Alert>}
        <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
          <Button variant="contained" disabled={!oldCredential.trim() || credentialBusy || rotating} onClick={() => void saveOldCredential()}>
            {t('admin:servers.native.import_credential')}
          </Button>
          <Button variant="outlined" startIcon={<VpnKeyIcon />} disabled={credentialBusy || rotating} onClick={() => server && onRotate(server)}>
            {t('admin:servers.action.rotate_node_credential')}
          </Button>
        </Box>
      </Box>}
      {provisioning && <>
        <TextField label={t('admin:servers.native.agent_id')} value={provisioning.agent_id} fullWidth slotProps={{ input: { readOnly: true } }} />
        <TextField label={t('admin:servers.native.endpoint')} value={provisioning.endpoint} fullWidth slotProps={{ input: { readOnly: true } }} />
        <TextField label={t('admin:servers.native.credential')} type="password" value={provisioning.credential}
          autoComplete="off" fullWidth slotProps={{ input: { readOnly: true } }} />
        {provisioning.endpoint.startsWith('http://') && <Alert severity="warning">{t('admin:servers.native.http_warning')}</Alert>}
        <TextField label={t('admin:servers.native.agent_version')} placeholder="vX.Y.Z" value={version}
          onChange={event => { invalidateMaterials(); setVersion(event.target.value) }} disabled={rotating}
          error={!!version && !versionValid}
          helperText={t(version && !versionValid ? 'admin:servers.native.version_invalid' : 'admin:servers.native.version_hint')} fullWidth />
        <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
          {selection.method === 'linux' ? <>
          <Button variant="contained" startIcon={scriptBusy ? <CircularProgress size={16} color="inherit" /> : <DownloadIcon />}
            disabled={!versionValid || !!scriptBusy || rotating} onClick={() => void deliverScript('download')}>
            {t('admin:servers.native.download_script')}
          </Button>
          <Button variant="outlined" startIcon={<ContentCopyIcon />} disabled={!versionValid || !!scriptBusy || rotating} onClick={() => void deliverScript('copy')}>
            {t('admin:servers.native.copy_script')}
          </Button>
          </> : <Button variant="contained" disabled={!versionValid || filesBusy || rotating}
            startIcon={filesBusy ? <CircularProgress size={16} color="inherit" /> : <DownloadIcon />} onClick={() => void generateFiles()}>
            {t('admin:servers.native.generate_files')}
          </Button>}
          <Button component="a" href="https://github.com/KazuhaHub/Passwall-Node/releases" target="_blank" rel="noopener noreferrer">
            {t('admin:servers.native.releases')}
          </Button>
        </Box>
        {scriptError && <Alert severity="error">{scriptError}</Alert>}
        {filesError && <Alert severity="error">{filesError}</Alert>}
        {selection.method === 'linux' ? <>
        <Typography variant="body2">{t('admin:servers.native.run_script_hint')}</Typography>
        <TextField label={t('admin:servers.native.run_script_command')} value={`chmod 0600 ./passwall-node-install-${provisioning.agent_id}.sh\nsudo bash ./passwall-node-install-${provisioning.agent_id}.sh`}
          fullWidth multiline minRows={2} slotProps={{ input: { readOnly: true } }} />
        </> : <>
          <Typography variant="body2">{t(selection.method === 'manual' ? 'admin:servers.native.manual_platform_hint' : 'admin:servers.native.docker_hint')}</Typography>
          {files && <>
            <Alert severity="success">{t('admin:servers.native.files_ready')}</Alert>
            <Typography variant="body2">{t('admin:servers.native.downloads_hint')}</Typography>
            {files.files.map(file => <Box component="section" aria-label={file.name} key={file.name} sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
              <TextField label={t('admin:servers.native.file_content', { name: file.name })} value={file.content}
                type={file.sensitive ? 'password' : 'text'} multiline={!file.sensitive} minRows={file.sensitive ? undefined : 2} maxRows={file.sensitive ? undefined : 10}
                autoComplete="off" fullWidth slotProps={{ input: { readOnly: true, sx: { fontFamily: 'monospace', fontSize: 13 } } }} />
              <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
                <Button variant="outlined" startIcon={<DownloadIcon />} onClick={() => downloadInstallationFile(file.name, file.content)}>{t('admin:servers.native.download_file', { name: file.name })}</Button>
                <Button startIcon={<ContentCopyIcon />} onClick={() => void copyToClipboard(file.content)}>{t('admin:servers.native.copy_file', { name: file.name })}</Button>
              </Box>
            </Box>)}
            {files.steps.map((step, index) => <Box component="section" aria-label={step.id ?? step.title} key={step.id ?? index} sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
              <Typography variant="subtitle2">{t('admin:servers.native.command_label', { number: index + 1, title: t(`admin:servers.native.step_title.${step.id}`, { defaultValue: step.title }) })}</Typography>
              {step.description && <Typography variant="body2">{t(`admin:servers.native.step_description.${selection.method}.${step.id}`, { defaultValue: step.description })}</Typography>}
              {step.commands?.map((command, commandIndex) => <Box key={commandIndex} sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}><TextField
                label={t('admin:servers.native.command_label', { number: index + 1, title: t(`admin:servers.native.step_title.${step.id}`, { defaultValue: step.title }) })}
                value={command} fullWidth multiline minRows={2} maxRows={10} slotProps={{ input: { readOnly: true, sx: { fontFamily: 'monospace', fontSize: 13 } } }} />
                <Button sx={{ alignSelf: 'flex-start' }} startIcon={<ContentCopyIcon />} onClick={() => void copyToClipboard(command)}>{t('admin:servers.native.copy_step')}</Button>
              </Box>)}
            </Box>)}
          </>}
        </>}
      </>}
    </DialogContent>
    <DialogActions sx={{ flexWrap: 'wrap', gap: 1 }}>
      <Button onClick={onClose}>{t('common:actions.close')}</Button>
      <Button component={RouterLink} to="/admin/nodes" variant={status?.state === 'running' ? 'contained' : 'outlined'} onClick={onClose}>
        {t('admin:servers.native.configure_nodes')}
      </Button>
    </DialogActions>
  </>
  if (embedded) return content
  return <Dialog open={!!server} onClose={onClose} maxWidth={false}
    slotProps={{ paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 760, maxWidth: '94vw' } } }}>
    <DialogTitle>{t('admin:servers.native.install_title', { name: server?.name ?? '' })}</DialogTitle>
    {content}
  </Dialog>
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
  error?: boolean
  helperText?: string
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
      error={p.error}
      helperText={p.helperText}
      slotProps={{
        input: {
          endAdornment: (
            <InputAdornment position="end">
              <IconButton size="small" onClick={() => p.onShow(!p.show)} aria-label="toggle visibility">
                {p.show ? <VisibilityOffIcon fontSize="small" /> : <VisibilityIcon fontSize="small" />}
              </IconButton>
            </InputAdornment>
          ),
        }
      }}
    />
  );
}
