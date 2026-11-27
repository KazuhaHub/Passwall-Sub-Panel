import { useCallback, useMemo, useState } from 'react'
import { fetchSAMLMetadata, putSAML, type SAMLConfig, type SAMLMetadataSummary } from '@/api/settings'
import { CodeField } from '@/components/CodeField'
import { LazyCodeEditor } from '@/components/LazyCodeEditor'
import { AsyncButton } from '@/components/common/AsyncButton'
import { CopyButton } from '@/components/common/CopyButton'
import { ErrorState } from '@/components/feedback/ErrorState'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { NumberField } from '@/components/form/NumberField'
import { Input } from '@/components/ui/input'
import { useAllGroups } from '@/query/groups'
import { settingsKeys } from '@/query/keys'
import { useSamlConfig } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { validateRequired, validateUrl } from '@/utils/validators'
import { Notice } from '@/components/common/Notice'
import { useAdminT } from '../../useAdminT'
import { Group, Pair, SectionForm, SelectRow, SwitchField, TextRow } from '../parts'
import { NewUserDefaults } from './NewUserDefaults'
import { GroupRulesEditor, RoleRulesEditor } from './RuleEditors'
import { SecretTextarea } from './SecretTextarea'
import { useSsoDraft } from './useSsoDraft'

// A fresh database serialises an empty list as null (Go's nil slice); the editors call array methods unconditionally.
const normalize = (c: SAMLConfig): SAMLConfig => ({ ...c, role_rules: c.role_rules ?? [] })

type Err = 'entity_id' | 'acs_url' | 'cert_pem' | 'metadata_url'

