import { useMemo, useRef, type RefObject } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import type { Cert } from '@/api/certs'
import { AsyncIconButton } from '@/components/common/AsyncButton'
import { MoreMenu } from '@/components/common/MoreMenu'
import { Button } from '@/components/ui/button'
import { DataTable } from '@/components/data/DataTable'
import { FilterBar } from '@/components/data/FilterBar'
import { EmptyState } from '@/components/feedback/EmptyState'
import type { UrlSelection } from '@/hooks/useUrlSelection'
import { IconCert, IconDelete, IconEdit, IconRefresh } from '@/lib/icons'
import { FIT, RecordCard, plainColumns, singleFacet } from '../logs/shared'
import { DomainsCell, ExpiryCell, HealthCell } from './cells'
import { caLabel, certHealth, type CertHealthId } from './model'
import { NodeLinks, useCertsT } from './shared'
import { HEALTH, useCertRows } from './useCertRows'
import type { CertData } from './useCertData'

// One empty list, so a list not read yet does not look like a new one on every render.
const NONE: never[] = []

export interface CertActions {
  renew: (c: Cert) => Promise<void>
  edit: (c: Cert) => void
  remove: (c: Cert) => void
  create: () => void
  goTab: (tab: 'accounts' | 'credentials') => void
}

/**
 * The list answers one question first: which certificates need attention. Problems sort to the top, the
 * state is in words with its reason, and every row says where the certificate is used. One visible action
 * (renew), the rest in the row menu; a click opens the whole certificate in a dialog (`?row=`).
 */
export function CertsTab({ data, sel, act }: { data: CertData; sel: UrlSelection<number>; act: CertActions }) {
  const t = useCertsT()
  const { f, setF, now, rows, counts } = useCertRows(data)
  const { renewBeforeDays } = data
  const accounts = data.accounts.data ?? NONE
  const creds = data.creds.data ?? NONE
  const accountOf = useMemo(() => new Map(accounts.map(a => [a.id, a])), [accounts])
  const credOf = useMemo(() => new Map(creds.map(c => [c.id, c])), [creds])

  // Handlers are read through a ref so a click does not rebuild every row; what the cells SHOW (usage, accounts,
  // the clock) is in the dependencies, because a row is not redrawn unless its columns change and the nodes
  // that say where a certificate is used arrive after the certificates.
  const ctx = useRef<Ctx>({ act, open: id => sel.select(id) })
  ctx.current = { act, open: id => sel.select(id) }

  const columns = useMemo<ColumnDef<Cert, any>[]>(() => plainColumns<Cert>([
    { id: 'status', header: t('certs.col_status'), meta: { label: t('certs.col_status'), hideable: false, className: 'max-w-0 w-[27%] overflow-hidden' },
      cell: c => <HealthCell cert={c.row.original} health={certHealth(c.row.original, now, renewBeforeDays)} /> },
    { id: 'name', header: t('certs.col_name'), meta: { label: t('certs.col_name'), hideable: false, className: `${FIT} w-[13%] font-medium` },
      cell: c => <span title={c.row.original.name}>{c.row.original.name}</span> },
    { id: 'domains', header: t('certs.col_domains'), meta: { label: t('certs.col_domains'), className: `${FIT} w-[15%]` },
      cell: c => <DomainsCell domains={c.row.original.domains} /> },
    { id: 'issuer', header: t('certs.col_issuer'), meta: { label: t('certs.col_issuer'), className: `${FIT} w-[12%]` },
      cell: c => <IssuerCell cert={c.row.original} accountOf={accountOf} credOf={credOf} /> },
    { id: 'expiry', header: t('certs.col_expiry'), meta: { label: t('certs.col_expiry'), className: `${FIT} w-[14%]` },
      cell: c => <ExpiryCell cert={c.row.original} health={certHealth(c.row.original, now, renewBeforeDays)} /> },
    { id: 'used', header: t('certs.col_used_by'), meta: { label: t('certs.col_used_by'), className: `${FIT} w-[14%]` },
      cell: c => <UsedByCell cert={c.row.original} usage={data.usage} known={data.usageKnown} /> },
    { id: 'auto_renew', header: t('certs.auto_renew'), meta: { label: t('certs.auto_renew'), className: 'w-20', defaultHidden: true },
      cell: c => (c.row.original.auto_renew ? t('certs.on') : t('certs.off')) },
    { id: 'actions', header: '', meta: { hideable: false, align: 'right', label: t('certs.col_actions'), className: 'w-20' },
      cell: c => <RowActions cert={c.row.original} ctx={ctx} /> },
  ]), [t, accountOf, credOf, data.usage, data.usageKnown, now, renewBeforeDays])

  const status = singleFacet<CertHealthId>(f.status, v => setF({ status: v }))
  const narrowed = f.q !== '' || f.status !== ''
  const empty = narrowed
    ? <EmptyState compact title={t('certs.empty_filtered')} action={<Button variant="outline" size="sm" onClick={() => setF({ q: '', status: '' })}>{t('certs.clear_filters')}</Button>} />
    : accounts.length === 0 || creds.length === 0
      ? <SetupGuide hasAccount={accounts.length > 0} hasCred={creds.length > 0} act={act} />
      : <EmptyState icon={<IconCert />} title={t('certs.empty')} description={t('certs.empty_hint')} action={<Button onClick={act.create}>{t('certs.new')}</Button>} />

  return (
    <DataTable<Cert>
      aria-label={t('certs.title')}
      data={rows}
      columns={columns}
      getRowId={c => String(c.id)}
      loading={data.certs.isPending}
      onRowOpen={c => sel.select(c.id)}
      activeRowId={sel.selected === null ? null : String(sel.selected)}
      maxHeight={null}
      renderCard={c => {
        const h = certHealth(c, now, renewBeforeDays)
        const nodes = data.usage.get(c.id) ?? []
        return <RecordCard title={c.name} status={<RowActions cert={c} ctx={ctx} />} lines={[<HealthCell key="h" cert={c} health={h} />, c.domains.join(', '), c.not_after ? <ExpiryCell key="e" cert={c} health={h} /> : null, data.usageKnown && nodes.length > 0 ? <NodeLinks key="n" nodes={nodes} max={2} /> : null]} />
      }}
      toolbar={(
        <FilterBar
          aria-label={t('certs.title')}
          search={{ value: f.q, onChange: q => setF({ q }), placeholder: t('certs.search_placeholder') }}
          facets={[{ id: 'status', label: t('certs.col_status'), options: HEALTH.map(id => ({ value: id, label: t(`certs.health.${id}`), count: counts[id] ?? 0 })), ...status }]}
          onClearAll={() => setF({ q: '', status: '' })}
        />
      )}
      empty={empty}
    />
  )
}

