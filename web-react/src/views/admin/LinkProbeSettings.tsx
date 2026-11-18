import { useCallback, useEffect, useMemo, useState } from 'react'
import { Box, Button, Card, CircularProgress, Divider, FormControlLabel, IconButton, Switch, TextField, Tooltip, Typography, useTheme } from '@mui/material'
import SaveIcon from '@mui/icons-material/Save'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import { useTranslation } from 'react-i18next'
import {
  getTransitProbeSettings, putTransitProbeSettings, transitProbeErrorOf,
  type TransitProbeSettingKey, type TransitProbeSettings, type TransitProbeSettingsView,
} from '@/api/transitProbe'
import { pushSnack } from '@/components/SnackbarHost'

// The link-probe section of the settings page. It has its own load and Save
// against /admin/transit/probe-settings; the page's system-settings form does
// not carry these keys.

type NumKey = 'transit_probe_interval_seconds' | 'transit_probe_timeout_ms' | 'transit_probe_withhold_after' | 'transit_probe_restore_after'
type BoolKey = 'transit_probe_enabled' | 'transit_probe_withhold'

const NUM_FIELDS: { key: NumKey; label: string; help: string; min: number; max: number }[] = [
  { key: 'transit_probe_interval_seconds', label: 'interval', help: 'interval_help', min: 30, max: 3600 },
  { key: 'transit_probe_timeout_ms', label: 'timeout', help: 'timeout_help', min: 500, max: 10000 },
  { key: 'transit_probe_withhold_after', label: 'withhold_after', help: 'withhold_after_help', min: 1, max: 20 },
  { key: 'transit_probe_restore_after', label: 'restore_after', help: 'restore_after_help', min: 1, max: 20 },
]

type Draft = Record<NumKey, string> & { transit_probe_url: string } & Record<BoolKey, boolean>

function toDraft(s: TransitProbeSettings): Draft {
  return {
    transit_probe_enabled: !!s.transit_probe_enabled,
    transit_probe_withhold: !!s.transit_probe_withhold,
    transit_probe_url: s.transit_probe_url ?? '',
    transit_probe_interval_seconds: s.transit_probe_interval_seconds ? String(s.transit_probe_interval_seconds) : '',
    transit_probe_timeout_ms: s.transit_probe_timeout_ms ? String(s.transit_probe_timeout_ms) : '',
    transit_probe_withhold_after: s.transit_probe_withhold_after ? String(s.transit_probe_withhold_after) : '',
    transit_probe_restore_after: s.transit_probe_restore_after ? String(s.transit_probe_restore_after) : '',
  }
}

/** Empty or 0 means "use the default"; anything else must be a whole number in range. */
function numberOf(text: string, min: number, max: number): { value: number; ok: boolean } {
  const trimmed = text.trim()
  if (trimmed === '') return { value: 0, ok: true }
  if (!/^\d+$/.test(trimmed)) return { value: 0, ok: false }
  const n = Number(trimmed)
  if (n === 0) return { value: 0, ok: true }
  return { value: n, ok: n >= min && n <= max }
}

