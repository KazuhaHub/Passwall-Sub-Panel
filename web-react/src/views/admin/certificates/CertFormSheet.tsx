import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { createCert, updateCert, type ACMEAccount, type Cert, type DNSCredential } from '@/api/certs'
import { confirm } from '@/components/ConfirmHost'
import { Field } from '@/components/form/Field'
import { useFieldErrors } from '@/components/form/useFieldErrors'
import { pushSnack } from '@/components/feedback/toasts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { certKeys } from '@/query/keys'
import { caLabel, parseDomains, sameDomains } from './model'
import { FormSheet, useCertsT, useCommonT } from './shared'
import type { CertData } from './useCertData'

interface Draft {
  name: string
  domains: string
  account: string
  cred: string
  autoRenew: boolean
}

/**
 * Issue a certificate, or change one: its name, the domains, which CA account it is issued under, which DNS
 * credential proves the domains, and whether it renews itself. Issuing and re-issuing go to the CA, which
 * rate-limits them, so the form says what a save will do before it does it.
 */
export function CertFormSheet({ cert, data, onClose }: { cert: Cert | null; data: CertData; onClose: () => void }) {
  const t = useCertsT()
  const c = useCommonT()
  const queryClient = useQueryClient()
  const accounts: ACMEAccount[] = data.accounts.data ?? []
  const creds: DNSCredential[] = data.creds.data ?? []
  const initial = (): Draft => cert
    ? { name: cert.name, domains: cert.domains.join('\n'), account: String(cert.acme_account_id || accounts[0]?.id || ''), cred: String(cert.dns_credential_id || creds[0]?.id || ''), autoRenew: cert.auto_renew }
    : { name: '', domains: '', account: String(accounts[0]?.id ?? ''), cred: String(creds[0]?.id ?? ''), autoRenew: true }
  const [start] = useState(initial)
  const [draft, setDraft] = useState(start)
  const [pending, setPending] = useState(false)
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => { const next = { ...draft, [k]: v }; setDraft(next); errors.change(k, next) }
  const errors = useFieldErrors<Draft>({
    name: d => (d.name.trim() ? null : t('certs.form.name_required')),
    domains: d => (parseDomains(d.domains).length ? null : t('certs.form.domains_required')),
    account: d => (d.account ? null : t('certs.form.account_required')),
    cred: d => (d.cred ? null : t('certs.form.cred_required')),
  })
  const dirty = JSON.stringify(draft) !== JSON.stringify(start)
  const domainsChanged = cert !== null && !sameDomains(cert.domains, parseDomains(draft.domains))

  async function submit() {
    if (!errors.validateAll(draft)) return
    const req = {
      name: draft.name.trim(),
      domains: parseDomains(draft.domains),
      acme_account_id: Number(draft.account),
      dns_credential_id: Number(draft.cred),
      auto_renew: draft.autoRenew,
    }
    if (cert && domainsChanged) {
      const ok = await confirm({
        title: t('certs.reissue_title'),
        message: t('certs.reissue_message', { name: cert.name }),
        confirmText: t('certs.reissue_confirm'),
        initialFocus: 'cancel',
      })
      if (!ok) return
    }
    setPending(true)
    try {
      if (cert) {
        const saved = await updateCert(cert.id, req)
        queryClient.setQueryData<Cert[]>(certKeys.list(data.scope), prev => prev ? prev.map(x => x.id === saved.id ? saved : x) : prev)
        void queryClient.invalidateQueries({ queryKey: [...certKeys.all(data.scope), 'detail', cert.id] })
        pushSnack(c('saved'), 'success')
        onClose()
      } else {
        await createCert(req)
        pushSnack(t('certs.create_queued'), 'success')
        onClose()
        void data.reload()
      }
    } catch { /* reported by the request layer; the draft stays so it can be retried */ } finally { setPending(false) }
  }

  return (
    <FormSheet
      title={cert ? t('certs.edit') : t('certs.new')}
      description={cert ? cert.name : t('certs.form.new_description')}
      dirty={dirty}
      pending={pending}
      submitLabel={cert ? c('actions.save') : c('actions.create')}
      onSubmit={() => void submit()}
      onClose={onClose}
    >
      {!cert && (
        <Alert role="note"><AlertDescription>{t('certs.form.rate_limit_note')}</AlertDescription></Alert>
      )}
      <Field label={t('certs.col_name')} required error={errors.errors.name}>
        <Input value={draft.name} autoFocus onChange={e => set('name', e.target.value)} onBlur={() => errors.blur('name', draft)} />
      </Field>
      <Field label={t('certs.col_domains')} required error={errors.errors.domains} hint={cert ? t('certs.form.domains_edit_hint') : t('certs.domains_help')}>
        <Textarea value={draft.domains} rows={3} placeholder={'*.example.com\nexample.com'} className="font-mono" onChange={e => set('domains', e.target.value)} onBlur={() => errors.blur('domains', draft)} />
      </Field>
      <Field label={t('certs.form.account_label')} required error={errors.errors.account} hint={cert ? t('certs.form.account_edit_hint') : undefined}>
        <NativeSelect value={draft.account} onChange={e => set('account', e.target.value)} onBlur={() => errors.blur('account', draft)}>
          {draft.account === '' && <option value="" />}
          {accounts.map(a => <option key={a.id} value={String(a.id)}>{a.name || a.email} — {caLabel(a.directory)}</option>)}
        </NativeSelect>
      </Field>
      <Field label={t('certs.form.cred_label')} required error={errors.errors.cred} hint={t('certs.form.cred_hint')}>
        <NativeSelect value={draft.cred} onChange={e => set('cred', e.target.value)} onBlur={() => errors.blur('cred', draft)}>
          {draft.cred === '' && <option value="" />}
          {creds.map(d => <option key={d.id} value={String(d.id)}>{d.name} ({d.provider})</option>)}
        </NativeSelect>
      </Field>
      <Field label={t('certs.auto_renew')} orientation="horizontal" hint={t('certs.form.auto_renew_hint', { days: data.renewBeforeDays })}>
        <Switch checked={draft.autoRenew} onCheckedChange={v => set('autoRenew', v)} />
      </Field>
      {domainsChanged && (
        <Alert role="note" className="text-warning-ink"><AlertDescription className="text-warning-ink">{t('certs.form.domains_changed_warning')}</AlertDescription></Alert>
      )}
    </FormSheet>
  )
}
