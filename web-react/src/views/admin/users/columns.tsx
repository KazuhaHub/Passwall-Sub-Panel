import type { TFunction } from 'i18next'
import type { ColumnDef } from '@tanstack/react-table'
import type { RiskLevels } from '@/api/riskCenter'
import type { User } from '@/api/types'
import { ExpiryCell, LastSeenCell, RiskCell, RoleBadge, StatusCell, TrafficCell, UserCell } from './cells'
import { RowActions } from './RowActions'

export interface UserColumnCtx {
  t: TFunction
  groupName: (id: number) => string
  usedBytesOf: (id: number) => number | undefined
  /** The risk column is shown to admins only: an operator is shown neither it nor anything read from the risk center, whose every endpoint would answer 403. */
  canRisk: boolean
  levels: RiskLevels | undefined
  openRisk: (id: number) => void
}

// The default columns share the frame in percentages (max-w-0 lets a cell shrink below its content) and clip with
// an ellipsis, so the checkbox and actions columns can never be pushed out of view; the full text is in `title`.
const FIT = 'max-w-0 overflow-hidden text-ellipsis'

// A sortable column needs an accessor (TanStack cannot sort one without), though the server does the sorting: the value is never compared here.
/**
 * The list's columns: who, group, status, traffic, expiry, last seen (and risk for an admin), with the rest behind
 * the column menu. The id of a sortable column IS the `sort_by` the server takes, so the table's sort state maps
 * straight onto the request. One stable array (the caller memoises on ctx), because DataTable memoises its rows on it.
 */
export function userColumns(c: UserColumnCtx): ColumnDef<User, any>[] {
  const { t } = c
  const cols: ColumnDef<User, any>[] = [
    { id: 'upn', accessorFn: (r: User) => r.upn, header: t('users.table.user'), meta: { hideable: false, label: t('users.table.user'), className: `${FIT} w-[22%]` },
      cell: x => <UserCell user={x.row.original} /> },
    { id: 'display_name', accessorFn: (r: User) => r.display_name, header: t('users.table.display_name'), meta: { label: t('users.table.display_name'), defaultHidden: true, className: `${FIT} w-[14%]` },
      cell: x => <span title={x.row.original.display_name}>{x.row.original.display_name || '—'}</span> },
    { id: 'group_id', accessorFn: (r: User) => r.group_id, header: t('users.table.group'), meta: { label: t('users.table.group'), className: `${FIT} w-[9%]` },
      cell: x => { const n = c.groupName(x.row.original.group_id); return <span title={n}>{n}</span> } },
    { id: 'role', accessorFn: (r: User) => r.role, header: t('users.table.role'), meta: { label: t('users.table.role'), defaultHidden: true },
      cell: x => <RoleBadge role={x.row.original.role} /> },
    { id: 'enabled', accessorFn: (r: User) => r.enabled, header: t('users.table.status'), meta: { label: t('users.table.status'), className: `${FIT} w-[22%]` },
      cell: x => <StatusCell user={x.row.original} /> },
    { id: 'traffic', header: t('users.table.traffic'), enableSorting: false, meta: { label: t('users.table.traffic'), align: 'right', className: 'w-[14%] min-w-28' },
      cell: x => <TrafficCell user={x.row.original} usedBytes={c.usedBytesOf(x.row.original.id)} /> },
    { id: 'expire_at', accessorFn: (r: User) => r.expire_at, header: t('users.table.expire'), meta: { label: t('users.table.expire'), className: 'w-[11%] min-w-28' },
      cell: x => <ExpiryCell user={x.row.original} /> },
  ]
  if (c.canRisk) {
    cols.push({ id: 'risk', header: t('users.table.risk'), enableSorting: false, meta: { label: t('users.table.risk'), className: 'w-[8%] min-w-20' },
      cell: x => <RiskCell userId={x.row.original.id} levels={c.levels} open={c.openRisk} /> })
  }
  cols.push(
    { id: 'last_online_at', accessorFn: (r: User) => r.last_online_at, header: t('users.table.last_online'), meta: { label: t('users.table.last_online'), className: 'w-[9%] min-w-24' },
      cell: x => <LastSeenCell value={x.row.original.last_online_at} /> },
    { id: 'id', header: 'ID', enableSorting: false, meta: { label: 'ID', defaultHidden: true, className: 'font-mono text-muted-foreground' },
      cell: x => String(x.row.original.id) },
    { id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t('users.table.actions'), className: 'w-16' },
      cell: x => <RowActions user={x.row.original} /> },
  )
  return cols
}
