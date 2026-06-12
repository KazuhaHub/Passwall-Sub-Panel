import React, { useEffect, useState, type FormEvent } from 'react'
import {
  Autocomplete,
  Box,
  Button,
  Card,
  Chip,
  CircularProgress,
  Divider,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  IconButton,
  InputAdornment,
  List,
  ListItemButton,
  ListItemText,
  Menu,
  MenuItem,
  Popover,
  Stack,
  Switch,
  Tab,
  Tabs,
  TextField,
  Tooltip,
  Typography,
  alpha,
  useTheme,
} from '@mui/material'
import SaveIcon from '@mui/icons-material/Save'
import VisibilityIcon from '@mui/icons-material/Visibility'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import HelpOutlineIcon from '@mui/icons-material/HelpOutline'
import VisibilityOffIcon from '@mui/icons-material/VisibilityOff'
import SendIcon from '@mui/icons-material/Send'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import CheckCircleIcon from '@mui/icons-material/CheckCircle'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline'
import { useTranslation } from 'react-i18next'

import {
  fetchSAMLMetadata,
  getMailSettings,
  getOIDC,
  getSAML,
  getUISettings,
  previewMailTemplate,
  putMailSettings,
  putMailTemplate,
  resetMailTemplate,
  putOIDC,
  putSAML,
  putUISettings,
  sendTestMail,
  getGeoIPStatus,
  updateGeoIPNow,
  type GeoIPStatus,
  type SAMLMetadataSummary,
  type MailReminderKind,
  type MailSettings,
  type MailTemplate,
  type OIDCConfig,
  type QuickLink,
  type SAMLConfig,
  type SSORoleRule,
  type SubClientApp,
  type SubClientFamily,
  type SubPlatform,
  type SubRenderFormat,
  type UISettings,
} from '@/api/settings'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import DragIndicatorIcon from '@mui/icons-material/DragIndicator'
import AppsIcon from '@mui/icons-material/Apps'
import type { LoginMode } from '@/api/types'
import PageHeader from '@/components/PageHeader'
import { pushSnack } from '@/components/SnackbarHost'
import { confirm } from '@/components/ConfirmHost'
import { QuickLinkIcon, QUICK_LINK_ICONS } from '@/components/QuickLinkIcon'
import {
  type FieldErrors,
  firstError,
  validateEmail,
  validateHost,
  validatePort,
  validateRequired,
  validateUrl,
} from '@/utils/validators'
import { useSiteStore } from '@/stores/site'
import { useTabParam } from '@/hooks/useTabParam'
import { listGroups } from '@/api/groups'
import type { Group } from '@/api/types'

type TabKey = 'general' | 'security' | 'brand' | 'subscription' | 'portal' | 'mail' | 'sso'

// COMMON_TIMEZONES is the option set in the Settings → 面板时区 picker.
// Uses the browser's own IANA database via Intl.supportedValuesOf, which
// returns ~400 entries on modern Chromium/Firefox/Safari — covers every
// timezone go's time.LoadLocation can resolve, with zero manual upkeep.
// freeSolo on the Autocomplete still lets admins type names verbatim.
// Falls back to a tiny hand-rolled list on browsers that don't support
// the API (pre-2022 builds) so the picker never collapses to "no
// options".
const COMMON_TIMEZONES: string[] = (() => {
  try {
    const fn = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf
    if (typeof fn === 'function') return fn('timeZone')
  } catch { /* fall through */ }
  return [
    'UTC', 'America/Los_Angeles', 'America/Denver', 'America/Chicago',
    'America/New_York', 'Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Taipei',
    'Asia/Tokyo', 'Asia/Seoul', 'Asia/Singapore', 'Europe/London',
    'Europe/Paris', 'Europe/Berlin', 'Europe/Moscow', 'Australia/Sydney',
  ]
})()

// GroupSlugPicker is a searchable dropdown over the admin's group catalogue.
// SAML/OIDC both use it for `default_group_slug` so admins don't have to
// remember slugs verbatim. Empty value means "no default group".
function GroupSlugPicker(props: {
  label: string
  value: string
  onChange: (slug: string) => void
  groups: Group[]
}) {
  const { label, value, onChange, groups } = props
  // Match by slug. If the stored slug isn't in the loaded list (stale or
  // not yet loaded) we still surface the raw string so the admin sees what
  // will be saved — losing it silently would be worse than a phantom row.
  const selected = groups.find(g => g.slug === value) ?? null
  return (
    <Autocomplete
      options={groups}
      value={selected}
      onChange={(_, g) => onChange(g?.slug ?? '')}
      getOptionLabel={g => `${g.name} (${g.slug})`}
      isOptionEqualToValue={(o, v) => o.slug === v.slug}
      renderOption={(p, g) => (
        <li {...p} key={g.id}>
          <Box>
            <Typography sx={{ fontSize: 14 }}>{g.name}</Typography>
            <Typography sx={{ fontSize: 12, opacity: 0.7 }}>{g.slug}</Typography>
          </Box>
        </li>
      )}
      renderInput={params => (
        <TextField {...params} label={label}
          placeholder={value && !selected ? value : ''}
          helperText={value && !selected ? `当前值: ${value}` : ''} />
      )}
      fullWidth
      autoHighlight
      clearOnEscape
    />
  )
}