/** SAML 2.0: the IdP, the service-provider values the IdP needs, attribute mapping, role and group rules, and what a new account gets. */
export function SamlPanel({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const query = useSamlConfig(scope)
  const groups = useAllGroups(scope)
  const [keyPem, setKeyPem] = useState('')
  const key = useMemo(() => settingsKeys.saml(scope), [scope])
  const norm = useCallback(normalize, [])
  const { draft: cfg, patch, adopt, dirty, discard, setDraft } = useSsoDraft(query, key, norm, keyPem !== '', onDirtyChange)
  const [errs, setErrs] = useState<Partial<Record<Err, string>>>({})
  const [pending, setPending] = useState(false)
  const [fetching, setFetching] = useState(false)
  const [fetched, setFetched] = useState<SAMLMetadataSummary | null>(null)
  const [fetchError, setFetchError] = useState('')

  // Auto mode hides SP and attribute editing: the server derives the entity id, ACS URL and a self-signed certificate from the
  // subscription base URL on save and resets the mapping to the documented defaults, so editable fields would be overwritten.
  const auto = cfg?.mode === 'auto'

  // Field checks fire only when SAML is on: typing into a disabled form must not nag before the admin has decided.
  function validate(c: SAMLConfig): Partial<Record<Err, string>> {
    if (!c.enabled) return {}
    const out: Partial<Record<Err, string>> = {}
    const put = (k: Err, key: string) => { if (key) out[k] = t(key) }
    if (c.mode === 'auto') put('metadata_url', validateUrl(c.idp.metadata_url, { required: true }))
    else {
      put('entity_id', validateRequired(c.sp.entity_id))
      put('acs_url', validateUrl(c.sp.acs_url, { required: true }))
      put('cert_pem', validateRequired(c.sp.cert_pem))
      put('metadata_url', validateUrl(c.idp.metadata_url, { required: true }))
    }
    return out
  }

  async function saveConfig(next: SAMLConfig, opts: { quietSuccess?: boolean } = {}) {
    const bad = validate(next)
    setErrs(bad)
    const first = (Object.keys(bad) as Err[])[0]
    if (first) { setTimeout(() => document.getElementById(`setting-saml_${first}`)?.focus(), 0); return null }
    setPending(true)
    try {
      const res = await putSAML({
        ...next,
        sp: { entity_id: next.sp.entity_id, acs_url: next.sp.acs_url, cert_pem: next.sp.cert_pem, key_pem: keyPem },
      })
      adopt(res.config); setKeyPem('')
      if (res.reload_error) pushSnack(t('settings.sso.reload_error', { error: res.reload_error }), 'warning')
      else if (!opts.quietSuccess) pushSnack(t('settings.sso.saved'), 'success')
      return res
    } catch { return null } finally { setPending(false) }
  }

  // Asks the server to read the IdP's metadata: the way to learn the URL is right before relying on it. A good answer
  // saves the configuration, as the button says.
  async function fetchAndVerify() {
    if (!cfg) return
    const url = cfg.idp.metadata_url.trim()
    if (!url) { setFetchError(t('settings.sso.saml.fetch_url_required')); setFetched(null); return }
    setFetching(true); setFetchError(''); setFetched(null)
    try {
      setFetched(await fetchSAMLMetadata(url))
      const res = await saveConfig({ ...cfg, idp: { ...cfg.idp, metadata_url: url } }, { quietSuccess: true })
      if (res && !res.reload_error) pushSnack(t('settings.sso.saml.fetch_saved'), 'success')
    } catch (e) { setFetchError(e instanceof Error ? e.message : String(e)) } finally { setFetching(false) }
  }

  if (query.isError && !cfg) return <ErrorState title={t('settings.saml_load_failed')} reason={query.error} retry={() => query.refetch()} />
  if (!cfg) return <CardSkeleton lines={6} />

  const patchSp = (p: Partial<SAMLConfig['sp']>) => setDraft(d => d && { ...d, sp: { ...d.sp, ...p } })
  const patchIdp = (p: Partial<SAMLConfig['idp']>) => setDraft(d => d && { ...d, idp: { ...d.idp, ...p } })
  const patchAttr = (p: Partial<SAMLConfig['attribute_mapping']>) => setDraft(d => d && { ...d, attribute_mapping: { ...d.attribute_mapping, ...p } })
  const patchDef = (p: Partial<SAMLConfig['new_user_defaults']>) => setDraft(d => d && { ...d, new_user_defaults: { ...d.new_user_defaults, ...p } })

  return (
    <SectionForm dirty={dirty} pending={pending} onSave={() => void saveConfig(cfg)} onDiscard={() => { discard(); setKeyPem(''); setErrs({}) }}>
      <Group title={t('settings.sso.saml.enabled')}>
        <SwitchField name="saml_enabled" label={t('settings.sso.saml.enabled')} checked={cfg.enabled} onChange={c => patch({ enabled: c })} />
        <SelectRow<SAMLConfig['mode']> name="saml_mode" label={t('settings.sso.saml.mode')} value={cfg.mode} onChange={m => patch({ mode: m })}
          options={[{ value: 'auto', label: t('settings.sso.saml.mode_auto') }, { value: 'manual', label: t('settings.sso.saml.mode_manual') }]} />
      </Group>

      <Group title={t('settings.sso.saml.idp_section')}>
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-60 flex-1">
            <TextRow name="saml_metadata_url" label={t('settings.sso.saml.idp_metadata_url')} required={cfg.enabled} value={cfg.idp.metadata_url} error={errs.metadata_url}
              onChange={s => {
                patchIdp({ metadata_url: s })
                // A verification of the old URL says nothing about the new one: force a new fetch.
                if (fetched || fetchError) { setFetched(null); setFetchError('') }
              }} />
          </div>
          {auto && (
            <AsyncButton type="button" variant="outline" className="mt-6" pending={fetching} disabled={pending || !cfg.idp.metadata_url.trim()} onClick={fetchAndVerify}>
              {t('settings.sso.saml.fetch_verify')}
            </AsyncButton>
          )}
        </div>
        {auto && fetched && (
          <Notice tone="info">
            <p>{t('settings.sso.saml.fetch_ok', { id: fetched.entity_id })}</p>
            <p className="text-xs text-muted-foreground">
              {t('settings.sso.saml.fetch_certs', {
                n: fetched.num_signing_certs,
                exp: fetched.signing_cert_expires_at ? t('settings.sso.saml.fetch_expires', { date: new Date(fetched.signing_cert_expires_at).toLocaleDateString() }) : '',
              })}
            </p>
          </Notice>
        )}
        {auto && fetchError && <Notice tone="danger" role="alert">{fetchError}</Notice>}
        <div className="max-w-xs">
          <Field label={t('settings.sso.saml.idp_refresh_hours')}>
            <NumberField id="setting-saml_metadata_refresh_hours" value={cfg.idp.metadata_refresh_hours} integer min={0} unit={t('settings.unit.hours')}
              onChange={n => { if (n !== null) patchIdp({ metadata_refresh_hours: n }) }} />
          </Field>
        </div>
      </Group>

      <Group title={t('settings.sso.saml.sp_section')}>
        {auto ? (
          <>
            <p className="text-sm text-muted-foreground">{t('settings.sso.saml.sp_auto_hint')}</p>
            {!cfg.sp.entity_id ? (
              <Notice tone="info">{t('settings.sso.saml.sp_save_first')}</Notice>
            ) : (
              <>
                <ReadOnly label={t('settings.sso.saml.sp_entity_id')} value={cfg.sp.entity_id} />
                <ReadOnly label={t('settings.sso.saml.sp_acs_url')} value={cfg.sp.acs_url} />
                <ReadOnly label={t('settings.sso.saml.sp_cert')} value={cfg.sp.cert_pem} multiline />
              </>
            )}
          </>
        ) : (
          <>
            <TextRow name="saml_entity_id" label={t('settings.sso.saml.sp_entity_id')} required={cfg.enabled} value={cfg.sp.entity_id} error={errs.entity_id} onChange={s => patchSp({ entity_id: s })} />
            <TextRow name="saml_acs_url" label={t('settings.sso.saml.sp_acs_url')} required={cfg.enabled} value={cfg.sp.acs_url} error={errs.acs_url} onChange={s => patchSp({ acs_url: s })} />
            <CodeField id="setting-saml_cert_pem" label={t('settings.sso.saml.sp_cert')} required={cfg.enabled} error={errs.cert_pem}
              language="pem" tabMovesFocus wrap minRows={4} maxRows={12} value={cfg.sp.cert_pem} onChange={cert_pem => patchSp({ cert_pem })} />
            <SecretTextarea id="setting-saml_key_pem" label={t('settings.sso.saml.sp_key')} isSet={cfg.sp.has_key_pem} value={keyPem} onChange={setKeyPem} />
          </>
        )}
      </Group>

      {/* Attribute mapping is admin policy in both modes: auto mode only derives the SP and IdP wiring. The defaults are filled in
          by the server, but a tenant may use its own claim URNs, so they stay editable. */}
      <Group title={t('settings.sso.saml.attr_section')}>
        <Pair>
          <TextRow name="saml_attr_upn" label={t('settings.sso.saml.attr_upn')} hint={t('settings.sso.saml.attr_upn_hint')} value={cfg.attribute_mapping.upn} onChange={s => patchAttr({ upn: s })} />
          <TextRow name="saml_attr_email" label={t('settings.sso.saml.attr_email')} value={cfg.attribute_mapping.email} onChange={s => patchAttr({ email: s })} />
        </Pair>
        <Pair>
          <TextRow name="saml_attr_display_name" label={t('settings.sso.saml.attr_display_name')} value={cfg.attribute_mapping.display_name} onChange={s => patchAttr({ display_name: s })} />
          <TextRow name="saml_attr_groups" label={t('settings.sso.saml.attr_groups')} value={cfg.attribute_mapping.groups} onChange={s => patchAttr({ groups: s })} />
        </Pair>
      </Group>

      <Group title={t('settings.sso.saml.group_section')}>
        <RoleRulesEditor value={cfg.role_rules ?? []} onChange={rules => patch({ role_rules: rules })} />
        <hr />
        <GroupRulesEditor value={cfg.group_rules ?? []} onChange={rules => patch({ group_rules: rules })} groups={groups.data ?? []} loaded={!!groups.data} />
      </Group>

      <Group title={t('settings.sso.saml.new_user_section')}>
        <NewUserDefaults prefix="saml" allowAutoCreate={cfg.allow_auto_create} onAllowAutoCreate={c => patch({ allow_auto_create: c })}
          groupSlug={cfg.default_group_slug} onGroupSlug={s => patch({ default_group_slug: s })} groups={groups.data ?? []}
          defaults={cfg.new_user_defaults} onDefaults={patchDef} />
      </Group>
    </SectionForm>
  )
}

/** A value the panel derived (auto mode) for pasting into the IdP: read-only, with a copy button. */
function ReadOnly({ label, value, multiline }: { label: string; value: string; multiline?: boolean }) {
  return (
    <Field label={label}>
      <div className="flex items-start gap-1.5">
        {multiline
          ? <LazyCodeEditor readOnly ariaLabel={label} language="pem" tabMovesFocus minRows={4} maxRows={12} value={value} onChange={() => {}} className="min-w-0 flex-1" />
          : <Input readOnly value={value} />}
        <CopyButton value={value} name={label} />
      </div>
    </Field>
  )
}
