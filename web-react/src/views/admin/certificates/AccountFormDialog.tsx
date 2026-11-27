import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { createACMEAccount, updateACMEAccount, type ACMEAccount } from '@/api/certs'
import { Field } from '@/components/form/Field'
import { SecretField } from '@/components/form/SecretField'
import { useFieldErrors } from '@/components/form/useFieldErrors'
import { pushSnack } from '@/components/feedback/toasts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { certKeys } from '@/query/keys'
import { CA_PRESETS, DEFAULT_DIRECTORY } from './model'
import { FormDialog } from '@/components/data/FormDialog'
import { useCertsT, useCommonT } from './shared'
import type { CertData } from './useCertData'

interface Draft { name: string; email: string; directory: string; keyType: string; eabKid: string; eabHmac: string }

/**
 * A CA account: who the certificates are issued as. The EAB HMAC key is a secret: the server never sends
 * it back, so on an existing account the box is empty, says it is set, and leaving it empty keeps it.
 */
export function AccountFormDialog({ account, data, onClose }: { account: ACMEAccount | null; data: CertData; onClose: () => void }) {
  const t = useCertsT()
  const c = useCommonT()
  const queryClient = useQueryClient()
  const [start] = useState<Draft>(() => account
    ? { name: account.name, email: account.email, directory: account.directory, keyType: account.key_type || 'EC256', eabKid: account.eab_key_id, eabHmac: '' }
    : { name: '', email: '', directory: DEFAULT_DIRECTORY, keyType: 'EC256', eabKid: '', eabHmac: '' })
  const [draft, setDraft] = useState(start)
  const [pending, setPending] = useState(false)
  const hmacStored = account?.has_eab_hmac === true
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => { const next = { ...draft, [k]: v }; setDraft(next); errors.change(k, next); if (k === 'eabKid' || k === 'eabHmac') { errors.change('eabKid', next); errors.change('eabHmac', next) } }
  const errors = useFieldErrors<Draft>({
    email: d => (d.email.trim() ? null : t('certs.form.email_required')),
    directory: d => (!d.directory.trim() ? t('certs.form.directory_required') : /^https?:\/\//.test(d.directory.trim()) ? null : t('certs.form.directory_url')),
    // The CA registers with both or neither; the server refuses one without the other.
    eabKid: d => (d.eabHmac && !d.eabKid.trim() ? t('certs.form.eab_kid_missing') : null),
    eabHmac: d => (d.eabKid.trim() && !d.eabHmac && !hmacStored ? t('certs.form.eab_hmac_missing') : null),
  })
  const dirty = JSON.stringify(draft) !== JSON.stringify(start)
  const preset = CA_PRESETS.find(p => p.url === draft.directory.trim())
  // An edit to the identity the CA knows clears the stored registration; the next issuance registers anew.
  const identityChanged = account !== null && (
    draft.email.trim() !== account.email || draft.directory.trim() !== account.directory || draft.eabKid.trim() !== account.eab_key_id || draft.eabHmac !== '')

  async function submit() {
    if (!errors.validateAll(draft)) return
    const req = {
      name: draft.name.trim(), email: draft.email.trim(), directory: draft.directory.trim(),
      eab_key_id: draft.eabKid.trim(), eab_hmac: draft.eabHmac, key_type: draft.keyType,
    }
    setPending(true)
    try {
      if (account) {
        const saved = await updateACMEAccount(account.id, req)
        queryClient.setQueryData<ACMEAccount[]>(certKeys.accounts(data.scope), prev => prev ? prev.map(x => x.id === saved.id ? saved : x) : prev)
      } else {
        await createACMEAccount(req)
        void data.accounts.refetch()
      }
      pushSnack(c('saved'), 'success')
      onClose()
    } catch { /* reported by the request layer; the draft stays so it can be retried */ } finally { setPending(false) }
  }

  return (
    <FormDialog
      title={account ? t('certs.acct_edit') : t('certs.acct_new')}
      description={t('certs.acct_subtitle')}
      dirty={dirty}
      pending={pending}
      submitLabel={c('actions.save')}
      onSubmit={() => void submit()}
      onClose={onClose}
    >
      <Field label={t('certs.col_name')} hint={t('certs.form.acct_name_hint')}>
        <Input value={draft.name} autoFocus placeholder={t('certs.acct_name_ph')} onChange={e => set('name', e.target.value)} />
      </Field>
      <Field label={t('certs.acct_email')} required error={errors.errors.email} hint={t('certs.acme_email_hint')}>
        <Input type="email" value={draft.email} placeholder="you@example.com" onChange={e => set('email', e.target.value)} onBlur={() => errors.blur('email', draft)} />
      </Field>
      <Field label={t('certs.acct_directory')} required error={errors.errors.directory} hint={t('certs.acme_directory_hint')}>
        <Input value={draft.directory} onChange={e => set('directory', e.target.value)} onBlur={() => errors.blur('directory', draft)} />
      </Field>
      <div className="-mt-2 flex flex-wrap gap-2" role="group" aria-label={t('certs.acct_ca')}>
        {CA_PRESETS.map(p => (
          <Button key={p.url} type="button" size="sm" variant={draft.directory.trim() === p.url ? 'default' : 'outline'} aria-pressed={draft.directory.trim() === p.url} onClick={() => set('directory', p.url)}>
            {p.label}
          </Button>
        ))}
      </div>
      <Field label={t('certs.acct_key_type')}>
        <NativeSelect value={draft.keyType} onChange={e => set('keyType', e.target.value)}>
          {(data.keyTypes.includes(draft.keyType) ? data.keyTypes : [draft.keyType, ...data.keyTypes]).map(k => <option key={k} value={k}>{k}</option>)}
        </NativeSelect>
      </Field>
      <div className="flex flex-col gap-4 rounded-lg border p-3">
        <p className="text-sm text-muted-foreground">{t('certs.acct_eab_hint')}</p>
        {preset?.eab && !draft.eabKid.trim() && <p className="text-sm text-warning-ink">{t('certs.form.eab_required_for', { ca: preset.label })}</p>}
        <Field label={t('certs.acct_eab_kid')} error={errors.errors.eabKid}>
          <Input value={draft.eabKid} className="font-mono" onChange={e => set('eabKid', e.target.value)} onBlur={() => errors.blur('eabKid', draft)} />
        </Field>
        <Field label={t('certs.acct_eab_hmac')} error={errors.errors.eabHmac} hint={hmacStored ? t('certs.secret_keep') : undefined}>
          <SecretField value={draft.eabHmac} isSet={hmacStored} onChange={v => set('eabHmac', v)} onBlur={() => errors.blur('eabHmac', draft)} />
        </Field>
      </div>
      {identityChanged && (
        <Alert role="note" className="text-warning-ink"><AlertDescription className="text-warning-ink">{t('certs.form.identity_changed_warning')}</AlertDescription></Alert>
      )}
    </FormDialog>
  )
}
