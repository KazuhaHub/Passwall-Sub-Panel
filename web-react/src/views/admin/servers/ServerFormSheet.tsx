import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { createServer, updateServer, type NativeInstallationSelection, type NativeServerProvisioning, type NodeUpdateChannel, type PanelType, type Server, type XUIAuthMethod } from '@/api/servers'
import { AsyncButton } from '@/components/common/AsyncButton'
import { DetailSheet } from '@/components/data/DetailSheet'
import { Field } from '@/components/form/Field'
import { FormSection } from '@/components/form/FormSection'
import { SecretField } from '@/components/form/SecretField'
import { TagsField } from '@/components/form/TagsField'
import { useFieldErrors } from '@/components/form/useFieldErrors'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { DialogNotice } from './dialogs/DialogNotice'
import { InstallMethodFields } from './install/InstallMethodFields'
import { EMPTY_FORM, createRequest, formOf, sameForm, tokenRequired, updateRequest, validateServerForm, withPanelType, type ServerField, type ServerForm } from './serverForm'
import type { ProbeState } from './useServerProbes'

const IDS: Record<ServerField, string> = { name: 'server-name', url: 'server-url', api_token: 'server-api-token', password: 'server-password' }
const ORDER: ServerField[] = ['name', 'url', 'api_token', 'password']

/**
 * Add or edit a server: one form for both, in a wide sheet. Grouped as the questions come: what kind of server it is,
 * what it is called, how PSP reaches it. A credential is a secret: its box is never prefilled and never echoed, and on
 * an edit EMPTY MEANS "KEEP THE STORED ONE" (typing replaces it), so saving other changes can never wipe a token.
 * Creating a Passwall Node continues into its installation (the caller opens it) instead of closing.
 */
