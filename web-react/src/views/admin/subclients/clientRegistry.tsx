// Sub-client registry editor + presets, extracted from SettingsView (v3.8.0
// §6.1) so it can drive the standalone Sub clients page. normalizeRegistry is
// also imported back by SettingsView (its load() still coerces null array
// fields before the full-settings PUT). The editor reads `theme.palette.md`
// internally rather than taking an `md` prop, so it has no SettingsView-local
// type dependency.
import { useState } from 'react'
import {
  Box,
  Button,
  Card,
  Chip,
  Divider,
  FormControlLabel,
  IconButton,
  Menu,
  MenuItem,
  Switch,
  TextField,
  Typography,
  useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import { useTranslation } from 'react-i18next'

import { confirm } from '@/components/ConfirmHost'
import type { SubClientApp, SubClientFamily, SubPlatform, SubRenderFormat } from '@/api/settings'

// CLIENT_PRESETS mirrors defaultSubClients() on the Go side — keep them in
// sync. Each family carries its UA keywords + render format and the import apps
// nested under it. "Add preset" appends a whole family; "reset" rebuilds the
// registry from this list.
export const CLIENT_PRESETS: SubClientFamily[] = [
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

export const PLATFORM_OPTIONS: SubPlatform[] = ['windows', 'macos', 'linux', 'android', 'ios', 'other']

export function clonePreset(p: SubClientFamily): SubClientFamily {
  return JSON.parse(JSON.stringify(p)) as SubClientFamily
}

// normalizeRegistry guards against null array fields from the server: a
// detection-only family serializes apps as JSON null (Go nil slice), and
// keyword / platform / recommended_for arrays can be null too. Coerce them all
// to [] on the way into form state so the editor's .map/.length never see null.
export function normalizeRegistry(fams?: SubClientFamily[] | null): SubClientFamily[] {
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
export default function ClientRegistryEditor({ families, onChange }: { families: SubClientFamily[]; onChange: (v: SubClientFamily[]) => void }) {
  const theme = useTheme()
  const md = theme.palette.md
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
        <Menu anchorEl={presetAnchor} open={!!presetAnchor} onClose={() => setPresetAnchor(null)} slotProps={{ paper: { sx: { maxHeight: 360 } } }}>
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
