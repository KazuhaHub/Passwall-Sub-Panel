import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'
import type { ColumnDef } from '@tanstack/react-table'
import type { Server } from '@/api/servers'
import { AsyncButton } from '@/components/common/AsyncButton'
import { MoreMenu } from '@/components/common/MoreMenu'
import { CountsCell, NameCell, StatusCell, TypeCell, VersionCell } from './cells'
import { useServersCtx } from './serversContext'

// The columns share the frame in percentages and clip with an ellipsis (max-w-0 lets a cell shrink below its content),
// so the checkbox and action columns can never be pushed out of view; the full text is in `title`.
const FIT = 'max-w-0 overflow-hidden text-ellipsis'

/**
 * The server list's columns. Defined for ONE stable array (the caller memoises on ctx), because DataTable memoises its
 * rows on the columns' identity. Server-sorted: the sortable ids are the ones the backend accepts (name, url,
 * panel_version, id).
 */
export function serverColumns(t: TFunction): ColumnDef<Server, any>[] {
  return [
    { id: 'name', header: t('admin:servers.table.name'), enableSorting: true, meta: { hideable: false, label: t('admin:servers.table.name'), className: FIT + ' w-[26%]' },
      cell: ctx => <NameCell s={ctx.row.original} /> },
    { id: 'type', header: t('admin:servers.table.type'), enableSorting: false, meta: { label: t('admin:servers.table.type'), className: 'w-28' },
      cell: ctx => <TypeCell s={ctx.row.original} /> },
    { id: 'status', header: t('admin:servers.table.status'), enableSorting: false, meta: { label: t('admin:servers.table.status'), className: FIT + ' w-[22%]' },
      cell: ctx => <StatusColumn s={ctx.row.original} /> },
    { id: 'panel_version', header: t('admin:servers.table.version'), enableSorting: true, meta: { label: t('admin:servers.table.version'), className: FIT + ' w-[24%]' },
      cell: ctx => <VersionColumn s={ctx.row.original} /> },
    { id: 'counts', header: t('admin:servers.table.counts'), enableSorting: false, meta: { label: t('admin:servers.table.counts'), align: 'right', className: 'w-24' },
      cell: ctx => <CountsColumn s={ctx.row.original} /> },
    { id: 'url', header: t('admin:servers.table.url'), enableSorting: true, meta: { label: t('admin:servers.table.url'), defaultHidden: true, className: 'font-mono text-xs' },
      cell: ctx => <span className="truncate" title={ctx.row.original.url}>{ctx.row.original.url}</span> },
    { id: 'remark', header: t('admin:servers.table.remark'), enableSorting: false, meta: { label: t('admin:servers.table.remark'), defaultHidden: true },
      cell: ctx => ctx.row.original.remark ?? '' },
    { id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t('admin:servers.table.actions'), className: 'w-36' },
      cell: ctx => <ActionsColumn s={ctx.row.original} /> },
  ]
}

export function StatusColumn({ s }: { s: Server }) {
  const c = useServersCtx()
  const access = c.accessOf(s)
  return <StatusCell s={s} probe={c.probeOf(s)} access={access} onOpenAccess={() => access && c.onOpenAccess(s, access)} />
}

export function VersionColumn({ s }: { s: Server }) {
  const c = useServersCtx()
  return <VersionCell s={s} nodeReleases={c.nodeReleases} nodeReleaseCheckFailed={c.nodeReleaseCheckFailed} suiReleaseVersion={c.suiReleaseVersion} />
}

export function CountsColumn({ s }: { s: Server }) {
  return <CountsCell counts={useServersCtx().countsOf(s)} />
}

/** ONE visible action: the connection test, the one an operator repeats. It shows its own wait for the whole request. */
export function ActionsColumn({ s }: { s: Server }) {
  const { t } = useTranslation('admin')
  const c = useServersCtx()
  return (
    <div className="flex items-center justify-end gap-1">
      <AsyncButton variant="outline" size="sm" disabled={c.testing !== null && c.testing !== s.id} pending={c.testing === s.id} onClick={() => c.onTest(s)}>
        {t('admin:servers.action.test')}
      </AsyncButton>
      <MoreMenu items={c.menuOf(s)} size="icon-xs" label={t('admin:servers.action.more_for', { name: s.name })} />
    </div>
  )
}