export default function SettingsView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('admin')
  const site = useSiteStore()

  const [tab, setTab] = useTabParam<TabKey>('tab', 'general',
    ['general', 'security', 'brand', 'subscription', 'portal', 'mail', 'sso'])
  const [settings, setSettings] = useState<UISettings | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [geoStatus, setGeoStatus] = useState<GeoIPStatus | null>(null)
  const [geoBusy, setGeoBusy] = useState(false)
  // changeGeoToken mirrors the SMTP-password "kept unchanged" pattern: when a
  // token is already stored, show a read-only chip until the admin clicks Change.
  const [changeGeoToken, setChangeGeoToken] = useState(false)
  // Same "kept unchanged" pattern for the captcha provider secret key.
  const [changeCaptchaSecret, setChangeCaptchaSecret] = useState(false)

  useEffect(() => { void load(); void loadGeoStatus() }, [])

  async function loadGeoStatus() {
    try { setGeoStatus(await getGeoIPStatus()) } catch { /* non-fatal: section still renders */ }
  }

  // runGeoUpdate triggers a DB refresh. The download runs server-side off the
  // request (it can take minutes — doing it inline made the reverse proxy
  // answer 502 before the panel could reply), so we kick it off then poll the
  // status until it finishes, refreshing the active-file dropdown + build dates
  // as we go and surfacing the real success/error from the backend.
  async function runGeoUpdate() {
    setGeoBusy(true)
    try {
      // Persist current settings first (token / source / edition / interval) so
      // the admin doesn't have to click Save separately — the backend update
      // reads the STORED token, not the in-memory form. Bail if validation failed
      // (save already surfaced why); a network error throws into the catch below.
      if (!(await save(undefined, { quiet: true }))) return
      await updateGeoIPNow() // 202: download now runs in the background
      const deadline = Date.now() + 5 * 60 * 1000
      while (Date.now() < deadline) {
        await new Promise(r => setTimeout(r, 2000))
        const st = await getGeoIPStatus()
        setGeoStatus(st)
        if (!st.update.updating) {
          if (st.update.last_error) {
            pushSnack(st.update.last_error, 'warning')
          } else {
            pushSnack(t('settings.geo.update_ok', { defaultValue: '数据库已更新：' }) + (st.update.last_file || ''), 'success')
          }
          return
        }
      }
      // Still running after the wait window — it keeps going server-side.
      pushSnack(t('settings.geo.update_running', { defaultValue: '更新仍在后台进行，稍后刷新查看结果。' }), 'info')
    } catch (e) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error || String(e)
      pushSnack(msg, 'warning')
    } finally { setGeoBusy(false) }
  }

  // Go's `json.Marshal(nil slice)` serialises as `null`, so a fresh DB
  // returns `quick_links: null` etc. instead of an empty array. The
  // editors all call `.length` / `.map` on these unconditionally — defend
  // by normalising at the load/save boundary so the rest of the
  // component can treat them as arrays.
  function normalize(s: UISettings): UISettings {
    return {
      ...s,
      sub_clients: normalizeRegistry(s.sub_clients),
      sub_client_filter_mode: s.sub_client_filter_mode ?? 'blacklist',
      quick_links: s.quick_links ?? [],
      timezone: s.timezone ?? '',
    }
  }

  async function load() {
    setLoading(true)
    try {
      const loaded = normalize(await getUISettings())
      if (!loaded.sub_base_url) {
        loaded.sub_base_url = window.location.origin
      }
      setSettings(loaded)
    }
    finally { setLoading(false) }
  }

  // save persists the whole settings object. Returns true on success, false on
  // a validation failure (already surfaced via snack), and throws on a network
  // error — so callers like runGeoUpdate can gate on the boolean. opts.quiet
  // suppresses the "saved" snack when save is a side-effect of another action.
  async function save(e?: FormEvent, opts: { quiet?: boolean } = {}): Promise<boolean> {
    e?.preventDefault()
    if (!settings) return false
    // Cross-tab submit guard. Brand.sub_base_url is required because the
    // subscription URL embeds it — saving an invalid one silently breaks
    // every client. Emergency-access values only matter when the feature
    // is on; off means "ignore the numbers".
    if (settings.sub_base_url) {
      const urlErr = validateUrl(settings.sub_base_url, { required: true })
      if (urlErr) {
        pushSnack(t(`admin:${urlErr}`) + ' (sub_base_url)', 'warning')
        return false
      }
    }
    if (settings.emergency_access_enabled) {
      if (!Number.isInteger(settings.emergency_access_hours) || settings.emergency_access_hours < 1) {
        pushSnack(t('admin:validation.positive_int') + ' (emergency_access_hours)', 'warning'); return false
      }
      if (!Number.isInteger(settings.emergency_access_max_count) || settings.emergency_access_max_count < 1) {
        pushSnack(t('admin:validation.positive_int') + ' (emergency_access_max_count)', 'warning'); return false
      }
      if (settings.emergency_access_quota_gb < 0) {
        pushSnack(t('admin:validation.non_negative_number') + ' (emergency_access_quota_gb)', 'warning'); return false
      }
    }
    // Announcement: if the admin enabled it but left the title or body
    // empty, the portal would render a chrome-less notice — protect against
    // accidental publishes.
    if (settings.global_announcement?.enabled) {
      if (!settings.global_announcement.title?.trim()) {
        pushSnack(t('admin:validation.required') + ' (announcement title)', 'warning'); return false
      }
      if (!settings.global_announcement.content?.trim()) {
        pushSnack(t('admin:validation.required') + ' (announcement content)', 'warning'); return false
      }
    }
    // Quick links: each enabled row must have a valid label + URL,
    // otherwise the portal would render an empty chip with a broken href.
    for (const [idx, l] of settings.quick_links.entries()) {
      if (!l.enabled) continue
      if (!l.label.trim()) {
        pushSnack(t('admin:validation.required') + ` (quick link #${idx + 1} label)`, 'warning'); return false
      }
      const urlErr = validateUrl(l.url, { required: true })
      if (urlErr) {
        pushSnack(t(`admin:${urlErr}`) + ` (quick link #${idx + 1} URL)`, 'warning'); return false
      }
    }
    setSaving(true)
    try {
      const saved = normalize(await putUISettings(settings))
      setSettings(saved)
      // Mirror brand-relevant fields into the live site store so the layout/header
      // updates immediately without a page reload.
      site.update({
        siteTitle: saved.site_title || 'Kazuha Hub Passwall',
        appTitle: saved.app_title || 'Passwall',
        logoUrl: saved.logo_url || '',
        logoUrlDark: saved.logo_url_dark || '',
        iconUrl: saved.icon_url || '',
        footerText: saved.footer_text || '© Kazuha Hub Passwall',
        themeColor: saved.theme_color || undefined,
      })
      setChangeGeoToken(false)
      setChangeCaptchaSecret(false)
      if (!opts.quiet) pushSnack(t('settings.saved'), 'success')
      return true
    } finally { setSaving(false) }
  }

  function patch<K extends keyof UISettings>(key: K, value: UISettings[K]) {
    setSettings(prev => prev ? { ...prev, [key]: value } : prev)
  }

  if (loading || !settings) {
    return <Box sx={{ p: 3, display: 'grid', placeItems: 'center', minHeight: 400 }}><CircularProgress /></Box>
  }

  const tabs: { key: TabKey; labelKey: string }[] = [
    { key: 'general', labelKey: 'settings.tab_general' },
    { key: 'security', labelKey: 'settings.tab_security' },
    { key: 'brand', labelKey: 'settings.tab_brand' },
    { key: 'subscription', labelKey: 'settings.tab_subscription' },
    { key: 'portal', labelKey: 'settings.tab_portal' },
    { key: 'mail', labelKey: 'settings.tab_mail' },
    { key: 'sso', labelKey: 'settings.tab_sso' },
  ]

  const saveBar = (
    <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
      <Button variant="contained" type="submit" disabled={saving}
        startIcon={saving ? <CircularProgress size={14} color="inherit" /> : <SaveIcon />}>
        {t('settings.save')}
      </Button>
    </Box>
  )

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader title={t('settings.title')} />

      <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ mt: 2, mb: 3, borderBottom: `1px solid ${md.outlineVariant}` }}>
        {tabs.map(tb => <Tab key={tb.key} value={tb.key} label={t(tb.labelKey)} />)}
      </Tabs>

      {tab === 'security' && (
        <Box component="form" onSubmit={save} sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          {saveBar}
          <Section title={t('settings.general.section_login')} md={md}>
            <TextField select fullWidth size="small" label={t('settings.general.login_mode')}
              value={settings.login_mode}
              onChange={e => patch('login_mode', e.target.value as LoginMode)}>
              <MenuItem value="dual">{t('settings.general.login_mode_dual')}</MenuItem>
              <MenuItem value="sso_first">{t('settings.general.login_mode_sso_first')}</MenuItem>
              <MenuItem value="sso_redirect">{t('settings.general.login_mode_sso_redirect')}</MenuItem>
              <MenuItem value="local_only">{t('settings.general.login_mode_local_only')}</MenuItem>
            </TextField>
            <FormControlLabel label={t('settings.general.disallow_user_local_login')}
              control={<Switch checked={settings.disallow_user_local_login}
                onChange={(_, c) => patch('disallow_user_local_login', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <FormControlLabel label={t('settings.general.disallow_user_password_change')}
              control={<Switch checked={settings.disallow_user_password_change}
                onChange={(_, c) => patch('disallow_user_password_change', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <FormControlLabel
              label={t('settings.general.allow_user_personal_rules', { defaultValue: '允许用户编辑个人规则' })}
              control={<Switch checked={settings.allow_user_personal_rules}
                onChange={(_, c) => patch('allow_user_personal_rules', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: -1 }}>
              {t('settings.general.allow_user_personal_rules_hint', {
                defaultValue: '关闭后用户仍可在自助页查看个人规则，但无法保存修改。管理端不受影响，可继续手动为指定用户编辑。',
              })}
            </Typography>
          </Section>

          <Section title={t('settings.general.section_login_security', { defaultValue: '登录安全' })} md={md}>
            {/* CAPTCHA — per-context (login / register / forgot). The provider +
                keys are shared; each context toggles independently. */}
            <Typography variant="body2" sx={{ fontWeight: 500, mb: 0.5 }}>
              {t('settings.general.captcha_contexts', { defaultValue: '验证码适用范围' })}
            </Typography>
            <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, mb: 1 }}>
              <FormControlLabel label={t('settings.general.captcha_ctx_login', { defaultValue: '登录' })}
                control={<Switch checked={settings.captcha_enabled} onChange={(_, c) => patch('captcha_enabled', c)} />}
                sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 0.75 } }} />
              <FormControlLabel label={t('settings.general.captcha_ctx_register', { defaultValue: '注册' })}
                control={<Switch checked={settings.captcha_register_enabled} onChange={(_, c) => patch('captcha_register_enabled', c)} />}
                sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 0.75 } }} />
              <FormControlLabel label={t('settings.general.captcha_ctx_forgot', { defaultValue: '找回密码' })}
                control={<Switch checked={settings.captcha_forgot_enabled} onChange={(_, c) => patch('captcha_forgot_enabled', c)} />}
                sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 0.75 } }} />
            </Box>
            {(settings.captcha_enabled || settings.captcha_register_enabled || settings.captcha_forgot_enabled) && (
              <>
                <TextField select fullWidth size="small"
                  label={t('settings.general.captcha_provider', { defaultValue: '验证码提供方' })}
                  value={settings.captcha_provider || 'image'}
                  onChange={e => patch('captcha_provider', e.target.value as UISettings['captcha_provider'])}>
                  <MenuItem value="image">{t('settings.general.captcha_provider_image', { defaultValue: '内置图形验证码（推荐，无外部依赖）' })}</MenuItem>
                  <MenuItem value="turnstile">Cloudflare Turnstile</MenuItem>
                  <MenuItem value="recaptcha">Google reCAPTCHA v2</MenuItem>
                  <MenuItem value="hcaptcha">hCaptcha</MenuItem>
                </TextField>
                {/* Trigger / threshold are login-only (register & forgot are
                    always-on when enabled — no pre-account failure history). */}
                {settings.captcha_enabled && (
                  <>
                    <TextField select fullWidth size="small"
                      label={t('settings.general.captcha_trigger', { defaultValue: '登录触发时机' })}
                      value={settings.captcha_trigger || 'after_failures'}
                      onChange={e => patch('captcha_trigger', e.target.value as UISettings['captcha_trigger'])}>
                      <MenuItem value="after_failures">{t('settings.general.captcha_trigger_after_failures', { defaultValue: '失败若干次后' })}</MenuItem>
                      <MenuItem value="always">{t('settings.general.captcha_trigger_always', { defaultValue: '始终显示' })}</MenuItem>
                    </TextField>
                    {settings.captcha_trigger !== 'always' && (
                      <NumField label={t('settings.general.captcha_fail_threshold', { defaultValue: '触发阈值（失败次数）' })}
                        value={settings.captcha_fail_threshold}
                        onChange={v => patch('captcha_fail_threshold', v)} />
                    )}
                  </>
                )}
                {settings.captcha_provider && settings.captcha_provider !== 'image' && (
                  <>
                    <TextField fullWidth size="small"
                      label={t('settings.general.captcha_site_key', { defaultValue: 'Site Key（公开）' })}
                      value={settings.captcha_site_key}
                      onChange={e => patch('captcha_site_key', e.target.value)} />
                    {settings.has_captcha_secret_key && !changeCaptchaSecret ? (
                      <Box>
                        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>
                          {t('settings.general.captcha_secret_key', { defaultValue: 'Secret Key（服务端校验）' })}
                        </Typography>
                        <Box sx={{
                          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                          gap: 1.5, minHeight: 40, px: 1.75, py: 0.5,
                          borderRadius: 1.5, border: `1px solid ${md.outlineVariant}`,
                        }}>
                          <Typography variant="body2">{t('settings.geo.token_kept', { defaultValue: '已保存（保持不变）' })}</Typography>
                          <Button size="small" variant="text" onClick={() => setChangeCaptchaSecret(true)}>
                            {t('settings.geo.token_change', { defaultValue: '更改' })}
                          </Button>
                        </Box>
                      </Box>
                    ) : (
                      <TextField fullWidth size="small" type="password" autoComplete="new-password"
                        label={t('settings.general.captcha_secret_key', { defaultValue: 'Secret Key（服务端校验）' })}
                        value={settings.captcha_secret_key ?? ''}
                        onChange={e => patch('captcha_secret_key', e.target.value)} />
                    )}
                  </>
                )}
              </>
            )}
            <Divider sx={{ my: 0.5, borderColor: md.outlineVariant }} />
            {/* Account lockout */}
            <FormControlLabel label={t('settings.general.lockout_enabled', { defaultValue: '启用失败锁定' })}
              control={<Switch checked={settings.lockout_enabled}
                onChange={(_, c) => patch('lockout_enabled', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            {settings.lockout_enabled && (
              <>
                <Pair>
                  <NumField label={t('settings.general.lockout_threshold', { defaultValue: '锁定阈值（失败次数）' })}
                    value={settings.lockout_threshold} onChange={v => patch('lockout_threshold', v)} />
                  <NumField label={t('settings.general.lockout_window_minutes', { defaultValue: '统计窗口（分钟）' })}
                    value={settings.lockout_window_minutes} onChange={v => patch('lockout_window_minutes', v)} />
                </Pair>
                <Pair>
                  <NumField label={t('settings.general.lockout_duration_minutes', { defaultValue: '锁定时长（分钟）' })}
                    value={settings.lockout_duration_minutes} onChange={v => patch('lockout_duration_minutes', v)} />
                  <TextField select fullWidth size="small"
                    label={t('settings.general.lockout_scope', { defaultValue: '锁定范围' })}
                    value={settings.lockout_scope || 'ip_upn'}
                    onChange={e => patch('lockout_scope', e.target.value as UISettings['lockout_scope'])}>
                    <MenuItem value="ip_upn">{t('settings.general.lockout_scope_ip_upn', { defaultValue: 'IP + 用户名（推荐）' })}</MenuItem>
                    <MenuItem value="ip">{t('settings.general.lockout_scope_ip', { defaultValue: '仅 IP' })}</MenuItem>
                  </TextField>
                </Pair>
              </>
            )}
            <Divider sx={{ my: 0.5, borderColor: md.outlineVariant }} />
            {/* Self-service password recovery */}
            <FormControlLabel label={t('settings.general.password_recovery_enabled', { defaultValue: '允许邮箱找回密码' })}
              control={<Switch checked={settings.password_recovery_enabled}
                onChange={(_, c) => patch('password_recovery_enabled', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            {settings.password_recovery_enabled && (
              <>
                <TextField select fullWidth size="small"
                  label={t('settings.general.password_recovery_delivery', { defaultValue: '投递方式' })}
                  value={settings.password_recovery_delivery || 'link'}
                  onChange={e => patch('password_recovery_delivery', e.target.value as UISettings['password_recovery_delivery'])}>
                  <MenuItem value="link">{t('settings.general.password_recovery_delivery_link', { defaultValue: '重置链接' })}</MenuItem>
                  <MenuItem value="otp">{t('settings.general.password_recovery_delivery_otp', { defaultValue: '验证码（OTP）' })}</MenuItem>
                </TextField>
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: -1 }}>
                  {t('settings.general.password_recovery_hint', { defaultValue: '需先配置 SMTP 邮件；仅对有邮箱且本地密码的账号生效，SSO 账号不受影响。' })}
                </Typography>
              </>
            )}
            <Divider sx={{ my: 0.5, borderColor: md.outlineVariant }} />
            {/* Self-service registration */}
            <FormControlLabel label={t('settings.general.registration_enabled', { defaultValue: '允许自助注册' })}
              control={<Switch checked={settings.registration_enabled}
                onChange={(_, c) => patch('registration_enabled', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            {settings.registration_enabled && (
              <>
                <FormControlLabel label={t('settings.general.registration_require_email_verification', { defaultValue: '要求邮箱验证（推荐）' })}
                  control={<Switch checked={settings.registration_require_email_verification}
                    onChange={(_, c) => patch('registration_require_email_verification', c)} />}
                  sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
                {settings.registration_require_email_verification && (
                  <TextField select fullWidth size="small"
                    label={t('settings.general.registration_delivery', { defaultValue: '验证邮件方式' })}
                    value={settings.registration_delivery || 'link'}
                    onChange={e => patch('registration_delivery', e.target.value as UISettings['registration_delivery'])}>
                    <MenuItem value="link">{t('settings.general.password_recovery_delivery_link', { defaultValue: '重置链接' })}</MenuItem>
                    <MenuItem value="otp">{t('settings.general.password_recovery_delivery_otp', { defaultValue: '验证码（OTP）' })}</MenuItem>
                  </TextField>
                )}
                <TextField fullWidth size="small"
                  label={t('settings.general.registration_email_domains', { defaultValue: '允许的邮箱域名（逗号分隔，空=不限）' })}
                  value={settings.registration_email_domains}
                  onChange={e => patch('registration_email_domains', e.target.value)}
                  placeholder="example.com, corp.org" />
                <Pair>
                  <NumField label={t('settings.general.registration_default_group_id', { defaultValue: '默认组 ID（0=第一个组）' })}
                    value={settings.registration_default_group_id}
                    onChange={v => patch('registration_default_group_id', v)} />
                  <NumField label={t('settings.general.registration_default_traffic_gb', { defaultValue: '默认流量 GB（0=不限）' })}
                    value={settings.registration_default_traffic_gb}
                    onChange={v => patch('registration_default_traffic_gb', v)} />
                </Pair>
                <NumField label={t('settings.general.registration_default_expire_days', { defaultValue: '默认有效期（天，0=不过期）' })}
                  value={settings.registration_default_expire_days}
                  onChange={v => patch('registration_default_expire_days', v)} />
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: -1 }}>
                  {t('settings.general.registration_hint', { defaultValue: '注册用户以邮箱作为登录名、角色为普通用户，加入默认组并继承上述配额/有效期。开启邮箱验证需先配置 SMTP；链接方式需先配置订阅基础 URL。' })}
                </Typography>
              </>
            )}
            <Divider sx={{ my: 0.5, borderColor: md.outlineVariant }} />
            {/* Two-factor authentication (2FA). 2FA is the umbrella; the
                authenticator app (TOTP), passkeys, and the email one-time code
                are PEER methods under it (not TOTP == 2FA). "Require for staff"
                is the enrollment policy, kept distinct from the methods. Email +
                the policy are gated on (TOTP || passkey) because both only make
                sense once at least one real factor can be enrolled. */}
            <Typography variant="body2" sx={{ fontWeight: 500, mt: 0.5 }}>
              {t('settings.general.twofa_section_title', { defaultValue: '两步验证（2FA）' })}
            </Typography>
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 2 }}>
              {t('settings.general.twofa_section_hint', { defaultValue: '为本地登录增加第二道验证。可启用下列任意方式（账号在「我的账号」里自助绑定）；登录时验证器代码、一次性备用码、以及账号已绑定的通行密钥都自动可用。' })}
            </Typography>
            {/* Each method (toggle + its hint) is one Box; the Stack spaces the
                method GROUPS apart so they read as distinct peers (TOTP / passkey
                / email) rather than one dense column. */}
            <Stack spacing={2.5}>
              {/* Method: Authenticator app (TOTP) */}
              <Box>
                <FormControlLabel label={t('settings.general.totp_enabled', { defaultValue: '允许验证器 App（TOTP）' })}
                  control={<Switch checked={settings.totp_enabled}
                    onChange={(_, c) => patch('totp_enabled', c)} />}
                  sx={{ ml: 0, display: 'flex', '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, ml: 0.25, mt: 0.5 }}>
                  {t('settings.general.totp_hint', { defaultValue: '允许有本地密码的账号在「我的账号」里用身份验证器 App（TOTP）开启验证。关闭仅阻止新开启，不会移除已启用账号的验证器。SSO 账号不受影响。' })}
                </Typography>
              </Box>

              {/* Method: Passkey (WebAuthn). Same [toggle] -> [hint] shape as the
                  other methods; the passwordless refinement is a clearly-nested
                  sub-toggle AFTER the hint (only shown once passkeys are on). */}
              <Box>
                <FormControlLabel label={t('settings.general.passkey_enabled', { defaultValue: '允许通行密钥（Passkey）' })}
                  control={<Switch checked={settings.passkey_enabled}
                    onChange={(_, c) => patch('passkey_enabled', c)} />}
                  sx={{ ml: 0, display: 'flex', '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, ml: 0.25, mt: 0.5 }}>
                  {t('settings.general.passkey_hint', { defaultValue: '允许有本地密码的账号在「我的账号」里绑定通行密钥（WebAuthn）。开启免密登录后，登录页会显示「使用通行密钥登录」按钮、无需输入用户名。需先配置「订阅基础 URL」（用于确定 RP 域名），且站点须为 HTTPS（localhost 除外）。SSO 账号不受影响。' })}
                </Typography>
                {settings.passkey_enabled && (
                  <Box sx={{ ml: 3, mt: 1, pl: 1.5, borderLeft: `2px solid ${md.outlineVariant}` }}>
                    <FormControlLabel label={t('settings.general.passkey_passwordless', { defaultValue: '允许通行密钥免密登录（无需用户名）' })}
                      control={<Switch checked={settings.passkey_passwordless}
                        onChange={(_, c) => patch('passkey_passwordless', c)} />}
                      sx={{ ml: 0, display: 'flex', '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
                  </Box>
                )}
              </Box>

              {/* Method: Email one-time code — a weaker challenge-time fallback,
                  only relevant once a real factor (TOTP/passkey) is enabled. */}
              {(settings.totp_enabled || settings.passkey_enabled) && (
                <Box>
                  <FormControlLabel label={t('settings.general.twofa_allow_email', { defaultValue: '邮箱一次性验证码（较弱，需配置 SMTP）' })}
                    control={<Switch checked={settings.twofa_allow_email} onChange={(_, c) => patch('twofa_allow_email', c)} />}
                    sx={{ ml: 0, display: 'flex', '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
                  <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, ml: 0.25, mt: 0.5 }}>
                    {t('settings.general.twofa_allow_email_hint', { defaultValue: '作为登录挑战时的备选验证方式（账号需已启用上面任一方式）。它较弱——掌握密码 + 邮箱即可通过，故默认关闭。' })}
                  </Typography>
                  {settings.twofa_allow_email && (
                    <Box sx={{ mt: 1, maxWidth: 280 }}>
                      <NumField label={t('settings.general.twofa_email_cooldown', { defaultValue: '验证码重发冷却（秒）' })}
                        value={settings.twofa_email_resend_cooldown_sec}
                        onChange={v => patch('twofa_email_resend_cooldown_sec', v)} />
                    </Box>
                  )}
                </Box>
              )}
            </Stack>
            {/* Policy: enrollment enforcement (not a method) */}
            {(settings.totp_enabled || settings.passkey_enabled) && (
              <Box sx={{ mt: 3 }}>
                <Typography variant="body2" sx={{ fontWeight: 500, mb: 1 }}>
                  {t('settings.general.twofa_policy_subheading', { defaultValue: '强制策略' })}
                </Typography>
                <FormControlLabel label={t('settings.general.require_2fa_for_staff', { defaultValue: '强制所有管理员/运营启用两步验证' })}
                  control={<Switch checked={settings.require_2fa_for_staff} onChange={(_, c) => patch('require_2fa_for_staff', c)} />}
                  sx={{ ml: 0, display: 'flex', '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, ml: 0.25, mt: 0.5 }}>
                  {t('settings.general.require_2fa_for_staff_hint', { defaultValue: '开启后，未绑定 TOTP 或通行密钥的管理员/运营登录后会被要求先完成设置才能使用面板。也可在「分组」对每组单独强制。仅本地账号生效。' })}
                </Typography>
              </Box>
            )}
            <Box sx={{ mt: 2.5, p: 1.25, borderRadius: 1.5, bgcolor: md.surfaceContainerHighest, display: 'flex', gap: 1, alignItems: 'flex-start' }}>
              <InfoOutlinedIcon sx={{ fontSize: 18, color: md.onSurfaceVariant, mt: '1px', flexShrink: 0 }} />
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                {t('settings.general.local_auth_scope_hint', { defaultValue: '两步验证与通行密钥只作用于本地登录；通过 SSO 登录的二次验证由身份提供商（IdP）负责，PSP 不重复施加。同时拥有本地密码与 SSO 绑定的账号，走 SSO 登录时不受此处约束。' })}
              </Typography>
            </Box>
          </Section>

          <Section title={t('settings.general.section_security')} md={md}>
            <Pair>
              <TextField fullWidth label={t('settings.general.jwt_issuer')}
                value={settings.jwt_issuer} onChange={e => patch('jwt_issuer', e.target.value)} />
            </Pair>
            <Pair>
              <NumField label={t('settings.general.jwt_access_ttl_minutes')} value={settings.jwt_access_ttl_minutes}
                onChange={v => patch('jwt_access_ttl_minutes', v)} />
              <NumField label={t('settings.general.jwt_refresh_ttl_minutes')} value={settings.jwt_refresh_ttl_minutes}
                onChange={v => patch('jwt_refresh_ttl_minutes', v)} />
            </Pair>
            <Pair>
              <NumField label={t('settings.general.sub_per_ip_per_min')} value={settings.sub_per_ip_per_min}
                onChange={v => patch('sub_per_ip_per_min', v)} />
              <NumField label={t('settings.general.login_per_ip_per_min')} value={settings.login_per_ip_per_min}
                onChange={v => patch('login_per_ip_per_min', v)} />
            </Pair>
          </Section>
        </Box>
      )}

      {tab === 'general' && (
        <Box component="form" onSubmit={save} sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          {saveBar}
          <Section title={t('settings.general.section_runtime')} md={md}>
            <Autocomplete
              freeSolo
              size="small"
              options={COMMON_TIMEZONES}
              value={settings.timezone || ''}
              inputValue={settings.timezone || ''}
              onInputChange={(_, v) => patch('timezone', v)}
              onChange={(_, v) => patch('timezone', (v as string) ?? '')}
              fullWidth
              renderInput={(params) => (
                <TextField {...params}
                  label={t('settings.general.timezone')}
                  placeholder={t('settings.general.timezone_placeholder', { defaultValue: '例如 America/Los_Angeles' })}
                  helperText={t('settings.general.timezone_hint')} />
              )} />
            <Pair>
              <NumField label={t('settings.general.cron_traffic_pull_minutes')} value={settings.cron_traffic_pull_minutes}
                onChange={v => patch('cron_traffic_pull_minutes', v)} />
              <NumField label={t('settings.general.cron_reconcile_minutes')} value={settings.cron_reconcile_minutes}
                onChange={v => patch('cron_reconcile_minutes', v)} />
            </Pair>
            <NumField label={t('settings.general.max_panel_concurrency')}
              value={settings.max_panel_concurrency}
              onChange={v => patch('max_panel_concurrency', v)}
              helperText={t('settings.general.max_panel_concurrency_hint', {
                defaultValue: '并发拉取每个 3X-UI 面板入站数据的上限。0 = 使用默认值 8。单面板部署调高无意义；多面板（5+）+ 3X-UI 服务器空闲时可调到 16-32。> 64 会被自动夹回 64。',
              })} />
            <Pair>
              <NumField label={t('settings.general.audit_retention_days')} value={settings.audit_retention_days}
                onChange={v => patch('audit_retention_days', v)} />
              <NumField label={t('settings.general.sync_task_retention_days')} value={settings.sync_task_retention_days}
                onChange={v => patch('sync_task_retention_days', v)} />
            </Pair>
            <NumField label={t('settings.general.auth_event_retention_days', { defaultValue: '认证日志保留（天）' })}
              value={settings.auth_event_retention_days}
              onChange={v => patch('auth_event_retention_days', v)}
              helperText={t('settings.general.auth_event_retention_days_hint', { defaultValue: '登录日志（含失败）的保留天数；最小 90。' })} />
            <NumField label={t('settings.general.traffic_history_days')}
              value={settings.traffic_history_days}
              onChange={v => patch('traffic_history_days', v)}
              helperText={t('settings.general.traffic_history_days_hint')} />
            <Pair>
              <NumField label={t('settings.general.expire_before_days', { defaultValue: '到期前 N 天提醒' })}
                value={settings.expire_before_days}
                onChange={v => patch('expire_before_days', v)}
                helperText={t('settings.general.expire_before_days_hint', {
                  defaultValue: '账号到期前多少天发送邮件提醒。',
                })} />
              <NumField label={t('settings.general.traffic_remain_percent', { defaultValue: '流量剩余 < N% 时提醒' })}
                value={settings.traffic_remain_percent}
                onChange={v => patch('traffic_remain_percent', v)}
                helperText={t('settings.general.traffic_remain_percent_hint', {
                  defaultValue: '剩余流量低于此百分比时发送邮件提醒。',
                })} />
            </Pair>
          </Section>

          <Section title={t('settings.general.emergency_section')} md={md}>
            <FormControlLabel label={t('settings.general.emergency_access_enabled')}
              control={<Switch checked={settings.emergency_access_enabled}
                onChange={(_, c) => patch('emergency_access_enabled', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <Pair>
              <NumField label={t('settings.general.emergency_access_hours')} value={settings.emergency_access_hours}
                onChange={v => patch('emergency_access_hours', v)} />
              <NumField label={t('settings.general.emergency_access_max_count')} value={settings.emergency_access_max_count}
                onChange={v => patch('emergency_access_max_count', v)} />
            </Pair>
            <NumField label={t('settings.general.emergency_access_quota_gb')}
              value={settings.emergency_access_quota_gb}
              onChange={v => patch('emergency_access_quota_gb', v)}
              step="any"
              helperText={t('settings.general.emergency_access_quota_gb_hint')} />
          </Section>

          {/* ACME certificate automation settings moved to the Certificates page
              (its own "ACME settings" tab) — the cert_* / acme_* fields still
              round-trip through this PUT untouched so they aren't reset. */}

          <Section title={t('settings.geo.section', { defaultValue: 'IP 地区显示（访问日志）' })} md={md}>
            <FormControlLabel label={t('settings.geo.enabled', { defaultValue: '启用 IP 地区显示' })}
              control={<Switch checked={settings.geo_ip_enabled} onChange={(_, c) => patch('geo_ip_enabled', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
              {t('settings.geo.hint', { defaultValue: '完全离线：用本地 .mmdb 库解析，不外呼第三方、不缓存。把库放进 ' })}
              <code>{geoStatus?.dir || '<ConfigDir>/geoip/'}</code>
              {t('settings.geo.hint2', { defaultValue: '，或在下方配置自动更新。国家/省较可靠，城市仅供参考。' })}
            </Typography>

            <TextField select fullWidth size="small" label={t('settings.geo.active_db', { defaultValue: '激活数据库' })}
              value={settings.geo_ip_db_file ?? ''}
              onChange={e => patch('geo_ip_db_file', e.target.value)}
              SelectProps={{ displayEmpty: true }}
              // displayEmpty renders the placeholder MenuItem when value="",
              // but with an empty value the InputLabel won't auto-shrink and
              // ends up overlapping that placeholder text. Force shrink so the
              // label floats into the outline notch.
              InputLabelProps={{ shrink: true }}
              helperText={t('settings.geo.active_db_hint', { defaultValue: '存在多个库时选用哪个（留空=按名取第一个）。只有一个激活源——不合并、无冲突。' })}>
              <MenuItem value="">{t('settings.geo.auto', { defaultValue: '（自动：按文件名第一个）' })}</MenuItem>
              {(geoStatus?.available || []).map(db => (
                <MenuItem key={db.file} value={db.file}>{db.file} — {db.granularity}{db.type ? ` · ${db.type}` : ''}</MenuItem>
              ))}
            </TextField>

            <Box sx={{ fontSize: 12, color: md.onSurfaceVariant, display: 'flex', flexDirection: 'column', gap: 0.25 }}>
              {(geoStatus?.available?.length ?? 0) === 0
                ? t('settings.geo.no_db', { defaultValue: '未发现 .mmdb 库——放一个进去，或配置自动更新后点「立即更新」。' })
                : geoStatus!.available.map(db => (
                  <span key={db.file}>
                    {db.active ? '● ' : '○ '}{db.file}
                    {db.error
                      ? ` — ⚠ ${db.error}`
                      : ` — ${db.granularity}${db.type ? ` · ${db.type}` : ''}${db.build_epoch ? ` · ${t('settings.geo.built', { defaultValue: '构建' })} ${new Date(db.build_epoch * 1000).toLocaleDateString()}` : ''}`}
                  </span>
                ))}
            </Box>

            <FormControlLabel label={t('settings.geo.auto_update', { defaultValue: '自动更新数据库' })}
              control={<Switch checked={settings.geo_ip_auto_update} onChange={(_, c) => patch('geo_ip_auto_update', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <TextField type="number" size="small" sx={{ maxWidth: 260 }}
              label={t('settings.geo.update_interval_hours', { defaultValue: '自动更新间隔（小时）' })}
              value={settings.geo_ip_update_interval_hours || 12}
              onChange={e => patch('geo_ip_update_interval_hours', Math.max(1, Math.floor(Number(e.target.value) || 12)))}
              inputProps={{ min: 1, step: 1 }}
              disabled={!settings.geo_ip_auto_update}
              helperText={t('settings.geo.update_interval_hint', { defaultValue: '最小 1 小时；修改后无需重启即生效。' })} />
            <Pair>
              <TextField select fullWidth size="small" label={t('settings.geo.source', { defaultValue: '更新来源' })}
                value={settings.geo_ip_update_source || 'maxmind'}
                onChange={e => {
                  // Switching source clears the edition so each source's own
                  // default applies — a leftover MaxMind edition would break IPinfo.
                  patch('geo_ip_update_source', e.target.value as UISettings['geo_ip_update_source'])
                  patch('geo_ip_update_edition', '')
                }}>
                <MenuItem value="maxmind">{t('settings.geo.source_maxmind', { defaultValue: 'MaxMind GeoLite2（城市级，推荐）' })}</MenuItem>
                <MenuItem value="dbip">{t('settings.geo.source_dbip', { defaultValue: 'DB-IP City Lite（城市级，免账号）' })}</MenuItem>
                <MenuItem value="ipinfo">{t('settings.geo.source_ipinfo', { defaultValue: 'IPinfo Lite（仅国家 + ASN）' })}</MenuItem>
                <MenuItem value="custom">{t('settings.geo.source_custom', { defaultValue: '自定义 URL' })}</MenuItem>
              </TextField>
              {(settings.geo_ip_update_source === 'maxmind' || settings.geo_ip_update_source === 'ipinfo') && (
                <TextField fullWidth size="small"
                  label={settings.geo_ip_update_source === 'ipinfo'
                    ? t('settings.geo.edition_ipinfo', { defaultValue: 'IPinfo 数据库（付费可填其他产品）' })
                    : t('settings.geo.edition', { defaultValue: 'MaxMind edition（付费可填 GeoIP2-City）' })}
                  value={settings.geo_ip_update_edition} onChange={e => patch('geo_ip_update_edition', e.target.value)}
                  placeholder={settings.geo_ip_update_source === 'ipinfo' ? 'ipinfo_lite' : 'GeoLite2-City'} />
              )}
              {settings.geo_ip_update_source === 'custom' && (
                <TextField fullWidth size="small" label={t('settings.geo.url', { defaultValue: '下载 URL（.mmdb / .gz / .tar.gz）' })}
                  value={settings.geo_ip_update_url} onChange={e => patch('geo_ip_update_url', e.target.value)}
                  placeholder="https://…/db.mmdb" />
              )}
            </Pair>
            {settings.geo_ip_update_source !== 'dbip' && (() => {
              const tokenLabel = settings.geo_ip_update_source === 'maxmind'
                ? t('settings.geo.token_maxmind', { defaultValue: 'MaxMind License Key' })
                : settings.geo_ip_update_source === 'ipinfo'
                  ? t('settings.geo.token_ipinfo', { defaultValue: 'IPinfo Token' })
                  : t('settings.geo.token_custom', { defaultValue: 'Token / 凭据（可选）' })
              // Kept-unchanged pattern (mirrors the SMTP password field): a stored
              // token shows as a read-only chip + Change, so saving never wipes it.
              return settings.has_geo_ip_update_token && !changeGeoToken ? (
                <Box>
                  <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>{tokenLabel}</Typography>
                  <Box sx={{
                    display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                    gap: 1.5, minHeight: 40, px: 1.75, py: 0.5,
                    borderRadius: 1.5, border: `1px solid ${md.outlineVariant}`,
                  }}>
                    <Typography variant="body2">{t('settings.geo.token_kept', { defaultValue: '已保存（保持不变）' })}</Typography>
                    <Button size="small" variant="text" onClick={() => setChangeGeoToken(true)}>
                      {t('settings.geo.token_change', { defaultValue: '更改' })}
                    </Button>
                  </Box>
                </Box>
              ) : (
                <TextField fullWidth size="small" type="password" autoComplete="new-password"
                  label={tokenLabel}
                  value={settings.geo_ip_update_token ?? ''}
                  onChange={e => patch('geo_ip_update_token', e.target.value)} />
              )
            })()}
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, flexWrap: 'wrap' }}>
              <Button variant="outlined" size="small" disabled={geoBusy} onClick={runGeoUpdate}
                startIcon={geoBusy ? <CircularProgress size={14} color="inherit" /> : undefined}>
                {t('settings.geo.update_now', { defaultValue: '立即更新' })}
              </Button>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                {t('settings.geo.update_hint', { defaultValue: '先保存设置（含 token）再点立即更新。MaxMind 库须 30 天内更新一次（EULA），建议开启自动更新。' })}
              </Typography>
            </Box>
            <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
              {t('settings.geo.attribution', { defaultValue: '数据来源需在使用处署名：MaxMind（GeoLite2）/ DB-IP / IPinfo —— 视所用库而定。' })}
            </Typography>
          </Section>
        </Box>
      )}

      {tab === 'brand' && (
        <Box component="form" onSubmit={save} sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          {saveBar}
          <Section title={t('settings.brand.section_text')} md={md}>
            <Pair>
              <TextField fullWidth label={t('settings.brand.site_title')}
                value={settings.site_title} onChange={e => patch('site_title', e.target.value)} />
              <TextField fullWidth label={t('settings.brand.app_title')}
                value={settings.app_title} onChange={e => patch('app_title', e.target.value)} />
            </Pair>
            <TextField fullWidth label={t('settings.brand.footer_text')}
              value={settings.footer_text} onChange={e => patch('footer_text', e.target.value)} />
            {(() => {
              // Live URL check so the admin sees the format error inline
              // instead of having to hit save and read the snack. validateUrl
              // returns '' for valid OR empty; we mark empty as required
              // separately so the asterisk and message agree.
              const err = settings.sub_base_url
                ? validateUrl(settings.sub_base_url, { required: true })
                : 'validation.required'
              return (
                <TextField required fullWidth label={t('settings.brand.sub_base_url')}
                  value={settings.sub_base_url} onChange={e => patch('sub_base_url', e.target.value)}
                  error={!!err}
                  helperText={err ? t(`admin:${err}`) : ''} />
              )
            })()}
          </Section>

          <Section title={t('settings.brand.section_assets')} md={md}>
            <TextField fullWidth label={t('settings.brand.icon_url')}
              value={settings.icon_url} onChange={e => patch('icon_url', e.target.value)} />
            <Pair>
              <TextField fullWidth label={t('settings.brand.logo_url')}
                value={settings.logo_url} onChange={e => patch('logo_url', e.target.value)} />
              <TextField fullWidth label={t('settings.brand.logo_url_dark')}
                value={settings.logo_url_dark} onChange={e => patch('logo_url_dark', e.target.value)} />
            </Pair>
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
              {t('settings.brand.asset_hint')}
            </Typography>
          </Section>

          <Section title={t('settings.brand.section_theme')} md={md}>
            <Box sx={{ display: 'flex', gap: 1.5, alignItems: 'center' }}>
              <Box
                component="input"
                type="color"
                value={settings.theme_color || '#0061A4'}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => patch('theme_color', e.target.value.toUpperCase())}
                sx={{
                  width: 56, height: 56, p: 0, border: 'none', borderRadius: 2,
                  bgcolor: 'transparent', cursor: 'pointer', flexShrink: 0,
                  '&::-webkit-color-swatch-wrapper': { p: 0 },
                  '&::-webkit-color-swatch': { border: `1px solid ${md.outlineVariant}`, borderRadius: 8 },
                }}
              />
              <TextField fullWidth label={t('settings.brand.theme_color')}
                value={settings.theme_color}
                onChange={e => patch('theme_color', e.target.value)}
                placeholder="#0061A4" />
              {settings.theme_color && (
                <Button size="small" variant="text" onClick={() => patch('theme_color', '')} sx={{ flexShrink: 0 }}>
                  {t('settings.brand.theme_color_clear')}
                </Button>
              )}
            </Box>
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
              {t('settings.brand.theme_color_hint')}
            </Typography>
          </Section>

          <Section title={t('settings.brand.section_email')} md={md}>
            <TextField fullWidth label={t('settings.brand.email_domain')}
              value={settings.email_domain} onChange={e => patch('email_domain', e.target.value)} />
          </Section>
        </Box>
      )}

      {tab === 'subscription' && (
        <Box component="form" onSubmit={save} sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          {saveBar}
          <Section title={t('settings.subscription.section_basic')} md={md}>
            <TextField fullWidth label={t('settings.subscription.sub_path')}
              value={'/' + (settings.sub_path || '').replace(/^\/+/, '')}
              onChange={e => {
                // Force a single leading slash; users can't delete it past
                // the first character. Stripping multiples handles paste of
                // "/sub" into the existing displayed slash.
                const stripped = e.target.value.replace(/^\/+/, '')
                patch('sub_path', stripped)
              }} />
            <TextField fullWidth label={t('settings.subscription.sub_import_tutorial_url')}
              value={settings.sub_import_tutorial_url}
              onChange={e => patch('sub_import_tutorial_url', e.target.value)} />
            <Pair>
              <NumField label={t('settings.subscription.sub_log_retention_days')}
                value={settings.sub_log_retention_days}
                onChange={v => patch('sub_log_retention_days', v)} />
              <NumField label={t('settings.subscription.sub_update_interval_hours')}
                value={settings.sub_update_interval_hours}
                onChange={v => patch('sub_update_interval_hours', v)} />
            </Pair>
            <TextField fullWidth
              label={t('settings.subscription.sub_profile_name_template')}
              placeholder="{{ site_title }} - {{ user }}"
              value={settings.sub_profile_name_template}
              onChange={e => patch('sub_profile_name_template', e.target.value)}
              helperText={t('settings.subscription.sub_profile_name_template_hint')} />
            <FormControlLabel
              label={t('settings.subscription.sub_region_flag_prefix')}
              control={<Switch checked={settings.sub_region_flag_prefix}
                onChange={(_, c) => patch('sub_region_flag_prefix', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: -1 }}>
              {t('settings.subscription.sub_region_flag_prefix_hint')}
            </Typography>
          </Section>

          <Section title={t('settings.subscription.section_protection')} md={md}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
              <TextField select size="small" label={t('settings.subscription.filter_mode', { defaultValue: '客户端过滤模式' })}
                value={settings.sub_client_filter_mode}
                onChange={e => patch('sub_client_filter_mode', e.target.value as 'blacklist' | 'whitelist')}
                sx={{ minWidth: 260 }}>
                <MenuItem value="blacklist">{t('settings.subscription.filter_mode_blacklist', { defaultValue: '黑名单（默认放行，仅拦禁用项）' })}</MenuItem>
                <MenuItem value="whitelist">{t('settings.subscription.filter_mode_whitelist', { defaultValue: '白名单（默认拦截，仅放行已知）' })}</MenuItem>
              </TextField>
              <Tooltip arrow placement="right" title={
                <Box sx={{ whiteSpace: 'pre-line', fontSize: 12, p: 0.5, maxWidth: 320 }}>
                  {t('settings.subscription.filter_mode_help', { defaultValue: '黑名单（默认）：只拦截你明确「禁用」的客户端族，未识别的客户端放行（按 mihomo 兜底）。\n\n白名单：只放行「已知且启用」的客户端族，未识别 / 未启用的一律拦截，并计入异常次数（达到阈值可能触发自动停用）。适合严格防滥用。' })}
                </Box>
              }>
                <IconButton size="small" sx={{ color: md.onSurfaceVariant }}>
                  <HelpOutlineIcon fontSize="small" />
                </IconButton>
              </Tooltip>
            </Box>
            <FormControlLabel label={t('settings.subscription.sub_block_auto_disable')}
              control={<Switch checked={settings.sub_block_auto_disable}
                onChange={(_, c) => patch('sub_block_auto_disable', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <NumField label={t('settings.subscription.sub_block_auto_disable_count')}
              value={settings.sub_block_auto_disable_count}
              onChange={v => patch('sub_block_auto_disable_count', v)} />
            <FormControlLabel label={t('settings.subscription.sub_block_notify_user', { defaultValue: '被禁客户端时邮件提醒用户' })}
              control={<Switch checked={settings.sub_block_notify_user}
                onChange={(_, c) => patch('sub_block_notify_user', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            {settings.sub_block_notify_user && (
              <NumField label={t('settings.subscription.sub_block_notify_max_per_day', { defaultValue: '每天最多发送（条）' })}
                value={settings.sub_block_notify_max_per_day}
                onChange={v => patch('sub_block_notify_max_per_day', v)} />
            )}
          </Section>

          <ClientRegistryEditor
            families={settings.sub_clients}
            onChange={v => patch('sub_clients', v)}
            md={md}
          />
        </Box>
      )}

      {tab === 'portal' && (
        <Box component="form" onSubmit={save} sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          {saveBar}
          <QuickLinksEditor
            links={settings.quick_links}
            onChange={v => patch('quick_links', v)}
            md={md}
          />
          <Section title={t('settings.portal.section_announcement')} md={md}>
            <FormControlLabel label={t('settings.portal.announcement.enabled')}
              control={<Switch checked={settings.global_announcement?.enabled ?? false}
                onChange={(_, c) => patch('global_announcement', { ...settings.global_announcement, enabled: c })} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <FormControlLabel
              label={t('settings.portal.announcement.popup', { defaultValue: '以弹窗形式展示（首次进入网站时弹窗）' })}
              control={<Switch checked={settings.global_announcement?.popup ?? false}
                disabled={!(settings.global_announcement?.enabled ?? false)}
                onChange={(_, c) => patch('global_announcement', { ...settings.global_announcement, popup: c })} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: -1 }}>
              {t('settings.portal.announcement.popup_hint', {
                defaultValue: '用户可点击"我知道了"关闭，或勾选"不再提醒"。提示状态仅保存在用户浏览器本地；公告内容更新后会再次弹出。',
              })}
            </Typography>
            <Pair>
              {/* Both fields size="small" so Title and Level land on
                  the same baseline. Default (medium) Title was ~56px
                  while the size="small" Level was ~40px — looked
                  misaligned on the same row. */}
              <TextField size="small" fullWidth required={!!settings.global_announcement?.enabled}
                label={t('settings.portal.announcement.title')}
                value={settings.global_announcement?.title ?? ''}
                onChange={e => patch('global_announcement', { ...settings.global_announcement, title: e.target.value })} />
              <TextField select size="small" fullWidth label={t('settings.portal.announcement.level')}
                value={settings.global_announcement?.level ?? 'info'}
                onChange={e => patch('global_announcement', { ...settings.global_announcement, level: e.target.value as 'info' | 'warning' | 'danger' })}>
                <MenuItem value="info">{t('settings.portal.announcement.level_info')}</MenuItem>
                <MenuItem value="warning">{t('settings.portal.announcement.level_warning')}</MenuItem>
                <MenuItem value="danger">{t('settings.portal.announcement.level_danger')}</MenuItem>
              </TextField>
            </Pair>
            <TextField fullWidth multiline minRows={4} required={!!settings.global_announcement?.enabled}
              label={t('settings.portal.announcement.content')}
              value={settings.global_announcement?.content ?? ''}
              onChange={e => patch('global_announcement', { ...settings.global_announcement, content: e.target.value })} />
          </Section>
        </Box>
      )}

      {tab === 'mail' && <MailTab />}

      {tab === 'sso' && <SsoTab />}
    </Box>
  )
}

function MailTab() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('admin')

  const [mail, setMail] = useState<MailSettings | null>(null)
  const [templates, setTemplates] = useState<MailTemplate[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [changePwd, setChangePwd] = useState(false)
  const [showPwd, setShowPwd] = useState(false)
  const [testTo, setTestTo] = useState('')
  const [testBusy, setTestBusy] = useState(false)
  const [activeTpl, setActiveTpl] = useTabParam<MailReminderKind>('tpl', 'expire_before',
    ['expire_before', 'expired', 'traffic_low', 'traffic_exhausted', 'account_disabled', 'account_enabled', 'announcement', 'blocked_client', 'password_reset', 'email_verify', 'login_2fa'])
  const [tplBusy, setTplBusy] = useState(false)
  const [previewBusy, setPreviewBusy] = useState(false)
  const [preview, setPreview] = useState<{ subject: string; body: string; kind: MailReminderKind } | null>(null)
  // Anchor for the "可用变量" popover next to the enable switch — opens a
  // cheat sheet so admins don't have to dig through code to remember the
  // {{.UPN}} / {{.ExpireAt}} / {{if .DisableDetail}} syntax.
  const [tplVarsAnchor, setTplVarsAnchor] = useState<HTMLElement | null>(null)
  type MailField = 'smtp_host' | 'smtp_port' | 'from_email' | 'test_to'
  const [errs, setErrs] = useState<FieldErrors<MailField>>({})

  useEffect(() => { void load() }, [])

  async function load() {
    setLoading(true)
    try {
      const r = await getMailSettings()
      setMail(r.settings)
      setTemplates(r.templates)
    } finally { setLoading(false) }
  }

  function patchMail<K extends keyof MailSettings>(key: K, value: MailSettings[K]) {
    setMail(prev => prev ? { ...prev, [key]: value } : prev)
  }

  // Same gate as SSO panels: only nag about required SMTP fields when the
  // admin has actually flipped mail.enabled on. Until then the placeholders
  // are aspirational and the form should stay quiet.
  function validateMail(m: MailSettings): FieldErrors<MailField> {
    if (!m.enabled) return {}
    return {
      smtp_host: validateHost(m.smtp_host, { required: true }),
      smtp_port: validatePort(m.smtp_port, { required: true }),
      from_email: validateEmail(m.from_email, { required: true }),
    }
  }

  async function save(e?: FormEvent) {
    e?.preventDefault()
    if (!mail) return
    const v = validateMail(mail)
    setErrs(v)
    const firstKey = firstError(v)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return }
    setSaving(true)
    try {
      const payload: MailSettings = { ...mail }
      // Drop the password only when we're in "keep existing" mode (there IS
      // an existing one AND user didn't click "change"). On a fresh setup
      // has_smtp_password is false, the input is shown, and dropping the
      // field would lose the new value the admin just typed.
      if (mail.has_smtp_password && !changePwd) delete payload.smtp_password
      const saved = await putMailSettings(payload)
      setMail(saved)
      setChangePwd(false)
      pushSnack(t('settings.mail.saved'), 'success')
    } finally { setSaving(false) }
  }

  async function test() {
    const err = validateEmail(testTo, { required: true })
    setErrs(prev => ({ ...prev, test_to: err }))
    if (err) { pushSnack(t(`admin:${err}`), 'warning'); return }
    setTestBusy(true)
    try {
      await sendTestMail(testTo)
      pushSnack(t('settings.mail.test_sent'), 'success')
    } finally { setTestBusy(false) }
  }

  function patchTpl(kind: MailReminderKind, patch: Partial<MailTemplate>) {
    setTemplates(prev => prev.map(t => t.kind === kind ? { ...t, ...patch } : t))
  }

  async function saveTpl(tpl: MailTemplate) {
    setTplBusy(true)
    try {
      const saved = await putMailTemplate(tpl)
      setTemplates(prev => prev.map(t => t.kind === saved.kind ? saved : t))
      pushSnack(t('settings.mail.saved'), 'success')
    } finally { setTplBusy(false) }
  }

  async function previewTpl(tpl: MailTemplate) {
    setPreviewBusy(true)
    try {
      const rendered = await previewMailTemplate(tpl)
      setPreview({ ...rendered, kind: tpl.kind })
    } finally { setPreviewBusy(false) }
  }

  async function resetTpl(kind: MailReminderKind) {
    if (!(await confirm({
      title: t('settings.mail.reset_confirm_title', { defaultValue: '重置为默认模板？' }),
      message: t('settings.mail.reset_confirm_body', { defaultValue: '当前模板将被默认模板覆盖，自定义内容会丢失。' }),
      confirmText: t('settings.mail.reset_confirm_ok', { defaultValue: '重置' }),
      destructive: true,
    }))) return
    setTplBusy(true)
    try {
      const restored = await resetMailTemplate(kind)
      setTemplates(prev => prev.map(t => t.kind === restored.kind ? restored : t))
      pushSnack(t('settings.mail.reset_done', { defaultValue: '已重置为默认模板' }), 'success')
    } finally { setTplBusy(false) }
  }

  if (loading || !mail) {
    return <Box sx={{ display: 'grid', placeItems: 'center', py: 6 }}><CircularProgress /></Box>
  }

  // Templates grouped for the master-detail list (scales better than the old
  // overflowing horizontal tab strip as the count grows).
  const TPL_GROUPS: { labelKey: string; kinds: MailReminderKind[] }[] = [
    { labelKey: 'group_ops', kinds: ['expire_before', 'expired', 'traffic_low', 'traffic_exhausted', 'account_disabled', 'account_enabled', 'announcement', 'blocked_client'] },
    { labelKey: 'group_account', kinds: ['password_reset', 'email_verify', 'login_2fa'] },
  ]
  // Fall back to a synthesized empty template if the backend response doesn't
  // include the active kind (e.g., user is on a pre-update binary that doesn't
  // know about `traffic_exhausted` yet). Without this, switching to such a tab
  // would silently render nothing — which looks like "click does nothing".
  // The user can click "重置为默认" to pull the real default from the backend.
  const currentTpl: MailTemplate = templates.find(t => t.kind === activeTpl) ?? {
    kind: activeTpl,
    subject: '',
    body: '',
    enabled: false,
  }
  const tplMissing = !templates.some(t => t.kind === activeTpl)

  return (
    <Box component="form" onSubmit={save} sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Button variant="contained" startIcon={saving ? <CircularProgress size={14} color="inherit" /> : <SaveIcon />}
          disabled={saving} type="submit">
          {t('settings.save')}
        </Button>
      </Box>

      <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <FormControlLabel label={t('settings.mail.enabled')}
          control={<Switch checked={mail.enabled} onChange={(_, c) => patchMail('enabled', c)} />}
          sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
      </Card>

      {/* SMTP server */}
      <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Typography sx={{ fontWeight: 500, mb: 1.5 }}>{t('settings.mail.section_smtp')}</Typography>
        <Divider sx={{ mb: 2 }} />
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
            <TextField fullWidth required={mail.enabled} label={t('settings.mail.smtp_host')}
              value={mail.smtp_host} onChange={e => patchMail('smtp_host', e.target.value)}
              error={!!errs.smtp_host}
              helperText={errs.smtp_host ? t(`admin:${errs.smtp_host}`) : ''}
              sx={{ flex: '2 1 280px' }} />
            <TextField type="number" required={mail.enabled} label={t('settings.mail.smtp_port')}
              value={mail.smtp_port} onChange={e => patchMail('smtp_port', Number(e.target.value))}
              error={!!errs.smtp_port}
              helperText={errs.smtp_port ? t(`admin:${errs.smtp_port}`) : ''}
              inputProps={{ min: 1, max: 65535 }}
              sx={{ width: 120 }} />
          </Box>
          <TextField select size="small" fullWidth label={t('settings.mail.encryption')}
            value={mail.encryption}
            onChange={e => patchMail('encryption', e.target.value as MailSettings['encryption'])}>
            <MenuItem value="none">{t('settings.mail.encryption_none')}</MenuItem>
            <MenuItem value="starttls">{t('settings.mail.encryption_starttls')}</MenuItem>
            <MenuItem value="tls">{t('settings.mail.encryption_tls')}</MenuItem>
          </TextField>
          <TextField fullWidth label={t('settings.mail.smtp_username')}
            value={mail.smtp_username} onChange={e => patchMail('smtp_username', e.target.value)} />

          {/* Password — kept-unchanged pattern */}
          {mail.has_smtp_password && !changePwd ? (
            <Box>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>{t('settings.mail.smtp_password')}</Typography>
              <Box sx={{
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                gap: 1.5, height: 56, px: 1.75,
                borderRadius: 1.5, border: `1px solid ${md.outlineVariant}`,
              }}>
                <Typography variant="body2">{t('settings.mail.password_kept')}</Typography>
                <Button size="small" variant="text" onClick={() => setChangePwd(true)}>
                  {t('settings.mail.password_change')}
                </Button>
              </Box>
            </Box>
          ) : (
            <TextField fullWidth type={showPwd ? 'text' : 'password'} label={t('settings.mail.smtp_password')}
              value={mail.smtp_password ?? ''}
              onChange={e => patchMail('smtp_password', e.target.value)}
              autoComplete="new-password"
              InputProps={{
                endAdornment: (
                  <InputAdornment position="end">
                    <IconButton size="small" onClick={() => setShowPwd(!showPwd)}>
                      {showPwd ? <VisibilityOffIcon fontSize="small" /> : <VisibilityIcon fontSize="small" />}
                    </IconButton>
                  </InputAdornment>
                ),
              }} />
          )}
        </Box>
      </Card>

      {/* Sender */}
      <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Typography sx={{ fontWeight: 500, mb: 1.5 }}>{t('settings.mail.section_sender')}</Typography>
        <Divider sx={{ mb: 2 }} />
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <TextField fullWidth required={mail.enabled} label={t('settings.mail.from_email')}
            value={mail.from_email} onChange={e => patchMail('from_email', e.target.value)}
            error={!!errs.from_email}
            helperText={errs.from_email ? t(`admin:${errs.from_email}`) : ''} />
          <TextField fullWidth label={t('settings.mail.from_name')}
            value={mail.from_name} onChange={e => patchMail('from_name', e.target.value)} />
        </Box>
      </Card>

      {/* v9: notify thresholds moved out of mail_settings into the global
          settings KV. Edit them on the General tab (look for
          "settings.general.expire_before_days" / "traffic_remain_percent").
          Removed the duplicate card here so admin doesn't think two pages
          can edit the same value. */}

      {/* Test send */}
      <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Typography sx={{ fontWeight: 500, mb: 1.5 }}>{t('settings.mail.section_test')}</Typography>
        <Divider sx={{ mb: 2 }} />
        {/* Stretch the button to the input box height (density-agnostic: the
            field is small in compact, medium in comfortable). The validation
            helper is taken out of flow so it grows neither the field's box nor
            the stretched button — the button tracks the box only. */}
        <Box sx={{ display: 'flex', gap: 2, alignItems: 'stretch' }}>
          <TextField fullWidth label={t('settings.mail.test_to')} type="email"
            value={testTo} onChange={e => setTestTo(e.target.value)}
            error={!!errs.test_to}
            helperText={errs.test_to ? t(`admin:${errs.test_to}`) : ''}
            sx={{ position: 'relative', '& .MuiFormHelperText-root': { position: 'absolute', top: '100%', left: 0, m: 0, mt: 0.5 } }} />
          <Button variant="outlined" disabled={!testTo || testBusy} onClick={test}
            startIcon={testBusy ? <CircularProgress size={14} /> : <SendIcon />}
            sx={{ whiteSpace: 'nowrap', flexShrink: 0 }}>
            {t('settings.mail.test_send')}
          </Button>
        </Box>
      </Card>

      {/* Templates */}
      <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Typography sx={{ fontWeight: 500, mb: 1.5 }}>{t('settings.mail.section_templates')}</Typography>
        <Divider sx={{ mb: 2 }} />
        <Box sx={{ display: 'flex', gap: 2, flexDirection: { xs: 'column', md: 'row' }, alignItems: 'flex-start' }}>
          {/* Master list: templates grouped by purpose; scales as the set grows. */}
          <Box sx={{
            width: { xs: '100%', md: 220 }, flexShrink: 0,
            border: { md: `1px solid ${md.outlineVariant}` }, borderRadius: 2,
            p: { xs: 0, md: 1 }, maxHeight: { md: 460 }, overflowY: { md: 'auto' },
          }}>
            {TPL_GROUPS.map(g => (
              <Box key={g.labelKey} sx={{ mb: 1 }}>
                <Typography variant="caption" sx={{ px: 1.5, py: 0.5, display: 'block', color: md.onSurfaceVariant, fontWeight: 600 }}>
                  {t(`settings.mail.${g.labelKey}`, { defaultValue: g.labelKey === 'group_account' ? '账号安全' : '运营提醒' })}
                </Typography>
                <List dense disablePadding>
                  {g.kinds.map(k => (
                    <ListItemButton key={k} selected={activeTpl === k}
                      onClick={() => setActiveTpl(k)} sx={{ borderRadius: 1.5, py: 0.5 }}>
                      <ListItemText primary={t(`settings.mail.kind.${k}`)}
                        primaryTypographyProps={{ fontSize: 14 }} />
                    </ListItemButton>
                  ))}
                </List>
              </Box>
            ))}
          </Box>

          {/* Detail: the selected template's editor. */}
          <Box sx={{ flex: 1, minWidth: 0, width: '100%' }}>
        {tplMissing && (
          <Box sx={{
            mb: 2, p: 1.5, borderRadius: 1.5,
            bgcolor: md.tertiaryContainer, color: md.onTertiaryContainer,
            fontSize: 13, display: 'flex', alignItems: 'center', gap: 1,
          }}>
            <InfoOutlinedIcon fontSize="small" />
            {t('settings.mail.tpl_missing_hint', { defaultValue: '该模板尚未初始化。点击"重置为默认"加载默认内容。' })}
          </Box>
        )}
        {(
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1 }}>
              <FormControlLabel label={t('settings.mail.tpl_enabled')}
                control={<Switch checked={currentTpl.enabled}
                  onChange={(_, c) => patchTpl(currentTpl.kind, { enabled: c })} />}
                sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
              {/* Variable cheat sheet — most edits get the syntax wrong on
                  first try ("{{ExpireAt}}" vs "{{.ExpireAt}}"). Inline help
                  is faster than digging through docs. */}
              <Button size="small" variant="text"
                startIcon={<HelpOutlineIcon fontSize="small" />}
                onClick={(e) => setTplVarsAnchor(e.currentTarget as HTMLElement)}>
                {t('settings.mail.tpl_vars_button', { defaultValue: '可用变量' })}
              </Button>
            </Box>
            <TextField fullWidth label={t('settings.mail.tpl_subject')}
              value={currentTpl.subject}
              onChange={e => patchTpl(currentTpl.kind, { subject: e.target.value })} />
            <TextField fullWidth multiline minRows={10} maxRows={20} label={t('settings.mail.tpl_body')}
              value={currentTpl.body}
              onChange={e => patchTpl(currentTpl.kind, { body: e.target.value })}
              sx={{ '& textarea': { fontSize: 13 } }} />
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
              <Button type="button" variant="text" color="warning" disabled={tplBusy}
                onClick={() => resetTpl(currentTpl.kind)}>
                {t('settings.mail.tpl_reset', { defaultValue: '重置为默认' })}
              </Button>
              <Box sx={{ display: 'flex', gap: 1 }}>
                <Button type="button" variant="outlined" disabled={previewBusy}
                  startIcon={previewBusy ? <CircularProgress size={14} /> : <VisibilityIcon />}
                  onClick={() => previewTpl(currentTpl)}>
                  {t('settings.mail.tpl_preview')}
                </Button>
                <Button variant="contained" disabled={tplBusy}
                  startIcon={tplBusy ? <CircularProgress size={14} color="inherit" /> : <SaveIcon />}
                  onClick={() => saveTpl(currentTpl)}>
                  {t('settings.mail.tpl_save')}
                </Button>
              </Box>
            </Box>
          </Box>
        )}
          </Box>
        </Box>
      </Card>

      {/* Template variable cheat sheet. Triggered by the "可用变量" button
          inside the editor; lives outside the Box so it can anchor to a
          known DOM node without re-rendering the form. */}
      <Popover
        open={!!tplVarsAnchor}
        anchorEl={tplVarsAnchor}
        onClose={() => setTplVarsAnchor(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        PaperProps={{ sx: { p: 2, maxWidth: 460, bgcolor: md.surfaceContainerHigh } }}>
        <Typography sx={{ fontWeight: 500, mb: 1 }}>
          {t('settings.mail.tpl_vars_title', { defaultValue: '模板可用变量' })}
        </Typography>
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 1.5 }}>
          {t('settings.mail.tpl_vars_hint', { defaultValue: '使用 Go template 语法 — {{.字段名}} 取值，{{if .字段名}}...{{end}} 控制是否渲染。' })}
        </Typography>
        <Box sx={{
          display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '6px 12px',
          fontSize: 12, lineHeight: 1.5,
        }}>
          {[
            ['{{.UPN}}', t('settings.mail.tpl_vars.upn', { defaultValue: '登录名（邮箱）' })],
            ['{{.DisplayName}}', t('settings.mail.tpl_vars.display_name', { defaultValue: '显示名（用于"你好 X"，无则留空）' })],
            ['{{.Email}}', t('settings.mail.tpl_vars.email', { defaultValue: '邮箱地址' })],
            ['{{.ExpireAt}}', t('settings.mail.tpl_vars.expire_at', { defaultValue: '到期时间（仅到期/即将到期模板）' })],
            ['{{.ExpireBeforeDays}}', t('settings.mail.tpl_vars.expire_before_days', { defaultValue: '提前提醒天数' })],
            ['{{.TrafficRemainPercent}}', t('settings.mail.tpl_vars.traffic_remain_percent', { defaultValue: '触发流量告警的百分比阈值' })],
            ['{{.TrafficRemainGB}}', t('settings.mail.tpl_vars.traffic_remain_gb', { defaultValue: '剩余流量 GB' })],
            ['{{.PeriodUsedGB}}', t('settings.mail.tpl_vars.period_used_gb', { defaultValue: '本周期已用 GB（流量耗尽模板）' })],
            ['{{.TrafficLimitGB}}', t('settings.mail.tpl_vars.traffic_limit_gb', { defaultValue: '流量上限 GB' })],
            ['{{.DisableDetail}}', t('settings.mail.tpl_vars.disable_detail', { defaultValue: '停用原因（可选，建议用 {{if}} 包裹）' })],
            ['{{.EnableDetail}}', t('settings.mail.tpl_vars.enable_detail', { defaultValue: '恢复备注（可选）' })],
            ['{{.AnnouncementTitle}}', t('settings.mail.tpl_vars.announcement_title', { defaultValue: '公告标题（仅公告模板）' })],
            ['{{.AnnouncementBodyHTML}}', t('settings.mail.tpl_vars.announcement_body_html', { defaultValue: '公告正文 HTML（仅公告模板）' })],
            ['{{.PanelURL}}', t('settings.mail.tpl_vars.panel_url', { defaultValue: '面板访问地址（CTA 按钮指向）' })],
            ['{{.SiteTitle}}', t('settings.mail.tpl_vars.site_title', { defaultValue: '站点名称（用于邮件头）' })],
            ['{{.LogoURL}}', t('settings.mail.tpl_vars.logo_url', { defaultValue: '站点 Logo（自动 dark 兜底）' })],
            ['{{.GeneratedAt}}', t('settings.mail.tpl_vars.generated_at', { defaultValue: '邮件生成时间' })],
            ['{{.OTPCode}}', t('settings.mail.tpl_vars.otp_code', { defaultValue: '一次性验证码（找回/验证/登录验证码模板）' })],
            ['{{.ResetLink}}', t('settings.mail.tpl_vars.reset_link', { defaultValue: '重置密码链接（仅重置密码模板·link 投递）' })],
            ['{{.VerifyLink}}', t('settings.mail.tpl_vars.verify_link', { defaultValue: '验证邮箱链接（仅验证邮箱模板·link 投递）' })],
            ['{{.ExpireMinutes}}', t('settings.mail.tpl_vars.expire_minutes', { defaultValue: '链接/验证码有效分钟数' })],
          ].map(([code, desc]) => (
            <React.Fragment key={code}>
              <Box component="code" sx={{
                fontFamily: 'ui-monospace, "SF Mono", "Cascadia Code", Menlo, Consolas, monospace',
                bgcolor: md.surfaceContainerHighest, px: 0.75, py: 0.25,
                borderRadius: 0.5, whiteSpace: 'nowrap', color: md.primary,
              }}>{code}</Box>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, alignSelf: 'center' }}>{desc}</Typography>
            </React.Fragment>
          ))}
        </Box>
      </Popover>

      <Dialog open={!!preview} onClose={() => setPreview(null)} maxWidth="md" fullWidth
        PaperProps={{ sx: { bgcolor: md.surfaceContainerHigh } }}>
        <DialogTitle>{preview && t('settings.mail.preview_title', { kind: t(`settings.mail.kind.${preview.kind}`) })}</DialogTitle>
        <DialogContent dividers>
          <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.75 }}>
            {t('settings.mail.preview_subject')}
          </Typography>
          <Box sx={{
            px: 1.5, py: 1.25, mb: 2, borderRadius: 1,
            border: `1px solid ${md.outlineVariant}`,
            bgcolor: md.surfaceContainerLow,
            wordBreak: 'break-word',
          }}>
            {preview?.subject}
          </Box>
          <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.75 }}>
            {t('settings.mail.preview_body')}
          </Typography>
          <Box sx={{ height: 520, border: `1px solid ${md.outlineVariant}`, bgcolor: '#fff' }}>
            <iframe
              title={t('settings.mail.preview_body')}
              srcDoc={preview?.body || ''}
              style={{ width: '100%', height: '100%', border: 0, background: '#fff' }}
            />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setPreview(null)}>{t('settings.mail.preview_close')}</Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}

type MdShape = {
  outlineVariant: string
  outline: string
  onSurface: string
  onSurfaceVariant: string
  surfaceContainerLow: string
  surfaceContainerHigh: string
  surfaceContainerHighest: string
  primary: string
}

// IconField is the single source of truth for a quick link's icon: a text
// field (emoji / image URL / "mui:Name") with a live preview, plus a built-in
// icon picker that just writes "mui:Name" into the same field — so there's no
// separate "type" to conflict with the typed value.
function IconField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { t } = useTranslation('admin')
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  return (
    <>
      <TextField size="small" label={t('settings.portal.link_table.icon', { defaultValue: '图标' })}
        value={value} onChange={e => onChange(e.target.value)}
        placeholder="😀 / https://… / mui:"
        InputProps={{
          startAdornment: value ? (
            <Box sx={{ display: 'flex', alignItems: 'center', mr: 0.75 }}>
              <QuickLinkIcon icon={value} size={18} />
            </Box>
          ) : undefined,
          endAdornment: (
            <IconButton size="small" edge="end"
              title={t('settings.portal.link_table.icon_pick', { defaultValue: '选择内置图标' })}
              onClick={e => setAnchor(e.currentTarget)}>
              <AppsIcon fontSize="small" />
            </IconButton>
          ),
        }}
        sx={{ flex: '1 1 200px' }} />
      <Menu open={!!anchor} anchorEl={anchor} onClose={() => setAnchor(null)}
        PaperProps={{ sx: { maxHeight: 360 } }}>
        <MenuItem onClick={() => { onChange(''); setAnchor(null) }}>
          {t('settings.portal.link_table.icon_none', { defaultValue: '无' })}
        </MenuItem>
        {QUICK_LINK_ICONS.map(ic => (
          <MenuItem key={ic.key} selected={value === `mui:${ic.key}`}
            onClick={() => { onChange(`mui:${ic.key}`); setAnchor(null) }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.25 }}>
              <ic.Icon fontSize="small" />{ic.label}
            </Box>
          </MenuItem>
        ))}
      </Menu>
    </>
  )
}

function QuickLinksEditor({ links, onChange, md }: { links: QuickLink[]; onChange: (v: QuickLink[]) => void; md: MdShape }) {
  const { t } = useTranslation('admin')
  const update = (i: number, patch: Partial<QuickLink>) =>
    onChange(links.map((l, idx) => idx === i ? { ...l, ...patch } : l))
  const remove = (i: number) => onChange(links.filter((_, idx) => idx !== i))
  const add = () => onChange([
    ...links,
    { label: '', url: '', icon: '', description: '', group: '', highlight: false, new_window: true, enabled: true, sort: (links.at(-1)?.sort ?? 0) + 10 },
  ])
  // Drag-to-reorder. Unlike the nodes table, quick links are part of the
  // settings doc and only persist when admin hits Save — so reordering is
  // purely a local-array swap + sort_order renumber. No server roundtrip.
  const [dragIndex, setDragIndex] = useState<number | null>(null)
  const [dropIndex, setDropIndex] = useState<number | null>(null)
  function commitDrop(from: number, to: number) {
    if (from === to) return
    const next = [...links]
    const [moved] = next.splice(from, 1)
    next.splice(to, 0, moved)
    onChange(next.map((l, idx) => ({ ...l, sort: (idx + 1) * 10 })))
  }
  // Live URL validation for each row — disabled links can stay blank, but
  // an enabled row with a bad URL would silently 404 in the portal.
  function urlError(l: QuickLink): string {
    if (!l.enabled) return ''
    return validateUrl(l.url, { required: true })
  }
  return (
    <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
      <Typography sx={{ fontWeight: 500, mb: 1.5, color: md.onSurface }}>{t('settings.portal.section_quick_links')}</Typography>
      <Divider sx={{ mb: 2 }} />
      {links.length === 0 ? (
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, py: 2, textAlign: 'center' }}>
          {t('settings.portal.no_links')}
        </Typography>
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
          {links.map((l, i) => (
            <Box key={i}
              draggable
              onDragStart={e => {
                setDragIndex(i)
                try { e.dataTransfer.setData('text/plain', String(i)) } catch { /* ignore */ }
                e.dataTransfer.effectAllowed = 'move'
              }}
              onDragOver={e => {
                if (dragIndex === null) return
                e.preventDefault()
                e.dataTransfer.dropEffect = 'move'
                if (dropIndex !== i) setDropIndex(i)
              }}
              onDragLeave={() => { if (dropIndex === i) setDropIndex(null) }}
              onDrop={e => {
                e.preventDefault()
                const from = dragIndex
                setDragIndex(null)
                setDropIndex(null)
                if (from === null) return
                commitDrop(from, i)
              }}
              onDragEnd={() => { setDragIndex(null); setDropIndex(null) }}
              sx={{
                display: 'flex', flexWrap: 'wrap', gap: 1.25, alignItems: 'center',
                p: 1.5, borderRadius: 2,
                border: `1px solid ${dropIndex === i && dragIndex !== null && dragIndex !== i ? md.primary : md.outlineVariant}`,
                bgcolor: md.surfaceContainerHigh,
                opacity: dragIndex === i ? 0.4 : 1,
                transition: 'border-color 120ms, opacity 120ms',
              }}>
              <Box sx={{ display: 'flex', alignItems: 'center', color: md.onSurfaceVariant, cursor: 'grab', mr: -0.5 }}>
                <DragIndicatorIcon fontSize="small" sx={{ opacity: 0.7 }} />
              </Box>
              <TextField size="small" required={l.enabled} label={t('settings.portal.link_table.label')}
                value={l.label} onChange={e => update(i, { label: e.target.value })}
                sx={{ flex: '1 1 160px' }} />
              {(() => {
                const err = urlError(l)
                return (
                  <TextField size="small" required={l.enabled} label={t('settings.portal.link_table.url')}
                    value={l.url} onChange={e => update(i, { url: e.target.value })}
                    error={!!err}
                    helperText={err ? t(`admin:${err}`) : undefined}
                    sx={{
                      flex: '2 1 240px',
                      // Float the helperText absolutely so it doesn't push
                      // the surrounding switches / delete button downward
                      // when validation triggers — the row keeps its
                      // alignItems: center geometry and the error text
                      // simply hangs under the URL input.
                      '& .MuiFormHelperText-root': {
                        position: 'absolute', top: '100%', left: 0,
                        marginTop: '2px', marginLeft: 0,
                      },
                    }} />
                )
              })()}
              <IconField value={l.icon} onChange={v => update(i, { icon: v })} />
              <TextField size="small" label={t('settings.portal.link_table.group', { defaultValue: '分组' })}
                value={l.group} onChange={e => update(i, { group: e.target.value })}
                sx={{ flex: '1 1 110px' }} />
              <TextField size="small" label={t('settings.portal.link_table.description', { defaultValue: '描述' })}
                value={l.description} onChange={e => update(i, { description: e.target.value })}
                sx={{ flex: '2 1 200px' }} />
              <FormControlLabel
                label={t('settings.portal.link_table.new_window')}
                control={<Switch size="small" checked={l.new_window}
                  onChange={(_, c) => update(i, { new_window: c })} />}
                sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1, fontSize: 13 } }}
              />
              <FormControlLabel
                label={t('settings.portal.link_table.enabled')}
                control={<Switch size="small" checked={l.enabled}
                  onChange={(_, c) => update(i, { enabled: c })} />}
                sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1, fontSize: 13 } }}
              />
              <FormControlLabel
                label={t('settings.portal.link_table.highlight', { defaultValue: '突出' })}
                control={<Switch size="small" checked={l.highlight}
                  onChange={(_, c) => update(i, { highlight: c })} />}
                sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1, fontSize: 13 } }}
              />
              <IconButton size="small" onClick={() => remove(i)} sx={{ color: md.onSurfaceVariant }}>
                <DeleteIcon fontSize="small" />
              </IconButton>
            </Box>
          ))}
        </Box>
      )}
      <Box sx={{ mt: 2 }}>
        <Button variant="outlined" size="small" startIcon={<AddIcon />} onClick={add}>
          {t('settings.portal.add_link')}
        </Button>
      </Box>
    </Card>
  )
}


