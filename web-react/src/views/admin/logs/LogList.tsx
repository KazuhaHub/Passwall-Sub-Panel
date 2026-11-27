import { useEffect, useMemo, type ReactNode } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import type { BulkContext, RowOptions } from '@/components/data/dataTableTypes'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { DataTable } from '@/components/data/DataTable'
import { DetailDialog } from '@/components/data/DetailDialog'
import { useRecordNav } from '@/components/data/useRecordNav'
import { FilterBar, type FilterBarFacet } from '@/components/data/FilterBar'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { ResultCount, useAdminT } from './shared'
import type { LogView } from './useLogView'

/** The part of a query a list needs; the page's own query hook satisfies it. */
export interface ListQuery<T> {
  data?: { items: T[]; total: number }
  isPending: boolean
  isError: boolean
  error: unknown
  isFetching: boolean
  refetch: () => Promise<unknown>
}

export interface LogListProps<T> {
  label: string
  /** Where the column widths an admin chose are kept (DataTable.tableId). One per list. */
  tableId: string
  view: LogView
  query: ListQuery<T>
  /** Must be stable (useMemo): DataTable rebuilds every row when it changes. */
  columns: ColumnDef<T, any>[]
  getId: (row: T) => number
  /** The row at phone width. */
  card: (row: T) => ReactNode
  searchPlaceholder?: string
  facets?: FilterBarFacet[]
  /** Chips and the like, in the filter row after the search box. */
  chips?: ReactNode
  /** What sits at the right end of the filter row (retention, clear). */
  actions?: ReactNode
  /** Anything beyond the keyword narrows the list: a facet, a user filter. */
  narrowed?: boolean
  onClearAll: () => void
  detail: (row: T) => { title: ReactNode; status?: ReactNode; actions?: ReactNode; body: ReactNode }
  /** Row checkboxes and the bulk bar, for a list with actions on several rows. */
  selection?: {
    value: Record<string, boolean>
    onChange: (next: Record<string, boolean>) => void
    rowLabel: (row: T) => string
    bulkActions: (ctx: BulkContext<T>) => ReactNode
    rowOptions?: (row: T) => RowOptions | undefined
  }
  /** One sentence for a list with nothing in it at all. */
  emptyTitle: string
  emptyDescription?: string
  /** Wording that names the records; the defaults say "logs". */
  words?: { loadFailed: string; refreshFailed: string; filteredEmpty: string }
}

/**
 * One server-paged record list: filter row with the result count, the table (cards on a phone), and
 * the record in a dialog. The keyword, the page and the open record live in the URL (useLogView).
 */
export function LogList<T>(p: LogListProps<T>) {
  const t = useAdminT()
  const { view, query } = p
  const items = query.data?.items
  const record = view.sel.selected === null ? undefined : items?.find(r => p.getId(r) === view.sel.selected)
  const { selected, clear } = view.sel
  // Previous / next walk the rows on this page, in the order the table shows them.
  const keys = useMemo(() => (items ?? []).map(p.getId), [items, p.getId])
  const nav = useRecordNav(keys, selected, view.sel.select)

  // A link to a record that is not on this page (another page, another filter, purged since) opens
  // nothing; drop the stale parameter once the list has settled instead of leaving it in the address.
  const settled = !query.isFetching && items !== undefined
  useEffect(() => {
    if (settled && selected !== null && !record) clear()
  }, [settled, selected, record, clear])

  const words = p.words ?? { loadFailed: t('logs.load_failed'), refreshFailed: t('logs.refresh_failed'), filteredEmpty: t('logs.empty_filtered') }
  if (query.isError && !query.data) {
    return <ErrorState title={words.loadFailed} reason={query.error} retry={() => query.refetch()} />
  }

  const filtered = view.q !== '' || p.narrowed === true
  const empty = filtered
    ? <EmptyState compact title={words.filteredEmpty} action={<Button variant="outline" size="sm" onClick={p.onClearAll}>{t('logs.clear_filters')}</Button>} />
    : <EmptyState compact title={p.emptyTitle} description={p.emptyDescription} />
  const shown = record ? p.detail(record) : null

  return (
    <div>
      {query.isError && (
        // The rows below are from the last read, not a clean history: say so rather than staying silent.
        <Alert variant="destructive" className="mb-3">
          <AlertDescription>{words.refreshFailed}</AlertDescription>
        </Alert>
      )}
      <DataTable<T> tableId={p.tableId}
        aria-label={p.label}
        mode="server"
        data={items ?? []}
        total={query.data?.total}
        columns={p.columns}
        getRowId={r => String(p.getId(r))}
        loading={query.isPending}
        page={view.page}
        onPageChange={view.setPage}
        pageSize={view.pageSize}
        onPageSizeChange={view.setPageSize}
        selectable={p.selection !== undefined}
        selection={p.selection?.value}
        onSelectionChange={p.selection?.onChange}
        rowLabel={p.selection?.rowLabel}
        bulkActions={p.selection?.bulkActions}
        rowOptions={p.selection?.rowOptions}
        onRowOpen={r => view.sel.select(p.getId(r))}
        activeRowId={view.sel.selected === null ? null : String(view.sel.selected)}
        renderCard={r => p.card(r)}
        maxHeight={null}
        toolbar={(
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <FilterBar
              aria-label={p.label}
              search={p.searchPlaceholder ? { value: view.q, onChange: view.setQ, placeholder: p.searchPlaceholder } : undefined}
              facets={p.facets}
              onClearAll={p.onClearAll}
            >
              {p.chips}
            </FilterBar>
            <ResultCount total={query.data?.total} />
            {p.actions && <div className="ml-auto flex items-center gap-2">{p.actions}</div>}
          </div>
        )}
        empty={empty}
      />
      <DetailDialog
        open={shown !== null}
        size="lg"
        nav={nav}
        onClose={view.sel.clear}
        title={shown?.title ?? ''}
        status={shown?.status}
        actions={shown?.actions}
      >
        {shown?.body}
      </DetailDialog>
    </div>
  )
}
