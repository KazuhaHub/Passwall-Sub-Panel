import { useMemo, type ReactNode } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import type { Front } from '@/api/fronts'
import { IconDelete, IconEdit } from '@/lib/icons'
import { MoreMenu } from '@/components/common/MoreMenu'
import { StatusBadge } from '@/components/common/StatusBadge'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { DataTable } from '@/components/data/DataTable'
import { useAdminT } from '../useAdminT'
import type { useFrontNames } from './useFrontNames'

/** Routes drawn in a row before the rest are counted: the graph above and the editor hold them all. */
const SHOWN_ROUTES = 3

export interface FrontListProps {
  fronts: readonly Front[]
  inactive: ReadonlySet<number>
  names: ReturnType<typeof useFrontNames>
  /** The server can edit fronts (the editor needs its options too). false: Edit is off, with `editOffReason`. */
  canEdit: boolean
  editOffReason?: string
  loading: boolean
  error: unknown
  onRetry: () => void
  empty: ReactNode
  onEdit: (front: Front) => void
  onDelete: (front: Front) => void
}

/** The fronts of one server. A row has one visible action (Edit) and a More menu (Delete); a click on the row edits too. */
export default function FrontList({ fronts, inactive, names, canEdit, editOffReason, loading, error, onRetry, empty, onEdit, onDelete }: FrontListProps) {
  const t = useAdminT()

  const routeLines = (f: Front) => [
    ...f.routes.map(r => t('admin:servers.fronts.ports.route_arrow', { names: r.server_names.join(', '), to: names.targetText(r.target) })),
    t('admin:servers.fronts.ports.default_arrow', { to: `${f.default.host}:${f.default.port}` }),
  ]

  const columns = useMemo<ColumnDef<Front>[]>(() => [
    {
      id: 'port', header: t('admin:servers.fronts.ports.port'), accessorFn: f => f.port, meta: { label: t('admin:servers.fronts.ports.port'), hideable: false, className: 'tabular-nums whitespace-nowrap font-medium' },
      cell: c => t('admin:servers.fronts.front_title', { port: c.row.original.port }),
    },
    {
      id: 'routes', header: t('admin:servers.fronts.routes'), enableSorting: false, meta: { label: t('admin:servers.fronts.routes'), hideable: false },
      cell: c => {
        const lines = routeLines(c.row.original)
        const shown = lines.slice(0, SHOWN_ROUTES)
        return (
          <div className="flex min-w-0 flex-col gap-0.5">
            {shown.map((line, i) => <span key={i} className="block max-w-[28rem] truncate" title={line}>{line}</span>)}
            {lines.length > shown.length && <span className="text-xs text-muted-foreground">{t('admin:servers.fronts.more_routes', { count: lines.length - shown.length })}</span>}
          </div>
        )
      },
    },
    {
      id: 'status', header: t('admin:servers.fronts.status'), enableSorting: false, meta: { label: t('admin:servers.fronts.status') },
      cell: c => inactive.has(c.row.original.id)
        ? <StatusBadge tone="warning" label={t('admin:servers.fronts.inactive')} />
        : <StatusBadge tone="success" label={t('admin:servers.fronts.active')} />,
    },
    {
      id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t('admin:servers.fronts.actions') },
      cell: c => {
        const f = c.row.original
        const label = t('admin:servers.fronts.edit_port', { port: f.port })
        const edit = (
          <Button variant="outline" size="sm" aria-label={label} aria-disabled={!canEdit || undefined}
            className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50" onClick={() => { if (canEdit) onEdit(f) }}>
            <IconEdit />{t('common:actions.edit')}
          </Button>
        )
        return (
          <div className="flex items-center justify-end gap-1">
            {canEdit || !editOffReason ? edit : <Tooltip><TooltipTrigger render={edit} /><TooltipContent>{editOffReason}</TooltipContent></Tooltip>}
            <MoreMenu items={[{ id: 'delete', label: t('admin:servers.fronts.delete_port', { port: f.port }), icon: IconDelete, destructive: true, onSelect: () => onDelete(f) }]} />
          </div>
        )
      },
    },
  // eslint-disable-next-line react-hooks/exhaustive-deps
  ], [t, names, inactive, canEdit, editOffReason, onEdit, onDelete])

  return (
    <DataTable<Front>
      data={fronts} columns={columns} getRowId={f => String(f.id)} pagination={false} columnMenu={false} maxHeight={null}
      loading={loading} error={error ? (error as Error) : null} onRetry={onRetry} empty={empty}
      onRowOpen={canEdit ? onEdit : undefined}
      renderCard={f => (
        <div className="flex flex-col gap-1.5 border-b px-3 py-3">
          <div className="flex items-center justify-between gap-2">
            <span className="font-medium">{t('admin:servers.fronts.front_title', { port: f.port })}</span>
            {inactive.has(f.id) ? <StatusBadge tone="warning" label={t('admin:servers.fronts.inactive')} /> : <StatusBadge tone="success" label={t('admin:servers.fronts.active')} />}
          </div>
          {routeLines(f).slice(0, SHOWN_ROUTES).map((line, i) => <span key={i} className="truncate text-sm text-muted-foreground" title={line}>{line}</span>)}
          <div className="flex justify-end gap-1">
            <Button variant="outline" size="sm" aria-label={t('admin:servers.fronts.edit_port', { port: f.port })} aria-disabled={!canEdit || undefined} onClick={() => { if (canEdit) onEdit(f) }}>{t('common:actions.edit')}</Button>
            <MoreMenu items={[{ id: 'delete', label: t('admin:servers.fronts.delete_port', { port: f.port }), icon: IconDelete, destructive: true, onSelect: () => onDelete(f) }]} />
          </div>
        </div>
      )}
    />
  )
}