function urlValid(text: string): boolean {
  const v = text.trim()
  if (v === '') return true
  if (!/^https?:\/\//i.test(v)) return false
  try {
    const u = new URL(v)
    if (!u.hostname) return false
    // new URL drops a default port, so check the typed authority too.
    const m = /^https?:\/\/[^/?#]*?:(\d*)(?:[/?#]|$)/i.exec(v)
    if (m) {
      const port = Number(m[1])
      if (!m[1] || port < 1 || port > 65535) return false
    }
    return true
  } catch { return false }
}

function validate(d: Draft): Partial<Record<TransitProbeSettingKey, true>> {
  const bad: Partial<Record<TransitProbeSettingKey, true>> = {}
  for (const f of NUM_FIELDS) if (!numberOf(d[f.key], f.min, f.max).ok) bad[f.key] = true
  if (!urlValid(d.transit_probe_url)) bad.transit_probe_url = true
  return bad
}

function toSettings(d: Draft): TransitProbeSettings {
  const n = (k: NumKey) => numberOf(d[k], 0, Number.MAX_SAFE_INTEGER).value
  return {
    transit_probe_enabled: d.transit_probe_enabled,
    transit_probe_withhold: d.transit_probe_withhold,
    transit_probe_url: d.transit_probe_url.trim(),
    transit_probe_interval_seconds: n('transit_probe_interval_seconds'),
    transit_probe_timeout_ms: n('transit_probe_timeout_ms'),
    transit_probe_withhold_after: n('transit_probe_withhold_after'),
    transit_probe_restore_after: n('transit_probe_restore_after'),
  }
}

export default function LinkProbeSettings() {
  const md = useTheme().palette.md
  const { t } = useTranslation('admin')
  const [view, setView] = useState<TransitProbeSettingsView | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [loadFailed, setLoadFailed] = useState(false)
  const [saving, setSaving] = useState(false)
  const [serverErrors, setServerErrors] = useState<Partial<Record<string, string>>>({})

  const adopt = useCallback((v: TransitProbeSettingsView) => {
    setView(v)
    setDraft(toDraft(v.settings))
    setServerErrors({})
  }, [])

  useEffect(() => {
    let live = true
    getTransitProbeSettings().then(v => { if (live) adopt(v) }).catch(() => { if (live) setLoadFailed(true) })
    return () => { live = false }
  }, [adopt])

  const invalid = useMemo(() => (draft ? validate(draft) : {}), [draft])

  if (loadFailed) {
    return (
      <Card sx={{ p: 2.5, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Typography sx={{ color: md.error, fontSize: 13 }}>{t('settings.probe.load_failed')}</Typography>
      </Card>
    )
  }
  if (!view || !draft) {
    return <Box sx={{ p: 2, display: 'grid', placeItems: 'center' }}><CircularProgress size={20} /></Box>
  }

  const patch = <K extends keyof Draft>(key: K, value: Draft[K]) => {
    setDraft(prev => (prev ? { ...prev, [key]: value } : prev))
    setServerErrors(prev => { if (!prev[key]) return prev; const next = { ...prev }; delete next[key]; return next })
  }

  const defaultText = (key: TransitProbeSettingKey) => {
    const v = view.defaults[key] ?? view.effective[key]
    return v === undefined ? '' : String(v)
  }

  const help = (key: string) => (
    <Tooltip title={t(`settings.probe.${key}`)} enterTouchDelay={0}>
      <IconButton size="small" aria-label={t(`settings.probe.${key}`)} sx={{ color: md.onSurfaceVariant }}>
        <InfoOutlinedIcon fontSize="small" />
      </IconButton>
    </Tooltip>
  )

  const fieldError = (key: TransitProbeSettingKey, rangeText?: string): string | undefined => {
    if (serverErrors[key]) return serverErrors[key]
    if (!invalid[key]) return undefined
    return rangeText
  }

  const dirtyKeys = (Object.keys(toSettings(draft)) as TransitProbeSettingKey[])
    .filter(k => toSettings(draft)[k] !== view.settings[k])
  const canSave = dirtyKeys.length > 0 && Object.keys(invalid).length === 0 && !saving

  const save = async () => {
    if (!canSave) return
    const next = toSettings(draft)
    const changed: Partial<TransitProbeSettings> = {}
    for (const k of dirtyKeys) (changed as Record<string, unknown>)[k] = next[k]
    setSaving(true)
    setServerErrors({})
    try {
      adopt(await putTransitProbeSettings(changed))
      pushSnack(t('settings.probe.saved'), 'success')
    } catch (err) {
      const e = transitProbeErrorOf(err)
      if (e?.status === 400 && e.field) {
        setServerErrors({ [e.field]: e.message || t('settings.probe.save_failed') })
      } else {
        pushSnack(e?.message || t('settings.probe.save_failed'), 'error')
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card data-testid="link-probe-settings" sx={{ p: 2.5, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}`, maxWidth: 880, mt: 2.5 }}>
      <Typography sx={{ fontWeight: 600, fontSize: 14, mb: 1, color: md.onSurface }}>{t('settings.probe.section')}</Typography>
      <Divider sx={{ mb: 1.5 }} />
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('settings.probe.intro')}</Typography>

        <Box sx={{ display: 'flex', alignItems: 'center' }}>
          <FormControlLabel
            control={<Switch checked={draft.transit_probe_enabled} onChange={(_, c) => patch('transit_probe_enabled', c)} />}
            label={t('settings.probe.enabled')} />
          {help('enabled_help')}
        </Box>

        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
          {NUM_FIELDS.slice(0, 2).map(f => (
            <NumberField key={f.key} f={f} draft={draft} patch={patch} defaultText={defaultText} fieldError={fieldError} help={help} />
          ))}
        </Box>

        <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 0.5 }}>
          <TextField size="small" fullWidth label={t('settings.probe.url')} value={draft.transit_probe_url}
            placeholder={defaultText('transit_probe_url')}
            onChange={e => patch('transit_probe_url', e.target.value)}
            error={!!fieldError('transit_probe_url', t('settings.probe.err_url'))}
            helperText={fieldError('transit_probe_url', t('settings.probe.err_url'))
              ?? t('settings.probe.url_default_hint', { value: defaultText('transit_probe_url') })} />
          {defaultText('transit_probe_url') !== '' && draft.transit_probe_url !== defaultText('transit_probe_url') && (
            <Button size="small" sx={{ mt: 0.5, flexShrink: 0 }}
              onClick={() => patch('transit_probe_url', defaultText('transit_probe_url'))}>
              {t('settings.probe.url_reset')}
            </Button>
          )}
          {help('url_help')}
        </Box>

        <Box sx={{ display: 'flex', alignItems: 'center' }}>
          <FormControlLabel
            control={<Switch checked={draft.transit_probe_withhold} onChange={(_, c) => patch('transit_probe_withhold', c)} />}
            label={t('settings.probe.withhold')} />
          {help('withhold_help')}
        </Box>

        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
          {NUM_FIELDS.slice(2).map(f => (
            <NumberField key={f.key} f={f} draft={draft} patch={patch} defaultText={defaultText} fieldError={fieldError} help={help} />
          ))}
        </Box>

        <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button variant="contained" size="small" startIcon={<SaveIcon />} disabled={!canSave} onClick={() => { void save() }}>
            {t('settings.probe.save')}
          </Button>
        </Box>
      </Box>
    </Card>
  )
}

function NumberField({ f, draft, patch, defaultText, fieldError, help }: {
  f: typeof NUM_FIELDS[number]
  draft: Draft
  patch: <K extends keyof Draft>(key: K, value: Draft[K]) => void
  defaultText: (key: TransitProbeSettingKey) => string
  fieldError: (key: TransitProbeSettingKey, rangeText?: string) => string | undefined
  help: (key: string) => React.ReactNode
}) {
  const { t } = useTranslation('admin')
  const err = fieldError(f.key, t('settings.probe.err_range', { min: f.min, max: f.max }))
  return (
    <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 0.5, flex: '1 1 260px' }}>
      <TextField size="small" fullWidth label={t(`settings.probe.${f.label}`)} value={draft[f.key]}
        placeholder={defaultText(f.key)}
        onChange={e => patch(f.key, e.target.value)}
        slotProps={{ htmlInput: { inputMode: 'numeric' } }}
        error={!!err}
        helperText={err ?? t('settings.probe.default_hint', { value: defaultText(f.key) })} />
      {help(f.help)}
    </Box>
  )
}