// CLIENT_PRESETS mirrors defaultSubClients() on the Go side — keep them in
// sync. Each family carries its UA keywords + render format and the import apps
// nested under it. "Add preset" appends a whole family; "reset" rebuilds the
// registry from this list.
const CLIENT_PRESETS: SubClientFamily[] = [
  {
    name: 'Clash / mihomo', keywords: ['clash', 'mihomo', 'meta'], render_format: 'mihomo', enabled: true,
    apps: [
      { name: 'Clash Verge Rev', platforms: ['windows', 'macos', 'linux'], import_url_template: 'clash://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}', install_url: 'https://github.com/clash-verge-rev/clash-verge-rev/releases', enabled: true, sort: 10, recommended_for: ['windows', 'macos', 'linux'] },
      { name: 'Clash Meta for Android', platforms: ['android'], import_url_template: 'clash://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}&update-interval={{ sub_update_interval_minutes }}', install_url: 'https://github.com/MetaCubeX/ClashMetaForAndroid/releases', enabled: true, sort: 20, recommended_for: ['android'] },
      { name: 'Clash Mi', platforms: ['windows', 'macos', 'linux', 'android', 'ios'], import_url_template: 'clashmi://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}', install_url: 'https://github.com/KaringX/clashmi/releases', enabled: true, sort: 25, recommended_for: ['ios'] },
    ],
  },
  {
    name: 'sing-box', keywords: ['sing-box', 'karing'], render_format: 'sing-box', enabled: true,
    apps: [
      { name: 'sing-box', platforms: ['ios', 'macos', 'android'], import_url_template: 'sing-box://import-remote-profile?url={{ sub_url_encoded }}#{{ profile_name_encoded }}', install_url: 'https://sing-box.sagernet.org/clients/', enabled: true, sort: 40, recommended_for: [] },
      { name: 'Karing', platforms: ['windows', 'macos', 'linux', 'android', 'ios'], import_url_template: 'karing://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}', install_url: 'https://github.com/KaringX/karing/releases', enabled: true, sort: 65, recommended_for: [] },
    ],
  },
  {
    name: 'Shadowrocket', keywords: ['shadowrocket'], render_format: 'uri-list', enabled: true,
    apps: [
      { name: 'Shadowrocket', platforms: ['ios'], import_url_template: 'shadowrocket://add/sub://{{ sub_url_b64_url_safe }}?remark={{ profile_name_encoded }}', install_url: 'https://apps.apple.com/app/shadowrocket/id932747118', enabled: true, sort: 60, recommended_for: [] },
    ],
  },
  { name: 'Quantumult X', keywords: ['quantumult'], render_format: 'mihomo', enabled: true, apps: [] },
  {
    name: 'V2rayNG', keywords: ['v2rayng'], render_format: 'uri-list', enabled: true,
    apps: [
      { name: 'V2rayNG', platforms: ['android'], import_url_template: 'v2rayng://install-sub?url={{ sub_url_encoded }}#{{ profile_name_encoded }}', install_url: 'https://github.com/2dust/v2rayNG/releases', enabled: true, sort: 55, recommended_for: [] },
    ],
  },
  {
    name: 'V2RayN', keywords: ['v2rayn', 'v2ray'], render_format: 'uri-list', enabled: true,
    apps: [
      { name: 'V2rayN', platforms: ['windows'], import_url_template: '{{ sub_url }}?remarks={{ profile_name_encoded }}', install_url: 'https://github.com/2dust/v2rayN/releases', enabled: true, sort: 50, recommended_for: [] },
    ],
  },
  { name: 'Passwall (OpenWrt)', keywords: ['passwall'], render_format: 'uri-list', enabled: true, apps: [] },
  {
    name: 'Stash', keywords: ['stash'], render_format: 'mihomo', enabled: true,
    apps: [
      { name: 'Stash', platforms: ['ios'], import_url_template: 'stash://install-config?url={{ sub_url_encoded }}', install_url: 'https://apps.apple.com/app/stash-rule-based-proxy/id1596063349', enabled: true, sort: 30, recommended_for: [] },
    ],
  },
]

