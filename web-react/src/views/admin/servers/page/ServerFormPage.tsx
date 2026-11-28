import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'
import { createServer, updateServer, type NativeInstallationSelection, type Server } from '@/api/servers'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useObjectLeaveGuard } from '@/components/common/ObjectTabs'
import { useBackTarget } from '@/components/common/objectReturn'
import { pushSnack } from '@/components/feedback/toasts'
import { FormValidityProvider, useFormValidity } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { useFieldErrors } from '@/components/form/useFieldErrors'
import { useCan } from '@/utils/permissions'
import { discardChangesConfirm } from '../../accessControl/confirmCopy'
import { leaveInstallation } from '../install/installHandoff'
import { DEFAULT_INSTALLATION } from '../install/installModel'
import { EMPTY_FORM, createRequest, formOf, sameForm, updateRequest, validateServerForm, type ServerField, type ServerForm } from '../serverForm'
import { SERVERS_PATH, serverInstallPath, serverPath } from '../serverRoutes'
import { useServerCache } from '../useServerRecord'
import { FIELD_IDS, FIELD_ORDER, ServerFormFields } from './ServerFormFields'

/**
 * The form element of a SaveBar form. Enter submits it, and the SaveBar's button is blocked while a field shows a
 * problem, so submit must respect the same blocked state or Enter would save what the button refuses.
 */
function GuardedForm({ onSubmit, children }: { onSubmit: () => void; children: ReactNode }) {
  const { blocked } = useFormValidity()
  return (
    <form noValidate onSubmit={e => { e.preventDefault(); if (!blocked) onSubmit() }}>
      {children}
    </form>
  )
}

const sameInstall = (a: NativeInstallationSelection) => JSON.stringify(a) === JSON.stringify(DEFAULT_INSTALLATION)

/**
 * The form of a server, to add one (`editing` null) or to edit one, without a page around it: the draft, its rules, the
 * save and the guard on leaving with unsaved edits. The draft starts as the stored record and follows it until the first
 * edit (a background re-read is not an edit); Save appears once the draft differs. Creating a Passwall Node continues
 * into its installation page; any other type lands on the new server's page. `leaving` is the owner's flag for an exit
 * that makes the draft moot (a delete done from the page); this sets it too when a create finishes.
 */
export function ServerSettingsForm({ editing, leaving, onDirtyChange }: {
  editing: Server | null
  leaving: { current: boolean }
  onDirtyChange?: (dirty: boolean) => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const navigate = useNavigate()
  const location = useLocation()
  const cache = useServerCache()

  // The record as last saved here. It yields to a newer one from the list (a re-read, a probe's version data) as soon as
  // one arrives, so what the form starts from is never older than what the page knows.
  const [seen, setSeen] = useState(editing)
  const [saved, setSaved] = useState<Server | null>(null)
  if (seen !== editing) { setSeen(editing); setSaved(null) }
  const current = saved ?? editing

  const initial = useMemo<ServerForm>(() => (current ? formOf(current) : EMPTY_FORM), [current])
  const [edit, setEdit] = useState<ServerForm | null>(null)
  const form = edit ?? initial
  const [install, setInstall] = useState<NativeInstallationSelection>(DEFAULT_INSTALLATION)
  const [busy, setBusy] = useState(false)
  const dirty = (edit !== null && !sameForm(edit, initial)) || (!current && !sameInstall(install))
  useEffect(() => { onDirtyChange?.(dirty) }, [dirty, onDirtyChange])
  useObjectLeaveGuard({ dirty, copy: discardChangesConfirm(t as (k: string) => string), bypass: leaving })

  const patch = (p: Partial<ServerForm>) => setEdit(prev => ({ ...(prev ?? initial), ...p }))
  const setForm = (next: (prev: ServerForm) => ServerForm) => setEdit(prev => next(prev ?? initial))

  // The rules are the pure ones in serverForm.ts; a message is the translated text, shown under its field.
  const say = (key: string | undefined) => (key ? t(`admin:${key}`) : null)
  const rules = (name: ServerField) => (d: ServerForm) => say(validateServerForm(d, current)[name])
  const fe = useFieldErrors<ServerForm>({
    name: rules('name'), url: rules('url'), api_token: rules('api_token'), password: rules('password'),
  })

  async function submit() {
    if (busy) return
    if (!fe.validateAll(form)) {
      const first = FIELD_ORDER.find(n => validateServerForm(form, current)[n])
      if (first) setTimeout(() => document.getElementById(FIELD_IDS[first])?.focus(), 0)
      return
    }
    setBusy(true)
    try {
      if (current) {
        const next = await updateServer(current.id, updateRequest(current, form))
        cache.patch(prev => prev.map(s => (s.id === next.id ? next : s)))
        cache.dropProbe(next.id)
        pushSnack(t('admin:servers.toast.saved'), 'success')
        setSaved(next)
        setEdit(null)
        fe.reset()
      } else {
        const created = await createServer(createRequest(form))
        pushSnack(t('admin:servers.toast.created'), 'success')
        void cache.invalidate()
        leaving.current = true
        // The new server's page opens in the place of this one, and its back link still goes to the list as it was.
        if ('server' in created) {
          leaveInstallation({ server: created.server, provisioning: created, selection: install, creating: true })
          void navigate(serverInstallPath(created.server.id), { replace: true, state: location.state })
        } else void navigate(serverPath(created.id), { replace: true, state: location.state })
      }
    } catch { /* The request layer already surfaced the failure; the draft stays so it can be retried. */ } finally {
      setBusy(false)
    }
  }
  function discard() { setEdit(null); setInstall(DEFAULT_INSTALLATION); fe.reset() }

  const native = form.panel_type === 'psp'
  return (
    <FormValidityProvider>
      <GuardedForm onSubmit={() => void submit()}>
        <ServerFormFields editing={current} form={form} patch={patch} setForm={setForm} busy={busy}
          error={n => fe.errors[n] ?? undefined} blur={n => fe.blur(n, form)} change={(n, next) => fe.change(n, next)} resetErrors={fe.reset}
          install={install} onInstallChange={setInstall} />
        <SaveBar dirty={dirty} pending={busy} onSave={() => void submit()} onDiscard={discard}
          saveLabel={current ? undefined : t(native ? 'admin:servers.native.create_continue' : 'admin:servers.create')} />
      </GuardedForm>
    </FormValidityProvider>
  )
}

/**
 * Adding a server: the form on a page of its own. An existing server is `ServerDetailPage`, which holds this same form
 * as its Settings tab.
 */
export function ServerFormPage() {
  const { t } = useTranslation(['admin', 'common'])
  const back = useBackTarget(SERVERS_PATH)
  const admin = useCan('config.write')
  // The create just finished: the draft is moot. A ref set before navigating, because `dirty` clears a render later.
  const leaving = useRef(false)
  if (!admin) {
    return <ObjectPage back={{ to: back, label: t('admin:servers.title') }} state={{ kind: 'forbidden', title: t('admin:servers.detail.create_forbidden') }} />
  }
  return (
    <ObjectPage back={{ to: back, label: t('admin:servers.title') }} title={t('admin:servers.create')}>
      <ServerSettingsForm editing={null} leaving={leaving} />
    </ObjectPage>
  )
}
