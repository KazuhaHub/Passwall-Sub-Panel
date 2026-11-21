// SubClientsView — the standalone Sub clients page (v3.8.0 §6.1). Hosts the global client-detection registry
// (sub_clients) + the unknown-client filter mode (sub_client_filter_mode); both are global per the plan's §3b
// (consumed before user identity is known, never per-group). Loads and saves the full UISettings via the same
// endpoint SettingsView uses — only the two registry fields are edited here, the rest round-trip unchanged.
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { putUISettings, type UISettings } from '@/api/settings'
import { useUISettings } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { IconAdd, IconReset } from '@/lib/icons'
import { HelpTip } from '@/components/common/HelpTip'
import { PageHeader } from '@/components/common/PageHeader'
import { ErrorState } from '@/components/feedback/ErrorState'
import { PageSkeleton } from '@/components/feedback/LoadingState'
import { pushToast } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { discardChangesConfirm } from './accessControl/confirmCopy'
import ClientRegistry from './subclients/clientRegistry'
import { normalizeRegistry } from './subclients/presets'
import { PresetMenu, useRegistryActions } from './subclients/useRegistryActions'

const normalize = (s: UISettings): UISettings => ({
  ...s,
  sub_clients: normalizeRegistry(s.sub_clients),
  sub_client_filter_mode: s.sub_client_filter_mode ?? 'blacklist',
})

// The two edited fields; everything else in the draft is carried through untouched.
const edited = (s: UISettings) => JSON.stringify([s.sub_clients, s.sub_client_filter_mode])

export default function SubClientsView() {
  const { t } = useTranslation(['admin', 'nav', 'common'])
  const scope = useQueryScope()
  const settingsQuery = useUISettings(scope)
  // The DRAFT, and the last settings the server confirmed (what Discard returns to). Both are seeded once from
  // the first successful read; a background revalidation must never overwrite edits the admin has not saved.
  const [base, setBase] = useState<UISettings | null>(null)
  const [settings, setSettings] = useState<UISettings | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (base !== null || !settingsQuery.data) return
    const first = normalize(settingsQuery.data)
    setBase(first)
    setSettings(first)
  }, [settingsQuery.data, base])

  const dirty = useMemo(() => base !== null && settings !== null && edited(base) !== edited(settings), [base, settings])
  useLeaveGuard(dirty, discardChangesConfirm(t), (next, current) => next.pathname !== current.pathname)

  const families = settings?.sub_clients ?? []
  const setFamilies = (next: typeof families) => setSettings(prev => (prev ? { ...prev, sub_clients: next } : prev))
  const actions = useRegistryActions(families, setFamilies)

  async function save() {
    if (!settings) return
    setSaving(true)
    try {
      // The full settings object, as the page always sent it, with the two edited fields as they now stand.
      const saved = normalize(await putUISettings({ ...settings, sub_clients: normalizeRegistry(settings.sub_clients) }))
      setBase(saved)
      setSettings(saved)
      pushToast({ message: t('admin:settings.saved'), severity: 'success' })
    } catch (err) {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error || String(err)
      pushToast({ message: msg, severity: 'warning' })
    } finally { setSaving(false) }
  }

  const header = (
    <PageHeader
      title={t('nav:admin.sub_clients')}
      primary={settings && <Button onClick={actions.addFamily}><IconAdd />{t('admin:settings.subscription.add_family')}</Button>}
      secondary={settings && <PresetMenu families={families} onPick={actions.addPreset} />}
      more={settings ? [{ id: 'reset', label: t('admin:settings.subscription.reset_to_presets'), icon: IconReset, destructive: true, onSelect: () => void actions.reset() }] : undefined}
    />
  )

  // A failed first read leaves nothing to edit; say so and offer a retry rather than spinning forever.
  if (!settings) {
    return (
      <div className="p-4 sm:p-6">
        {header}
        {settingsQuery.isError
          ? <ErrorState title={t('admin:settings.load_failed')} reason={settingsQuery.error} retry={() => settingsQuery.refetch()} />
          : <PageSkeleton />}
      </div>
    )
  }

  return (
    <div className="p-4 sm:p-6">
      <FormValidityProvider>
        {header}
        <div className="flex flex-col gap-5">
          <section className="rounded-xl border bg-card p-4">
            <div className="mb-3 flex items-center justify-between gap-2">
              <h2 className="text-sm font-medium">{t('admin:settings.subscription.section_detection')}</h2>
              <HelpTip text={t('admin:settings.subscription.filter_mode_help')} />
            </div>
            <Field label={t('admin:settings.subscription.filter_mode')} className="max-w-md">
              <NativeSelect value={settings.sub_client_filter_mode}
                onChange={e => setSettings({ ...settings, sub_client_filter_mode: e.target.value as 'blacklist' | 'whitelist' })}>
                <option value="blacklist">{t('admin:settings.subscription.filter_mode_blacklist')}</option>
                <option value="whitelist">{t('admin:settings.subscription.filter_mode_whitelist')}</option>
              </NativeSelect>
            </Field>
          </section>

          <section className="flex flex-col gap-3">
            <div>
              <h2 className="text-sm font-medium">{t('admin:settings.subscription.section_clients')}</h2>
              {/* Always on: the order is what the server matches in, so it is stated where it can be changed. */}
              <p className="mt-1 max-w-3xl text-sm text-foreground">{t('admin:settings.subscription.order_note')}</p>
              <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{t('admin:settings.subscription.registry_hint')}</p>
            </div>
            <ClientRegistry families={families} onChange={setFamilies} actions={actions} />
          </section>
        </div>
        <SaveBar dirty={dirty} pending={saving} onSave={() => void save()} onDiscard={() => { if (base) setSettings(base) }} />
      </FormValidityProvider>
    </div>
  )
}