const PLATFORM_OPTIONS: SubPlatform[] = ['windows', 'macos', 'linux', 'android', 'ios', 'other']

function clonePreset(p: SubClientFamily): SubClientFamily {
  return JSON.parse(JSON.stringify(p)) as SubClientFamily
}

// normalizeRegistry guards against null array fields from the server: a
// detection-only family serializes apps as JSON null (Go nil slice), and
// keyword / platform / recommended_for arrays can be null too. Coerce them all
// to [] on the way into form state so the editor's .map/.length never see null.
function normalizeRegistry(fams?: SubClientFamily[] | null): SubClientFamily[] {
  return (fams ?? []).map(f => ({
    ...f,
    keywords: f.keywords ?? [],
    apps: (f.apps ?? []).map(a => ({
      ...a,
      platforms: a.platforms ?? [],
      recommended_for: a.recommended_for ?? [],
    })),
  }))
}

// ClientRegistryEditor is the unified detection-family → import-app editor
// (v3.3.0). Each family owns UA keywords + render format + an enabled gate;
// nested apps are the portal's one-click import targets and inherit the
// family's format. Disabling a family blocks its UA AND hides its apps, so the
// portal can never advertise a client that's actually blocked.
function ClientRegistryEditor({ families, onChange, md }: { families: SubClientFamily[]; onChange: (v: SubClientFamily[]) => void; md: MdShape }) {
  const { t } = useTranslation('admin')
  const [presetAnchor, setPresetAnchor] = useState<HTMLElement | null>(null)

  const updateFamily = (fi: number, patch: Partial<SubClientFamily>) =>
    onChange(families.map((f, i) => i === fi ? { ...f, ...patch } : f))
  const removeFamily = (fi: number) => onChange(families.filter((_, i) => i !== fi))
  const addFamily = () => onChange([...families, { name: '', keywords: [], render_format: 'mihomo', enabled: true, apps: [] }])
  const addPresetFamily = (p: SubClientFamily) => {
    onChange([...families, clonePreset(p)])
    setPresetAnchor(null)
  }
  async function resetToPresets() {
    if (!(await confirm({
      title: t('settings.subscription.reset_clients_confirm_title'),
      message: t('settings.subscription.reset_clients_confirm_body'),
      confirmText: t('settings.subscription.reset_clients_confirm_ok'),
      destructive: true,
    }))) return
    onChange(CLIENT_PRESETS.map(clonePreset))
  }

  const updateApp = (fi: number, ai: number, patch: Partial<SubClientApp>) =>
    onChange(families.map((f, i) => i !== fi ? f : { ...f, apps: f.apps.map((a, j) => j === ai ? { ...a, ...patch } : a) }))
  const removeApp = (fi: number, ai: number) =>
    onChange(families.map((f, i) => i !== fi ? f : { ...f, apps: f.apps.filter((_, j) => j !== ai) }))
  const addApp = (fi: number) =>
    onChange(families.map((f, i) => i !== fi ? f : {
      ...f,
      apps: [...f.apps, { name: '', platforms: [], import_url_template: '', install_url: '', enabled: true, sort: (f.apps.at(-1)?.sort ?? fi * 10) + 10, recommended_for: [] }],
    }))

  return (
    <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
      <Typography sx={{ fontWeight: 500, mb: 0.5, color: md.onSurface }}>{t('settings.subscription.section_clients')}</Typography>
      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mb: 1.5 }}>
        {t('settings.subscription.registry_hint', { defaultValue: '检测族按 UA 决定订阅格式；族下的 App 是门户的一键导入项。关闭某个族会同时拦截该族客户端拉取、并在门户隐藏其全部导入项，因此不会出现「已禁用却仍展示」。' })}
      </Typography>
      <Divider sx={{ mb: 2 }} />
      {families.length === 0 ? (
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, py: 2, textAlign: 'center' }}>
          {t('settings.subscription.no_rules')}
        </Typography>
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          {families.map((f, fi) => (
            <Box key={fi} sx={{ p: 2, borderRadius: 2, border: `1px solid ${md.outlineVariant}`, bgcolor: md.surfaceContainerHigh, opacity: f.enabled ? 1 : 0.6 }}>
              <Box sx={{ display: 'flex', gap: 1.25, flexWrap: 'wrap', alignItems: 'center' }}>
                <TextField size="small" label={t('settings.subscription.rule_field.name')}
                  value={f.name} onChange={e => updateFamily(fi, { name: e.target.value })} sx={{ flex: '1 1 160px' }} />
                <TextField size="small" label={t('settings.subscription.rule_field.keywords')}
                  value={f.keywords.join(', ')}
                  onChange={e => updateFamily(fi, { keywords: e.target.value.split(',').map(s => s.trim()).filter(Boolean) })}
                  sx={{ flex: '2 1 220px' }} />
                <TextField select size="small" label={t('settings.subscription.rule_field.render_format')}
                  value={f.render_format}
                  onChange={e => updateFamily(fi, { render_format: e.target.value as SubRenderFormat })}
                  sx={{ width: 150 }}>
                  <MenuItem value="mihomo">mihomo</MenuItem>
                  <MenuItem value="sing-box">sing-box</MenuItem>
                  <MenuItem value="uri-list">URI 列表 (V2rayN / Passwall)</MenuItem>
                </TextField>
                <FormControlLabel label={t('settings.subscription.rule_field.enabled')}
                  control={<Switch size="small" checked={f.enabled} onChange={(_, c) => updateFamily(fi, { enabled: c })} />}
                  sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1, fontSize: 13 } }} />
                <Box sx={{ flex: 1 }} />
                <IconButton size="small" onClick={() => removeFamily(fi)} sx={{ color: md.onSurfaceVariant }}>
                  <DeleteIcon fontSize="small" />
                </IconButton>
              </Box>
              <Box sx={{ mt: 1.5, pl: { xs: 0, sm: 2 }, borderLeft: { sm: `2px solid ${md.outlineVariant}` }, display: 'flex', flexDirection: 'column', gap: 1.5 }}>
                {f.apps.length === 0 && (
                  <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, fontStyle: 'italic' }}>
                    {t('settings.subscription.no_apps', { defaultValue: '该族暂无导入 App（仅用于检测）' })}
                  </Typography>
                )}
                {f.apps.map((a, ai) => (
                  <Box key={ai} sx={{ p: 1.5, borderRadius: 2, border: `1px solid ${md.outlineVariant}`, bgcolor: md.surfaceContainerLow, display: 'flex', flexDirection: 'column', gap: 1.25 }}>
                    <Box sx={{ display: 'flex', gap: 1.25, flexWrap: 'wrap', alignItems: 'center' }}>
                      <TextField size="small" label={t('settings.subscription.client_field.name')}
                        value={a.name} onChange={e => updateApp(fi, ai, { name: e.target.value })} sx={{ flex: '1 1 180px' }} />
                      <TextField size="small" type="number" label={t('settings.subscription.client_field.sort')}
                        value={a.sort} onChange={e => updateApp(fi, ai, { sort: Number(e.target.value) })} sx={{ width: 100 }} />
                      <FormControlLabel label={t('settings.subscription.client_field.enabled')}
                        control={<Switch size="small" checked={a.enabled} onChange={(_, c) => updateApp(fi, ai, { enabled: c })} />}
                        sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1, fontSize: 13 } }} />
                      <Box sx={{ flex: 1 }} />
                      <IconButton size="small" onClick={() => removeApp(fi, ai)} sx={{ color: md.onSurfaceVariant }}>
                        <DeleteIcon fontSize="small" />
                      </IconButton>
                    </Box>
                    <Box>
                      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.75 }}>
                        {t('settings.subscription.client_field.platforms', { defaultValue: '支持的平台' })}
                      </Typography>
                      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.75 }}>
                        {PLATFORM_OPTIONS.map(p => {
                          const selected = a.platforms.includes(p)
                          return (
                            <Chip key={p} size="small"
                              label={t(`settings.subscription.platform.${p}`, { defaultValue: p })}
                              color={selected ? 'primary' : 'default'} variant={selected ? 'filled' : 'outlined'}
                              onClick={() => {
                                const next = selected ? a.platforms.filter(x => x !== p) : [...a.platforms, p]
                                const nextRec = (a.recommended_for ?? []).filter(x => next.includes(x))
                                updateApp(fi, ai, { platforms: next, recommended_for: nextRec })
                              }} />
                          )
                        })}
                      </Box>
                    </Box>
                    <Box>
                      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.75 }}>
                        {t('settings.subscription.client_field.recommended_for', { defaultValue: '在哪些平台作为推荐客户端' })}
                      </Typography>
                      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.75 }}>
                        {PLATFORM_OPTIONS.filter(p => a.platforms.includes(p)).map(p => {
                          const selected = (a.recommended_for ?? []).includes(p)
                          return (
                            <Chip key={p} size="small"
                              label={t(`settings.subscription.platform.${p}`, { defaultValue: p })}
                              color={selected ? 'primary' : 'default'} variant={selected ? 'filled' : 'outlined'}
                              onClick={() => {
                                const cur = a.recommended_for ?? []
                                updateApp(fi, ai, { recommended_for: selected ? cur.filter(x => x !== p) : [...cur, p] })
                              }} />
                          )
                        })}
                        {a.platforms.length === 0 && (
                          <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, fontStyle: 'italic' }}>
                            {t('settings.subscription.client_field.recommended_for_empty', { defaultValue: '请先选择支持的平台' })}
                          </Typography>
                        )}
                      </Box>
                    </Box>
                    <TextField size="small" fullWidth label={t('settings.subscription.client_field.import_url_template')}
                      value={a.import_url_template} onChange={e => updateApp(fi, ai, { import_url_template: e.target.value })} />
                    <TextField size="small" fullWidth label={t('settings.subscription.client_field.install_url')}
                      value={a.install_url} onChange={e => updateApp(fi, ai, { install_url: e.target.value })} />
                  </Box>
                ))}
                <Box>
                  <Button variant="text" size="small" startIcon={<AddIcon />} onClick={() => addApp(fi)}>
                    {t('settings.subscription.add_app', { defaultValue: '添加 App' })}
                  </Button>
                </Box>
              </Box>
            </Box>
          ))}
        </Box>
      )}
      <Box sx={{ mt: 2, display: 'flex', gap: 1.25, flexWrap: 'wrap' }}>
        <Button variant="outlined" size="small" startIcon={<AddIcon />} onClick={addFamily}>
          {t('settings.subscription.add_family', { defaultValue: '添加检测族' })}
        </Button>
        <Button variant="text" size="small" onClick={e => setPresetAnchor(e.currentTarget)}>
          {t('settings.subscription.add_preset')}
        </Button>
        <Button variant="text" size="small" color="warning" onClick={resetToPresets}>
          {t('settings.subscription.reset_to_presets')}
        </Button>
        <Menu anchorEl={presetAnchor} open={!!presetAnchor} onClose={() => setPresetAnchor(null)} PaperProps={{ sx: { maxHeight: 360 } }}>
          {CLIENT_PRESETS.map(p => {
            const exists = families.some(f => f.name === p.name)
            return (
              <MenuItem key={p.name} disabled={exists} onClick={() => addPresetFamily(p)}>
                <Box>
                  <Typography sx={{ fontSize: 14 }}>{p.name}</Typography>
                  <Typography sx={{ fontSize: 12, opacity: 0.7 }}>
                    {p.keywords.join(', ')} · {p.render_format} · {p.apps.length} app{p.apps.length === 1 ? '' : 's'}
                    {exists ? ` · ${t('settings.subscription.preset_exists', { defaultValue: '已存在' })}` : ''}
                  </Typography>
                </Box>
              </MenuItem>
            )
          })}
        </Menu>
      </Box>
    </Card>
  )
}

