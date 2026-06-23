// SubClientsView — the standalone Sub clients page (v3.8.0 §6.1). Hosts the
// global client-detection registry (sub_clients) + the unknown-client filter
// mode (sub_client_filter_mode); both are global per the plan's §3b (consumed
// before user identity is known, never per-group). Loads and saves the full
// UISettings via the same endpoint SettingsView uses — only the two registry
// fields are edited here, the rest round-trip unchanged.
import { useEffect, useState, type FormEvent } from 'react'
import {
  Box,
  Button,
  Card,
  CircularProgress,
  IconButton,
  MenuItem,
  TextField,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import SaveIcon from '@mui/icons-material/Save'
import HelpOutlineIcon from '@mui/icons-material/HelpOutline'
import { useTranslation } from 'react-i18next'

import { getUISettings, putUISettings, type UISettings } from '@/api/settings'
import PageHeader from '@/components/PageHeader'
import { pushSnack } from '@/components/SnackbarHost'
import ClientRegistryEditor, { normalizeRegistry } from './subclients/clientRegistry'

export default function SubClientsView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'nav'])
  const [settings, setSettings] = useState<UISettings | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => { void load() }, [])

  function normalize(s: UISettings): UISettings {
    return {
      ...s,
      sub_clients: normalizeRegistry(s.sub_clients),
      sub_client_filter_mode: s.sub_client_filter_mode ?? 'blacklist',
    }
  }

  async function load() {
    setLoading(true)
    try {
      setSettings(normalize(await getUISettings()))
    } finally { setLoading(false) }
  }

  async function save(e?: FormEvent) {
    e?.preventDefault()
    if (!settings) return
    setSaving(true)
    try {
      setSettings(normalize(await putUISettings(settings)))
      pushSnack(t('settings.saved'), 'success')
    } catch (err) {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error || String(err)
      pushSnack(msg, 'warning')
    } finally { setSaving(false) }
  }

  function patch<K extends keyof UISettings>(key: K, value: UISettings[K]) {
    setSettings(prev => prev ? { ...prev, [key]: value } : prev)
  }

  if (loading || !settings) {
    return <Box sx={{ p: 3, display: 'grid', placeItems: 'center', minHeight: 400 }}><CircularProgress /></Box>
  }

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader title={t('nav:admin.sub_clients')} />
      <Box component="form" onSubmit={save} sx={{ display: 'flex', flexDirection: 'column', gap: 3, mt: 2 }}>
        <Card sx={{ p: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
          <Typography sx={{ fontWeight: 500, mb: 1.5, color: md.onSurface }}>
            {t('settings.subscription.section_detection', { defaultValue: '客户端检测与过滤' })}
          </Typography>
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
        </Card>

        <ClientRegistryEditor
          families={settings.sub_clients}
          onChange={v => patch('sub_clients', v)}
        />

        {/* Sticky bottom action bar — matches SettingsView's convention so Save
            sits in the same place across admin settings pages. */}
        <Box sx={{
          position: 'sticky', bottom: 0, zIndex: 2,
          mx: -3, px: 3, py: 1.5,
          display: 'flex', justifyContent: 'flex-end', gap: 1,
          bgcolor: md.surface, borderTop: `1px solid ${md.outlineVariant}`,
        }}>
          <Button variant="text" type="button" onClick={() => void load()} disabled={saving}>
            {t('settings.cancel', { defaultValue: '取消' })}
          </Button>
          <Button variant="contained" type="submit" disabled={saving}
            startIcon={saving ? <CircularProgress size={14} color="inherit" /> : <SaveIcon />}>
            {t('settings.save')}
          </Button>
        </Box>
      </Box>
    </Box>
  )
}
