import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { deleteACMEAccount, deleteCert, deleteDNSCred, renewCert, type ACMEAccount, type Cert, type DNSCredential } from '@/api/certs'
import { PageHeader } from '@/components/common/PageHeader'
import { confirm } from '@/components/ConfirmHost'
import { ErrorState } from '@/components/feedback/ErrorState'
import { EmptyState } from '@/components/feedback/EmptyState'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { urlCodecs, useUrlState } from '@/hooks/useUrlState'
import { IconRefresh } from '@/lib/icons'
import { certKeys } from '@/query/keys'
import { AccountFormSheet } from './certificates/AccountFormSheet'
import { AccountsTab } from './certificates/AccountsTab'
import { CertDetailSheet } from './certificates/CertDetail'
import { CertFormSheet } from './certificates/CertFormSheet'
import { CertsTab, type CertActions } from './certificates/CertsTab'
import { CredFormSheet } from './certificates/CredFormSheet'
import { CredsTab } from './certificates/CredsTab'
import { DeleteDialog } from './certificates/DeleteDialog'
import { RenewalSettings } from './certificates/RenewalSettings'
import { caLabel } from './certificates/model'
import { NodeLinks, useCertsT, useCommonT } from './certificates/shared'
import { useCertData } from './certificates/useCertData'
import { parseRowId } from './logs/shared'

const TABS = ['certs', 'accounts', 'credentials'] as const
type Tab = (typeof TABS)[number]

type Editor =
  | { kind: 'cert'; item: Cert | null }
  | { kind: 'account'; item: ACMEAccount | null }
  | { kind: 'cred'; item: DNSCredential | null }
type Removal =
  | { kind: 'cert'; item: Cert }
  | { kind: 'account'; item: ACMEAccount }
  | { kind: 'cred'; item: DNSCredential }

/**
 * Certificates: issue them, see which need attention, and manage the CA accounts and DNS credentials they
 * are issued with. The three lists are tabs (`?tab=`); a certificate opens in a sheet (`?row=`), and the
 * three forms open in a wide sheet each. This page is administrator-only on the server: an operator who
 * reaches it is told so rather than shown raw errors.
 */
