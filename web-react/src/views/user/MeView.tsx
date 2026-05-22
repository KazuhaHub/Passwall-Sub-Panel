import { lazy, Suspense, useEffect, useMemo, useRef, useState, type FormEvent, type MouseEvent } from 'react'
import {
  Avatar,
  Box,
  Button,
  Card,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  ListItemIcon,
  ListItemText,
  Menu,
  Select,
  MenuItem,
  Tab,
  Tabs,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  Typography,
  useMediaQuery,
  useTheme,
} from '@mui/material'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import KeyIcon from '@mui/icons-material/VpnKey'
import LockIcon from '@mui/icons-material/Lock'
import RuleIcon from '@mui/icons-material/Rule'
import EmergencyIcon from '@mui/icons-material/MedicalServices'
import AccessTimeIcon from '@mui/icons-material/AccessTime'
import DataUsageIcon from '@mui/icons-material/DataUsage'
import ConfirmationNumberIcon from '@mui/icons-material/ConfirmationNumberOutlined'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import LaunchIcon from '@mui/icons-material/Launch'
import DownloadIcon from '@mui/icons-material/Download'
import StarIcon from '@mui/icons-material/Star'
import HelpOutlineIcon from '@mui/icons-material/HelpOutline'
import VisibilityIcon from '@mui/icons-material/Visibility'
import VisibilityOffIcon from '@mui/icons-material/VisibilityOff'
import { QRCodeSVG } from 'qrcode.react'
import { useTranslation } from 'react-i18next'

import {
  changeMyPassword,
  getMyProfile,
  getMyRules,
  getMyServerStatus,
  resetMyCredentials,
  updateMyRules,
  useEmergencyAccess,
  type MeProfile,
  type MyNodeStatus,
} from '@/api/me'
import { useTabParam } from '@/hooks/useTabParam'
import type { M3Tokens } from '@/theme'
import { useSiteStore } from '@/stores/site'
import {
  getMyTrafficHistory,
  getMyUsage,
  type TrafficHistoryItem,
  type TrafficHistoryPeriod,
  type UsageReport,
} from '@/api/traffic'

const TrafficChart = lazy(() => import('@/components/TrafficChart'))
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { copyToClipboard } from '@/utils/clipboard'

function bytesToHuman(n: number) {
  if (n === 0) return '0'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, u = 0
  while (v >= 1024 && u < units.length - 1) { v /= 1024; u++ }
  return `${v.toFixed(2)} ${units[u]}`
}

// Coarse user-agent platform detection: returns one of the values used in
// SubImportClient.recommended_for so the user portal can pick the right hero
// client without manual priority juggling. Order matters — iPad/iPhone must
// be checked before "mac" (some iPads claim macOS in their UA), and Android
// before "linux" (Android UAs contain "Linux"). Returns null when nothing
// matches (curl, exotic platforms) so the hero just doesn't render.
function detectPlatform(): string | null {
  const ua = (navigator.userAgent || '').toLowerCase()
  if (/iphone|ipad|ipod/.test(ua)) return 'ios'
  if (/android/.test(ua)) return 'android'
  if (/windows/.test(ua)) return 'windows'
  if (/mac os x|macintosh/.test(ua)) return 'macos'
  if (/linux/.test(ua)) return 'linux'
  return null
}

// Keep the protocol + host visible so the user has SOME context the link is
// theirs; mask everything after the last `/` (token + path-specific bits).
function maskUrl(url: string): string {
  try {
    const u = new URL(url)
    return `${u.protocol}//${u.host}/${'•'.repeat(16)}`
  } catch {
    // Fall back if the URL isn't parseable for some reason.
    return '•'.repeat(Math.min(40, url.length))
  }
}