interface SectionProps { title: string; children: React.ReactNode; md: MdShape }
function Section({ title, children, md }: SectionProps) {
  return (
    <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
      <Typography sx={{ fontWeight: 500, mb: 1.5, color: md.onSurface }}>{title}</Typography>
      <Divider sx={{ mb: 2 }} />
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>{children}</Box>
    </Card>
  )
}

function Pair({ children }: { children: React.ReactNode }) {
  return <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap', '& > *': { flex: '1 1 220px' } }}>{children}</Box>
}

// RoleRulesEditor: attribute-driven role mapping. Each row maps an IdP
// attribute value to a panel role plus a per-rule Keep switch + free-
// form admin note. Panel role accepts arbitrary strings (free-form
// Autocomplete) so admins can plan for custom roles before the
// backend recognises them. Order matters — first-match wins — so the
// editor exposes drag-to-reorder with a left-side handle, identical
// to the pattern NodesView uses for admin reordering.
const builtinRoleSuggestions = ['admin', 'operator', 'user']

function RoleRulesEditor({ value, onChange, md }: {
  value: SSORoleRule[]
  onChange: (rules: SSORoleRule[]) => void
  md: MdShape
}) {
  const { t } = useTranslation('admin')
  const rules = value ?? []
  const [dragIndex, setDragIndex] = useState<number | null>(null)
  const [dropIndex, setDropIndex] = useState<number | null>(null)

  function patchRule(idx: number, patch: Partial<SSORoleRule>) {
    onChange(rules.map((r, i) => i === idx ? { ...r, ...patch } : r))
  }
  function addRule() {
    onChange([...rules, { attribute: '', value: '', role: 'admin', keep: false, note: '' }])
  }
  function removeRule(idx: number) {
    onChange(rules.filter((_, i) => i !== idx))
  }
  function moveRule(from: number, to: number) {
    if (from === to) return
    const next = rules.slice()
    const [m] = next.splice(from, 1)
    next.splice(to, 0, m)
    onChange(next)
  }

  return (
    <Box>
      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mb: 1 }}>
        {t('settings.sso.role_rules_hint')}
      </Typography>
      {rules.length === 0 && (
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, fontStyle: 'italic', mb: 1 }}>
          {t('settings.sso.role_rules_empty')}
        </Typography>
      )}
      {rules.map((r, i) => {
        const isBeingDragged = dragIndex === i
        const isDropTarget = dropIndex === i && dragIndex !== null && dragIndex !== i
        return (
          <Box key={i}
            draggable
            onDragStart={e => {
              setDragIndex(i)
              try { e.dataTransfer.setData('text/plain', String(i)) } catch { /* Firefox */ }
              e.dataTransfer.effectAllowed = 'move'
            }}
            onDragOver={e => {
              if (dragIndex === null) return
              e.preventDefault()
              e.dataTransfer.dropEffect = 'move'
              if (dropIndex !== i) setDropIndex(i)
            }}
            onDragLeave={() => {
              if (dropIndex === i) setDropIndex(null)
            }}
            onDrop={e => {
              e.preventDefault()
              const from = dragIndex
              setDragIndex(null)
              setDropIndex(null)
              if (from === null || from === i) return
              moveRule(from, i)
            }}
            onDragEnd={() => {
              setDragIndex(null)
              setDropIndex(null)
            }}
            sx={{
              display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap',
              mb: 1, py: 0.75, px: 0.5, borderRadius: 1,
              opacity: isBeingDragged ? 0.4 : 1,
              bgcolor: isDropTarget ? alpha(md.primary, 0.08) : 'transparent',
              transition: 'background-color 120ms',
            }}>
            <Tooltip title={t('settings.sso.role_rules_drag') as string}>
              <Box sx={{ cursor: 'grab', display: 'inline-flex', color: md.onSurfaceVariant, px: 0.25 }}>
                <DragIndicatorIcon fontSize="small" sx={{ opacity: 0.7 }} />
              </Box>
            </Tooltip>
            <TextField size="small" sx={{ flex: '2 1 180px' }}
              label={t('settings.sso.role_rules_attribute')}
              placeholder={t('settings.sso.role_rules_attribute_placeholder')}
              value={r.attribute}
              onChange={e => patchRule(i, { attribute: e.target.value })} />
            <TextField size="small" sx={{ flex: '2 1 140px' }}
              label={t('settings.sso.role_rules_value')}
              value={r.value}
              onChange={e => patchRule(i, { value: e.target.value })} />
            <Autocomplete
              size="small" freeSolo disableClearable
              sx={{ flex: '1 1 130px' }}
              options={builtinRoleSuggestions}
              value={r.role}
              onChange={(_, v) => patchRule(i, { role: typeof v === 'string' ? v : '' })}
              onInputChange={(_, v) => patchRule(i, { role: v })}
              renderInput={(params) => (
                <TextField {...params} label={t('settings.sso.role_rules_role')} />
              )} />
            <Tooltip title={t('settings.sso.role_rules_keep_hint') as string}>
              <FormControlLabel
                sx={{ ml: 0, '& .MuiFormControlLabel-label': { fontSize: 12, ml: 0.5 } }}
                control={<Switch size="small" checked={!!r.keep}
                  onChange={(_, c) => patchRule(i, { keep: c })} />}
                label={t('settings.sso.role_rules_keep')} />
            </Tooltip>
            <TextField size="small" sx={{ flex: '3 1 200px' }}
              label={t('settings.sso.role_rules_note')}
              placeholder={t('settings.sso.role_rules_note_placeholder')}
              value={r.note ?? ''}
              onChange={e => patchRule(i, { note: e.target.value })} />
            <IconButton size="small" onClick={() => removeRule(i)}
              aria-label={t('settings.sso.role_rules_remove')}>
              <DeleteIcon fontSize="small" />
            </IconButton>
          </Box>
        )
      })}
      <Button size="small" variant="outlined" startIcon={<AddIcon />} onClick={addRule}>
        {t('settings.sso.role_rules_add')}
      </Button>
    </Box>
  )
}

