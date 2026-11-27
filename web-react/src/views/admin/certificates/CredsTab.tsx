import { useMemo, useRef } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import type { DNSCredential } from '@/api/certs'
import { IconButton } from '@/components/common/IconButton'
import { MoreMenu } from '@/components/common/MoreMenu'
import { Button } from '@/components/ui/button'
import { DataTable } from '@/components/data/DataTable'
import { EmptyState } from '@/components/feedback/EmptyState'
import { IconDelete, IconEdit, IconKey } from '@/lib/icons'
import { FIT, RecordCard, plainColumns } from '../logs/shared'
import { useCertsT } from './shared'
import type { CertData } from './useCertData'

export interface CredActions { create: () => void; edit: (c: DNSCredential) => void; remove: (c: DNSCredential) => void }

/** The DNS credentials the DNS-01 challenge uses. Only the key names come back from the server, never the values. */
export function CredsTab({ data, act }: { data: CertData; act: CredActions }) {
  const t = useCertsT()
  const certs = data.certs.data
  const usedBy = useMemo(() => {
    const m = new Map<number, number>()
    for (const c of certs ?? []) m.set(c.dns_credential_id, (m.get(c.dns_credential_id) ?? 0) + 1)
    return m
  }, [certs])
  const label = useMemo(() => new Map(data.providers.map(p => [p.name, p.label])), [data.providers])
  const ctx = useRef({ act })
  ctx.current = { act }

  const columns = useMemo<ColumnDef<DNSCredential, any>[]>(() => plainColumns<DNSCredential>([
    { id: 'name', header: t('certs.col_name'), meta: { label: t('certs.col_name'), hideable: false, className: `${FIT} w-[24%] font-medium` },
      cell: c => <span title={c.row.original.name}>{c.row.original.name}</span> },
    { id: 'provider', header: t('certs.cred_provider'), meta: { label: t('certs.cred_provider'), className: 'whitespace-nowrap' },
      cell: c => { const p = c.row.original.provider; const l = label.get(p); return <span title={p}>{l ? `${l} (${p})` : p}</span> } },
    { id: 'keys', header: t('certs.cred_keys'), meta: { label: t('certs.cred_keys'), className: `${FIT} w-[28%] font-mono text-xs` },
      cell: c => <span title={c.row.original.keys.join(', ')}>{c.row.original.keys.join(', ') || '—'}</span> },
    { id: 'used', header: t('certs.acct_used_by'), meta: { label: t('certs.acct_used_by'), align: 'right', className: 'w-[12%] tabular-nums' },
      cell: c => usedBy.get(c.row.original.id) ?? 0 },
    { id: 'actions', header: '', meta: { hideable: false, align: 'right', label: t('certs.col_actions'), className: 'w-20' },
      cell: c => <Actions cred={c.row.original} ctx={ctx} /> },
  // usedBy and label are in the dependencies: a row is not redrawn unless its columns change.
  ]), [t, usedBy, label])

  return (
    <DataTable<DNSCredential>
      tableId="admin.certs.creds"
      aria-label={t('certs.cred_title')}
      data={data.creds.data ?? []}
      columns={columns}
      getRowId={c => String(c.id)}
      loading={data.creds.isPending}
      onRowOpen={c => act.edit(c)}
      maxHeight={null}
      renderCard={c => <RecordCard title={c.name} status={<Actions cred={c} ctx={ctx} />} lines={[c.provider, c.keys.join(', ')]} />}
      empty={<EmptyState icon={<IconKey />} title={t('certs.cred_empty')} description={t('certs.cred_empty_hint')} action={<Button onClick={act.create}>{t('certs.cred_new')}</Button>} />}
    />
  )
}

function Actions({ cred, ctx }: { cred: DNSCredential; ctx: { current: { act: CredActions } } }) {
  const t = useCertsT()
  return (
    <div className="flex items-center justify-end gap-0.5">
      <IconButton label={t('common:actions.edit')} size="icon-xs" onClick={() => ctx.current.act.edit(cred)}><IconEdit /></IconButton>
      <MoreMenu size="icon-xs" label={t('certs.row_menu', { name: cred.name })}
        items={[{ id: 'delete', label: t('common:actions.delete'), icon: IconDelete, destructive: true, onSelect: () => ctx.current.act.remove(cred) }]} />
    </div>
  )
}