// ServerStatusPanel renders the caller's own nodes' availability (the "服务器
//状态" tab). Data is sanitized server-side — name + region + coarse status
// only. Status dot: ok=primary, down=error, unknown=muted.
function ServerStatusPanel({ md }: { md: M3Tokens }) {
  const { t } = useTranslation('user')
  const [nodes, setNodes] = useState<MyNodeStatus[] | null>(null)
  const [loading, setLoading] = useState(true)
  const seq = useRef(0)
  useEffect(() => {
    const my = ++seq.current
    setLoading(true)
    getMyServerStatus()
      .then(list => { if (my === seq.current) setNodes(list) })
      .catch(() => { if (my === seq.current) setNodes([]) })
      .finally(() => { if (my === seq.current) setLoading(false) })
  }, [])

  const meta = (s: MyNodeStatus['status']) => {
    switch (s) {
      case 'ok': return { color: md.primary, label: t('status.ok', { defaultValue: '正常' }) }
      case 'down': return { color: md.error, label: t('status.down', { defaultValue: '离线' }) }
      default: return { color: md.onSurfaceVariant, label: t('status.unknown', { defaultValue: '未知' }) }
    }
  }

  if (loading) {
    return <Box sx={{ display: 'flex', justifyContent: 'center', py: 6 }}><CircularProgress /></Box>
  }
  if (!nodes || nodes.length === 0) {
    return (
      <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}`, textAlign: 'center', color: md.onSurfaceVariant }}>
        <Typography variant="body2">{t('status.empty', { defaultValue: '暂无可显示的节点' })}</Typography>
      </Card>
    )
  }
  const okCount = nodes.filter(n => n.status === 'ok').length
  const checkedAt = nodes.find(n => n.checked_at)?.checked_at
  return (
    <Card sx={{ p: { xs: 2.5, sm: 3 }, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1.5, mb: 2, flexWrap: 'wrap' }}>
        <Typography sx={{ fontWeight: 500 }}>{t('status.title', { defaultValue: '服务器状态' })}</Typography>
        <Typography variant="body2" sx={{ color: okCount === nodes.length ? md.primary : md.error }}>
          {t('status.summary', { ok: okCount, total: nodes.length, defaultValue: '{{ok}}/{{total}} 正常' })}
        </Typography>
      </Box>
      <Box sx={{ display: 'flex', flexDirection: 'column' }}>
        {nodes.map((n, i) => {
          const m = meta(n.status)
          return (
            <Box key={i} sx={{ display: 'flex', alignItems: 'center', gap: 1.5, py: 1.25, borderBottom: i < nodes.length - 1 ? `1px solid ${md.outlineVariant}` : 'none' }}>
              <Box sx={{ width: 10, height: 10, borderRadius: '50%', bgcolor: m.color, flexShrink: 0 }} />
              <Box sx={{ flex: 1, minWidth: 0 }}>
                <Typography sx={{ fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{n.name}</Typography>
                {n.region && <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>{n.region}</Typography>}
              </Box>
              <Typography variant="body2" sx={{ color: m.color, fontWeight: 500, flexShrink: 0 }}>{m.label}</Typography>
            </Box>
          )
        })}
      </Box>
      {checkedAt && (
        <Typography variant="caption" sx={{ display: 'block', mt: 2, color: md.onSurfaceVariant }}>
          {t('status.checked_at', { time: new Date(checkedAt).toLocaleString(), defaultValue: '最后检查：{{time}}' })}
        </Typography>
      )}
    </Card>
  )
}

export default function MeView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('user')

  const [tab, setTab] = useTabParam<'overview' | 'traffic' | 'clients' | 'status'>('tab', 'overview', ['overview', 'traffic', 'clients', 'status'])
  const [profile, setProfile] = useState<MeProfile | null>(null)
  const [usage, setUsage] = useState<UsageReport | null>(null)
  const [loading, setLoading] = useState(true)
  // Announcement popup: starts hidden, opens after the profile loads
  // unless the visitor has previously chosen "don't remind again" for
  // this exact announcement version.
  const [announceOpen, setAnnounceOpen] = useState(false)

  const [pwdOpen, setPwdOpen] = useState(false)
  const [pwdBusy, setPwdBusy] = useState(false)
  const [pwdOld, setPwdOld] = useState('')
  const [pwdNew, setPwdNew] = useState('')
  const [pwdConfirm, setPwdConfirm] = useState('')

  const [rulesOpen, setRulesOpen] = useState(false)
  const [rulesText, setRulesText] = useState('')
  const [rulesSaved, setRulesSaved] = useState('')
  const [rulesBusy, setRulesBusy] = useState(false)

  const [trendItems, setTrendItems] = useState<TrafficHistoryItem[]>([])
  const [trendBusy, setTrendBusy] = useState(false)
  const [trendPeriod, setTrendPeriod] = useState<TrafficHistoryPeriod>('day')
  const [trendDays, setTrendDays] = useState(7)
  // Range options filtered by admin-configured TrafficHistoryDays (mirrors
  // the admin chart's logic so a retention=90 panel hides "last 1 year"
  // here too). 1d is always available — even a 1-day retention covers it
  // — and only renders Hour granularity. Hour granularity itself is
  // additionally capped to the 7-day raw retention window (the chart
  // currently reads from raw snapshots, not the hourly rollup).
  const rawRetentionDays = 7
  const trendRangeOptions = useMemo(() => {
    const all = [1, 7, 30, 90, 180, 365]
    const retention = profile?.traffic_history_days
    const historyDays = retention && retention > 0 ? retention : Number.POSITIVE_INFINITY
    const cap = trendPeriod === 'hour' ? Math.min(historyDays, rawRetentionDays) : historyDays
    return all.filter(d => d <= cap || d === 1)
  }, [trendPeriod, profile?.traffic_history_days])
  // Clamp trendDays whenever the option set changes: largest available
  // option that isn't bigger than the current selection.
  useEffect(() => {
    if (trendRangeOptions.length === 0) return
    if (!trendRangeOptions.includes(trendDays)) {
      setTrendDays(trendRangeOptions[trendRangeOptions.length - 1])
    }
  }, [trendRangeOptions, trendDays])
  // Period ↔ Range coupling: 1d locks Period to Hour; ≥30d forbids Hour.
  // Snap rather than disable so the chart never queries an invalid combo.
  useEffect(() => {
    if (trendDays === 1 && trendPeriod !== 'hour') setTrendPeriod('hour')
    else if (trendDays >= 30 && trendPeriod === 'hour') setTrendPeriod('day')
  }, [trendDays, trendPeriod])

  const [emergencyBusy, setEmergencyBusy] = useState(false)
  const [menuAnchor, setMenuAnchor] = useState<HTMLElement | null>(null)
  // Sub URL is sensitive credential material — keep masked by default so a
  // casual screenshot / screen-share doesn't leak it. Copy still works
  // without revealing. Auto-hides on profile reload so a "reset credentials"
  // action doesn't accidentally re-expose the old URL.
  const [subUrlRevealed, setSubUrlRevealed] = useState(false)
  const isMobile = useMediaQuery(theme.breakpoints.down('sm'))

  useEffect(() => { void load() }, [])
  useEffect(() => { void loadTrend()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [trendPeriod, trendDays])

  // Decide whether to fire the announcement popup after the profile is
  // fetched. We compare the visitor's stored dismissal key against the
  // current announcement's updated_at — a fresh edit invalidates every
  // visitor's previous "don't remind" choice automatically.
  useEffect(() => {
    const a = profile?.global_announcement
    if (!a?.enabled || !a.popup || !a.title) return
    const key = `psp.announce.dismiss`
    try {
      const stored = localStorage.getItem(key)
      if (stored === (a.updated_at || a.title)) return
    } catch { /* localStorage unavailable — fall through and show */ }
    setAnnounceOpen(true)
  }, [profile])

  function closeAnnounce(persist: boolean) {
    if (persist) {
      const a = profile?.global_announcement
      const stamp = a?.updated_at || a?.title || ''
      try { localStorage.setItem('psp.announce.dismiss', stamp) } catch { /* ignore */ }
    }
    setAnnounceOpen(false)
  }

  // Last-wins guard: period/range changes snap-and-reload (see below), so
  // rapid toggling can overlap; only the latest response should paint.
  const trendSeq = useRef(0)

  async function loadTrend() {
    const seq = ++trendSeq.current
    setTrendBusy(true)
    try {
      const since = new Date()
      since.setHours(0, 0, 0, 0)
      since.setDate(since.getDate() - (trendDays - 1))
      const sinceStr = `${since.getFullYear()}-${String(since.getMonth() + 1).padStart(2, '0')}-${String(since.getDate()).padStart(2, '0')}`
      const today = new Date()
      const untilStr = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`
      const res = await getMyTrafficHistory({ period: trendPeriod, since: sinceStr, until: untilStr })
      if (seq === trendSeq.current) setTrendItems(res.items)
    } catch { /* ignore */ }
    finally { if (seq === trendSeq.current) setTrendBusy(false) }
  }

  async function tryEmergency() {
    setEmergencyBusy(true)
    try {
      const res = await useEmergencyAccess()
      // Backend returns 200 only on success (forbidden / quota-reached come
      // back as HTTP errors and throw above).
      const until = res.extended_until ?? res.until
      pushSnack(t('emergency.granted', { until: until ? new Date(until).toLocaleString() : '' }), 'success')
      // Optimistic merge: the response carries the freshly-incremented
      // counts, so update the badge immediately. A subsequent load() refetches
      // the full profile (and would over-write this) — without the optimistic
      // step the user sees the old "remaining" until the GET completes.
      setProfile(prev => prev ? {
        ...prev,
        expire_at: res.extended_until ?? res.until ?? prev.expire_at,
        emergency_access: prev.emergency_access ? {
          ...prev.emergency_access,
          used_count: res.used_count ?? prev.emergency_access.used_count,
          remaining: res.remaining ?? prev.emergency_access.remaining,
          max_count: res.max_count ?? prev.emergency_access.max_count,
          available: false,
          status: 'active',
          emergency_until: res.emergency_until ?? prev.emergency_access.emergency_until,
        } : prev.emergency_access,
      } : prev)
      await load()
    } catch (e) {
      const msg = (e as { message?: string }).message ?? t('emergency.no_quota')
      pushSnack(msg, 'warning')
    } finally { setEmergencyBusy(false) }
  }

  async function load() {
    setLoading(true)
    try {
      const [p, u] = await Promise.all([
        getMyProfile(),
        getMyUsage().catch(() => null),
      ])
      setProfile(p); setUsage(u)
      // Always re-mask on reload — e.g., after "重置凭证" the URL changed and
      // leaving the old reveal flag on would briefly display the new URL.
      setSubUrlRevealed(false)
    } finally { setLoading(false) }
  }

  async function copy(text: string) {
    // copyToClipboard handles its own toast (success / fallback /
    // failure) so callers don't need to wrap the call.
    await copyToClipboard(text)
  }

  async function reset() {
    const ok = await confirm({
      title: t('sub.reset'),
      message: t('sub.reset_warn'),
      destructive: true,
      confirmText: t('sub.reset'),
    })
    if (!ok) return
    const r = await resetMyCredentials()
    pushSnack(t('sub.reset_ok', { uuid: r.uuid }), 'success')
    await load()
  }

  function openPwd() {
    setPwdOld(''); setPwdNew(''); setPwdConfirm(''); setPwdOpen(true)
  }
  async function submitPwd(e: FormEvent) {
    e.preventDefault()
    if (pwdNew !== pwdConfirm) { pushSnack(t('password.mismatch'), 'warning'); return }
    setPwdBusy(true)
    try {
      await changeMyPassword(pwdOld, pwdNew)
      pushSnack(t('password.saved'), 'success')
      setPwdOpen(false)
    } finally { setPwdBusy(false) }
  }

  async function openRules() {
    setRulesOpen(true); setRulesBusy(true)
    try {
      const text = await getMyRules()
      setRulesText(text); setRulesSaved(text)
    } finally { setRulesBusy(false) }
  }
  async function saveRules() {
    setRulesBusy(true)
    try {
      const text = rulesText.trim()
      await updateMyRules(text)
      setRulesText(text); setRulesSaved(text)
      pushSnack(t('rules.saved'), 'success')
      setRulesOpen(false)
    } catch (e) {
      // 403 = admin turned off self-service rule editing. Localize it here
      // since the backend returns a hardcoded English string.
      const status = (e as { response?: { status?: number } })?.response?.status
      pushSnack(status === 403 ? t('rules.disabled_toast') : t('rules.save_failed'), 'error')
    } finally { setRulesBusy(false) }
  }

  if (loading) {
    return <Box sx={{ p: 3, display: 'grid', placeItems: 'center', minHeight: 400 }}><CircularProgress /></Box>
  }
  if (!profile) return null

  const announcement = profile.global_announcement
  // Popup mode is gated by both an admin opt-in (announcement.popup) and
  // per-browser dismissal state in localStorage. The dismissal key embeds
  // updated_at so editing the announcement (which bumps the timestamp)
  // re-shows the popup for everyone who previously dismissed.
  const announcementPopup = !!announcement?.enabled && !!announcement.popup && !!announcement.title
  const importClients = (profile.sub_import_clients || [])
    .filter(c => c.enabled)
    .slice()
    .sort((a, b) => (a.sort || 0) - (b.sort || 0))
  const quickLinks = (profile.quick_links || [])
    .filter(l => l.enabled)
    .slice()
    .sort((a, b) => (a.sort || 0) - (b.sort || 0))

  function announcementStyle(level: string) {
    if (level === 'danger') {
      return { bg: md.errorContainer, fg: md.onErrorContainer, Icon: ErrorOutlineIcon }
    }
    if (level === 'warning') {
      // M3 has no built-in warning role for the baseline-purple palette;
      // hard-code an amber pair that adapts roughly to light vs dark.
      const isDark = theme.palette.mode === 'dark'
      return {
        bg: isDark ? '#3F2E00' : '#FFF8E1',
        fg: isDark ? '#FFE49A' : '#7A5C00',
        Icon: WarningAmberIcon,
      }
    }
    return { bg: md.primaryContainer, fg: md.onPrimaryContainer, Icon: InfoOutlinedIcon }
  }

  function buildImportURL(c: { import_url_template: string }): string {
    const subUrl = profile?.sub_url || ''
    // Server resolves SubProfileNameTemplate against the user and
    // exposes the result as profile_name on /api/user/me. Falling
    // back to the bare display/UPN preserves behavior on clients
    // updated before the backend rolled out.
    const profileName = profile?.profile_name
      || profile?.display_name
      || profile?.upn
      || 'Passwall'
    // btoa requires latin-1; the sub URL is ASCII so unescape(encodeURIComponent())
    // pre-encodes safely. Used by V2rayNG / Shadowrocket deep links which
    // expect the URL itself to be base64-wrapped in the query.
    let subUrlB64 = ''
    try { subUrlB64 = btoa(unescape(encodeURIComponent(subUrl))) } catch { /* ignore */ }
    // URL-safe base64 with padding stripped — the form most production
    // panels (Xboard / v2board themes / 3x-ui frontend) use for the
    // Shadowrocket deep link `shadowrocket://add/sub://<b64>?remark=...`.
    // Standard `+/=` characters parse unreliably inside Shadowrocket's
    // URI consumer; replacing them keeps the body in [A-Za-z0-9_-].
    const subUrlB64UrlSafe = subUrlB64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
    // CMfA reads `update-interval` from the intent URI in MINUTES (its
    // Profile-Update-Interval HTTP header is in hours; the URI param is
    // a separate format chosen by the CMfA author). Default 24h when the
    // admin hasn't set a value, matching the header fallback in render.go.
    const intervalHours = profile?.sub_update_interval_hours && profile.sub_update_interval_hours > 0
      ? profile.sub_update_interval_hours
      : 24
    const intervalMinutes = intervalHours * 60
    return c.import_url_template
      .replaceAll('{{ sub_url_encoded }}', encodeURIComponent(subUrl))
      // sub_url_b64 — raw standard base64, fits opaque schemes like
      // sub://<b64> where the whole tail is one payload.
      // sub_url_b64_url_safe — URL-safe variant (+ -> -, / -> _, no
      // padding). The form Xboard / v2board themes / 3x-ui use inside
      // shadowrocket://add/sub://<b64>?remark=... so the body stays
      // inside [A-Za-z0-9_-] and survives Shadowrocket's URI parser.
      .replaceAll('{{ sub_url_b64_url_safe }}', subUrlB64UrlSafe)
      .replaceAll('{{ sub_url_b64 }}', subUrlB64)
      .replaceAll('{{ sub_url }}', subUrl)
      .replaceAll('{{ profile_name_encoded }}', encodeURIComponent(profileName))
      .replaceAll('{{ profile_name }}', profileName)
      .replaceAll('{{ sub_update_interval_minutes }}', String(intervalMinutes))
      .replaceAll('{{ sub_update_interval_hours }}', String(intervalHours))
  }

  // triggerImport dispatches between two import flows:
  //   - Custom URI scheme (e.g. clashmi://, v2rayng://): navigate to
  //     the URL so the OS hands off to the client app.
  //   - Plain https:// URL (V2rayN, Surge, anything without a
  //     registered scheme): copy to clipboard with a toast, since the
  //     desktop client requires the user to paste into a Subscription
  //     dialog and "navigating" to an https URL would just open it in
  //     the browser and dump raw YAML/uri-list as text.
  async function triggerImport(url: string) {
    if (/^https?:\/\//i.test(url)) {
      await copyToClipboard(url)
      return
    }
    // Custom app scheme (clash://, karing://, ...). Block script-capable
    // pseudo-schemes so a malformed/hostile import template can't execute
    // code in the user's origin via navigation.
    const scheme = (url.match(/^([a-z][a-z0-9+.-]*):/i)?.[1] ?? '').toLowerCase()
    const dangerous = ['javascript', 'data', 'vbscript', 'blob', 'file']
    if (!scheme || dangerous.includes(scheme)) {
      pushSnack(t('import.invalid_url', { defaultValue: '无效的导入链接' }), 'error')
      return
    }
    window.location.href = url
  }

  // Quick links are admin-configured web URLs. Require http(s) so a hostile
  // config can't smuggle a javascript:/data: URL that runs on click.
  function openQuickLink(l: { url: string; new_window: boolean }) {
    if (!/^https?:\/\//i.test(l.url)) {
      pushSnack(t('links.invalid_url', { defaultValue: '链接无效（仅支持 http/https）' }), 'error')
      return
    }
    if (l.new_window) window.open(l.url, '_blank', 'noopener,noreferrer')
    else window.location.href = l.url
  }

  function emergencyStatusText(): string {
    const emergency = profile?.emergency_access
    if (!emergency) return ''
    const status = emergency.status || (emergency.available ? 'available' : emergency.remaining <= 0 ? 'no_quota' : 'not_eligible')
    if (status === 'available') {
      return t('emergency.status_available', {
        hours: emergency.duration_hours,
        defaultValue: `账号到期或流量超限后，可临时恢复 ${emergency.duration_hours} 小时连接。`,
      })
    }
    if (status === 'active') {
      const until = emergency.emergency_until ? new Date(emergency.emergency_until).toLocaleString() : ''
      return t('emergency.status_active', {
        until,
        defaultValue: until ? `紧急访问已开启，有效期至 ${until}。` : '紧急访问已开启。',
      })
    }
    if (status === 'no_quota') {
      return t('emergency.status_no_quota', { defaultValue: '紧急访问次数已用完。' })
    }
    if (status === 'not_eligible') {
      return t('emergency.status_not_eligible', { defaultValue: '当前账号未到期且未超流量，暂不能使用紧急访问。' })
    }
    if (status === 'disabled') {
      return t('emergency.status_disabled', { defaultValue: '紧急访问未开启。' })
    }
    if (status === 'invalid_settings') {
      return t('emergency.status_invalid_settings', { defaultValue: '紧急访问设置不完整，请联系管理员。' })
    }
    return t('emergency.status_unavailable', { defaultValue: '当前暂不能使用紧急访问。' })
  }

  return (
    <Box sx={{ p: { xs: 2, sm: 3 }, maxWidth: 1200, mx: 'auto' }}>
      {announcement?.enabled && announcement.title && (() => {
        const s = announcementStyle(announcement.level)
        return (
          <Box sx={{
            p: 2, mb: 2, borderRadius: 2,
            bgcolor: s.bg, color: s.fg,
            display: 'flex', gap: 1.5, alignItems: 'flex-start',
          }}>
            <s.Icon sx={{ flexShrink: 0, mt: 0.25 }} />
            <Box sx={{ flex: 1, minWidth: 0 }}>
              <Typography sx={{ fontWeight: 500, mb: 0.5 }}>{announcement.title}</Typography>
              {announcement.content && (
                <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>{announcement.content}</Typography>
              )}
            </Box>
          </Box>
        )
      })()}

      {/* Compact header — avatar + identity + kebab menu */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mb: 3 }}>
        <Avatar sx={{ width: 48, height: 48, bgcolor: md.primary, color: md.onPrimary, fontSize: 18, fontWeight: 500 }}>
          {(profile.display_name || profile.upn).charAt(0).toUpperCase()}
        </Avatar>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Typography variant="h6" sx={{ fontWeight: 500, lineHeight: 1.2 }}>
            {profile.display_name || profile.upn}
          </Typography>
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {profile.upn}{profile.email ? ` · ${profile.email}` : ''}
          </Typography>
        </Box>
        {/* Kebab is only worth rendering when 修改密码 is actually available
            (local accounts where admin allows it). 个人规则 + 重置凭证 both
            live inside the Sub URL card now — they affect the subscription
            content directly so co-locating makes the consequence obvious. */}
        {profile.can_change_password && (<>
          <IconButton onClick={(e: MouseEvent<HTMLElement>) => setMenuAnchor(e.currentTarget)}>
            <MoreVertIcon />
          </IconButton>
          <Menu open={!!menuAnchor} anchorEl={menuAnchor} onClose={() => setMenuAnchor(null)}
            anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
            transformOrigin={{ vertical: 'top', horizontal: 'right' }}
            PaperProps={{ sx: { mt: 1, minWidth: 200 } }}>
            <MenuItem onClick={() => { setMenuAnchor(null); openPwd() }}>
              <ListItemIcon><LockIcon fontSize="small" /></ListItemIcon>
              <ListItemText primary={t('actions.change_password')} />
            </MenuItem>
          </Menu>
        </>)}
      </Box>

      {/* Section tabs — keep the page navigable as it grows. Server status is
          its own tab so it doesn't pile onto the already-long overview. */}
      <Tabs value={tab} onChange={(_, v) => setTab(v as 'overview' | 'traffic' | 'clients' | 'status')}
        variant="scrollable" scrollButtons="auto"
        sx={{ mb: { xs: 2, sm: 3 }, minHeight: 40 }}>
        <Tab value="overview" label={t('tabs.overview', { defaultValue: '概览' })} sx={{ minHeight: 40 }} />
        <Tab value="traffic" label={t('tabs.traffic', { defaultValue: '流量' })} sx={{ minHeight: 40 }} />
        <Tab value="clients" label={t('tabs.clients', { defaultValue: '客户端' })} sx={{ minHeight: 40 }} />
        <Tab value="status" label={t('tabs.server_status', { defaultValue: '服务器状态' })} sx={{ minHeight: 40 }} />
      </Tabs>

      {tab === 'status' && <ServerStatusPanel md={md} />}

      {tab === 'overview' && (<>
      {/* HERO — pick the client whose recommended_for covers the visitor's
          detected platform. Falls back to nothing if no client is configured
          for this OS (or detection fails entirely). */}
      {(() => {
        const platform = detectPlatform()
        const hero = platform
          ? importClients.find(c => c.recommended_for?.includes(platform))
          : undefined
        if (!hero) return null
        return (
          <Card sx={{ p: { xs: 2.5, sm: 3 }, mb: { xs: 2, sm: 3 }, bgcolor: md.primaryContainer, color: md.onPrimaryContainer }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
              <StarIcon sx={{ fontSize: 18 }} />
              <Typography sx={{ fontSize: 12, fontWeight: 500, textTransform: 'uppercase', letterSpacing: '.5px' }}>
                {t('import.recommended_label', { defaultValue: '推荐客户端' })}
              </Typography>
            </Box>
            <Typography sx={{ fontWeight: 500, mb: 0.5, fontSize: { xs: 20, sm: 24 }, lineHeight: 1.2 }}>
              {hero.name}
            </Typography>
            <Typography variant="body2" sx={{ mb: 2, opacity: 0.85 }}>
              {hero.platforms.map(p => t(`import.platform_${p}`, { defaultValue: p })).join(' · ')}
            </Typography>
            {/* On mobile pack the three buttons into a 2-col grid (导入 +
                安装 on row 1, 查看教程 spans row 2) instead of stacking each
                full-width — saves ~2 vertical button-heights. Desktop keeps
                the inline row with comfortable large buttons. */}
            <Box sx={{
              display: { xs: 'grid', sm: 'flex' },
              gridTemplateColumns: { xs: '1fr 1fr', sm: 'none' },
              gap: { xs: 1, sm: 1.5 },
              flexWrap: 'wrap',
            }}>
              <Button size={isMobile ? 'medium' : 'large'} variant="contained"
                startIcon={<LaunchIcon />}
                onClick={() => { void triggerImport(buildImportURL(hero)) }}
                sx={{ bgcolor: md.primary, color: md.onPrimary, '&:hover': { bgcolor: md.primary } }}>
                {t('import.import')}
              </Button>
              <Button size={isMobile ? 'medium' : 'large'} variant="outlined"
                startIcon={<DownloadIcon />}
                onClick={() => window.open(hero.install_url, '_blank', 'noopener,noreferrer')}
                sx={{ borderColor: md.onPrimaryContainer, color: md.onPrimaryContainer,
                  '&:hover': { borderColor: md.onPrimaryContainer, bgcolor: 'rgba(0,0,0,.06)' } }}>
                {t('import.install')}
              </Button>
              {profile.sub_import_tutorial_url && (
                <Button size={isMobile ? 'medium' : 'large'} variant="outlined"
                  startIcon={<HelpOutlineIcon />}
                  onClick={() => window.open(profile.sub_import_tutorial_url, '_blank', 'noopener,noreferrer')}
                  sx={{
                    borderColor: md.onPrimaryContainer, color: md.onPrimaryContainer,
                    gridColumn: { xs: '1 / -1', sm: 'auto' },
                    '&:hover': { borderColor: md.onPrimaryContainer, bgcolor: 'rgba(0,0,0,.06)' },
                  }}>
                  {t('import.tutorial')}
                </Button>
              )}
            </Box>
          </Card>
        )
      })()}
      </>)}

      {/* Two-column layout below the hero. Each column is an independent
          flex stack so a tall card on one side doesn't open a gap on the
          other (grid-template-rows would force row alignment by max
          height).
          On mobile (xs) the column wrappers switch to `display: contents`
          which removes them from the layout tree, so all 6 cards become
          direct children of THIS outer flex-column. Each card then uses
          its `order` to land in the desired mobile sequence:
            sub → usage → quick → emerg → trend → other
          On md+ the columns become real flex containers again, and order
          resets so cards follow DOM source order inside each column. */}
      <Box sx={{
        display: 'flex',
        flexDirection: { xs: 'column', md: 'row' },
        gap: { xs: 2, sm: 3 },
        // alignItems means different things in row vs column:
        //   row (md+):    vertical alignment — flex-start so cards keep
        //                 their natural height, don't grow to match a
        //                 tall sibling
        //   column (xs):  horizontal alignment — stretch so every card
        //                 fills the viewport width. Without this the
        //                 right-column cards (usage / quick) shrink to
        //                 their content size and look narrower than the
        //                 wider-content sub-url card.
        alignItems: { xs: 'stretch', md: 'flex-start' },
      }}>

      {/* Left column */}
      <Box sx={{
        display: { xs: 'contents', md: 'flex' },
        flexDirection: 'column',
        gap: { xs: 2, sm: 3 },
        flex: { md: 1.5 },
        minWidth: 0,
        width: { xs: '100%', md: 'auto' },
      }}>
      {tab === 'overview' && (
      <Box sx={{ order: { xs: 1, md: 0 }, width: { xs: '100%', md: 'auto' } }}>
      {/* Sub URL — masked by default for screenshot/screen-share safety.
          Click the eye icon to reveal both the URL text AND the QR code.
          Copy still works while masked (clipboard is private). */}
      <Card sx={{ p: { xs: 2.5, sm: 3 }, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Typography sx={{ fontWeight: 500, mb: 0.5 }}>{t('sub.title')}</Typography>
        <Typography variant="body2" sx={{ mb: 2 }}>{t('sub.intro')}</Typography>
        <Box sx={{
          display: 'grid',
          gridTemplateColumns: { xs: '1fr', sm: 'auto 1fr' },
          gap: 2.5, alignItems: 'stretch',
        }}>
          <Box sx={{
            justifySelf: { xs: 'center', sm: 'auto' },
            p: 1.5, borderRadius: 2, bgcolor: '#fff',
            border: `1px solid ${md.outlineVariant}`, lineHeight: 0,
            position: 'relative',
            cursor: subUrlRevealed ? 'default' : 'pointer',
          }}
            onClick={() => { if (!subUrlRevealed) setSubUrlRevealed(true) }}>
            <Box sx={{
              filter: subUrlRevealed ? 'none' : 'blur(8px)',
              transition: 'filter .2s',
            }}>
              <QRCodeSVG value={profile.sub_url} size={isMobile ? 160 : 140} level="M" />
            </Box>
            {!subUrlRevealed && (
              <Box sx={{
                position: 'absolute', inset: 0, display: 'grid', placeItems: 'center',
                borderRadius: 2,
              }}>
                {/* Eye sits on a fixed-dark chip so it stays readable in
                    both themes — the QR container itself is forced
                    white (QR codes need white background, black
                    modules), which makes a theme-aware icon nearly
                    invisible in dark mode. */}
                <Box sx={{
                  width: 48, height: 48, borderRadius: '50%',
                  bgcolor: 'rgba(0,0,0,0.55)',
                  display: 'grid', placeItems: 'center',
                  boxShadow: '0 2px 8px rgba(0,0,0,0.3)',
                }}>
                  <VisibilityIcon sx={{ color: '#fff', fontSize: 24 }} />
                </Box>
              </Box>
            )}
          </Box>
          <Box sx={{ minWidth: 0, display: 'flex', flexDirection: 'column' }}>
            <Box sx={{
              display: 'flex', alignItems: 'center', gap: 1, p: 1.5,
              bgcolor: md.surfaceContainerHighest, borderRadius: 1.5,
              fontSize: 13,
            }}>
              <Box sx={{ flex: 1, wordBreak: 'break-all', fontFamily: 'inherit' }}>
                {subUrlRevealed ? profile.sub_url : maskUrl(profile.sub_url)}
              </Box>
              <IconButton size="small" onClick={() => setSubUrlRevealed(v => !v)}
                aria-label={subUrlRevealed ? t('sub.hide', { defaultValue: '隐藏' }) : t('sub.reveal', { defaultValue: '显示' })}>
                {subUrlRevealed ? <VisibilityOffIcon fontSize="small" /> : <VisibilityIcon fontSize="small" />}
              </IconButton>
              <IconButton size="small" onClick={() => copy(profile.sub_url)} aria-label={t('sub.copy')}>
                <ContentCopyIcon fontSize="small" />
              </IconButton>
            </Box>
            <Typography variant="caption" sx={{ display: 'block', mt: 1.5, color: md.onSurfaceVariant }}>
              {subUrlRevealed
                ? t('sub.qr_hint', { defaultValue: '在手机客户端扫描左侧二维码可直接导入。' })
                : t('sub.masked_hint', { defaultValue: '订阅链接已隐藏 — 点击 👁 显示完整 URL 与二维码。复制按钮无需先显示。' })}
            </Typography>
            {/* Actions sit in the QR-row's right column, below the hint —
                this fills the whitespace that's there anyway (the QR is
                taller than URL + hint stacked) instead of adding a footer
                band. No borderTop so the card height stays compact. */}
            <Box sx={{
              mt: 'auto', pt: 2,
              display: 'flex', justifyContent: 'flex-end', gap: 0.5, mr: -1,
            }}>
              <Button size="small" variant="text"
                startIcon={<RuleIcon fontSize="small" />}
                onClick={openRules}>
                {t('actions.personal_rules')}
              </Button>
              <Button size="small" color="error" variant="text"
                startIcon={<KeyIcon fontSize="small" />}
                onClick={() => void reset()}>
                {t('sub.reset')}
              </Button>
            </Box>
          </Box>
        </Box>
      </Card>
      </Box>
      )}{/* end sub url order wrapper */}

      {tab === 'overview' && (
      <Box sx={{ order: { xs: 3, md: 0 }, width: { xs: '100%', md: 'auto' } }}>
      {/* Quick links — moved into the left column so the overview's two
          columns stay balanced (left: sub URL + quick links; right: usage +
          emergency). order:3 keeps the mobile sequence unchanged. */}
      {quickLinks.length > 0 && (
        <Card sx={{ p: { xs: 2.5, sm: 3 }, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
          <Typography sx={{ fontWeight: 500, mb: 1.5 }}>{t('links.title')}</Typography>
          <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
            {quickLinks.map(l => (
              <Button key={l.url} size="small" variant="outlined"
                onClick={() => openQuickLink(l)}>
                {l.label}
              </Button>
            ))}
          </Box>
        </Card>
      )}
      </Box>
      )}{/* end quick links order wrapper */}

      {tab === 'traffic' && (
      <Box sx={{ order: { xs: 5, md: 0 }, width: { xs: '100%', md: 'auto' } }}>
      {/* Traffic trend chart — its own tab now, so a plain Card (no collapse). */}
      <Card sx={{ p: { xs: 2.5, sm: 3 }, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Typography sx={{ fontWeight: 500, mb: 1.5 }}>{t('trend.title')}</Typography>
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, mb: 1.5, flexWrap: 'wrap' }}>
            {/* Range first, granularity second — pick the time window before
                deciding zoom level. 1d (Today) forces Hour; ≥30d hides Hour
                (raw retention is only 7 days, so longer windows have no
                hourly data to render). */}
            <Select size="small" value={trendDays}
              onChange={e => setTrendDays(Number(e.target.value))}
              sx={{ height: 36, minWidth: 130 }}>
              {trendRangeOptions.includes(1) && <MenuItem value={1}>{t('trend.range_1', { defaultValue: '今天' })}</MenuItem>}
              {trendRangeOptions.includes(7) && <MenuItem value={7}>{t('trend.range_7')}</MenuItem>}
              {trendRangeOptions.includes(30) && <MenuItem value={30}>{t('trend.range_30')}</MenuItem>}
              {trendRangeOptions.includes(90) && <MenuItem value={90}>{t('trend.range_90')}</MenuItem>}
              {trendRangeOptions.includes(180) && <MenuItem value={180}>{t('trend.range_180', { defaultValue: '最近半年' })}</MenuItem>}
              {trendRangeOptions.includes(365) && <MenuItem value={365}>{t('trend.range_365', { defaultValue: '最近一年' })}</MenuItem>}
            </Select>
            {/* Segmented granularity toggle, matching the admin traffic
                chart. Hour only shows for ≤7d ranges; day/week/month are
                disabled on the 1d (Today) range. */}
            <ToggleButtonGroup value={trendPeriod} exclusive size="small"
              onChange={(_, v) => v && setTrendPeriod(v as TrafficHistoryPeriod)}
              sx={{ '& .MuiToggleButton-root': { px: 2, height: 36 } }}>
              {trendDays <= 7 && (
                <ToggleButton value="hour">{t('trend.period_hour', { defaultValue: '按小时' })}</ToggleButton>
              )}
              <ToggleButton value="day" disabled={trendDays === 1}>{t('trend.period_day', { defaultValue: '按天' })}</ToggleButton>
              <ToggleButton value="week" disabled={trendDays === 1}>{t('trend.period_week', { defaultValue: '按周' })}</ToggleButton>
              <ToggleButton value="month" disabled={trendDays === 1}>{t('trend.period_month', { defaultValue: '按月' })}</ToggleButton>
            </ToggleButtonGroup>
          </Box>
          <Suspense fallback={<Box sx={{ height: 280, display: 'grid', placeItems: 'center' }}><CircularProgress size={24} /></Box>}>
            {trendBusy
              ? <Box sx={{ height: 280, display: 'grid', placeItems: 'center' }}><CircularProgress size={24} /></Box>
              : <TrafficChart items={trendItems} height={280} />}
          </Suspense>
      </Card>
      </Box>
      )}{/* end trend order wrapper */}

      {tab === 'clients' && (
      <Box sx={{ order: { xs: 6, md: 0 }, width: { xs: '100%', md: 'auto' } }}>
      {/* Other clients (excludes the hero, which is shown above). When no
          client matches the visitor's platform, this falls back to the full
          list so the user portal still surfaces all import targets. */}
      {(() => {
        const platform = detectPlatform()
        const heroName = platform
          ? importClients.find(c => c.recommended_for?.includes(platform))?.name
          : undefined
        const others = heroName ? importClients.filter(c => c.name !== heroName) : importClients
        if (others.length === 0) return null
        return (
          // Its own "客户端" tab now — plain Card, no collapse. The tutorial
          // link lives here too (the hero card that used to carry it is over
          // in 概览).
          <Card sx={{ p: { xs: 2.5, sm: 3 }, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 2, flexWrap: 'wrap', mb: 1 }}>
              <Typography sx={{ fontWeight: 500 }}>
                {heroName ? t('import.others_title', { defaultValue: '更多客户端' }) : t('import.title')}
              </Typography>
              {profile.sub_import_tutorial_url && (
                <Button size="small" variant="outlined"
                  onClick={() => window.open(profile.sub_import_tutorial_url, '_blank', 'noopener,noreferrer')}>
                  {t('import.tutorial')}
                </Button>
              )}
            </Box>
              {!heroName && (
                <Typography variant="body2" sx={{ mb: 2 }}>{t('import.intro')}</Typography>
              )}
              <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: 2, mt: 1.5 }}>
                {others.map(c => (
                  <Box key={c.name} sx={{ p: 2, borderRadius: 2, border: `1px solid ${md.outlineVariant}`, bgcolor: md.surface }}>
                    <Typography sx={{ fontWeight: 500, mb: 0.5 }}>{c.name}</Typography>
                    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 1.5 }}>
                      {c.platforms.map(p => t(`import.platform_${p}`, { defaultValue: p })).join(' · ')}
                    </Typography>
                    <Box sx={{ display: 'flex', gap: 1 }}>
                      <Button size="small" variant="contained"
                        onClick={() => { void triggerImport(buildImportURL(c)) }}>
                        {t('import.import')}
                      </Button>
                      <Button size="small" variant="outlined"
                        onClick={() => window.open(c.install_url, '_blank', 'noopener,noreferrer')}>
                        {t('import.install')}
                      </Button>
                    </Box>
                  </Box>
                ))}
              </Box>
          </Card>
        )
      })()}
      </Box>
      )}{/* end other clients order wrapper */}

      </Box>{/* end left col */}

      {/* Right column */}
      {tab === 'overview' && (
      <Box sx={{
        display: { xs: 'contents', md: 'flex' },
        flexDirection: 'column',
        gap: { xs: 2, sm: 3 },
        flex: { md: 1 },
        minWidth: 0,
        width: { xs: '100%', md: 'auto' },
      }}>

      <Box sx={{ order: { xs: 2, md: 0 }, width: { xs: '100%', md: 'auto' } }}>
      {/* Usage panel — 流量 + 到期 with progress bars */}
      <UsagePanel
        limitBytes={profile.traffic_limit_bytes}
        usage={usage}
        expireAt={profile.expire_at ?? null}
        resetPeriod={profile.traffic_reset_period}
        md={md}
      />
      </Box>{/* end usage order wrapper */}

      <Box sx={{ order: { xs: 4, md: 0 }, width: { xs: '100%', md: 'auto' } }}>
      {/* Emergency access */}
      {profile.emergency_access?.enabled && (() => {
        const ea = profile.emergency_access
        const quotaActive = (ea.quota_bytes ?? 0) > 0
        const windowActive = !!ea.emergency_until && new Date(ea.emergency_until) > new Date()
        const usedPct = quotaActive ? Math.min(100, (ea.used_bytes / ea.quota_bytes) * 100) : 0
        const metaSx = {
          display: 'inline-flex', alignItems: 'center', gap: 0.5,
          fontSize: 12, opacity: 0.78,
          '& .MuiSvgIcon-root': { fontSize: 14 },
        } as const
        return (
        <Card sx={{ p: { xs: 2.5, sm: 3 }, bgcolor: md.tertiaryContainer, color: md.onTertiaryContainer }}>
          {/* Header row: on mobile we stack the action button below so the
              long "Use emergency access" label doesn't squeeze the status
              text into a 1-2-word-wide column. */}
          <Box sx={{
            display: 'flex',
            alignItems: { xs: 'stretch', sm: 'flex-start' },
            gap: 2,
            flexDirection: { xs: 'column', sm: 'row' },
          }}>
            <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 2, flex: 1, minWidth: 0 }}>
              <EmergencyIcon sx={{ mt: 0.25, flexShrink: 0 }} />
              <Box sx={{ flex: 1, minWidth: 0 }}>
                <Typography sx={{ fontWeight: 500, mb: 0.5 }}>{t('emergency.title')}</Typography>
                <Typography variant="body2" sx={{ mb: 1.25 }}>{emergencyStatusText()}</Typography>
                <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: 2, rowGap: 0.5 }}>
                  <Box sx={metaSx}>
                    <AccessTimeIcon />
                    {t('emergency.meta_duration', {
                      hours: ea.duration_hours,
                      defaultValue: `每次 ${ea.duration_hours} 小时`,
                    })}
                  </Box>
                  {quotaActive && (
                    <Box sx={metaSx}>
                      <DataUsageIcon />
                      {t('emergency.meta_quota', {
                        total: bytesToHuman(ea.quota_bytes),
                        defaultValue: `每次 ${bytesToHuman(ea.quota_bytes)} 流量`,
                      })}
                    </Box>
                  )}
                  <Box sx={metaSx}>
                    <ConfirmationNumberIcon />
                    {t('emergency.meta_count', {
                      remaining: ea.remaining,
                      max: ea.max_count,
                      defaultValue: `${ea.remaining} / ${ea.max_count} 次可用`,
                    })}
                  </Box>
                </Box>
              </Box>
            </Box>
            <Button
              variant="contained"
              disabled={emergencyBusy || ea.remaining <= 0 || !ea.available}
              startIcon={emergencyBusy ? <CircularProgress size={14} color="inherit" /> : null}
              onClick={tryEmergency}
              sx={{
                bgcolor: md.tertiary, color: md.onTertiary,
                alignSelf: { xs: 'stretch', sm: 'flex-start' },
                whiteSpace: 'nowrap',
                flexShrink: 0,
                '&:hover': { bgcolor: md.tertiary },
              }}>
              {t('emergency.use')}
            </Button>
          </Box>
          {windowActive && quotaActive && (
            <Box sx={{ mt: 2 }}>
              <Box sx={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', mb: 0.5 }}>
                <Typography variant="caption" sx={{ opacity: 0.82, fontVariantNumeric: 'tabular-nums' }}>
                  {bytesToHuman(ea.used_bytes)} / {bytesToHuman(ea.quota_bytes)}
                </Typography>
                <Typography variant="caption" sx={{ opacity: 0.82, fontVariantNumeric: 'tabular-nums' }}>
                  {usedPct.toFixed(0)}%
                </Typography>
              </Box>
              <Box sx={{ height: 6, borderRadius: 3, bgcolor: 'rgba(0,0,0,0.12)', overflow: 'hidden' }}>
                <Box sx={{
                  height: '100%', width: `${usedPct}%`,
                  bgcolor: md.tertiary, borderRadius: 3,
                  transition: 'width .4s ease',
                }} />
              </Box>
            </Box>
          )}
        </Card>
        )
      })()}
      </Box>{/* end emergency order wrapper */}

      </Box>
      )}{/* end right col */}

      </Box>{/* end two-col flex */}

      {/* Global announcement popup (opt-in via admin settings) */}
      {announcementPopup && (() => {
        const s = announcementStyle(announcement.level)
        return (
          // Reduced borderRadius from the project default (16px) to 12px —
          // the popup body is so short that the larger radius made it look
          // pill-shaped. Two text buttons replace the prior checkbox: the
          // tertiary "不再提醒" persists localStorage dismissal, the primary
          // "我知道了" closes for this session only.
          <Dialog open={announceOpen} onClose={() => closeAnnounce(false)}
            PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 480, maxWidth: '90vw' } }}>
            <DialogTitle sx={{ pt: 3, display: 'flex', alignItems: 'center', gap: 1.25 }}>
              <s.Icon sx={{ color: s.fg }} />
              {announcement.title}
            </DialogTitle>
            <DialogContent>
              <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>
                {announcement.content}
              </Typography>
            </DialogContent>
            <DialogActions>
              <Button variant="text" onClick={() => closeAnnounce(true)}>
                {t('announce.mute', { defaultValue: '不再提醒' })}
              </Button>
              <Button variant="contained" onClick={() => closeAnnounce(false)}>
                {t('announce.ack', { defaultValue: '我知道了' })}
              </Button>
            </DialogActions>
          </Dialog>
        )
      })()}

      {/* Change password dialog */}
      <Dialog open={pwdOpen} onClose={() => !pwdBusy && setPwdOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 440, maxWidth: '90vw' } }}>
        <DialogTitle>{t('password.title')}</DialogTitle>
        <DialogContent>
          <Box component="form" id="pwd-form" onSubmit={submitPwd} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            <TextField required fullWidth type="password" label={t('password.old')}
              value={pwdOld} onChange={e => setPwdOld(e.target.value)} autoComplete="current-password" />
            <TextField required fullWidth type="password" label={t('password.new')}
              value={pwdNew} onChange={e => setPwdNew(e.target.value)} autoComplete="new-password" />
            <TextField required fullWidth type="password" label={t('password.confirm')}
              value={pwdConfirm} onChange={e => setPwdConfirm(e.target.value)} autoComplete="new-password"
              error={pwdConfirm.length > 0 && pwdConfirm !== pwdNew}
              helperText={pwdConfirm.length > 0 && pwdConfirm !== pwdNew ? t('password.mismatch') : ''} />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setPwdOpen(false)} disabled={pwdBusy} variant="text">{t('common.cancel')}</Button>
          <Button type="submit" form="pwd-form" variant="contained" disabled={pwdBusy}
            startIcon={pwdBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common.ok')}
          </Button>
        </DialogActions>
      </Dialog>

      {/* Personal rules dialog */}
      <Dialog open={rulesOpen} onClose={() => !rulesBusy && setRulesOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 600, maxWidth: '95vw' } }}>
        <DialogTitle>{t('rules.title')}</DialogTitle>
        <DialogContent>
          {!profile.can_edit_personal_rules && (
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mb: 1.5 }}>
              {t('rules.readonly_hint', { defaultValue: '管理员已关闭用户自助编辑。当前仅供查看，如需修改请联系管理员。' })}
            </Typography>
          )}
          {/* Format hint above the textarea — visible always so users get
              context without having to clear the field first. */}
          {profile.can_edit_personal_rules && (
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 1 }}>
              {t('rules.hint')}
            </Typography>
          )}
          {rulesBusy
            ? <Box sx={{ display: 'grid', placeItems: 'center', py: 4 }}><CircularProgress size={24} /></Box>
            : <TextField fullWidth multiline minRows={10} maxRows={20}
                value={rulesText} onChange={e => setRulesText(e.target.value)}
                placeholder={t('rules.placeholder')}
                InputProps={{ readOnly: !profile.can_edit_personal_rules }}
                sx={{ '& textarea': { fontSize: 13, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace' } }} />}
        </DialogContent>
        {/* px: 3 (24px) matches MUI's button left-padding so the "插入示例
            规则" text aligns with the dialog content's left edge — without
            it the button's intrinsic padding pushed the text inward and
            looked off. */}
        <DialogActions>
          {/* Insert-example only when the textarea is empty AND the user
              can edit — gives a one-click bootstrap that they can then
              tweak instead of staring at the placeholder. */}
          {profile.can_edit_personal_rules && !rulesText.trim() && !rulesBusy && (
            <Button onClick={() => setRulesText(t('rules.placeholder'))}
              variant="text" sx={{ mr: 'auto' }}>
              {t('rules.insert_example')}
            </Button>
          )}
          <Button onClick={() => setRulesOpen(false)} disabled={rulesBusy} variant="text">
            {profile.can_edit_personal_rules ? t('common.cancel') : t('common.ok')}
          </Button>
          {profile.can_edit_personal_rules && (
            <Button onClick={saveRules}
              disabled={rulesBusy || rulesText.trim() === rulesSaved.trim()}
              variant="contained"
              startIcon={rulesBusy ? <CircularProgress size={16} color="inherit" /> : null}>
              {t('common.ok')}
            </Button>
          )}
        </DialogActions>
      </Dialog>
    </Box>
  )
}

interface UsagePanelProps {
  limitBytes: number
  usage: UsageReport | null
  expireAt: string | null
  // Reset cadence (never/monthly/quarterly/yearly). Shown as a small caption
  // beside "本周期已用" so the user understands when the counter rolls back —
  // without it, "Period used" alone is ambiguous (which period?).
  resetPeriod: string
  md: M3Tokens
}

function UsagePanel({ limitBytes, usage, expireAt, resetPeriod, md }: UsagePanelProps) {
  const { t } = useTranslation('user')
  // The expiry shown here is in the viewer's local timezone, while the panel
  // sets cutoffs against its own zone. Surface that only when they differ, so
  // a user whose browser tz ≠ panel tz understands why their date may not
  // match what an admin quoted.
  const panelTz = useSiteStore(s => s.timezone)
  const browserTz = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone } catch { return '' } })()
  const expireTzDiffers = !!panelTz && !!browserTz && panelTz !== browserTz
  const limitGB = limitBytes / 1024 / 1024 / 1024
  const usedBytes = usage?.period_used_bytes ?? 0
  const todayBytes = usage?.today_used_bytes ?? 0
  const isUnlimited = limitGB === 0
  const usagePercent = isUnlimited ? 0 : Math.min(100, (usedBytes / limitBytes) * 100)
  const usageColor = usagePercent >= 90 ? md.error : usagePercent >= 70 ? md.tertiary : md.primary

  // Expiry — derive remaining days + a progress relative to a 30-day window.
  let expireRemainingDays: number | null = null
  let expireDateStr = ''
  let expireExpired = false
  if (expireAt) {
    const d = new Date(expireAt)
    expireDateStr = d.toLocaleDateString()
    const diffDays = Math.ceil((d.getTime() - Date.now()) / 86400000)
    expireRemainingDays = diffDays
    expireExpired = diffDays < 0
  }
  const expirePercent = expireRemainingDays === null
    ? 0
    : Math.max(0, Math.min(100, (expireRemainingDays / 30) * 100))
  const expireColor = expireExpired
    ? md.error
    : (expireRemainingDays !== null && expireRemainingDays <= 7) ? md.tertiary : md.primary

  return (
    <Card sx={{ p: { xs: 2.5, sm: 3 }, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
      <Typography sx={{ fontWeight: 500, mb: 2.5, fontSize: 16 }}>
        {t('profile.usage_section', { defaultValue: '我的用量' })}
      </Typography>

      {/* Period traffic with progress — single "X.XX / Y GB" line keeps used
          and total side-by-side with one unit so the comparison reads at a
          glance, instead of mixing a big "used" number with a small "limit"
          fraction. */}
      <Box sx={{ mb: 3 }}>
        <Box sx={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', mb: 1 }}>
          <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
            {t('profile.traffic_used')}
            {resetPeriod && resetPeriod !== 'never' && (
              <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant, opacity: 0.7, ml: 0.75 }}>
                · {t(`profile.reset_period.${resetPeriod}`)}
              </Typography>
            )}
          </Typography>
          <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, fontVariantNumeric: 'tabular-nums' }}>
            {isUnlimited
              ? t('profile.unlimited')
              : `${usagePercent.toFixed(1)}%`}
          </Typography>
        </Box>
        <Box sx={{ mb: 1 }}>
          <Typography sx={{ fontSize: 22, fontWeight: 500, fontVariantNumeric: 'tabular-nums' }}>
            {isUnlimited
              ? bytesToHuman(usedBytes)
              : `${(usedBytes / 1024 / 1024 / 1024).toFixed(2)} / ${limitGB.toFixed(0)} GB`}
          </Typography>
        </Box>
        {!isUnlimited && (
          <Box sx={{ height: 8, borderRadius: 4, bgcolor: md.surfaceContainerHighest, overflow: 'hidden' }}>
            <Box sx={{
              height: '100%', width: `${usagePercent}%`,
              bgcolor: usageColor, borderRadius: 4,
              transition: 'width .4s ease',
            }} />
          </Box>
        )}
      </Box>

      {/* Today + Expire side-by-side */}
      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr' }, gap: 3 }}>
        <Box>
          <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mb: 0.5 }}>
            {t('profile.today_used')}
          </Typography>
          <Typography sx={{ fontSize: 18, fontWeight: 500, fontVariantNumeric: 'tabular-nums' }}>
            {bytesToHuman(todayBytes)}
          </Typography>
        </Box>
        <Box>
          <Box sx={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', mb: 0.5 }}>
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
              {t('profile.expire')}
            </Typography>
            {expireRemainingDays !== null && (
              <Typography sx={{ fontSize: 12, color: expireColor, fontVariantNumeric: 'tabular-nums' }}>
                {expireExpired
                  ? t('profile.expired_days', { days: Math.abs(expireRemainingDays) })
                  : expireRemainingDays === 0
                    ? t('profile.today')
                    : t('profile.expire_in', { days: expireRemainingDays })}
              </Typography>
            )}
          </Box>
          <Typography sx={{ fontSize: 18, fontWeight: 500, fontVariantNumeric: 'tabular-nums' }}>
            {expireAt ? expireDateStr : t('profile.expire_permanent')}
          </Typography>
          {expireAt && expireTzDiffers && (
            <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant, mt: 0.25 }}>
              {t('profile.expire_tz_hint', { tz: browserTz })}
            </Typography>
          )}
          {expireAt && (
            <Box sx={{ height: 6, borderRadius: 3, bgcolor: md.surfaceContainerHighest, overflow: 'hidden', mt: 1 }}>
              <Box sx={{
                height: '100%', width: `${expirePercent}%`,
                bgcolor: expireColor, borderRadius: 3,
                transition: 'width .4s ease',
              }} />
            </Box>
          )}
        </Box>
      </Box>
    </Card>
  )
}