export default function CertificatesView() {
  const t = useCertsT()
  const c = useCommonT()
  const queryClient = useQueryClient()
  const data = useCertData()
  const [tab, setTab] = useUrlState('tab', urlCodecs.oneOf(TABS, 'certs'))
  const sel = useUrlSelection<number>('row', { parse: parseRowId })
  const [editor, setEditor] = useState<Editor | null>(null)
  const [removal, setRemoval] = useState<Removal | null>(null)

  const accounts = data.accounts.data ?? []
  const creds = data.creds.data ?? []
  const certs = data.certs.data ?? []
  const refresh = () => data.reload()

  // A tab switch clears what belongs to the tab it leaves: the opened certificate and the list's own
  // keyword and filter (they would carry over into a list they mean nothing for).
  function goTab(next: Tab) {
    setTab(next)
    sel.clear()
  }

  async function renew(cert: Cert) {
    // One issuance at a time per certificate: the CA rate-limits them, and the server already queues only one.
    if (cert.status === 'pending') return
    const account = accounts.find(a => a.id === cert.acme_account_id)
    const ok = await confirm({
      title: t('certs.renew_confirm_title'),
      message: t('certs.renew_confirm_message', { name: cert.name, ca: account ? caLabel(account.directory) : '—' }),
      confirmText: t('certs.renew'),
      initialFocus: 'cancel',
    })
    if (!ok) return
    try {
      await renewCert(cert.id)
      pushSnack(t('certs.renew_queued'), 'success')
      await refresh()
      void queryClient.invalidateQueries({ queryKey: [...certKeys.all(data.scope), 'detail', cert.id] })
    } catch { /* reported by the request layer */ }
  }

  const certActions: CertActions = {
    renew,
    // The detail sheet and the form sheet are both modal: close the first before opening the second.
    edit: cert => { sel.clear(); setEditor({ kind: 'cert', item: cert }) },
    remove: cert => setRemoval({ kind: 'cert', item: cert }),
    create: () => setEditor({ kind: 'cert', item: null }),
    goTab,
  }

  if (data.isFailed && !data.certs.data) {
    const status = (data.failed as { response?: { status?: number } } | undefined)?.response?.status
    return (
      <div className="p-4 sm:p-6">
        <PageHeader title={t('certs.page_title')} />
        {status === 403
          ? <EmptyState title={t('certs.admin_only_title')} description={t('certs.admin_only_hint')} />
          : <ErrorState title={t('certs.load_failed')} reason={data.failed} retry={refresh} />}
      </div>
    )
  }

  const canIssue = accounts.length > 0 && creds.length > 0
  const primary = tab === 'certs'
    ? <Button disabled={data.pending || !canIssue} title={canIssue ? undefined : t('certs.issue_needs_setup')} onClick={certActions.create}>{t('certs.new')}</Button>
    : tab === 'accounts'
      ? <Button onClick={() => setEditor({ kind: 'account', item: null })}>{t('certs.acct_new')}</Button>
      : <Button onClick={() => setEditor({ kind: 'cred', item: null })}>{t('certs.cred_new')}</Button>
  const shown = sel.selected === null ? undefined : certs.find(x => x.id === sel.selected)

  const nodesOf = (cert: Cert) => data.usage.get(cert.id) ?? []
  const certsOf = (pick: (x: Cert) => number, id: number) => certs.filter(x => pick(x) === id)
  const certList = (list: Cert[]) => list.map(x => x.name).join(', ')

  return (
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('certs.page_title')}
        count={tab === 'certs' && data.certs.data ? certs.length : undefined}
        description={t('certs.page_subtitle')}
        primary={primary}
        more={[{ id: 'refresh', label: t('certs.refresh'), icon: IconRefresh, onSelect: () => void refresh() }]}
      />
      <Tabs value={tab} onValueChange={v => goTab(v as Tab)}>
        {/* Three tabs are wider than a phone: the tab row scrolls on its own, the page does not. */}
        <div className="max-w-full overflow-x-auto">
        <TabsList>
          <TabsTrigger value="certs">{t('certs.tab_certs')}</TabsTrigger>
          <TabsTrigger value="accounts">{t('certs.tab_acme')}</TabsTrigger>
          <TabsTrigger value="credentials">{t('certs.tab_creds')}</TabsTrigger>
        </TabsList>
        </div>
      </Tabs>
      <div className="mt-3">
        {data.isFailed && (
          <ErrorState compact title={t('certs.refresh_failed')} reason={data.failed} retry={refresh} />
        )}
        {tab === 'certs' && (
          <>
            <CertsTab data={data} sel={sel} act={certActions} />
            <RenewalSettings data={data} />
          </>
        )}
        {tab === 'accounts' && (
          <AccountsTab data={data} act={{
            create: () => setEditor({ kind: 'account', item: null }),
            edit: a => setEditor({ kind: 'account', item: a }),
            remove: a => setRemoval({ kind: 'account', item: a }),
          }} />
        )}
        {tab === 'credentials' && (
          <CredsTab data={data} act={{
            create: () => setEditor({ kind: 'cred', item: null }),
            edit: d => setEditor({ kind: 'cred', item: d }),
            remove: d => setRemoval({ kind: 'cred', item: d }),
          }} />
        )}
      </div>

      {shown && !editor && <CertDetailSheet cert={shown} data={data} act={certActions} onClose={sel.clear} />}

      {editor?.kind === 'cert' && <CertFormSheet cert={editor.item} data={data} onClose={() => setEditor(null)} />}
      {editor?.kind === 'account' && <AccountFormSheet account={editor.item} data={data} onClose={() => setEditor(null)} />}
      {editor?.kind === 'cred' && <CredFormSheet cred={editor.item} data={data} onClose={() => setEditor(null)} />}

      {removal?.kind === 'cert' && (() => {
        const nodes = nodesOf(removal.item)
        return (
          <DeleteDialog
            title={t('certs.delete_title')}
            name={removal.item.name}
            uses={nodes.length > 0 ? <NodeLinks nodes={nodes} /> : null}
            blocked={nodes.length > 0}
            unknown={!data.usageKnown}
            onRetry={() => data.nodes.refetch()}
            resolve={t('certs.delete.cert_resolve')}
            onDelete={async () => {
              await deleteCert(removal.item.id)
              pushSnack(t('certs.deleted'), 'success')
              setRemoval(null)
              sel.clear()
              await Promise.all([refresh(), queryClient.invalidateQueries({ queryKey: [...certKeys.all(data.scope), 'detail'] })])
            }}
            onClose={() => setRemoval(null)}
          />
        )
      })()}
      {removal?.kind === 'account' && (() => {
        const using = certsOf(x => x.acme_account_id, removal.item.id)
        return (
          <DeleteDialog
            title={t('certs.acct_delete_title')}
            name={removal.item.name || removal.item.email}
            uses={using.length > 0 ? certList(using) : null}
            blocked={using.length > 0}
            resolve={t('certs.delete.account_resolve')}
            onDelete={async () => {
              await deleteACMEAccount(removal.item.id)
              pushSnack(c('actions.deleted'), 'success')
              setRemoval(null)
              await refresh()
            }}
            onClose={() => setRemoval(null)}
          />
        )
      })()}
      {removal?.kind === 'cred' && (() => {
        const using = certsOf(x => x.dns_credential_id, removal.item.id)
        return (
          <DeleteDialog
            title={t('certs.cred_delete_title')}
            name={removal.item.name}
            uses={using.length > 0 ? certList(using) : null}
            // The server has no guard here: the certificates would fail at their next issuance, so say so.
            blocked={false}
            consequence={t('certs.delete.cred_consequence')}
            resolve={t('certs.delete.cred_resolve')}
            onDelete={async () => {
              await deleteDNSCred(removal.item.id)
              pushSnack(t('certs.deleted'), 'success')
              setRemoval(null)
              await refresh()
            }}
            onClose={() => setRemoval(null)}
          />
        )
      })()}
    </div>
  )
}
