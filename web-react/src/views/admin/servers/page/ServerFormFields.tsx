import { useTranslation } from 'react-i18next'
import type { NativeInstallationSelection, NodeUpdateChannel, PanelType, Server, XUIAuthMethod } from '@/api/servers'
import { Notice } from '@/components/common/Notice'
import { Field } from '@/components/form/Field'
import { FormSection } from '@/components/form/FormSection'
import { SecretField } from '@/components/form/SecretField'
import { TagsField } from '@/components/form/TagsField'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { InstallMethodFields } from '../install/InstallMethodFields'
import { tokenRequired, withPanelType, type ServerField, type ServerForm } from '../serverForm'

/** The id each validated field's control carries, so a failed save can send focus to the first problem. */
export const FIELD_IDS: Record<ServerField, string> = { name: 'server-name', url: 'server-url', api_token: 'server-api-token', password: 'server-password' }
export const FIELD_ORDER: ServerField[] = ['name', 'url', 'api_token', 'password']

/**
 * The fields of the server form, for both adding and editing, grouped as the questions come: what kind of server it is,
 * what it is called, how PSP reaches it. A credential is a secret: its box is never prefilled and never echoed, and on
 * an edit EMPTY MEANS "KEEP THE STORED ONE" (typing replaces it), so saving other changes can never wipe a token.
 */
export function ServerFormFields({ editing, form, patch, setForm, busy, error, blur, change, resetErrors, install, onInstallChange }: {
  editing: Server | null
  form: ServerForm
  patch: (p: Partial<ServerForm>) => void
  setForm: (next: (prev: ServerForm) => ServerForm) => void
  busy: boolean
  error: (name: ServerField) => string | undefined
  blur: (name: ServerField) => void
  /** A validated field changed: re-check it once it already shows a problem. */
  change: (name: ServerField, next: ServerForm) => void
  resetErrors: () => void
  /** The install method chosen while adding a Passwall Node; the installation page opens with it. */
  install: NativeInstallationSelection
  onInstallChange: (s: NativeInstallationSelection) => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const native = form.panel_type === 'psp'
  const tokenSet = !!editing?.has_api_token
  const passwordSet = !!editing?.has_password
  const needToken = tokenRequired(form)

  return (
    <div className="flex max-w-3xl flex-col gap-6">
      <FormSection title={t('admin:servers.form.group_type')}>
        <Field label={t('admin:servers.field.panel_type')}>
          <NativeSelect value={form.panel_type} disabled={!!editing || busy}
            onChange={e => { setForm(prev => withPanelType(prev, e.target.value as PanelType)); resetErrors() }}>
            <option value="psp">Passwall Node</option>
            <option value="3xui">3X-UI</option>
            <option value="sui">S-UI</option>
          </NativeSelect>
        </Field>
        {!editing && !native && <Notice tone="info">{t('admin:servers.install_reinstall.new_manual')}</Notice>}
      </FormSection>

      <FormSection title={t('admin:servers.form.group_basics')}>
        <Field label={t('admin:servers.field.name')} required hint={native ? undefined : t('admin:servers.hint.name')} error={error('name')}>
          <Input id={FIELD_IDS.name} autoFocus={!editing} value={form.name} placeholder={t('admin:servers.placeholder.name')}
            onChange={e => { patch({ name: e.target.value }); change('name', { ...form, name: e.target.value }) }} onBlur={() => blur('name')} />
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
          <Field label={t('admin:servers.field.url')} required error={error('url')}>
            <Input id={FIELD_IDS.url} value={form.url} placeholder={t('admin:servers.placeholder.url')} inputMode="url"
              onChange={e => { patch({ url: e.target.value }); change('url', { ...form, url: e.target.value }) }} onBlur={() => blur('url')} />
          </Field>
          <Field label={t('admin:servers.field.auth_method')}>
            <NativeSelect value={form.auth_method} onChange={e => { patch({ auth_method: e.target.value as XUIAuthMethod }); resetErrors() }}>
              <option value="token">{t('admin:servers.auth_method.token')}</option>
              {form.panel_type === '3xui' && <option value="password">{t('admin:servers.auth_method.password')}</option>}
            </NativeSelect>
          </Field>
          {needToken ? (
            <Field label={t('admin:servers.field.api_token')} required={!tokenSet} hint={tokenSet ? t('admin:servers.hint.configured') : undefined} error={error('api_token')}>
              <SecretField id={FIELD_IDS.api_token} value={form.api_token} isSet={tokenSet} placeholder={t('admin:servers.placeholder.api_token')}
                onChange={v => { patch({ api_token: v }); change('api_token', { ...form, api_token: v }) }} onBlur={() => blur('api_token')} />
            </Field>
          ) : (
            <>
              <Field label={t('admin:servers.field.username')}>
                <Input value={form.username} placeholder={t('admin:servers.placeholder.username')} autoComplete="off" onChange={e => patch({ username: e.target.value })} />
              </Field>
              <Field label={t('admin:servers.field.password')} required={!passwordSet} hint={passwordSet ? t('admin:servers.hint.configured') : undefined} error={error('password')}>
                <SecretField id={FIELD_IDS.password} value={form.password} isSet={passwordSet} placeholder={t('admin:servers.placeholder.password')}
                  onChange={v => { patch({ password: v }); change('password', { ...form, password: v }) }} onBlur={() => blur('password')} />
              </Field>
            </>
          )}
          <Field orientation="horizontal" label={<span className="text-destructive">{t('admin:servers.field.insecure_https')}</span>} hint={t('admin:servers.hint.insecure_https')}>
            <Switch checked={form.insecure_https} onCheckedChange={checked => patch({ insecure_https: checked })} />
          </Field>
        </FormSection>
      )}
    </div>
  )
}
