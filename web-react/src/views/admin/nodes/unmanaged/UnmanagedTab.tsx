import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import type { UnmanagedInbound } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList } from '@/components/ui/combobox'
import { DataTable } from '@/components/data/DataTable'
import { filterRows } from '@/components/data/dataTableLogic'
import { FilterBar } from '@/components/data/FilterBar'
import { describeError } from '@/components/feedback/ErrorState'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Field } from '@/components/form/Field'
import { useTableViewState } from '@/hooks/useTableViewState'
import { urlCodecs, useUrlState } from '@/hooks/useUrlState'
import { useAllNodes, useAllServers, useUnmanagedInbounds } from '@/query/nodes'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { useNodesT } from '../shared/useNodesT'
import { ClaimDialog } from './ClaimDialog'
import { ImportDialog } from './ImportDialog'

interface ServerOption { id: number; name: string }

/**
 * Inbounds on a server that PSP does not manage. Nothing loads until a server is picked (one upstream round
 * trip, and a dead server cannot stall the page). Claim and import are admin actions.
 */
export default function UnmanagedTab({ onImported }: { onImported: () => void }) {
  const t = useNodesT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const admin = useCan('config.write')
  const servers = useAllServers(scope)
  const nodes = useAllNodes(scope)
  const [panelId, setPanelId] = useState<number | null>(null)
  const [q, setQ] = useUrlState('uq', urlCodecs.string())
  const query = useUnmanagedInbounds(scope, panelId)
  const view = useTableViewState('admin.nodes.unmanaged', { pageSize: 25 })
  const [claim, setClaim] = useState<UnmanagedInbound | null>(null)
  const [importing, setImporting] = useState<UnmanagedInbound | null>(null)
  const serverList = useMemo<ServerOption[]>(() => (servers.data ?? []).map(s => ({ id: s.id, name: s.name })), [servers.data])
  const tagOptions = useMemo(() => [...new Set((nodes.data ?? []).flatMap(n => n.tags ?? []))].sort((a, b) => a.localeCompare(b)), [nodes.data])
  const all = query.data?.items
  const rows = useMemo(() => filterRows(all ?? [], u => `${u.Protocol} ${u.Remark} ${u.InboundID} ${u.Port}`, q), [all, q])
  const refresh = () => queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) })

  const columns = useMemo<ColumnDef<UnmanagedInbound>[]>(() => [
    { accessorKey: 'InboundID', header: t('nodes.unmanaged.col_id'), meta: { label: t('nodes.unmanaged.col_id'), hideable: false } },
    { accessorKey: 'Protocol', header: t('nodes.unmanaged.col_protocol'), meta: { label: t('nodes.unmanaged.col_protocol') } },
    { accessorKey: 'Port', header: t('nodes.unmanaged.col_port'), meta: { label: t('nodes.unmanaged.col_port'), align: 'right' } },
    { accessorKey: 'Remark', header: t('nodes.unmanaged.col_remark'), meta: { label: t('nodes.unmanaged.col_remark') } },
    { accessorKey: 'ClientCount', header: t('nodes.unmanaged.col_clients'), meta: { label: t('nodes.unmanaged.col_clients'), align: 'right', className: 'tabular-nums' } },
    {
      id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t('nodes.col.actions') },
      cell: c => admin && (
        <div className="flex justify-end gap-2">
          {c.row.original.ClientCount > 0 && <Button variant="outline" size="sm" onClick={() => setClaim(c.row.original)}>{t('nodes.claim')}</Button>}
          <Button variant="outline" size="sm" onClick={() => setImporting(c.row.original)}>{t('nodes.import')}</Button>
        </div>
      ),
    },
  ], [t, admin])

  const picked = serverList.find(s => s.id === panelId) ?? null
  return (
    <div className="flex flex-col gap-3">
      <Field label={t('nodes.unmanaged.server')} className="max-w-xs">
        {p => (
          <Combobox items={serverList} value={picked} onValueChange={v => setPanelId((v as ServerOption | null)?.id ?? null)}
            itemToStringLabel={(s: ServerOption) => s.name} isItemEqualToValue={(a: ServerOption, b: ServerOption) => a.id === b.id}>
            <ComboboxInput id={p.id} placeholder={t('nodes.unmanaged.server_placeholder')} showClear />
            <ComboboxContent>
              <ComboboxEmpty>{t('nodes.order.move_none')}</ComboboxEmpty>
              <ComboboxList>{(s: ServerOption) => <ComboboxItem key={s.id} value={s}>{s.name}</ComboboxItem>}</ComboboxList>
            </ComboboxContent>
          </Combobox>
        )}
      </Field>
      {panelId === null ? (
        <EmptyState title={t('nodes.unmanaged.pick_server')} />
      ) : (
        <DataTable<UnmanagedInbound>
          aria-label={t('nodes.tabs.unmanaged')}
          data={rows}
          columns={columns}
          getRowId={u => `${u.PanelID}-${u.InboundID}`}
          loading={query.isPending}
          error={query.isError ? t('nodes.unmanaged.load_failed', { reason: describeError(query.error) ?? '' }) : null}
          onRetry={() => void query.refetch()}
          pageSize={view.state.pageSize}
          onPageSizeChange={view.setPageSize}
          columnVisibility={view.state.columnVisibility}
          onColumnVisibilityChange={view.setColumnVisibility}
          toolbar={<FilterBar search={{ value: q, onChange: setQ, placeholder: t('nodes.unmanaged.search_placeholder') }} onClearAll={() => setQ('')} />}
          empty={<EmptyState compact title={t(q ? 'nodes.empty.filtered_unmanaged' : 'nodes.unmanaged.none')} />}
        />
      )}
      {claim && <ClaimDialog inbound={claim} onClose={() => setClaim(null)} onClaimed={() => void refresh()} />}
      {importing && <ImportDialog inbound={importing} server={(servers.data ?? []).find(s => s.id === importing.PanelID)} tagOptions={tagOptions} onClose={() => setImporting(null)} onImported={() => { void refresh(); onImported() }} />}
    </div>
  )
}
