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
  Menu,
  MenuItem,
  Popover,
  Switch,
  Tab,
  Tabs,
  TextField,
  Typography,
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
  type SAMLMetadataSummary,
  type MailReminderKind,
  type MailSettings,
  type MailTemplate,
  type OIDCConfig,
  type QuickLink,
  type SAMLConfig,
  type SSORoleRule,
  type SubClientRule,
  type SubImportClient,
  type UISettings,
} from '@/api/settings'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import DragIndicatorIcon from '@mui/icons-material/DragIndicator'
import type { LoginMode } from '@/api/types'
import { pushSnack } from '@/components/SnackbarHost'
import { confirm } from '@/components/ConfirmHost'
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

type TabKey = 'general' | 'brand' | 'subscription' | 'portal' | 'mail' | 'sso'

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
    ['general', 'brand', 'subscription', 'portal', 'mail', 'sso'])
  const [settings, setSettings] = useState<UISettings | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => { void load() }, [])

  // Go's `json.Marshal(nil slice)` serialises as `null`, so a fresh DB
  // returns `quick_links: null` etc. instead of an empty array. The
  // editors all call `.length` / `.map` on these unconditionally — defend
  // by normalising at the load/save boundary so the rest of the
  // component can treat them as arrays.
  function normalize(s: UISettings): UISettings {
    return {
      ...s,
      sub_client_rules: s.sub_client_rules ?? [],
      sub_import_clients: s.sub_import_clients ?? [],
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

  async function save(e?: FormEvent) {
    e?.preventDefault()
    if (!settings) return
    // Cross-tab submit guard. Brand.sub_base_url is required because the
    // subscription URL embeds it — saving an invalid one silently breaks
    // every client. Emergency-access values only matter when the feature
    // is on; off means "ignore the numbers".
    if (settings.sub_base_url) {
      const urlErr = validateUrl(settings.sub_base_url, { required: true })
      if (urlErr) {
        pushSnack(t(`admin:${urlErr}`) + ' (sub_base_url)', 'warning')
        return
      }
    }
    if (settings.emergency_access_enabled) {
      if (!Number.isInteger(settings.emergency_access_hours) || settings.emergency_access_hours < 1) {
        pushSnack(t('admin:validation.positive_int') + ' (emergency_access_hours)', 'warning'); return
      }
      if (!Number.isInteger(settings.emergency_access_max_count) || settings.emergency_access_max_count < 1) {
        pushSnack(t('admin:validation.positive_int') + ' (emergency_access_max_count)', 'warning'); return
      }
      if (!Number.isInteger(settings.emergency_access_quota_gb) || settings.emergency_access_quota_gb < 0) {
        pushSnack(t('admin:validation.non_negative_int') + ' (emergency_access_quota_gb)', 'warning'); return
      }
    }
    // Announcement: if the admin enabled it but left the title or body
    // empty, the portal would render a chrome-less notice — protect against
    // accidental publishes.
    if (settings.global_announcement?.enabled) {
      if (!settings.global_announcement.title?.trim()) {
        pushSnack(t('admin:validation.required') + ' (announcement title)', 'warning'); return
      }
      if (!settings.global_announcement.content?.trim()) {
        pushSnack(t('admin:validation.required') + ' (announcement content)', 'warning'); return
      }
    }
    // Quick links: each enabled row must have a valid label + URL,
    // otherwise the portal would render an empty chip with a broken href.
    for (const [idx, l] of settings.quick_links.entries()) {
      if (!l.enabled) continue
      if (!l.label.trim()) {
        pushSnack(t('admin:validation.required') + ` (quick link #${idx + 1} label)`, 'warning'); return
      }
      const urlErr = validateUrl(l.url, { required: true })
      if (urlErr) {
        pushSnack(t(`admin:${urlErr}`) + ` (quick link #${idx + 1} URL)`, 'warning'); return
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
      pushSnack(t('settings.saved'), 'success')
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
      <Typography variant="h4" sx={{ mb: 1 }}>{t('settings.title')}</Typography>

      <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ mt: 2, mb: 3, borderBottom: `1px solid ${md.outlineVariant}` }}>
        {tabs.map(tb => <Tab key={tb.key} value={tb.key} label={t(tb.labelKey)} />)}
      </Tabs>

      {tab === 'general' && (
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
              helperText={t('settings.general.emergency_access_quota_gb_hint')} />
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
            <FormControlLabel label={t('settings.subscription.sub_block_auto_disable')}
              control={<Switch checked={settings.sub_block_auto_disable}
                onChange={(_, c) => patch('sub_block_auto_disable', c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
            <NumField label={t('settings.subscription.sub_block_auto_disable_count')}
              value={settings.sub_block_auto_disable_count}
              onChange={v => patch('sub_block_auto_disable_count', v)} />
          </Section>

          <ClientRulesEditor
            rules={settings.sub_client_rules}
            onChange={v => patch('sub_client_rules', v)}
            md={md}
          />

          <ImportClientsEditor
            clients={settings.sub_import_clients}
            onChange={v => patch('sub_import_clients', v)}
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
    ['expire_before', 'expired', 'traffic_low', 'traffic_exhausted', 'account_disabled', 'account_enabled', 'announcement'])
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

  const TPL_KINDS: MailReminderKind[] = ['expire_before', 'expired', 'traffic_low', 'traffic_exhausted', 'account_disabled', 'account_enabled', 'announcement']
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

      {/* Thresholds */}
      <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Typography sx={{ fontWeight: 500, mb: 1.5 }}>{t('settings.mail.section_thresholds')}</Typography>
        <Divider sx={{ mb: 2 }} />
        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
          <TextField type="number" label={t('settings.mail.expire_before_days')}
            value={mail.expire_before_days}
            onChange={e => patchMail('expire_before_days', Number(e.target.value))}
            sx={{ flex: '1 1 240px' }} />
          <TextField type="number" label={t('settings.mail.traffic_remain_percent')}
            value={mail.traffic_remain_percent}
            onChange={e => patchMail('traffic_remain_percent', Number(e.target.value))}
            sx={{ flex: '1 1 240px' }} />
        </Box>
      </Card>

      {/* Test send */}
      <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Typography sx={{ fontWeight: 500, mb: 1.5 }}>{t('settings.mail.section_test')}</Typography>
        <Divider sx={{ mb: 2 }} />
        <Box sx={{ display: 'flex', gap: 2, alignItems: 'flex-end' }}>
          <TextField fullWidth label={t('settings.mail.test_to')} type="email"
            value={testTo} onChange={e => setTestTo(e.target.value)}
            error={!!errs.test_to}
            helperText={errs.test_to ? t(`admin:${errs.test_to}`) : ''} />
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
        <Tabs value={activeTpl} onChange={(_, v) => setActiveTpl(v as MailReminderKind)}
          variant="scrollable" scrollButtons="auto"
          sx={{ borderBottom: `1px solid ${md.outlineVariant}`, mb: 2 }}>
          {TPL_KINDS.map(k => <Tab key={k} value={k} label={t(`settings.mail.kind.${k}`)} />)}
        </Tabs>
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

function QuickLinksEditor({ links, onChange, md }: { links: QuickLink[]; onChange: (v: QuickLink[]) => void; md: MdShape }) {
  const { t } = useTranslation('admin')
  const update = (i: number, patch: Partial<QuickLink>) =>
    onChange(links.map((l, idx) => idx === i ? { ...l, ...patch } : l))
  const remove = (i: number) => onChange(links.filter((_, idx) => idx !== i))
  const add = () => onChange([
    ...links,
    { label: '', url: '', new_window: true, enabled: true, sort: (links.at(-1)?.sort ?? 0) + 10 },
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

function ClientRulesEditor({ rules, onChange, md }: { rules: SubClientRule[]; onChange: (v: SubClientRule[]) => void; md: MdShape }) {
  const { t } = useTranslation('admin')
  const update = (i: number, patch: Partial<SubClientRule>) =>
    onChange(rules.map((r, idx) => idx === i ? { ...r, ...patch } : r))
  const remove = (i: number) => onChange(rules.filter((_, idx) => idx !== i))
  const add = () => onChange([
    ...rules,
    { name: '', keywords: [], render_format: 'mihomo', enabled: true },
  ])
  return (
    <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
      <Typography sx={{ fontWeight: 500, mb: 1.5, color: md.onSurface }}>{t('settings.subscription.section_clients')}</Typography>
      <Divider sx={{ mb: 2 }} />
      {rules.length === 0 ? (
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, py: 2, textAlign: 'center' }}>
          {t('settings.subscription.no_rules')}
        </Typography>
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
          {rules.map((r, i) => (
            <Box key={i} sx={{
              display: 'flex', flexWrap: 'wrap', gap: 1.25, alignItems: 'center',
              p: 1.5, borderRadius: 2, border: `1px solid ${md.outlineVariant}`,
              bgcolor: md.surfaceContainerHigh,
            }}>
              <TextField size="small" label={t('settings.subscription.rule_field.name')}
                value={r.name} onChange={e => update(i, { name: e.target.value })}
                sx={{ flex: '1 1 160px' }} />
              <TextField size="small" label={t('settings.subscription.rule_field.keywords')}
                value={r.keywords.join(', ')}
                onChange={e => update(i, { keywords: e.target.value.split(',').map(s => s.trim()).filter(Boolean) })}
                sx={{ flex: '2 1 240px' }} />
              <TextField select size="small" label={t('settings.subscription.rule_field.render_format')}
                value={r.render_format}
                onChange={e => update(i, { render_format: e.target.value as 'mihomo' | 'sing-box' | 'uri-list' })}
                sx={{ width: 150 }}>
                <MenuItem value="mihomo">mihomo</MenuItem>
                <MenuItem value="sing-box">sing-box</MenuItem>
                <MenuItem value="uri-list">URI 列表 (V2rayN / Passwall)</MenuItem>
              </TextField>
              <FormControlLabel
                label={t('settings.subscription.rule_field.enabled')}
                control={<Switch size="small" checked={r.enabled}
                  onChange={(_, c) => update(i, { enabled: c })} />}
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
          {t('settings.subscription.add_rule')}
        </Button>
      </Box>
    </Card>
  )
}

// IMPORT_CLIENT_PRESETS is the known-client cheat sheet for the
// "Add preset" menu. Each entry mirrors the shape an admin would type in
// by hand; clicking it appends a fully-configured row that they can then
// tweak (re-order, rename, toggle off, etc).
//
// Sort defaults to 100; the add handler bumps it based on the current
// tail position so freshly-added presets always land at the bottom.
const IMPORT_CLIENT_PRESETS: Array<Omit<SubImportClient, 'sort' | 'enabled'>> = [
  {
    name: 'Clash Verge Rev',
    platforms: ['windows', 'macos', 'linux'],
    render_format: 'mihomo',
    import_url_template: 'clash://install-config?url={{ sub_url_encoded }}',
    install_url: 'https://github.com/clash-verge-rev/clash-verge-rev/releases',
    recommended_for: ['windows', 'macos', 'linux'],
  },
  {
    name: 'Clash Meta for Android',
    platforms: ['android'],
    render_format: 'mihomo',
    // update-interval is in MINUTES per CMfA PR #732 (deliberate units
    // mismatch with the Profile-Update-Interval HTTP header which is hours).
    import_url_template: 'clash://install-config?url={{ sub_url_encoded }}&update-interval={{ sub_update_interval_minutes }}',
    install_url: 'https://github.com/MetaCubeX/ClashMetaForAndroid/releases',
    recommended_for: ['android'],
  },
  {
    name: 'Clash Mi',
    platforms: ['windows', 'macos', 'linux', 'android', 'ios'],
    render_format: 'mihomo',
    // ClashMi registers clashmi://, clash://, clashmeta:// and flclash://.
    // Using `clashmi://` keeps iOS from offering Stash (which only owns
    // clash://) when both apps are installed.
    import_url_template: 'clashmi://install-config?url={{ sub_url_encoded }}',
    install_url: 'https://github.com/KaringX/clashmi/releases',
    recommended_for: ['ios'],
  },
  {
    name: 'Stash',
    platforms: ['ios'],
    render_format: 'mihomo',
    import_url_template: 'stash://install-config?url={{ sub_url_encoded }}',
    install_url: 'https://apps.apple.com/app/stash-rule-based-proxy/id1596063349',
    recommended_for: [],
  },
  {
    name: 'sing-box',
    platforms: ['ios', 'macos', 'android'],
    render_format: 'sing-box',
    import_url_template: 'sing-box://import-remote-profile?url={{ sub_url_encoded }}#{{ profile_name_encoded }}',
    install_url: 'https://sing-box.sagernet.org/clients/',
    recommended_for: [],
  },
  {
    name: 'V2rayN',
    platforms: ['windows'],
    render_format: 'uri-list',
    import_url_template: '{{ sub_url }}',
    install_url: 'https://github.com/2dust/v2rayN/releases',
    recommended_for: [],
  },
  {
    name: 'V2rayNG',
    platforms: ['android'],
    render_format: 'uri-list',
    // V2rayNG's install-sub intent expects the `url` query param to be
    // URL-encoded, NOT base64. Previous preset wrapped in base64 which
    // V2rayNG would reject silently. Fragment is the subscription name.
    // See 2dust/v2rayNG#4141.
    import_url_template: 'v2rayng://install-sub?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}',
    install_url: 'https://github.com/2dust/v2rayNG/releases',
    recommended_for: [],
  },
  {
    name: 'Shadowrocket',
    platforms: ['ios'],
    render_format: 'uri-list',
    import_url_template: 'sub://{{ sub_url_b64 }}',
    install_url: 'https://apps.apple.com/app/shadowrocket/id932747118',
    recommended_for: [],
  },
  {
    // Karing is the sister app to Clash Mi from KaringX, popular alongside
    // ClashMi on iOS. Distinct karing:// scheme so it doesn't collide with
    // Stash. Sing-box core, so render format is sing-box.
    name: 'Karing',
    platforms: ['windows', 'macos', 'linux', 'android', 'ios'],
    render_format: 'sing-box',
    import_url_template: 'karing://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}',
    install_url: 'https://github.com/KaringX/karing/releases',
    recommended_for: [],
  },
]

function ImportClientsEditor({ clients, onChange, md }: { clients: SubImportClient[]; onChange: (v: SubImportClient[]) => void; md: MdShape }) {
  const { t } = useTranslation('admin')
  const [presetAnchor, setPresetAnchor] = useState<HTMLElement | null>(null)
  const update = (i: number, patch: Partial<SubImportClient>) =>
    onChange(clients.map((c, idx) => idx === i ? { ...c, ...patch } : c))
  const remove = (i: number) => onChange(clients.filter((_, idx) => idx !== i))
  const add = () => onChange([
    ...clients,
    {
      name: '', platforms: [], render_format: 'mihomo',
      import_url_template: '', install_url: '', enabled: true,
      sort: (clients.at(-1)?.sort ?? 0) + 10,
      recommended_for: [],
    },
  ])
  function addPreset(preset: Omit<SubImportClient, 'sort' | 'enabled'>) {
    onChange([
      ...clients,
      { ...preset, sort: (clients.at(-1)?.sort ?? 0) + 10, enabled: true },
    ])
    setPresetAnchor(null)
  }
  const PLATFORM_OPTIONS: Array<SubImportClient['platforms'][number]> = ['windows', 'macos', 'linux', 'android', 'ios', 'other']
  return (
    <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
      <Typography sx={{ fontWeight: 500, mb: 1.5, color: md.onSurface }}>{t('settings.subscription.section_imports')}</Typography>
      <Divider sx={{ mb: 2 }} />
      {clients.length === 0 ? (
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, py: 2, textAlign: 'center' }}>
          {t('settings.subscription.no_clients')}
        </Typography>
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          {clients.map((c, i) => (
            <Box key={i} sx={{
              display: 'flex', flexDirection: 'column', gap: 1.25,
              p: 2, borderRadius: 2, border: `1px solid ${md.outlineVariant}`,
              bgcolor: md.surfaceContainerHigh,
            }}>
              <Box sx={{ display: 'flex', gap: 1.25, flexWrap: 'wrap', alignItems: 'center' }}>
                <TextField size="small" label={t('settings.subscription.client_field.name')}
                  value={c.name} onChange={e => update(i, { name: e.target.value })}
                  sx={{ flex: '1 1 200px' }} />
                <TextField select size="small" label={t('settings.subscription.client_field.render_format')}
                  value={c.render_format}
                  onChange={e => update(i, { render_format: e.target.value as 'mihomo' | 'sing-box' | 'uri-list' })}
                  sx={{ width: 180 }}>
                  <MenuItem value="mihomo">mihomo</MenuItem>
                  <MenuItem value="sing-box">sing-box</MenuItem>
                  <MenuItem value="uri-list">URI 列表</MenuItem>
                </TextField>
                <TextField size="small" type="number" label={t('settings.subscription.client_field.sort')}
                  value={c.sort} onChange={e => update(i, { sort: Number(e.target.value) })}
                  sx={{ width: 100 }} />
                <FormControlLabel
                  label={t('settings.subscription.client_field.enabled')}
                  control={<Switch size="small" checked={c.enabled}
                    onChange={(_, ck) => update(i, { enabled: ck })} />}
                  sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1, fontSize: 13 } }}
                />
                <Box sx={{ flex: 1 }} />
                <IconButton size="small" onClick={() => remove(i)} sx={{ color: md.onSurfaceVariant }}>
                  <DeleteIcon fontSize="small" />
                </IconButton>
              </Box>
              <Box>
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.75 }}>
                  {t('settings.subscription.client_field.platforms', { defaultValue: '支持的平台' })}
                </Typography>
                <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.75 }}>
                  {PLATFORM_OPTIONS.map(p => {
                    const selected = c.platforms.includes(p)
                    return (
                      <Chip key={p}
                        size="small"
                        label={t(`settings.subscription.platform.${p}`, { defaultValue: p })}
                        color={selected ? 'primary' : 'default'}
                        variant={selected ? 'filled' : 'outlined'}
                        onClick={() => {
                          const cur = c.platforms
                          const nextPlatforms = selected ? cur.filter(x => x !== p) : [...cur, p]
                          // Removing a platform should also drop it from
                          // recommended_for — otherwise the backend silently
                          // strips it on save and the admin would wonder why
                          // the highlight chip disappeared mid-edit.
                          const nextRec = (c.recommended_for ?? []).filter(x => nextPlatforms.includes(x))
                          update(i, {
                            platforms: nextPlatforms as SubImportClient['platforms'],
                            recommended_for: nextRec as SubImportClient['recommended_for'],
                          })
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
                  {PLATFORM_OPTIONS.filter(p => c.platforms.includes(p)).map(p => {
                    const selected = (c.recommended_for ?? []).includes(p)
                    return (
                      <Chip key={p}
                        size="small"
                        label={t(`settings.subscription.platform.${p}`, { defaultValue: p })}
                        color={selected ? 'primary' : 'default'}
                        variant={selected ? 'filled' : 'outlined'}
                        onClick={() => {
                          const cur = c.recommended_for ?? []
                          update(i, {
                            recommended_for: selected ? cur.filter(x => x !== p) : [...cur, p],
                          })
                        }} />
                    )
                  })}
                  {c.platforms.length === 0 && (
                    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, fontStyle: 'italic' }}>
                      {t('settings.subscription.client_field.recommended_for_empty', { defaultValue: '请先选择支持的平台' })}
                    </Typography>
                  )}
                </Box>
              </Box>
              <TextField size="small" fullWidth label={t('settings.subscription.client_field.import_url_template')}
                value={c.import_url_template}
                onChange={e => update(i, { import_url_template: e.target.value })} />
              <TextField size="small" fullWidth label={t('settings.subscription.client_field.install_url')}
                value={c.install_url}
                onChange={e => update(i, { install_url: e.target.value })} />
            </Box>
          ))}
        </Box>
      )}
      <Box sx={{ mt: 2, display: 'flex', gap: 1.25 }}>
        <Button variant="outlined" size="small" startIcon={<AddIcon />} onClick={add}>
          {t('settings.subscription.add_client')}
        </Button>
        <Button variant="text" size="small" onClick={e => setPresetAnchor(e.currentTarget)}>
          {t('settings.subscription.add_preset', { defaultValue: '从预设添加…' })}
        </Button>
        <Menu anchorEl={presetAnchor} open={!!presetAnchor} onClose={() => setPresetAnchor(null)}
          PaperProps={{ sx: { maxHeight: 360 } }}>
          {IMPORT_CLIENT_PRESETS.map(p => {
            // Existence check helps the admin avoid double-adding the
            // same client by accident — disabled MenuItems show the
            // entry but don't re-add.
            const exists = clients.some(c => c.name === p.name)
            return (
              <MenuItem key={p.name} disabled={exists} onClick={() => addPreset(p)}>
                <Box>
                  <Typography sx={{ fontSize: 14 }}>{p.name}</Typography>
                  <Typography sx={{ fontSize: 12, opacity: 0.7 }}>
                    {p.platforms.join(', ')} · {p.render_format}
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
// attribute value to a panel role. Empty Attribute is the "groups"
// shortcut documented on the backend SSORoleRule type. Reused by both
// SAML and OIDC config sections so the wire format stays identical.
function RoleRulesEditor({ value, onChange, md }: {
  value: SSORoleRule[]
  onChange: (rules: SSORoleRule[]) => void
  md: MdShape
}) {
  const { t } = useTranslation('admin')
  const rules = value ?? []
  function patchRule(idx: number, patch: Partial<SSORoleRule>) {
    onChange(rules.map((r, i) => i === idx ? { ...r, ...patch } : r))
  }
  function addRule() {
    onChange([...rules, { attribute: '', value: '', role: 'admin' }])
  }
  function removeRule(idx: number) {
    onChange(rules.filter((_, i) => i !== idx))
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
      {rules.map((r, i) => (
        <Box key={i} sx={{ display: 'flex', gap: 1, alignItems: 'center', mb: 1 }}>
          <TextField size="small" sx={{ flex: '2 1 200px' }}
            label={t('settings.sso.role_rules_attribute')}
            placeholder={t('settings.sso.role_rules_attribute_placeholder')}
            value={r.attribute}
            onChange={e => patchRule(i, { attribute: e.target.value })} />
          <TextField size="small" sx={{ flex: '2 1 160px' }}
            label={t('settings.sso.role_rules_value')}
            value={r.value}
            onChange={e => patchRule(i, { value: e.target.value })} />
          <TextField select size="small" sx={{ flex: '1 1 110px' }}
            label={t('settings.sso.role_rules_role')}
            value={r.role}
            onChange={e => patchRule(i, { role: e.target.value })}>
            <MenuItem value="admin">{t('users.role.admin')}</MenuItem>
            <MenuItem value="operator">{t('users.role.operator')}</MenuItem>
            <MenuItem value="user">{t('users.role.user')}</MenuItem>
          </TextField>
          <IconButton size="small" onClick={() => removeRule(i)}
            aria-label={t('settings.sso.role_rules_remove')}>
            <DeleteIcon fontSize="small" />
          </IconButton>
        </Box>
      ))}
      <Button size="small" variant="outlined" startIcon={<AddIcon />} onClick={addRule}>
        {t('settings.sso.role_rules_add')}
      </Button>
    </Box>
  )
}

function NumField({ label, value, onChange, helperText }: { label: string; value: number; onChange: (v: number) => void; helperText?: string }) {
  return (
    <TextField fullWidth type="number" label={label}
      value={value} onChange={e => onChange(Number(e.target.value))}
      inputProps={{ min: 0 }} helperText={helperText} />
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
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      setFetchError(msg)
    } finally { setFetching(false) }
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
      admin_group_ids: c.admin_group_ids ?? [],
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

  async function save(e?: FormEvent) {
    e?.preventDefault()
    if (!cfg) return
    const v = validateSaml(cfg)
    setErrs(v)
    const firstKey = firstError(v)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return }
    setSaving(true)
    try {
      const res = await putSAML({
        ...cfg,
        sp: {
          entity_id: cfg.sp.entity_id, acs_url: cfg.sp.acs_url, cert_pem: cfg.sp.cert_pem,
          // Send empty only in "keep existing" mode (there is a stored key
          // and the admin didn't click "change"). On a fresh setup keyPem
          // holds the value the admin just pasted; it must reach the backend.
          key_pem: (cfg.sp.has_key_pem && !changeKey) ? '' : keyPem,
        },
      })
      setCfg(normalizeSAML(res.config))
      setChangeKey(false); setKeyPem('')
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
              disabled={fetching || !cfg.idp.metadata_url.trim()}
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

      {/* Group resolution — admin groups + default group are admin policy
          (not derived from IdP metadata), so they stay editable in both modes. */}
      <Section title={t('settings.sso.saml.group_section', { defaultValue: '分组解析' })} md={md}>
        <RoleRulesEditor
          value={cfg.role_rules ?? []}
          onChange={rules => patch('role_rules', rules)}
          md={md}
        />
        <TextField fullWidth label={t('settings.sso.saml.admin_groups')} value={cfg.admin_group_ids.join(', ')}
          onChange={e => patch('admin_group_ids', e.target.value.split(',').map(s => s.trim()).filter(Boolean))}
          helperText={t('settings.sso.admin_groups_legacy_hint')} />
        <FormControlLabel
          label={t('settings.sso.keep_admin_when_not_in_group')}
          control={<Switch checked={cfg.keep_admin_when_not_in_group}
            onChange={(_, c) => patch('keep_admin_when_not_in_group', c)} />}
          sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: -1 }}>
          {t('settings.sso.keep_admin_when_not_in_group_hint')}
        </Typography>
        <GroupSlugPicker
          label={t('settings.sso.saml.default_group')}
          value={cfg.default_group_slug}
          onChange={slug => patch('default_group_slug', slug)}
          groups={groups}
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
      admin_group_ids: c.admin_group_ids ?? [],
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
        <TextField fullWidth label={t('settings.sso.oidc.admin_groups')} value={cfg.admin_group_ids.join(', ')}
          onChange={e => patch('admin_group_ids', e.target.value.split(',').map(s => s.trim()).filter(Boolean))}
          helperText={t('settings.sso.admin_groups_legacy_hint')} />
        <FormControlLabel
          label={t('settings.sso.keep_admin_when_not_in_group')}
          control={<Switch checked={cfg.keep_admin_when_not_in_group}
            onChange={(_, c) => patch('keep_admin_when_not_in_group', c)} />}
          sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }} />
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: -1 }}>
          {t('settings.sso.keep_admin_when_not_in_group_hint')}
        </Typography>
        <GroupSlugPicker
          label={t('settings.sso.oidc.default_group')}
          value={cfg.default_group_slug}
          onChange={slug => patch('default_group_slug', slug)}
          groups={groups}
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