interface Ctx { act: CertActions; open: (id: number) => void }

function IssuerCell({ cert, accountOf, credOf }: {
  cert: Cert
  accountOf: Map<number, { name: string; email: string; directory: string }>
  credOf: Map<number, { name: string; provider: string }>
}) {
  const t = useCertsT()
  const a = accountOf.get(cert.acme_account_id)
  const d = credOf.get(cert.dns_credential_id)
  if (!a) return <span className="text-muted-foreground">—</span>
  return (
    <div className="min-w-0">
      <div className="truncate" title={a.name || a.email}>{caLabel(a.directory)}</div>
      {d && <div className="truncate text-xs text-muted-foreground" title={d.name}>{t('certs.dns_via', { provider: d.provider })}</div>}
    </div>
  )
}

function UsedByCell({ cert, usage, known }: { cert: Cert; usage: CertData['usage']; known: boolean }) {
  const t = useCertsT()
  if (!known) return <span className="text-muted-foreground" title={t('certs.usage_unknown')}>—</span>
  const nodes = usage.get(cert.id) ?? []
  if (nodes.length === 0) return <span className="text-muted-foreground">{t('certs.not_used')}</span>
  return <NodeLinks nodes={nodes} max={2} className="block truncate" />
}

/** One visible action (renew) and the rest in the menu. Renew is off while an issuance is already in flight. */
function RowActions({ cert, ctx }: { cert: Cert; ctx: RefObject<Ctx> }) {
  const t = useCertsT()
  const busy = cert.status === 'pending'
  const { act, open } = ctx.current
  return (
    <div className="flex items-center justify-end gap-0.5">
      <AsyncIconButton
        label={t('certs.renew')}
        tooltip={busy ? t('certs.renew_busy') : undefined}
        size="icon-xs"
        aria-disabled={busy || undefined}
        onClick={() => act.renew(cert)}
      >
        <IconRefresh />
      </AsyncIconButton>
      <MoreMenu
        size="icon-xs"
        label={t('certs.row_menu', { name: cert.name })}
        items={[
          { id: 'detail', label: t('certs.detail_title'), onSelect: () => open(cert.id) },
          { id: 'edit', label: t('common:actions.edit'), icon: IconEdit, onSelect: () => act.edit(cert) },
          { id: 'delete', label: t('common:actions.delete'), icon: IconDelete, destructive: true, onSelect: () => act.remove(cert) },
        ]}
      />
    </div>
  )
}

/** Nothing can be issued before a CA account and a DNS credential exist: say which are missing and offer the way there. */
function SetupGuide({ hasAccount, hasCred, act }: { hasAccount: boolean; hasCred: boolean; act: CertActions }) {
  const t = useCertsT()
  return (
    <EmptyState
      icon={<IconCert />}
      title={t('certs.setup_title')}
      description={t(hasAccount ? 'certs.need_cred_first' : hasCred ? 'certs.need_acct_first' : 'certs.need_acct_and_cred')}
      action={(
        <div className="flex flex-wrap justify-center gap-2">
          {!hasAccount && <Button onClick={() => act.goTab('accounts')}>{t('certs.acct_new')}</Button>}
          {!hasCred && <Button variant={hasAccount ? 'default' : 'outline'} onClick={() => act.goTab('credentials')}>{t('certs.cred_new')}</Button>}
        </div>
      )}
    />
  )
}
