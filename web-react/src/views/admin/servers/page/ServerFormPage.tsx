import { useMemo, useRef, useState, type ReactNode } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'
import { createServer, updateServer, type NativeInstallationSelection, type Server } from '@/api/servers'
import { AsyncButton } from '@/components/common/AsyncButton'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useObjectLeaveGuard } from '@/components/common/ObjectTabs'
import { useBackTarget } from '@/components/common/objectReturn'
import { Badge } from '@/components/ui/badge'
import { pushSnack } from '@/components/feedback/toasts'
import { FormValidityProvider, useFormValidity } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { useFieldErrors } from '@/components/form/useFieldErrors'
import { IconDelete, IconDownload } from '@/lib/icons'
import { useCan } from '@/utils/permissions'
import { discardChangesConfirm } from '../../accessControl/confirmCopy'
import { ServerDeleteDialog } from '../../nodes/delete/ServerDeleteDialog'
import { leaveInstallation } from '../install/installHandoff'
import { DEFAULT_INSTALLATION } from '../install/installModel'
import { EMPTY_FORM, createRequest, formOf, sameForm, updateRequest, validateServerForm, type ServerField, type ServerForm } from '../serverForm'
import { SERVERS_PATH, serverInstallPath, serverPath } from '../serverRoutes'
import { typeLabel } from '../serverModel'
import { useServerCache } from '../useServerRecord'
import { useServerProbes, type ProbeState } from '../useServerProbes'
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

/** The last connection test, in words: reached (with how many inbounds), not reached (with the reason), or not tested yet. */
function TestResult({ probe }: { probe?: ProbeState }) {
  const { t } = useTranslation('admin')
  if (!probe || probe.status === 'unknown') return <span className="text-sm text-muted-foreground">{t('admin:servers.status.unknown')}</span>
  if (probe.status === 'checking') return <span role="status" className="text-sm text-muted-foreground">{t('admin:servers.status.checking')}</span>
  if (probe.status === 'ok') return <span role="status" className="text-sm">{typeof probe.inbound_count === 'number' ? t('admin:servers.status.ok_count', { count: probe.inbound_count }) : t('admin:servers.status.ok')}</span>
  if (probe.status === 'unconfigured') return <span className="text-sm text-muted-foreground">{t('admin:servers.status.unconfigured')}</span>
  return <span role="alert" className="text-sm text-destructive">{t('admin:servers.status.fail')}{probe.error ? `: ${probe.error}` : ''}</span>
}

const sameInstall = (a: NativeInstallationSelection) => JSON.stringify(a) === JSON.stringify(DEFAULT_INSTALLATION)

/**
 * Add a server (`editing` null) or edit one: one form for both, on a page of its own. The draft starts as the stored
 * record and follows it until the first edit (a background re-read is not an edit); Save appears once the draft
 * differs, and leaving the page with it asks. Creating a Passwall Node continues into its installation page; any other
 * type lands on the new server's page. The connection test, the installation and the delete are the header's.
 */
export function ServerFormPage({ editing }: { editing: Server | null }) {
  const { t } = useTranslation(['admin', 'common'])
  const navigate = useNavigate()
  const location = useLocation()
  const back = useBackTarget(SERVERS_PATH)
  const admin = useCan('config.write')
  const cache = useServerCache()

  // The record as last saved here. It yields to a newer one from the list (a re-read, a probe's version data) as soon as
  // one arrives, so what the header shows is never older than what the page knows.
  const [seen, setSeen] = useState(editing)
  const [saved, setSaved] = useState<Server | null>(null)
  if (seen !== editing) { setSeen(editing); setSaved(null) }
  const current = saved ?? editing

  const initial = useMemo<ServerForm>(() => (current ? formOf(current) : EMPTY_FORM), [current])
  const [edit, setEdit] = useState<ServerForm | null>(null)
  const form = edit ?? initial
  const [install, setInstall] = useState<NativeInstallationSelection>(DEFAULT_INSTALLATION)
  const [busy, setBusy] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const dirty = (edit !== null && !sameForm(edit, initial)) || (!current && !sameInstall(install))
  // The create or the delete just finished: the draft is moot. A ref set before navigating, because `dirty` clears a render later.
  const leaving = useRef(false)
  useObjectLeaveGuard({ dirty, copy: discardChangesConfirm(t as (k: string) => string), bypass: leaving })

  const patch = (p: Partial<ServerForm>) => setEdit(prev => ({ ...(prev ?? initial), ...p }))
  const setForm = (next: (prev: ServerForm) => ServerForm) => setEdit(prev => next(prev ?? initial))

  // The rules are the pure ones in serverForm.ts; a message is the translated text, shown under its field.
  const say = (key: string | undefined) => (key ? t(`admin:${key}`) : null)
  const rules = (name: ServerField) => (d: ServerForm) => say(validateServerForm(d, current)[name])
  const fe = useFieldErrors<ServerForm>({
    name: rules('name'), url: rules('url'), api_token: rules('api_token'), password: rules('password'),
  })

  // The probe of this one server, on demand only: opening the page never reaches the upstream server.
  const probes = useServerProbes([], cache.patch)

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

  if (!current && !admin) {
    return <ObjectPage back={{ to: back, label: t('admin:servers.title') }} state={{ kind: 'forbidden', title: t('admin:servers.detail.create_forbidden') }} />
  }

  const native = form.panel_type === 'psp'
  const probe = current ? probes.stateFor(current) : undefined
  const fields = (
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

  if (!current) {
    return <ObjectPage back={{ to: back, label: t('admin:servers.title') }} title={t('admin:servers.create')}>{fields}</ObjectPage>
  }
  return (
    <ObjectPage
      back={{ to: back, label: t('admin:servers.title') }}
      title={current.name}
      badges={<Badge variant="outline">{typeLabel(current)}</Badge>}
      subtitle={current.panel_type === 'psp' ? undefined : current.url}
      status={(
        <>
          <TestResult probe={probe} />
          <span className="text-xs text-muted-foreground">{t('admin:servers.form.test_hint')}</span>
        </>
      )}
      secondary={(
        <AsyncButton variant="outline" disabled={busy || probe?.status === 'checking'} onClick={() => probes.runTest(current)}>{t('admin:servers.action.test')}</AsyncButton>
      )}
      more={[
        // The installation is the administrator's, as in the list's menu.
        ...(admin && current.panel_type === 'psp'
          ? [{ id: 'install', label: t('admin:servers.install_reinstall.action'), icon: IconDownload, onSelect: () => void navigate(serverInstallPath(current.id), { state: location.state }) }]
          : []),
        { id: 'delete', label: t('admin:servers.action.delete'), icon: IconDelete, destructive: true, onSelect: () => setDeleting(true) },
      ]}
    >
      {fields}
      {deleting && (
        <ServerDeleteDialog server={current} onClose={() => setDeleting(false)}
          onDeleted={() => {
            leaving.current = true
            setDeleting(false)
            pushSnack(t('admin:servers.toast.deleted'), 'success')
            cache.dropProbe(current.id)
            void cache.invalidate()
            void navigate(back, { replace: true })
          }} />
      )}
    </ObjectPage>
  )
}
