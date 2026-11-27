import { useId, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { createDNSCred, updateDNSCred, type DNSCredential } from '@/api/certs'
import { Field } from '@/components/form/Field'
import { SecretField } from '@/components/form/SecretField'
import { IconButton } from '@/components/common/IconButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { IconAdd, IconDelete } from '@/lib/icons'
import { certKeys } from '@/query/keys'
import { buildCredentials, isCustomProvider } from './model'
import { FormDialog } from '@/components/data/FormDialog'
import { useCertsT, useCommonT } from './shared'
import type { CertData } from './useCertData'

interface Pair { k: string; v: string }

/**
 * A DNS credential: what the CA's DNS-01 challenge uses to add a record. The provider list comes from the
 * server and each provider has its own fields; a provider with no fixed schema (exec, httpreq, one the list
 * does not know) takes free KEY / VALUE pairs. Values are write-only: the server returns only the key names,
 * so on an existing credential every box starts empty and an empty box keeps the stored value.
 */
export function CredFormDialog({ cred, data, onClose }: { cred: DNSCredential | null; data: CertData; onClose: () => void }) {
  const t = useCertsT()
  const c = useCommonT()
  const queryClient = useQueryClient()
  const listId = useId()
  const [name, setName] = useState(cred?.name ?? '')
  const [provider, setProvider] = useState(cred?.provider ?? '')
  const [values, setValues] = useState<Record<string, string>>({})
  const [pairs, setPairs] = useState<Pair[]>(cred && cred.keys.length ? cred.keys.map(k => ({ k, v: '' })) : [{ k: '', v: '' }])
  const [tried, setTried] = useState(false)
  const [pending, setPending] = useState(false)

  const info = useMemo(() => data.providers.find(p => p.name === provider) ?? null, [data.providers, provider])
  const custom = isCustomProvider(info)
  const fields = info?.fields ?? []
  const editing = cred !== null

  // Switching provider resets the inputs so stale fields do not carry over between two vendors' schemas.
  function changeProvider(next: string) {
    setProvider(next)
    setValues({})
    setPairs([{ k: '', v: '' }])
  }

  const built = buildCredentials({ editing, custom, fields, values, pairs })
  const nameError = tried && !name.trim() ? t('certs.form.name_required') : undefined
  const providerError = tried && !provider.trim() ? t('certs.form.provider_required') : undefined
  const missing = (key: string, optional?: boolean) => tried && !editing && !optional && !(values[key] ?? '').trim()
  const kvError = tried && custom && !built.ok ? t('certs.validation_secret_required') : undefined
  const dirty = name !== (cred?.name ?? '') || provider !== (cred?.provider ?? '')
    || Object.values(values).some(v => v !== '') || (custom && pairs.some((p, i) => p.v !== '' || p.k !== (cred?.keys[i] ?? '')))

  async function submit() {
    setTried(true)
    if (!name.trim() || !provider.trim() || !built.ok) return
    const body = { name: name.trim(), provider: provider.trim(), credentials: built.credentials }
    setPending(true)
    try {
      if (cred) {
        const saved = await updateDNSCred(cred.id, body)
        queryClient.setQueryData<DNSCredential[]>(certKeys.creds(data.scope), prev => prev ? prev.map(x => x.id === saved.id ? saved : x) : prev)
      } else {
        await createDNSCred(body)
        void data.creds.refetch()
      }
      pushSnack(c('saved'), 'success')
      onClose()
    } catch { /* reported by the request layer; the draft stays so it can be retried */ } finally { setPending(false) }
  }

  return (
    <FormDialog
      title={cred ? t('certs.cred_edit') : t('certs.cred_new')}
      description={t('certs.cred_subtitle')}
      dirty={dirty}
      pending={pending}
      submitLabel={c('actions.save')}
      onSubmit={() => void submit()}
      onClose={onClose}
    >
      <Field label={t('certs.col_name')} required error={nameError}>
        <Input value={name} autoFocus onChange={e => setName(e.target.value)} />
      </Field>
      <Field label={t('certs.cred_provider')} required error={providerError} hint={t('certs.provider_help')}>
        {/* A text box with suggestions rather than a closed list: a provider the catalogue does not know is a valid answer. */}
        <Input value={provider} list={listId} spellCheck={false} autoComplete="off" onChange={e => changeProvider(e.target.value)} />
      </Field>
      <datalist id={listId}>
        {data.providers.map(p => <option key={p.name} value={p.name}>{`${p.label} (${p.name})`}</option>)}
      </datalist>

      {provider.trim() !== '' && (custom ? (
        <div className="flex flex-col gap-3">
          <p className="text-sm text-muted-foreground">{t('certs.cred_kv_help')}</p>
          {editing && <p className="text-xs text-muted-foreground">{t('certs.cred_edit_keep_hint')}</p>}
          {pairs.map((p, i) => (
            <div key={i} className="flex items-start gap-2">
              <Field label="KEY" className="flex-1">
                <Input value={p.k} className="font-mono" placeholder="CF_DNS_API_TOKEN" spellCheck={false} onChange={e => setPairs(prev => prev.map((x, j) => (j === i ? { ...x, k: e.target.value } : x)))} />
              </Field>
              <Field label="VALUE" className="flex-[2]">
                <SecretField value={p.v} isSet={editing && cred.keys.includes(p.k)} onChange={v => setPairs(prev => prev.map((x, j) => (j === i ? { ...x, v } : x)))} />
              </Field>
              <IconButton label={t('certs.cred_remove_field')} size="icon-sm" className="mt-6" onClick={() => setPairs(prev => prev.filter((_, j) => j !== i))}><IconDelete /></IconButton>
            </div>
          ))}
          {kvError && <p role="alert" className="text-xs text-destructive">{kvError}</p>}
          <div><Button type="button" variant="outline" size="sm" onClick={() => setPairs(prev => [...prev, { k: '', v: '' }])}><IconAdd />{t('certs.cred_add_field')}</Button></div>
        </div>
      ) : (
        fields.map(f => {
          const stored = editing && cred.keys.includes(f.key)
          const label = f.label + (f.optional ? ` (${t('certs.optional')})` : '')
          const error = missing(f.key, f.optional) ? t('certs.form.value_required') : undefined
          return (
            <Field key={f.key} label={label} required={!f.optional && !editing} error={error} hint={<span className="font-mono">{f.key}</span>}>
              {f.secret
                ? <SecretField value={values[f.key] ?? ''} isSet={stored} onChange={v => setValues(prev => ({ ...prev, [f.key]: v }))} />
                : <Input value={values[f.key] ?? ''} spellCheck={false} placeholder={stored ? t('certs.secret_keep') : undefined} onChange={e => setValues(prev => ({ ...prev, [f.key]: e.target.value }))} />}
            </Field>
          )
        })
      ))}
    </FormDialog>
  )
}