function NumField({ label, value, onChange, helperText, step }: { label: string; value: number; onChange: (v: number) => void; helperText?: string; step?: number | string }) {
  // step defaults to 1 (integer fields: hours, count, ports, days). Pass
  // step="any" for fields that accept fractional values (e.g. GB quotas).
  return (
    <TextField fullWidth type="number" label={label}
      value={value} onChange={e => onChange(Number(e.target.value))}
      inputProps={{ min: 0, step: step ?? 1 }} helperText={helperText} />
  )
}

function ResetPeriodField({ value, onChange }: { value: string; onChange: (v: string) => void; md: MdShape }) {
  const { t } = useTranslation('admin')
  return (
    <TextField select size="small" fullWidth label={t('users.field.traffic_reset_period')}
      value={value} onChange={e => onChange(e.target.value)}>
      <MenuItem value="never">{t('users.reset_period.never')}</MenuItem>
      <MenuItem value="monthly">{t('users.reset_period.monthly')}</MenuItem>
      <MenuItem value="quarterly">{t('users.reset_period.quarterly')}</MenuItem>
      <MenuItem value="yearly">{t('users.reset_period.yearly')}</MenuItem>
    </TextField>
  )
}

function SsoTab() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('admin')
  const [sub, setSub] = useTabParam<'saml' | 'oidc'>('sub', 'saml', ['saml', 'oidc'])

  return (
    <Box>
      <Tabs value={sub} onChange={(_, v) => setSub(v)} sx={{ mb: 3, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Tab value="saml" label={t('settings.sso.tab_saml')} />
        <Tab value="oidc" label={t('settings.sso.tab_oidc')} />
      </Tabs>
      {sub === 'saml' ? <SamlPanel /> : <OidcPanel />}
    </Box>
  )
}

function SamlPanel() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('admin')
  const [cfg, setCfg] = useState<SAMLConfig | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [changeKey, setChangeKey] = useState(false)
  const [keyPem, setKeyPem] = useState('')
  // IdP metadata fetch state: only meaningful in auto mode. fetchResult is
  // a successful parse; fetchError is the user-facing failure message.
  // Cleared whenever the URL field changes so stale verifications don't
  // mislead the admin.
  const [fetching, setFetching] = useState(false)
  const [fetchResult, setFetchResult] = useState<SAMLMetadataSummary | null>(null)
  const [fetchError, setFetchError] = useState('')
  type SamlField = 'entity_id' | 'acs_url' | 'cert_pem' | 'metadata_url'
  const [errs, setErrs] = useState<FieldErrors<SamlField>>({})
  const [groups, setGroups] = useState<Group[]>([])

  // Auto mode hides SP / attribute editing because the backend auto-derives
  // entity_id, ACS URL and a self-signed cert from sub_base_url on save,
  // and resets the attribute mapping to the documented Entra defaults.
  // Surfacing those fields as editable would be a UX trap — admin types in
  // values that get silently overwritten.
  const isAuto = cfg?.mode === 'auto'

  async function copyToClipboard(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      pushSnack(t('settings.sso.saml.copied', { defaultValue: '已复制' }), 'success')
    } catch {
      pushSnack(t('settings.sso.saml.copy_failed', { defaultValue: '复制失败' }), 'warning')
    }
  }

  useEffect(() => { void load(); void loadGroups() }, [])
  async function loadGroups() {
    try { setGroups((await listGroups()).items) } catch { /* dropdown stays empty */ }
  }
  // Fresh DB serialises `admin_group_ids` as null (Go nil slice -> JSON
  // null). The editor calls `.join(', ')` unconditionally; defend at the
  // load/save boundary so the rest of the component stays array-safe.
  function normalizeSAML(c: SAMLConfig): SAMLConfig {
    return {
      ...c,
      role_rules: c.role_rules ?? [],
    }
  }
  async function load() {
    setLoading(true)
    try { setCfg(normalizeSAML(await getSAML())) }
    finally { setLoading(false) }
  }
  function patch<K extends keyof SAMLConfig>(key: K, value: SAMLConfig[K]) {
    setCfg(prev => prev ? { ...prev, [key]: value } : prev)
  }
  function patchSp<K extends keyof SAMLConfig['sp']>(key: K, value: SAMLConfig['sp'][K]) {
    setCfg(prev => prev ? { ...prev, sp: { ...prev.sp, [key]: value } } : prev)
  }
  function patchIdp<K extends keyof SAMLConfig['idp']>(key: K, value: SAMLConfig['idp'][K]) {
    setCfg(prev => prev ? { ...prev, idp: { ...prev.idp, [key]: value } } : prev)
  }
  function patchAttr<K extends keyof SAMLConfig['attribute_mapping']>(key: K, value: SAMLConfig['attribute_mapping'][K]) {
    setCfg(prev => prev ? { ...prev, attribute_mapping: { ...prev.attribute_mapping, [key]: value } } : prev)
  }
  function patchDef<K extends keyof SAMLConfig['new_user_defaults']>(key: K, value: SAMLConfig['new_user_defaults'][K]) {
    setCfg(prev => prev ? { ...prev, new_user_defaults: { ...prev.new_user_defaults, [key]: value } } : prev)
  }

  // Field-level checks only fire when SAML is enabled — typing into the
  // form while disabled would otherwise nag with required-field errors
  // before the admin has even decided to flip the switch.
  // Auto mode: only the IdP URL is admin-provided; SP fields are derived
  // by the backend from sub_base_url on save.
  function validateSaml(c: SAMLConfig): FieldErrors<SamlField> {
    if (!c.enabled) return {}
    if (c.mode === 'auto') {
      return { metadata_url: validateUrl(c.idp.metadata_url, { required: true }) }
    }
    return {
      entity_id: validateRequired(c.sp.entity_id),
      acs_url: validateUrl(c.sp.acs_url, { required: true }),
      cert_pem: validateRequired(c.sp.cert_pem),
      metadata_url: validateUrl(c.idp.metadata_url, { required: true }),
    }
  }

  async function saveConfig(nextCfg: SAMLConfig, opts: { quietSuccess?: boolean } = {}) {
    const v = validateSaml(nextCfg)
    setErrs(v)
    const firstKey = firstError(v)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return null }
    setSaving(true)
    try {
      const res = await putSAML({
        ...nextCfg,
        sp: {
          entity_id: nextCfg.sp.entity_id, acs_url: nextCfg.sp.acs_url, cert_pem: nextCfg.sp.cert_pem,
          // Send empty only in "keep existing" mode (there is a stored key
          // and the admin didn't click "change"). On a fresh setup keyPem
          // holds the value the admin just pasted; it must reach the backend.
          key_pem: (nextCfg.sp.has_key_pem && !changeKey) ? '' : keyPem,
        },
      })
      setCfg(normalizeSAML(res.config))
      setChangeKey(false); setKeyPem('')
      if (res.reload_error) pushSnack(t('settings.sso.reload_error', { error: res.reload_error }), 'warning')
      else if (!opts.quietSuccess) pushSnack(t('settings.sso.saved'), 'success')
      return res
    } finally { setSaving(false) }
  }

  async function onFetchMetadata() {
    if (!cfg) return
    const url = cfg.idp.metadata_url.trim()
    if (!url) {
      setFetchError(t('settings.sso.saml.fetch_url_required', { defaultValue: '请先填写 IdP Metadata URL' }))
      setFetchResult(null)
      return
    }
    setFetching(true); setFetchError(''); setFetchResult(null)
    try {
      const summary = await fetchSAMLMetadata(url)
      setFetchResult(summary)
      const res = await saveConfig({ ...cfg, idp: { ...cfg.idp, metadata_url: url } }, { quietSuccess: true })
      if (res && !res.reload_error) {
        pushSnack(t('settings.sso.saml.fetch_saved', { defaultValue: 'Metadata verified and SAML settings saved' }), 'success')
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      setFetchError(msg)
    } finally { setFetching(false) }
  }

  async function save(e?: FormEvent) {
    e?.preventDefault()
    if (!cfg) return
    await saveConfig(cfg)
  }

  if (loading || !cfg) return <Box sx={{ display: 'grid', placeItems: 'center', py: 6 }}><CircularProgress /></Box>

  return (
    <Box component="form" onSubmit={save} sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Button variant="contained" type="submit" disabled={saving}
          startIcon={saving ? <CircularProgress size={14} color="inherit" /> : <SaveIcon />}>
          {t('settings.save')}
        </Button>
      </Box>

      <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <FormControlLabel label={t('settings.sso.saml.enabled')}
          control={<Switch checked={cfg.enabled} onChange={(_, c) => patch('enabled', c)} />}
          sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
        <Box sx={{ mt: 2 }}>
          <TextField select size="small" fullWidth label={t('settings.sso.saml.mode')}
            value={cfg.mode} onChange={e => patch('mode', e.target.value as 'auto' | 'manual')}>
            <MenuItem value="auto">{t('settings.sso.saml.mode_auto')}</MenuItem>
            <MenuItem value="manual">{t('settings.sso.saml.mode_manual')}</MenuItem>
          </TextField>
        </Box>
      </Card>

      {/* Identity Provider — auto mode adds a Fetch & verify button under the URL */}
      <Section title={t('settings.sso.saml.idp_section')} md={md}>
        <Box sx={{ display: 'flex', gap: 1.5, alignItems: 'flex-start', flexWrap: 'wrap' }}>
          <TextField sx={{ flex: '1 1 320px' }} required={cfg.enabled}
            label={t('settings.sso.saml.idp_metadata_url')} value={cfg.idp.metadata_url}
            onChange={e => {
              patchIdp('metadata_url', e.target.value)
              // Stale verification once the URL changes — force a re-fetch.
              if (fetchResult || fetchError) { setFetchResult(null); setFetchError('') }
            }}
            error={!!errs.metadata_url}
            helperText={errs.metadata_url ? t(`admin:${errs.metadata_url}`) : ''} />
          {isAuto && (
            <Button variant="outlined" size="medium" onClick={onFetchMetadata}
              disabled={fetching || saving || !cfg.idp.metadata_url.trim()}
              startIcon={fetching ? <CircularProgress size={14} /> : null}
              sx={{ height: 56, whiteSpace: 'nowrap' }}>
              {t('settings.sso.saml.fetch_verify', { defaultValue: 'Fetch & verify' })}
            </Button>
          )}
        </Box>
        {isAuto && fetchResult && (
          <Box sx={{
            display: 'flex', alignItems: 'center', gap: 1, p: 1.25,
            borderRadius: 1.5, bgcolor: md.surfaceContainerLowest, border: `1px solid ${md.outlineVariant}`,
          }}>
            <CheckCircleIcon fontSize="small" sx={{ color: md.primary }} />
            <Box sx={{ fontSize: 13 }}>
              <Box>
                {t('settings.sso.saml.fetch_ok', { defaultValue: 'Verified · entity_id={{id}}', id: fetchResult.entity_id })}
              </Box>
              <Box sx={{ color: md.onSurfaceVariant, fontSize: 12 }}>
                {t('settings.sso.saml.fetch_certs', {
                  defaultValue: '{{n}} signing certificate(s){{exp}}',
                  n: fetchResult.num_signing_certs,
                  exp: fetchResult.signing_cert_expires_at
                    ? `, expires ${new Date(fetchResult.signing_cert_expires_at).toLocaleDateString()}`
                    : '',
                })}
              </Box>
            </Box>
          </Box>
        )}
        {isAuto && fetchError && (
          <Box sx={{
            display: 'flex', alignItems: 'center', gap: 1, p: 1.25,
            borderRadius: 1.5, bgcolor: md.surfaceContainerLowest, border: `1px solid ${md.error}`,
          }}>
            <ErrorOutlineIcon fontSize="small" sx={{ color: md.error }} />
            <Typography sx={{ fontSize: 13, color: md.error }}>{fetchError}</Typography>
          </Box>
        )}
        <NumField label={t('settings.sso.saml.idp_refresh_hours')} value={cfg.idp.metadata_refresh_hours}
          onChange={v => patchIdp('metadata_refresh_hours', v)} />
      </Section>

      {/* Service Provider — auto mode shows read-only values for pasting into the IdP */}
      <Section title={t('settings.sso.saml.sp_section')} md={md}>
        {isAuto ? (
          <>
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
              {t('settings.sso.saml.sp_auto_hint', {
                defaultValue: '这些字段由面板根据「公网基地址」自动生成（首次保存时生成自签名证书）。请把它们粘贴到 IdP 侧的 Service Provider 配置。',
              })}
            </Typography>
            {!cfg.sp.entity_id ? (
              <Box sx={{
                display: 'flex', alignItems: 'center', gap: 1, p: 1.25, borderRadius: 1.5,
                bgcolor: md.surfaceContainerLowest, border: `1px solid ${md.outlineVariant}`,
              }}>
                <InfoOutlinedIcon fontSize="small" sx={{ color: md.onSurfaceVariant }} />
                <Typography sx={{ fontSize: 13 }}>
                  {t('settings.sso.saml.sp_save_first', { defaultValue: '保存后将生成 SP 信息' })}
                </Typography>
              </Box>
            ) : (
              <>
                <TextField fullWidth label={t('settings.sso.saml.sp_entity_id')} value={cfg.sp.entity_id}
                  InputProps={{
                    readOnly: true,
                    endAdornment: (
                      <InputAdornment position="end">
                        <IconButton size="small" onClick={() => copyToClipboard(cfg.sp.entity_id)}>
                          <ContentCopyIcon fontSize="small" />
                        </IconButton>
                      </InputAdornment>
                    ),
                  }} />
                <TextField fullWidth label={t('settings.sso.saml.sp_acs_url')} value={cfg.sp.acs_url}
                  InputProps={{
                    readOnly: true,
                    endAdornment: (
                      <InputAdornment position="end">
                        <IconButton size="small" onClick={() => copyToClipboard(cfg.sp.acs_url)}>
                          <ContentCopyIcon fontSize="small" />
                        </IconButton>
                      </InputAdornment>
                    ),
                  }} />
                <Box sx={{ position: 'relative' }}>
                  <TextField fullWidth multiline minRows={4} label={t('settings.sso.saml.sp_cert')}
                    value={cfg.sp.cert_pem}
                    InputProps={{ readOnly: true }}
                    sx={{ '& textarea': { fontSize: 12 } }} />
                  <IconButton size="small"
                    sx={{ position: 'absolute', top: 8, right: 8 }}
                    onClick={() => copyToClipboard(cfg.sp.cert_pem)}>
                    <ContentCopyIcon fontSize="small" />
                  </IconButton>
                </Box>
              </>
            )}
          </>
        ) : (
          <>
            <TextField fullWidth required={cfg.enabled} label={t('settings.sso.saml.sp_entity_id')} value={cfg.sp.entity_id}
              onChange={e => patchSp('entity_id', e.target.value)}
              error={!!errs.entity_id}
              helperText={errs.entity_id ? t(`admin:${errs.entity_id}`) : ''} />
            <TextField fullWidth required={cfg.enabled} label={t('settings.sso.saml.sp_acs_url')} value={cfg.sp.acs_url}
              onChange={e => patchSp('acs_url', e.target.value)}
              error={!!errs.acs_url}
              helperText={errs.acs_url ? t(`admin:${errs.acs_url}`) : ''} />
            <TextField fullWidth required={cfg.enabled} multiline minRows={4} label={t('settings.sso.saml.sp_cert')} value={cfg.sp.cert_pem}
              onChange={e => patchSp('cert_pem', e.target.value)}
              error={!!errs.cert_pem}
              helperText={errs.cert_pem ? t(`admin:${errs.cert_pem}`) : ''}
              sx={{ '& textarea': { fontSize: 12 } }} />
            {cfg.sp.has_key_pem && !changeKey ? (
              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1.5, height: 56, px: 1.75, borderRadius: 1.5, border: `1px solid ${md.outlineVariant}` }}>
                <Typography variant="body2">{t('settings.sso.saml.sp_key_kept')}</Typography>
                <Button size="small" variant="text" onClick={() => setChangeKey(true)}>{t('settings.sso.saml.sp_key_change')}</Button>
              </Box>
            ) : (
              <TextField fullWidth multiline minRows={4} label={t('settings.sso.saml.sp_key')} value={keyPem}
                onChange={e => setKeyPem(e.target.value)}
                sx={{ '& textarea': { fontSize: 12 } }} />
            )}
          </>
        )}
      </Section>

      {/* Attribute mapping is admin policy in both modes — auto mode
          only derives SP / IdP wiring from metadata. Defaults are
          prefilled by ApplySAMLDefaults on the backend, but the admin
          must be able to override per-tenant claim URNs (e.g. a custom
          Entra namespace for the UPN claim). */}
      <Section title={t('settings.sso.saml.attr_section')} md={md}>
        <Pair>
          <TextField fullWidth label={t('settings.sso.saml.attr_upn')} value={cfg.attribute_mapping.upn}
            onChange={e => patchAttr('upn', e.target.value)}
            helperText={t('settings.sso.saml.attr_upn_hint')} />
          <TextField fullWidth label={t('settings.sso.saml.attr_email')} value={cfg.attribute_mapping.email}
            onChange={e => patchAttr('email', e.target.value)} />
        </Pair>
        <Pair>
          <TextField fullWidth label={t('settings.sso.saml.attr_display_name')} value={cfg.attribute_mapping.display_name}
            onChange={e => patchAttr('display_name', e.target.value)} />
          <TextField fullWidth label={t('settings.sso.saml.attr_groups')} value={cfg.attribute_mapping.groups}
            onChange={e => patchAttr('groups', e.target.value)} />
        </Pair>
      </Section>

      {/* Group resolution holds rule-based role assignment only. The
          default group slug is provisioning-time policy and lives with
          the other "what does a fresh SSO user look like" defaults
          below. */}
      <Section title={t('settings.sso.saml.group_section', { defaultValue: '分组解析' })} md={md}>
        <RoleRulesEditor
          value={cfg.role_rules ?? []}
          onChange={rules => patch('role_rules', rules)}
          md={md}
        />
      </Section>

      <Section title={t('settings.sso.saml.new_user_section')} md={md}>
        <FormControlLabel
          label={t('settings.sso.allow_auto_create', { defaultValue: '允许通过 SSO 自动创建账户' })}
          control={<Switch checked={cfg.allow_auto_create}
            onChange={(_, c) => patch('allow_auto_create', c)} />}
          sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: -1 }}>
          {t('settings.sso.allow_auto_create_hint', {
            defaultValue: '关闭后，未在面板中预先创建的账户首次 SSO 登录会被跳转到“联系管理员”页；IdP 管理员组不受影响。',
          })}
        </Typography>
        <GroupSlugPicker
          label={t('settings.sso.saml.default_group')}
          value={cfg.default_group_slug}
          onChange={slug => patch('default_group_slug', slug)}
          groups={groups}
        />
        <Pair>
          <NumField label={t('settings.sso.saml.expire_days')} value={cfg.new_user_defaults.expire_days}
            onChange={v => patchDef('expire_days', v)} />
          <NumField label={t('settings.sso.saml.traffic_limit_gb')}
            value={Math.round(cfg.new_user_defaults.traffic_limit_bytes / 1024 / 1024 / 1024)}
            onChange={v => patchDef('traffic_limit_bytes', v * 1024 * 1024 * 1024)} />
        </Pair>
        <ResetPeriodField
          value={cfg.new_user_defaults.traffic_reset_period}
          onChange={v => patchDef('traffic_reset_period', v)}
          md={md}
        />
      </Section>
    </Box>
  )
}

