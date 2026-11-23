import { useCallback, useMemo, useState } from 'react'
import { putOIDC, type OIDCConfig } from '@/api/settings'
import { ErrorState } from '@/components/feedback/ErrorState'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { SecretField } from '@/components/form/SecretField'
import { TagsField } from '@/components/form/TagsField'
import { useAllGroups } from '@/query/groups'
import { settingsKeys } from '@/query/keys'
import { useOidcConfig } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { validateRequired, validateUrl } from '@/utils/validators'
import { useAdminT } from '../../useAdminT'
import { Group, Pair, SectionForm, SwitchField, TextRow } from '../parts'
import { NewUserDefaults } from './NewUserDefaults'
import { GroupRulesEditor, RoleRulesEditor } from './RuleEditors'
import { useSsoDraft } from './useSsoDraft'

// Scopes and role rules come back as null on a fresh database (Go's nil slice), and the editors call array methods on them.
const normalize = (c: OIDCConfig): OIDCConfig => ({ ...c, scopes: c.scopes ?? [], role_rules: c.role_rules ?? [] })

type Err = 'issuer_url' | 'client_id' | 'redirect_url'

/** OIDC / OAuth2: the provider, the client, the claims, role and group rules, and what a new account gets. */
export function OidcPanel({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const query = useOidcConfig(scope)
  const groups = useAllGroups(scope)
  const [secret, setSecret] = useState('')
  const key = useMemo(() => settingsKeys.oidc(scope), [scope])
  const norm = useCallback(normalize, [])
  const { draft: cfg, patch, adopt, dirty, discard, setDraft } = useSsoDraft(query, key, norm, secret !== '', onDirtyChange)
  const [errs, setErrs] = useState<Partial<Record<Err, string>>>({})
  const [pending, setPending] = useState(false)

  function validate(c: OIDCConfig): Partial<Record<Err, string>> {
    if (!c.enabled) return {}
    const out: Partial<Record<Err, string>> = {}
    const put = (k: Err, key: string) => { if (key) out[k] = t(key) }
    put('issuer_url', validateUrl(c.issuer_url, { required: true }))
    put('client_id', validateRequired(c.client_id))
    put('redirect_url', validateUrl(c.redirect_url, { required: true }))
    return out
  }

  async function save() {
    if (!cfg) return
    const bad = validate(cfg)
    setErrs(bad)
    const first = (Object.keys(bad) as Err[])[0]
    if (first) { setTimeout(() => document.getElementById(`setting-oidc_${first}`)?.focus(), 0); return }
    setPending(true)
    try {
      // The secret is sent only when typed; empty means "keep the stored one" (the server's contract), so a save never wipes it.
      const res = await putOIDC({ ...cfg, client_secret: secret })
      adopt(res.config); setSecret('')
      if (res.reload_error) pushSnack(t('settings.sso.reload_error', { error: res.reload_error }), 'warning')
      else pushSnack(t('settings.sso.saved'), 'success')
    } catch { /* reported by the request layer */ } finally { setPending(false) }
  }

  if (query.isError && !cfg) return <ErrorState title={t('settings.oidc_load_failed')} reason={query.error} retry={() => query.refetch()} />
  if (!cfg) return <CardSkeleton lines={6} />

  const patchAttr = (p: Partial<OIDCConfig['attribute_mapping']>) => setDraft(d => d && { ...d, attribute_mapping: { ...d.attribute_mapping, ...p } })
  const patchDef = (p: Partial<OIDCConfig['new_user_defaults']>) => setDraft(d => d && { ...d, new_user_defaults: { ...d.new_user_defaults, ...p } })

  return (
    <SectionForm dirty={dirty} pending={pending} onSave={() => void save()} onDiscard={() => { discard(); setSecret(''); setErrs({}) }}>
      <Group title={t('settings.sso.oidc.enabled')}>
        <SwitchField name="oidc_enabled" label={t('settings.sso.oidc.enabled')} checked={cfg.enabled} onChange={c => patch({ enabled: c })} />
      </Group>

      <Group title={t('settings.sso.oidc.issuer_url')}>
        <TextRow name="oidc_issuer_url" label={t('settings.sso.oidc.issuer_url')} required={cfg.enabled} value={cfg.issuer_url} error={errs.issuer_url} onChange={s => patch({ issuer_url: s })} />
        <TextRow name="oidc_client_id" label={t('settings.sso.oidc.client_id')} required={cfg.enabled} value={cfg.client_id} error={errs.client_id} onChange={s => patch({ client_id: s })} />
        <Field label={t('settings.sso.oidc.client_secret')} hint={cfg.has_client_secret ? t('settings.secret_hint') : undefined}>
          <SecretField id="setting-oidc_client_secret" value={secret} isSet={cfg.has_client_secret} onChange={setSecret} />
        </Field>
        <TextRow name="oidc_redirect_url" label={t('settings.sso.oidc.redirect_url')} required={cfg.enabled} value={cfg.redirect_url} error={errs.redirect_url} onChange={s => patch({ redirect_url: s })} />
        <Field label={t('settings.sso.oidc.scopes')}>
          {p => <TagsField id="setting-oidc_scopes" aria-labelledby={p.labelId} value={cfg.scopes} onChange={scopes => patch({ scopes })} />}
        </Field>
      </Group>

      <Group title={t('settings.sso.oidc.attr_section')}>
        <Pair>
          <TextRow name="oidc_attr_username" label={t('settings.sso.oidc.attr_username')} value={cfg.attribute_mapping.username} onChange={s => patchAttr({ username: s })} />
          <TextRow name="oidc_attr_email" label={t('settings.sso.oidc.attr_email')} value={cfg.attribute_mapping.email} onChange={s => patchAttr({ email: s })} />
        </Pair>
        <Pair>
          <TextRow name="oidc_attr_display_name" label={t('settings.sso.oidc.attr_display_name')} value={cfg.attribute_mapping.display_name} onChange={s => patchAttr({ display_name: s })} />
          <TextRow name="oidc_attr_groups" label={t('settings.sso.oidc.attr_groups')} value={cfg.attribute_mapping.groups} onChange={s => patchAttr({ groups: s })} />
        </Pair>
        <RoleRulesEditor value={cfg.role_rules ?? []} onChange={rules => patch({ role_rules: rules })} />
        <hr />
        <GroupRulesEditor value={cfg.group_rules ?? []} onChange={rules => patch({ group_rules: rules })} groups={groups.data ?? []} loaded={!!groups.data} />
      </Group>

      <Group title={t('settings.sso.oidc.new_user_section')}>
        <NewUserDefaults prefix="oidc" allowAutoCreate={cfg.allow_auto_create} onAllowAutoCreate={c => patch({ allow_auto_create: c })}
          groupSlug={cfg.default_group_slug} onGroupSlug={s => patch({ default_group_slug: s })} groups={groups.data ?? []}
          defaults={cfg.new_user_defaults} onDefaults={patchDef} />
      </Group>
    </SectionForm>
  )
}
