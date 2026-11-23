import { useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getTransitProbeSettings, putTransitProbeSettings, transitProbeErrorOf,
  type TransitProbeSettingKey, type TransitProbeSettings, type TransitProbeSettingsView,
} from '@/api/transitProbe'
import { InfoTip } from '@/components/common/HelpTip'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushToast } from '@/components/feedback/toasts'
import { Field, type FieldControlProps } from '@/components/form/Field'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { useFieldErrors, type FieldRules } from '@/components/form/useFieldErrors'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { UNSAFE_DataRouterContext } from 'react-router'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { discardChangesConfirm } from './accessControl/confirmCopy'

// The link-probe section of the settings page. It has its own load and Save
// against /admin/transit/probe-settings; the page's system-settings form does
// not carry these keys. Empty (or 0) in a number box means "use the default" and
// an empty URL means transport-only; neither is ever replaced by the default on
// the way to the server.

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
  const { t } = useTranslation('admin')
  const [view, setView] = useState<TransitProbeSettingsView | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [attempt, setAttempt] = useState(0)
  const [saving, setSaving] = useState(false)

  // Each box says what is wrong with it when it is left, in words that tell how to fix it.
  const rules: FieldRules<Draft> = {
    transit_probe_url: d => (urlValid(d.transit_probe_url) ? null : t('settings.probe.err_url')),
    ...Object.fromEntries(NUM_FIELDS.map(f => [f.key, (d: Draft) => (numberOf(d[f.key], f.min, f.max).ok ? null : t('settings.probe.err_range', { min: f.min, max: f.max }))])),
  }
  const fe = useFieldErrors<Draft>(rules)

  const resetErrors = fe.reset
  const adopt = useCallback((v: TransitProbeSettingsView) => {
    setView(v)
    setDraft(toDraft(v.settings))
    resetErrors()
  }, [resetErrors])

  useEffect(() => {
    let live = true
    setLoadError(null)
    getTransitProbeSettings().then(v => { if (live) adopt(v) }).catch(err => { if (live) setLoadError(err ?? true) })
    return () => { live = false }
  }, [adopt, attempt])

  const dirtyKeys = useMemo(() => {
    if (!view || !draft) return []
    const next = toSettings(draft)
    return (Object.keys(next) as TransitProbeSettingKey[]).filter(k => next[k] !== view.settings[k])
  }, [view, draft])
  const dirty = dirtyKeys.length > 0

  // Probe settings are one section of the settings page: leaving to another route would lose the unsaved boxes.
  // The guard needs a data router (the app's); this section is also mounted by pages and tests under a plain one.
  const dataRouter = useContext(UNSAFE_DataRouterContext) !== null

  if (loadError) {
    return (
      <Card data-testid="link-probe-settings" className="mt-5 max-w-3xl">
        <CardContent>
          <ErrorState compact title={t('settings.probe.load_failed')} reason={loadError === true ? undefined : loadError}
            retry={() => { setLoadError(null); setAttempt(n => n + 1) }} />
        </CardContent>
      </Card>
    )
  }
  if (!view || !draft) {
    return <Card className="mt-5 max-w-3xl"><CardContent><CardSkeleton lines={4} /></CardContent></Card>
  }

  const patch = <K extends keyof Draft>(key: K, value: Draft[K]) => {
    const next = { ...draft, [key]: value }
    setDraft(next)
    fe.change(key, next)
  }

  const defaultText = (key: TransitProbeSettingKey) => {
    const v = view.defaults[key] ?? view.effective[key]
    return v === undefined ? '' : String(v)
  }

  const save = async () => {
    // The SaveBar is off while a box shows a problem, but a box never left has shown nothing yet: check them all.
    if (!fe.validateAll(draft)) return
    const next = toSettings(draft)
    const changed: Partial<TransitProbeSettings> = {}
    for (const k of dirtyKeys) (changed as Record<string, unknown>)[k] = next[k]
    setSaving(true)
    try {
      adopt(await putTransitProbeSettings(changed))
      pushToast({ message: t('settings.probe.saved'), severity: 'success' })
    } catch (err) {
      const e = transitProbeErrorOf(err)
      if (e?.status === 400 && e.field && e.field in draft) {
        // The server's refusal sits on the field it names; it does not stop a retry.
        fe.setError(e.field as keyof Draft, e.message || t('settings.probe.save_failed'))
      } else {
        pushToast({ message: e?.message || t('settings.probe.save_failed'), severity: 'error' })
      }
    } finally {
      setSaving(false)
    }
  }

  const numberField = (f: typeof NUM_FIELDS[number]) => (
    <Field key={f.key} label={t(`settings.probe.${f.label}`)} error={fe.errors[f.key]} errorBlocksSave={fe.blocks(f.key)}
      hint={t('settings.probe.default_hint', { value: defaultText(f.key) })}>
      {p => (
        <div className="flex items-center gap-1">
          <Input {...pick(p)} inputMode="numeric" value={draft[f.key]} placeholder={defaultText(f.key)}
            onChange={e => patch(f.key, e.target.value)} onBlur={() => fe.blur(f.key, draft)} />
          <InfoTip text={t(`settings.probe.${f.help}`)} />
        </div>
      )}
    </Field>
  )
  const switchField = (key: BoolKey, label: string, help: string) => (
    <Field orientation="horizontal" label={label} error={fe.errors[key]} errorBlocksSave={fe.blocks(key)}>
      {p => (
        <div className="flex items-center gap-1">
          <Switch id={p.id} aria-describedby={p['aria-describedby']} checked={draft[key]} onCheckedChange={c => patch(key, c)} />
          <InfoTip text={help} />
        </div>
      )}
    </Field>
  )
  const defaultUrl = defaultText('transit_probe_url')

  return (
    <Card data-testid="link-probe-settings" className="mt-5 max-w-3xl">
      <CardHeader>
        <CardTitle>{t('settings.probe.section')}</CardTitle>
        <CardDescription>{t('settings.probe.intro')}</CardDescription>
      </CardHeader>
      <CardContent>
        <FormValidityProvider>
          <form className="flex flex-col gap-5" noValidate onSubmit={e => { e.preventDefault(); void save() }}>
            {switchField('transit_probe_enabled', t('settings.probe.enabled'), t('settings.probe.enabled_help'))}

            <div className="grid gap-4 sm:grid-cols-2">{NUM_FIELDS.slice(0, 2).map(numberField)}</div>

            <Field label={t('settings.probe.url')} error={fe.errors.transit_probe_url} errorBlocksSave={fe.blocks('transit_probe_url')}
              hint={t('settings.probe.url_default_hint', { value: defaultUrl })}>
              {p => (
                <div className="flex items-center gap-1">
                  <Input {...pick(p)} value={draft.transit_probe_url} placeholder={defaultUrl}
                    onChange={e => patch('transit_probe_url', e.target.value)} onBlur={() => fe.blur('transit_probe_url', draft)} />
                  {defaultUrl !== '' && draft.transit_probe_url !== defaultUrl && (
                    <Button type="button" variant="outline" className="shrink-0" onClick={() => patch('transit_probe_url', defaultUrl)}>
                      {t('settings.probe.url_reset')}
                    </Button>
                  )}
                  <InfoTip text={t('settings.probe.url_help')} />
                </div>
              )}
            </Field>

            {switchField('transit_probe_withhold', t('settings.probe.withhold'), t('settings.probe.withhold_help'))}

            <div className="grid gap-4 sm:grid-cols-2">{NUM_FIELDS.slice(2).map(numberField)}</div>

            {dataRouter && <LeaveGuard dirty={dirty} />}
            <SaveBar dirty={dirty} pending={saving} saveLabel={t('settings.probe.save')}
              onSave={() => void save()} onDiscard={() => { setDraft(toDraft(view.settings)); fe.reset() }} />
          </form>
        </FormValidityProvider>
      </CardContent>
    </Card>
  )
}

/** The Field wiring a text box takes. */
function pick(p: FieldControlProps) {
  return { id: p.id, 'aria-describedby': p['aria-describedby'], 'aria-invalid': p['aria-invalid'], 'aria-required': p['aria-required'] }
}

/** Stands in front of leaving the route with unsaved values. A component of its own so it can be left out without a data router. */
function LeaveGuard({ dirty }: { dirty: boolean }) {
  const { t } = useTranslation('admin')
  useLeaveGuard(dirty, discardChangesConfirm(t), (next, current) => next.pathname !== current.pathname)
  return null
}