function OidcPanel() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('admin')
  const [cfg, setCfg] = useState<OIDCConfig | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [changeSecret, setChangeSecret] = useState(false)
  const [secret, setSecret] = useState('')
  type OidcField = 'issuer_url' | 'client_id' | 'redirect_url'
  const [errs, setErrs] = useState<FieldErrors<OidcField>>({})
  const [groups, setGroups] = useState<Group[]>([])

  useEffect(() => { void load(); void loadGroups() }, [])
  async function loadGroups() {
    try { setGroups((await listGroups()).items) } catch { /* dropdown stays empty */ }
  }
  // Same null-array gotcha as SAML: scopes + admin_group_ids come back
  // as null on a fresh DB, and the editor calls `.join` on them.
  function normalizeOIDC(c: OIDCConfig): OIDCConfig {
    return {
      ...c,
      scopes: c.scopes ?? [],
      role_rules: c.role_rules ?? [],
    }
  }
  async function load() {
    setLoading(true)
    try { setCfg(normalizeOIDC(await getOIDC())) }
    finally { setLoading(false) }
  }
  function patch<K extends keyof OIDCConfig>(key: K, value: OIDCConfig[K]) {
    setCfg(prev => prev ? { ...prev, [key]: value } : prev)
  }
  function patchAttr<K extends keyof OIDCConfig['attribute_mapping']>(key: K, value: OIDCConfig['attribute_mapping'][K]) {
    setCfg(prev => prev ? { ...prev, attribute_mapping: { ...prev.attribute_mapping, [key]: value } } : prev)
  }
  function patchDef<K extends keyof OIDCConfig['new_user_defaults']>(key: K, value: OIDCConfig['new_user_defaults'][K]) {
    setCfg(prev => prev ? { ...prev, new_user_defaults: { ...prev.new_user_defaults, [key]: value } } : prev)
  }

  function validateOidc(c: OIDCConfig): FieldErrors<OidcField> {
    if (!c.enabled) return {}
    return {
      issuer_url: validateUrl(c.issuer_url, { required: true }),
      client_id: validateRequired(c.client_id),
      redirect_url: validateUrl(c.redirect_url, { required: true }),
    }
  }

  async function save(e?: FormEvent) {
    e?.preventDefault()
    if (!cfg) return
    const v = validateOidc(cfg)
    setErrs(v)
    const firstKey = firstError(v)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return }
    setSaving(true)
    try {
      // Send empty only when keeping the existing secret (one is stored AND
      // admin didn't click "change"). Fresh setup has no stored secret, so
      // `secret` holds the value the admin just typed; it must be forwarded.
      const res = await putOIDC({
        ...cfg,
        client_secret: (cfg.has_client_secret && !changeSecret) ? '' : secret,
      })
      setCfg(normalizeOIDC(res.config))
      setChangeSecret(false); setSecret('')
      if (res.reload_error) pushSnack(t('settings.sso.reload_error', { error: res.reload_error }), 'warning')
      else pushSnack(t('settings.sso.saved'), 'success')
    } finally { setSaving(false) }
  }

  if (loading || !cfg) return <Box sx={{ display: 'grid', placeItems: 'center', py: 6 }}><CircularProgress /></Box>

  return (
    <Box component="form" onSubmit={save} sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Button variant="contained" type="submit" disabled={saving}
          startIcon={saving ? <CircularProgress size={14} color="inherit" /> : <SaveIcon />}>
          {t('settings.save')}
        </Button>
      </Box>

      <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <FormControlLabel label={t('settings.sso.oidc.enabled')}
          control={<Switch checked={cfg.enabled} onChange={(_, c) => patch('enabled', c)} />}
          sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
      </Card>

      <Section title={t('settings.sso.oidc.issuer_url')} md={md}>
        <TextField fullWidth required={cfg.enabled} label={t('settings.sso.oidc.issuer_url')} value={cfg.issuer_url}
          onChange={e => patch('issuer_url', e.target.value)}
          error={!!errs.issuer_url}
          helperText={errs.issuer_url ? t(`admin:${errs.issuer_url}`) : ''} />
        <TextField fullWidth required={cfg.enabled} label={t('settings.sso.oidc.client_id')} value={cfg.client_id}
          onChange={e => patch('client_id', e.target.value)}
          error={!!errs.client_id}
          helperText={errs.client_id ? t(`admin:${errs.client_id}`) : ''} />
        {cfg.has_client_secret && !changeSecret ? (
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1.5, height: 56, px: 1.75, borderRadius: 1.5, border: `1px solid ${md.outlineVariant}` }}>
            <Typography variant="body2">{t('settings.sso.oidc.client_secret_kept')}</Typography>
            <Button size="small" variant="text" onClick={() => setChangeSecret(true)}>{t('settings.sso.oidc.client_secret_change')}</Button>
          </Box>
        ) : (
          <TextField fullWidth type="password" label={t('settings.sso.oidc.client_secret')} value={secret}
            onChange={e => setSecret(e.target.value)} autoComplete="new-password" />
        )}
        <TextField fullWidth required={cfg.enabled} label={t('settings.sso.oidc.redirect_url')} value={cfg.redirect_url}
          onChange={e => patch('redirect_url', e.target.value)}
          error={!!errs.redirect_url}
          helperText={errs.redirect_url ? t(`admin:${errs.redirect_url}`) : ''} />
        <TextField fullWidth label={t('settings.sso.oidc.scopes')} value={cfg.scopes.join(', ')}
          onChange={e => patch('scopes', e.target.value.split(',').map(s => s.trim()).filter(Boolean))} />
      </Section>

      <Section title={t('settings.sso.oidc.attr_section')} md={md}>
        <Pair>
          <TextField fullWidth label={t('settings.sso.oidc.attr_username')} value={cfg.attribute_mapping.username}
            onChange={e => patchAttr('username', e.target.value)} />
          <TextField fullWidth label={t('settings.sso.oidc.attr_email')} value={cfg.attribute_mapping.email}
            onChange={e => patchAttr('email', e.target.value)} />
        </Pair>
        <Pair>
          <TextField fullWidth label={t('settings.sso.oidc.attr_display_name')} value={cfg.attribute_mapping.display_name}
            onChange={e => patchAttr('display_name', e.target.value)} />
          <TextField fullWidth label={t('settings.sso.oidc.attr_groups')} value={cfg.attribute_mapping.groups}
            onChange={e => patchAttr('groups', e.target.value)} />
        </Pair>
        <RoleRulesEditor
          value={cfg.role_rules ?? []}
          onChange={rules => patch('role_rules', rules)}
          md={md}
        />
      </Section>

      <Section title={t('settings.sso.oidc.new_user_section')} md={md}>
        <FormControlLabel
          label={t('settings.sso.allow_auto_create', { defaultValue: '允许通过 SSO 自动创建账户' })}
          control={<Switch checked={cfg.allow_auto_create}
            onChange={(_, c) => patch('allow_auto_create', c)} />}
          sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: -1 }}>
          {t('settings.sso.allow_auto_create_hint', {
            defaultValue: '关闭后，未在面板中预先创建的账户首次 SSO 登录会被跳转到"联系管理员"页；IdP 管理员组不受影响。',
          })}
        </Typography>
        <GroupSlugPicker
          label={t('settings.sso.oidc.default_group')}
          value={cfg.default_group_slug}
          onChange={slug => patch('default_group_slug', slug)}
          groups={groups}
        />
        <Pair>
          <NumField label={t('settings.sso.oidc.expire_days')} value={cfg.new_user_defaults.expire_days}
            onChange={v => patchDef('expire_days', v)} />
          <NumField label={t('settings.sso.oidc.traffic_limit_gb')}
            value={Math.round(cfg.new_user_defaults.traffic_limit_bytes / 1024 / 1024 / 1024)}
            onChange={v => patchDef('traffic_limit_bytes', v * 1024 * 1024 * 1024)} />
        </Pair>
        <ResetPeriodField
          value={cfg.new_user_defaults.traffic_reset_period}
          onChange={v => patchDef('traffic_reset_period', v)}
          md={md}
        />
      </Section>
    </Box>
  )
}