export function ServerFormSheet({ editing, install, onInstallChange, probe, onTest, onClose, onUpdated, onCreated }: {
  editing: Server | null
  /** The install method chosen while adding a Passwall Node; the installation flow opens with it. */
  install: NativeInstallationSelection
  onInstallChange: (s: NativeInstallationSelection) => void
  probe?: ProbeState
  onTest?: () => Promise<void>
  onClose: () => void
  onUpdated: (saved: Server) => void
  /** A created server; for a Passwall Node also the provisioning (identity and credential) to install with. */
  onCreated: (server: Server | null, provisioning: NativeServerProvisioning | null) => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const initial = useMemo<ServerForm>(() => (editing ? formOf(editing) : EMPTY_FORM), [editing])
  const [form, setForm] = useState<ServerForm>(initial)
  const [busy, setBusy] = useState(false)
  const dirty = !sameForm(form, initial) || (!editing && JSON.stringify(install) !== JSON.stringify({ method: 'linux', os: 'linux', arch: 'amd64', dockerRemoteUpgrade: true }))
  const guard = useDirtyClose(dirty, discardChangesConfirm(t as (k: string) => string))
  const native = form.panel_type === 'psp'
  const patch = (p: Partial<ServerForm>) => setForm(prev => ({ ...prev, ...p }))

  // The rules are the pure ones in serverForm.ts; a message is the translated text, shown under its field.
  const say = (key: string | undefined) => (key ? t(`admin:${key}`) : null)
  const rules = (name: ServerField) => (d: ServerForm) => say(validateServerForm(d, editing)[name])
  const fe = useFieldErrors<ServerForm>({
    name: rules('name'), url: rules('url'), api_token: rules('api_token'), password: rules('password'),
  })
  const err = (name: ServerField) => fe.errors[name] ?? undefined
  const blur = (name: ServerField) => fe.blur(name, form)

  async function submit() {
    if (!fe.validateAll(form)) {
      const first = ORDER.find(n => validateServerForm(form, editing)[n])
      if (first) setTimeout(() => document.getElementById(IDS[first])?.focus(), 0)
      return
    }
    setBusy(true)
    try {
      if (editing) {
        const saved = await updateServer(editing.id, updateRequest(editing, form))
        onUpdated(saved)
        pushSnack(t('admin:servers.toast.saved'), 'success')
        onClose()
      } else {
        const created = await createServer(createRequest(form))
        pushSnack(t('admin:servers.toast.created'), 'success')
        if ('server' in created) onCreated(created.server, created)
        else { onCreated(null, null); onClose() }
      }
    } catch { /* The request layer already surfaced the failure; the draft stays so it can be retried. */ } finally {
      setBusy(false)
    }
  }

  const tokenSet = !!editing?.has_api_token
  const passwordSet = !!editing?.has_password
  const needToken = tokenRequired(form)

  return (
    <DetailSheet
      open
      onClose={() => guard(onClose)}
      title={editing ? t('admin:servers.edit_title', { name: editing.name }) : t('admin:servers.create')}
      className="data-[side=right]:sm:w-[min(40rem,96vw)] data-[side=right]:sm:max-w-[min(40rem,96vw)]"
      footer={(
        <>
          <Button variant="ghost" disabled={busy} onClick={() => guard(onClose)}>{t('common:actions.cancel')}</Button>
          <AsyncButton pending={busy} onClick={() => submit()}>
            {t(!editing && native ? 'admin:servers.native.create_continue' : 'common:actions.ok')}
          </AsyncButton>
        </>
      )}
    >
      <form noValidate onSubmit={e => { e.preventDefault(); void submit() }} className="flex flex-col gap-6">
        <FormSection title={t('admin:servers.form.group_type')}>
          <Field label={t('admin:servers.field.panel_type')}>
            <NativeSelect value={form.panel_type} disabled={!!editing || busy}
              onChange={e => { setForm(prev => withPanelType(prev, e.target.value as PanelType)); fe.reset() }}>
              <option value="psp">Passwall Node</option>
              <option value="3xui">3X-UI</option>
              <option value="sui">S-UI</option>
            </NativeSelect>
          </Field>
          {!editing && !native && <DialogNotice>{t('admin:servers.install_reinstall.new_manual')}</DialogNotice>}
        </FormSection>

        <FormSection title={t('admin:servers.form.group_basics')}>
          <Field label={t('admin:servers.field.name')} required hint={native ? undefined : t('admin:servers.hint.name')} error={err('name')}>
            <Input id={IDS.name} autoFocus={!editing} value={form.name} placeholder={t('admin:servers.placeholder.name')}
              onChange={e => { patch({ name: e.target.value }); fe.change('name', { ...form, name: e.target.value }) }} onBlur={() => blur('name')} />
          </Field>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-[10rem_minmax(0,1fr)]">
            <Field label={t('admin:servers.field.region')}>
              <Input maxLength={32} value={form.region} onChange={e => patch({ region: e.target.value })} />
            </Field>
            <Field label={t('admin:servers.field.tags')}>
              {p => <TagsField id={p.id} aria-labelledby={p.labelId} value={form.tags} onChange={tags => patch({ tags })} />}
            </Field>
          </div>
          <Field label={t('admin:servers.field.remark')}>
            <Input value={form.remark} onChange={e => patch({ remark: e.target.value })} />
          </Field>
        </FormSection>

        {native ? (
          <FormSection title={t('admin:servers.form.group_node')}>
            <Field label={t('admin:servers.field.update_channel')} hint={editing ? t('admin:servers.hint.update_channel_short') : t('admin:servers.hint.update_channel')}>
              <NativeSelect value={form.update_channel} disabled={busy} onChange={e => patch({ update_channel: e.target.value as NodeUpdateChannel })}>
                <option value="stable">{t('admin:servers.native.release_stable')}</option>
                <option value="rc">{t('admin:servers.native.release_rc')}</option>
                <option value="beta">{t('admin:servers.native.release_beta')}</option>
              </NativeSelect>
            </Field>
            {!editing && <InstallMethodFields selection={install} onChange={onInstallChange} disabled={busy} />}
            {!editing && <p className="text-sm text-muted-foreground">{t('admin:servers.native.create_record_hint')}</p>}
            <p className="text-sm text-muted-foreground">{t('admin:servers.native.outbound_hint')}</p>
          </FormSection>
        ) : (
          <FormSection title={t('admin:servers.form.group_connection')}>
            <Field label={t('admin:servers.field.url')} required error={err('url')}>
              <Input id={IDS.url} value={form.url} placeholder={t('admin:servers.placeholder.url')} inputMode="url"
                onChange={e => { patch({ url: e.target.value }); fe.change('url', { ...form, url: e.target.value }) }} onBlur={() => blur('url')} />
            </Field>
            <Field label={t('admin:servers.field.auth_method')}>
              <NativeSelect value={form.auth_method} onChange={e => { patch({ auth_method: e.target.value as XUIAuthMethod }); fe.reset() }}>
                <option value="token">{t('admin:servers.auth_method.token')}</option>
                {form.panel_type === '3xui' && <option value="password">{t('admin:servers.auth_method.password')}</option>}
              </NativeSelect>
            </Field>
            {needToken ? (
              <Field label={t('admin:servers.field.api_token')} required={!tokenSet} hint={tokenSet ? t('admin:servers.hint.configured') : undefined} error={err('api_token')}>
                <SecretField id={IDS.api_token} value={form.api_token} isSet={tokenSet} placeholder={t('admin:servers.placeholder.api_token')}
                  onChange={v => { patch({ api_token: v }); fe.change('api_token', { ...form, api_token: v }) }} onBlur={() => blur('api_token')} />
              </Field>
            ) : (
              <>
                <Field label={t('admin:servers.field.username')}>
                  <Input value={form.username} placeholder={t('admin:servers.placeholder.username')} autoComplete="off" onChange={e => patch({ username: e.target.value })} />
                </Field>
                <Field label={t('admin:servers.field.password')} required={!passwordSet} hint={passwordSet ? t('admin:servers.hint.configured') : undefined} error={err('password')}>
                  <SecretField id={IDS.password} value={form.password} isSet={passwordSet} placeholder={t('admin:servers.placeholder.password')}
                    onChange={v => { patch({ password: v }); fe.change('password', { ...form, password: v }) }} onBlur={() => blur('password')} />
                </Field>
              </>
            )}
            <Field orientation="horizontal" label={<span className="text-destructive">{t('admin:servers.field.insecure_https')}</span>} hint={t('admin:servers.hint.insecure_https')}>
              <Switch checked={form.insecure_https} onCheckedChange={checked => patch({ insecure_https: checked })} />
            </Field>
          </FormSection>
        )}

        {editing && onTest && (
          <FormSection title={t('admin:servers.form.group_test')}>
            <div className="flex flex-wrap items-center gap-3">
              <AsyncButton variant="outline" disabled={busy || probe?.status === 'checking'} onClick={() => onTest()}>{t('admin:servers.action.test')}</AsyncButton>
              <TestResult probe={probe} />
            </div>
            <p className="text-xs text-muted-foreground">{t('admin:servers.form.test_hint')}</p>
          </FormSection>
        )}
      </form>
    </DetailSheet>
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
