import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { downloadCert, getCertDetail, type Cert, type CertPEM } from '@/api/certs'
import { AsyncButton } from '@/components/common/AsyncButton'
import { CopyButton } from '@/components/common/CopyButton'
import { DetailSheet } from '@/components/data/DetailSheet'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { LoadingState } from '@/components/feedback/LoadingState'
import { IconDelete, IconDownload, IconEdit, IconRefresh } from '@/lib/icons'
import { certKeys } from '@/query/keys'
import { CodeSection, DetailList, DetailRow, TimeCell } from '../logs/shared'
import { HealthCell, healthReason } from './cells'
import { caLabel, certHealth } from './model'
import { NodeLinks, useCertsT, useCommonT } from './shared'
import type { CertActions } from './CertsTab'
import type { CertData } from './useCertData'

/**
 * One certificate in full, in the sheet a row opens. The list's copy of the certificate is shown at once
 * and the detail read (which also carries the issuance task of a pending one) replaces it when it arrives.
 */
export function CertDetailSheet({ cert, data, act, onClose }: { cert: Cert; data: CertData; act: CertActions; onClose: () => void }) {
  const t = useCertsT()
  const detail = useQuery({
    queryKey: [...certKeys.all(data.scope), 'detail', cert.id],
    queryFn: () => getCertDetail(cert.id),
    staleTime: 0,
  })
  const shown = detail.data?.cert ?? cert
  const task = detail.data?.task
  const health = certHealth(shown, Date.now(), data.renewBeforeDays)
  const account = (data.accounts.data ?? []).find(a => a.id === shown.acme_account_id)
  const cred = (data.creds.data ?? []).find(c => c.id === shown.dns_credential_id)
  const nodes = data.usage.get(shown.id) ?? []
  const busy = shown.status === 'pending'
  const [pem, setPem] = useState(false)

  return (
      <DetailSheet
        open
        onClose={onClose}
        title={shown.name}
        status={<HealthCell cert={shown} health={health} reason={false} />}
        actions={(
          <>
            <Button variant="outline" size="sm" onClick={() => act.edit(shown)}><IconEdit />{t('common:actions.edit')}</Button>
            <AsyncButton variant="outline" size="sm" icon={<IconRefresh />} aria-disabled={busy || undefined} className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50" title={busy ? t('certs.renew_busy') : undefined} onClick={() => { if (!busy) return act.renew(shown) }}>
              {t('certs.renew')}
            </AsyncButton>
            <Button variant="outline" size="sm" className="text-destructive" onClick={() => act.remove(shown)}><IconDelete />{t('common:actions.delete')}</Button>
          </>
        )}
      >
        {shown.status === 'failed' && shown.last_error && (
          <CodeSection title={t('certs.failure_reason')} text={shown.last_error} name={t('certs.failure_reason')} />
        )}
        {(health.id === 'expired' || health.id === 'expiring') && (
          <p className="mb-4 text-sm text-muted-foreground">{healthReason(t, shown, health)}</p>
        )}
        {shown.status === 'pending' && (
          <section className="mb-4 rounded-md border bg-muted/40 p-3 text-sm">
            <h3 className="mb-1 text-xs font-medium">{t('certs.progress')}</h3>
            {detail.isPending ? <LoadingState /> : task ? (
              <p className="text-muted-foreground">
                {t('certs.progress_task', { status: task.status, attempts: task.attempts })}
                {task.next_run_at && <> · {t('certs.next_retry')}: <TimeCell value={task.next_run_at} /></>}
                {task.last_error && <> · {task.last_error}</>}
              </p>
            ) : <p className="text-muted-foreground">{t('certs.progress_queued')}</p>}
          </section>
        )}
        <DetailList>
          <DetailRow label={t('certs.col_domains')} mono>
            <ul className="flex flex-col gap-0.5">{shown.domains.map(d => <li key={d}>{d}</li>)}</ul>
          </DetailRow>
          <DetailRow label={t('certs.acct_ca')}>{account ? `${account.name || account.email} · ${caLabel(account.directory)}` : '—'}</DetailRow>
          <DetailRow label={t('certs.form.cred_label')}>{cred ? `${cred.name} (${cred.provider})` : '—'}</DetailRow>
          <DetailRow label={t('certs.not_before')}><TimeCell value={shown.not_before} /></DetailRow>
          <DetailRow label={t('certs.col_expiry')}><TimeCell value={shown.not_after} /></DetailRow>
          <DetailRow label={t('certs.auto_renew')}>{shown.auto_renew ? t('certs.on') : t('certs.off')}</DetailRow>
          <DetailRow label={t('certs.fingerprint')} mono hidden={!shown.fingerprint}>{shown.fingerprint}</DetailRow>
          <DetailRow label={t('certs.col_used_by')}>
            {!data.usageKnown ? t('certs.usage_unknown') : nodes.length === 0 ? t('certs.not_used') : <NodeLinks nodes={nodes} />}
          </DetailRow>
        </DetailList>
        {shown.fingerprint && <MaterialSection cert={shown} onView={() => setPem(true)} />}
        {/* Inside the sheet, so the two dialogs nest and focus stays trapped in the top one. */}
        {pem && <PemDialog id={shown.id} onClose={() => setPem(false)} />}
      </DetailSheet>
  )
}

