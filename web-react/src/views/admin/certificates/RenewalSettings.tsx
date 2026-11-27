import { useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { putUISettings } from '@/api/settings'
import { Field } from '@/components/form/Field'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { NumberField } from '@/components/form/NumberField'
import { SaveBar } from '@/components/form/SaveBar'
import { pushSnack } from '@/components/feedback/toasts'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { settingsKeys } from '@/query/keys'
import { useCertsT, useCommonT } from './shared'
import type { CertData } from './useCertData'

interface Draft { before: number | null; hours: number | null }

/**
 * The auto-renew threshold and how often the panel checks, for every certificate. They are part of the one
 * panel-wide settings record, which is written back whole (as every settings editor does), with only these
 * two numbers changed.
 */
export function RenewalSettings({ data }: { data: CertData }) {
  const t = useCertsT()
  const c = useCommonT()
  const queryClient = useQueryClient()
  const settings = data.settings.data
  const saved = useMemo<Draft | null>(() => settings ? { before: settings.cert_renew_before_days, hours: settings.cert_renew_check_interval_hours } : null, [settings])
  const [draft, setDraft] = useState<Draft | null>(saved)
  const [pending, setPending] = useState(false)
  // The first read fills the form; later reads (a refocus) must not wipe what is being typed.
  useEffect(() => { setDraft(d => d ?? saved) }, [saved])
  const dirty = !!draft && !!saved && (draft.before !== saved.before || draft.hours !== saved.hours)
  useLeaveGuard(dirty, { title: c('ui.form.unsaved'), message: c('ui.form.unsaved_leave'), confirmText: c('ui.form.discard'), destructive: true, initialFocus: 'cancel' },
    (next, cur) => next.pathname !== cur.pathname || new URLSearchParams(next.search).get('tab') !== new URLSearchParams(cur.search).get('tab'))

  async function save() {
    if (!settings || !draft || draft.before === null || draft.hours === null) return
    setPending(true)
    try {
      const updated = await putUISettings({ ...settings, cert_renew_before_days: draft.before, cert_renew_check_interval_hours: draft.hours })
      queryClient.setQueryData(settingsKeys.ui(data.scope), updated)
      setDraft({ before: updated.cert_renew_before_days, hours: updated.cert_renew_check_interval_hours })
      pushSnack(c('saved'), 'success')
    } catch { /* reported by the request layer */ } finally { setPending(false) }
  }

  if (data.settings.isError) return null
  return (
    <FormValidityProvider>
      <section className="mt-8 max-w-3xl" aria-labelledby="cert-renewal-title">
        <h2 id="cert-renewal-title" className="text-base font-semibold">{t('certs.renew_title')}</h2>
        <p className="mb-3 text-sm text-muted-foreground">{t('certs.renew_subtitle')}</p>
        {draft ? (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t('certs.acme_renew_before_days')} hint={t('certs.renew_before_hint')}>
              <NumberField value={draft.before} integer min={1} unit={t('certs.unit_days')} onChange={v => setDraft({ ...draft, before: v })} />
            </Field>
            <Field label={t('certs.acme_renew_check_hours')} hint={t('certs.renew_check_hint')}>
              <NumberField value={draft.hours} integer min={1} unit={t('certs.unit_hours')} onChange={v => setDraft({ ...draft, hours: v })} />
            </Field>
          </div>
        ) : null}
        <SaveBar dirty={dirty} pending={pending} onSave={() => void save()} onDiscard={() => setDraft(saved)} />
      </section>
    </FormValidityProvider>
  )
}
