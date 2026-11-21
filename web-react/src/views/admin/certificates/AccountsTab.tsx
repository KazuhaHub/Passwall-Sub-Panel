import { useMemo, useRef } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import type { ACMEAccount } from '@/api/certs'
import { IconButton } from '@/components/common/IconButton'
import { MoreMenu } from '@/components/common/MoreMenu'
import { StatusDot } from '@/components/common/StatusDot'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DataTable } from '@/components/data/DataTable'
import { EmptyState } from '@/components/feedback/EmptyState'
import { IconDelete, IconEdit, IconShield } from '@/lib/icons'
import { FIT, RecordCard, plainColumns } from '../logs/shared'
import { caLabel } from './model'
import { useCertsT } from './shared'
import type { CertData } from './useCertData'

export interface AccountActions { create: () => void; edit: (a: ACMEAccount) => void; remove: (a: ACMEAccount) => void }

/** The CA accounts. A certificate is issued under the account it selects, so each row says how many use it. */
export function AccountsTab({ data, act }: { data: CertData; act: AccountActions }) {
  const t = useCertsT()
  const certs = data.certs.data
  const usedBy = useMemo(() => {
    const m = new Map<number, number>()
    for (const c of certs ?? []) m.set(c.acme_account_id, (m.get(c.acme_account_id) ?? 0) + 1)
    return m
  }, [certs])
  const ctx = useRef({ act })
  ctx.current = { act }

  const columns = useMemo<ColumnDef<ACMEAccount, any>[]>(() => plainColumns<ACMEAccount>([
    { id: 'name', header: t('certs.col_name'), meta: { label: t('certs.col_name'), hideable: false, className: `${FIT} w-[22%] font-medium` },
      cell: c => { const a = c.row.original; return <span title={a.name || a.email}>{a.name || '—'}</span> } },
    { id: 'email', header: t('certs.acct_email'), meta: { label: t('certs.acct_email'), className: `${FIT} w-[24%] font-mono text-[0.8125rem]` },
      cell: c => <span title={c.row.original.email}>{c.row.original.email}</span> },
    { id: 'ca', header: t('certs.acct_ca'), meta: { label: t('certs.acct_ca'), className: 'w-[26%]' },
      cell: c => <CaCell account={c.row.original} /> },
    { id: 'key_type', header: t('certs.acct_key_type'), meta: { label: t('certs.acct_key_type'), className: 'w-[10%] font-mono text-[0.8125rem]' },
      cell: c => c.row.original.key_type || 'EC256' },
    { id: 'used', header: t('certs.acct_used_by'), meta: { label: t('certs.acct_used_by'), align: 'right', className: 'w-[10%] tabular-nums' },
      cell: c => usedBy.get(c.row.original.id) ?? 0 },
    { id: 'actions', header: '', meta: { hideable: false, align: 'right', label: t('certs.col_actions'), className: 'w-20' },
      cell: c => <Actions account={c.row.original} ctx={ctx} /> },
  // usedBy is in the dependencies: a row is not redrawn unless its columns change, so a count that
  // arrives after the first paint would otherwise stay at what the first paint showed.
  ]), [t, usedBy])

  return (
    <DataTable<ACMEAccount>
      aria-label={t('certs.acct_title')}
      data={data.accounts.data ?? []}
      columns={columns}
      getRowId={a => String(a.id)}
      loading={data.accounts.isPending}
      onRowOpen={a => act.edit(a)}
      maxHeight={null}
      renderCard={a => <RecordCard title={a.name || a.email} status={<Actions account={a} ctx={ctx} />} lines={[a.email, `${caLabel(a.directory)} · ${a.key_type || 'EC256'}`]} />}
      empty={<EmptyState icon={<IconShield />} title={t('certs.acct_empty')} description={t('certs.acct_empty_hint')} action={<Button onClick={act.create}>{t('certs.acct_new')}</Button>} />}
    />
  )
}

function CaCell({ account }: { account: ACMEAccount }) {
  const t = useCertsT()
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span title={account.directory}>{caLabel(account.directory)}</span>
      {account.eab_key_id && <Badge variant="outline">EAB</Badge>}
      {!account.registered && <StatusDot tone="neutral" label={t('certs.acct_unregistered')} />}
    </div>
  )
}

function Actions({ account, ctx }: { account: ACMEAccount; ctx: { current: { act: AccountActions } } }) {
  const t = useCertsT()
  return (
    <div className="flex items-center justify-end gap-0.5">
      <IconButton label={t('common:actions.edit')} size="icon-xs" onClick={() => ctx.current.act.edit(account)}><IconEdit /></IconButton>
      <MoreMenu size="icon-xs" label={t('certs.row_menu', { name: account.name || account.email })}
        items={[{ id: 'delete', label: t('common:actions.delete'), icon: IconDelete, destructive: true, onSelect: () => ctx.current.act.remove(account) }]} />
    </div>
  )
}