function triggerDownload(filename: string, content: string) {
  const blob = new Blob([content], { type: 'application/x-pem-file' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

/**
 * The issued material. The list and detail reads never carry a PEM, so each of these asks the dedicated
 * download endpoint, and the private key is only ever fetched by an explicit click on one of the three.
 */
function MaterialSection({ cert, onView }: { cert: Cert; onView: () => void }) {
  const t = useCertsT()
  async function save(which: 'cert' | 'key') {
    const pem = await downloadCert(cert.id)
    const base = (pem.name || 'cert').replace(/[^a-zA-Z0-9._-]/g, '_')
    if (which === 'cert') triggerDownload(`${base}.fullchain.pem`, pem.cert_pem)
    else triggerDownload(`${base}.key.pem`, pem.key_pem)
  }
  return (
    <section className="mt-5">
      <h3 className="mb-1.5 text-xs font-medium text-muted-foreground">{t('certs.material_title')}</h3>
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" onClick={onView}>{t('certs.view_pem')}</Button>
        <AsyncButton variant="outline" size="sm" icon={<IconDownload />} onClick={() => save('cert').catch(() => {})}>{t('certs.download_cert')}</AsyncButton>
        <AsyncButton variant="outline" size="sm" icon={<IconDownload />} className="text-destructive" onClick={() => save('key').catch(() => {})}>{t('certs.download_key')}</AsyncButton>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">{t('certs.material_hint')}</p>
    </section>
  )
}

/** The chain and the private key, copyable: the explicit "show me the material" action. Held in this component's state only, gone on close. */
function PemDialog({ id, onClose }: { id: number; onClose: () => void }) {
  const t = useCertsT()
  const c = useCommonT()
  // No cache entry: a private key must not sit in the query cache after the dialog closes.
  const [state, setState] = useState<{ pem?: CertPEM }>({})
  useEffect(() => {
    let live = true
    downloadCert(id).then(pem => { if (live) setState({ pem }) }).catch(() => { if (live) onClose() })
    return () => { live = false }
    // Read once per opening; the request layer already reported a failure.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])
  return (
    <Dialog open onOpenChange={open => { if (!open) onClose() }}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('certs.pem_title')}{state.pem ? ` — ${state.pem.name}` : ''}</DialogTitle>
          <DialogDescription>{t('certs.material_hint')}</DialogDescription>
        </DialogHeader>
        {!state.pem ? <LoadingState /> : (
          <div className="flex min-w-0 flex-col gap-4">
            <PemBox label={t('certs.cert_chain_label')} value={state.pem.cert_pem} name={t('certs.copy_cert')} />
            <PemBox label={t('certs.private_key_label')} value={state.pem.key_pem} name={t('certs.copy_key')} danger />
          </div>
        )}
        <DialogFooter><Button variant="outline" onClick={onClose}>{c('actions.close')}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function PemBox({ label, value, name, danger }: { label: string; value: string; name: string; danger?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="mb-1 flex items-center">
        <span className={`flex-1 text-xs font-medium ${danger ? 'text-destructive' : 'text-muted-foreground'}`}>{label}</span>
        <CopyButton value={value} name={name} size="icon-xs" />
      </div>
      <pre className={`max-h-52 overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs whitespace-pre ${danger ? 'border-destructive/60' : ''}`}>{value}</pre>
    </div>
  )
}
