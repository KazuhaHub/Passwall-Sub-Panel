import { useEffect, useId, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import {
  flexRender, getCoreRowModel, getExpandedRowModel, getPaginationRowModel, getSortedRowModel, useReactTable,
  type ColumnDef, type ExpandedState, type Row, type SortingState, type VisibilityState,
} from '@tanstack/react-table'
import {
  ArrowDownIcon, ArrowUpDownIcon, ArrowUpIcon, ChevronRightIcon, GripVerticalIcon, SearchXIcon, TriangleAlertIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { cn } from '@/lib/utils'
import { BulkActionBar } from './BulkActionBar'
import { ColumnMenu, columnLabel } from './ColumnMenu'
import { DataTablePagination } from './DataTablePagination'
import { DEFAULT_PAGE_SIZE, clampPage } from './dataTableLogic'
import type { BulkContext, DataTableProps } from './dataTableTypes'
import { TipButton } from './TipButton'
import { useRowReorder } from './useRowReorder'

// Anything inside a row that does its own thing on click or on keys: a click or
// Enter there must not also open the row.
const OWN_CLICK = 'a,button,input,select,textarea,label,[role="checkbox"],[role="combobox"],[role="menuitem"],[role="switch"],[data-no-row-click]'

// Widths of the leading columns, so the first data column can stick right after them.
const DRAG_W = 2 // rem
const SELECT_W = 2.5 // rem

function useControlled<V>(value: V | undefined, onChange: ((v: V) => void) | undefined, initial: V): [V, (v: V) => void] {
  const [own, setOwn] = useState(initial)
  const controlled = value !== undefined
  return [controlled ? value : own, (v: V) => { if (!controlled) setOwn(v); onChange?.(v) }]
}

function columnIdOf<T>(def: ColumnDef<T, never>): string | undefined {
  if (def.id) return def.id
  return 'accessorKey' in def && def.accessorKey !== undefined ? String(def.accessorKey) : undefined
}

function DefaultError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useTranslation('common')
  const detail = error instanceof Error ? error.message : typeof error === 'string' ? error : null
  return (
    <div role="alert" className="flex flex-col items-center gap-2 px-4 py-10 text-center">
      <TriangleAlertIcon className="size-6 text-destructive" aria-hidden="true" />
      <p className="text-sm font-medium">{t('ui.data.error')}</p>
      <p className="text-sm text-muted-foreground">{detail ?? t('ui.data.error_hint')}</p>
      {onRetry && <Button variant="outline" size="sm" onClick={onRetry}>{t('ui.data.retry')}</Button>}
    </div>
  )
}

/**
 * The shared table: TanStack Table for the model, components/ui/table for the
 * markup, and everything a list page needs around it.
 *
 * TWO DATA MODES. `client` (default): `data` is every row; sorting and paging
 * happen here, and the caller filters beforehand (filterRows). `server`: `data`
 * is the page the API returned; sorting and paging are the caller's, reported
 * through onSortingChange / onPageChange / onPageSizeChange, and `total` is the
 * count across pages. Paging and sorting state can be held by the caller (pass
 * page, pageSize, sorting, ...) or left to the table; the same goes for column
 * visibility, selection and expansion, so a page that wants none of it passes
 * nothing.
 *
 * Page numbers are 1-based, as in the URL and the API.
 */
export function DataTable<T>(props: DataTableProps<T>) {
  const {
    data, columns, getRowId, mode = 'client', total: serverTotal, pagination = true, pageSizeOptions,
    columnMenu = true, selectable, rowLabel, bulkActions, onRowOpen, activeRowId, rowClassName, toolbar,
    loading, error, onRetry, empty, onReorder, getSubRows, defaultExpanded, renderCard, maxHeight = '70dvh', className,
  } = props
  const { t } = useTranslation('common')
  const server = mode === 'server'

  // ---- state: each piece is the caller's if they pass it, the table's otherwise ----
  const [page, setPage] = useControlled(props.page, props.onPageChange, 1)
  const [pageSize, setPageSize] = useControlled(props.pageSize, props.onPageSizeChange, DEFAULT_PAGE_SIZE)
  const [sorting, setSorting] = useControlled<SortingState>(props.sorting, props.onSortingChange, [])
  const initialVisibility = useMemo(() => {
    const v: VisibilityState = {}
    for (const def of columns) {
      const id = columnIdOf(def as ColumnDef<T, never>)
      if (id && def.meta?.defaultHidden) v[id] = false
    }
    return v
    // Only the first render's columns decide the starting visibility.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const [chosenVisibility, setVisibility] = useControlled<VisibilityState>(props.columnVisibility, props.onColumnVisibilityChange, initialVisibility)
  // A caller that holds the state (useTableViewState) starts from {} and would
  // otherwise show defaultHidden columns; explicit choices still win.
  const visibility = useMemo(() => ({ ...initialVisibility, ...chosenVisibility }), [initialVisibility, chosenVisibility])
  const [selection, setSelection] = useControlled<Record<string, boolean>>(props.selection, props.onSelectionChange, {})
  const [expanded, setExpanded] = useControlled<ExpandedState>(props.expanded, props.onExpandedChange, defaultExpanded ? true : {})

  const tree = getSubRows !== undefined
  const total = server ? (serverTotal ?? data.length) : data.length
  const effectivePage = pagination ? clampPage(page, total, pageSize) : 1

  // The page shrank under the caller (rows deleted, a filter applied): bring them back.
  useEffect(() => {
    if (pagination && page !== effectivePage) setPage(effectivePage)
    // setPage is recreated each render; the page numbers are the trigger.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, effectivePage, pagination])

  const table = useReactTable<T>({
    data: data as T[],
    columns,
    getRowId,
    state: {
      sorting,
      columnVisibility: visibility,
      rowSelection: selection,
      expanded,
      pagination: { pageIndex: effectivePage - 1, pageSize: pagination ? pageSize : Math.max(data.length, 1) },
    },
    onSortingChange: u => setSorting(typeof u === 'function' ? u(sorting) : u),
    onColumnVisibilityChange: u => setVisibility(typeof u === 'function' ? u(visibility) : u),
    onRowSelectionChange: u => setSelection(typeof u === 'function' ? u(selection) : u),
    onExpandedChange: u => setExpanded(typeof u === 'function' ? u(expanded) : u),
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: server ? undefined : getSortedRowModel(),
    // Also when pagination is off: with paginateExpandedRows false, tanstack applies
    // row expansion inside the pagination model, so leaving it out loses tree children.
    getPaginationRowModel: server ? undefined : getPaginationRowModel(),
    getExpandedRowModel: tree ? getExpandedRowModel() : undefined,
    getSubRows,
    manualSorting: server,
    manualPagination: server,
    paginateExpandedRows: false,
    enableSubRowSelection: false,
    enableMultiSort: false,
    // First click is ascending for every column, numbers included (tanstack
    // guesses descending for numbers), so the arrow always means the same thing.
    sortDescFirst: false,
  })

  const rows = table.getRowModel().rows
  const topLevelShown = tree ? rows.filter(r => r.depth === 0).length : rows.length
  const rowIds = useMemo(() => rows.map(r => r.id), [rows])
  const visibleColumns = table.getVisibleLeafColumns()
  const selectedIds = Object.keys(selection).filter(id => selection[id])
  const nameOf = (row: Row<T>) => rowLabel?.(row.original) ?? row.id

  // ---- reorder ----
  const hintId = `${useId()}-reorder-hint`
  const bodyRef = useRef<HTMLTableSectionElement>(null)
  const frameRef = useRef<HTMLDivElement>(null)
  const reorderOn = onReorder !== undefined && !tree
  const reorder = useRowReorder({
    enabled: reorderOn && sorting.length === 0,
    ids: rowIds,
    nameOf: id => { const r = rows.find(x => x.id === id); return r ? nameOf(r) : id },
    onReorder,
    bodyRef,
    scrollRef: frameRef,
  })

  // ---- row focus and keys ----
  const [tabId, setTabId] = useState<string | null>(null)
  const tabStop = tabId !== null && rowIds.includes(tabId) ? tabId : rowIds[0]
  const interactive = onRowOpen !== undefined || selectable === true || tree

  const onRowKeyDown = (e: KeyboardEvent<HTMLTableRowElement>, row: Row<T>) => {
    // Only when the row itself has focus: keys typed into an input or a menu in a cell are theirs.
    if (e.target !== e.currentTarget || e.defaultPrevented) return
    const tr = e.currentTarget
    if (e.key === 'Enter' && onRowOpen) { e.preventDefault(); onRowOpen(row.original); return }
    if (e.key === ' ' && selectable) { e.preventDefault(); row.toggleSelected(); return }
    if (tree && e.key === 'ArrowRight' && row.getCanExpand() && !row.getIsExpanded()) { e.preventDefault(); row.toggleExpanded(true); return }
    if (tree && e.key === 'ArrowLeft') {
      e.preventDefault()
      if (row.getIsExpanded()) row.toggleExpanded(false)
      else if (row.getParentRow()) tr.parentElement?.querySelector<HTMLElement>(`[data-id="${CSS.escape(row.getParentRow()!.id)}"]`)?.focus()
      return
    }
    let next: Element | null | undefined
    if (e.key === 'ArrowDown') next = tr.nextElementSibling
    else if (e.key === 'ArrowUp') next = tr.previousElementSibling
    else if (e.key === 'Home') next = tr.parentElement?.firstElementChild
    else if (e.key === 'End') next = tr.parentElement?.lastElementChild
    else return
    e.preventDefault()
    if (next instanceof HTMLElement && next.hasAttribute('data-row')) next.focus()
  }
  const onRowClick = (e: MouseEvent<HTMLElement>, row: Row<T>) => {
    if (!onRowOpen || e.defaultPrevented || e.button !== 0) return
    // A click inside a popover rendered in a portal bubbles through React but is not in the row.
    if (!e.currentTarget.contains(e.target as Node)) return
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    if ((e.target as HTMLElement).closest(OWN_CLICK)) return
    const sel = window.getSelection()
    if (sel && !sel.isCollapsed && sel.anchorNode && e.currentTarget.contains(sel.anchorNode)) return
    onRowOpen(row.original)
  }

  // ---- layout bits ----
  const phone = useMediaQuery('(max-width: 767px)')
  const cards = phone && renderCard !== undefined
  const leadWidth = (reorderOn ? DRAG_W : 0) + (selectable ? SELECT_W : 0)
  const pin = (first: boolean): { className?: string; style?: CSSProperties } =>
    first ? { className: 'sticky z-[1]', style: { left: `${leadWidth}rem` } } : {}
  const colSpan = visibleColumns.length + (reorderOn ? 1 : 0) + (selectable ? 1 : 0)
  const allOnPage = rows.length > 0 && rows.every(r => selection[r.id])
  const someOnPage = rows.some(r => selection[r.id])
  const toggleAll = (on: boolean) => {
    const next = { ...selection }
    for (const r of rows) { if (on) next[r.id] = true; else delete next[r.id] }
    setSelection(next)
  }
  const clear = () => setSelection({})
  const bulkCtx: BulkContext<T> = {
    selectedIds,
    selectedRows: rows.filter(r => selection[r.id]).map(r => r.original),
    clear,
  }

  const showError = error !== undefined && error !== null && error !== false
  const skeleton = loading === true && data.length === 0 && !showError
  const isEmpty = !loading && !showError && rows.length === 0

  const bodyContent = (): ReactNode => {
    if (showError) {
      return <TableRow className="hover:bg-transparent"><TableCell colSpan={colSpan} className="whitespace-normal"><DefaultError error={error} onRetry={onRetry} /></TableCell></TableRow>
    }
    if (skeleton) {
      return Array.from({ length: 6 }, (_, i) => (
        <TableRow key={i} data-skeleton-row="" className="hover:bg-transparent">
          {Array.from({ length: colSpan }, (_, j) => (
            <TableCell key={j}><Skeleton className={cn('h-4', j === 0 ? 'w-32' : 'w-20')} /></TableCell>
          ))}
        </TableRow>
      ))
    }
    if (isEmpty) {
      return (
        <TableRow className="hover:bg-transparent">
          <TableCell colSpan={colSpan} className="h-32 whitespace-normal text-center text-muted-foreground">
            {empty ?? (
              <span className="inline-flex items-center gap-2"><SearchXIcon className="size-4" aria-hidden="true" />{t('ui.data.empty')}</span>
            )}
          </TableCell>
        </TableRow>
      )
    }
    return rows.map((row, index) => {
      const selected = selection[row.id] === true
      const active = activeRowId !== undefined && activeRowId !== null && activeRowId === row.id
      const dropBefore = reorder.insertion === index
      const dropAfter = reorder.insertion === rows.length && index === rows.length - 1
      const moving = reorder.movingId === row.id
      return (
        <TableRow
          key={row.id}
          data-row=""
          data-id={row.id}
          data-depth={tree ? row.depth : undefined}
          data-state={selected ? 'selected' : undefined}
          data-active={active ? 'true' : undefined}
          data-drop={dropBefore ? 'before' : dropAfter ? 'after' : undefined}
          tabIndex={interactive ? (row.id === tabStop ? 0 : -1) : undefined}
          onFocus={interactive ? e => { if (e.target === e.currentTarget) setTabId(row.id) } : undefined}
          onClick={onRowOpen ? e => onRowClick(e, row) : undefined}
          onKeyDown={interactive ? e => onRowKeyDown(e, row) : undefined}
          className={cn(
            'group/row outline-none focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring',
            onRowOpen && 'cursor-pointer',
            active && 'bg-brand/[0.07] hover:bg-brand/10',
            moving && 'opacity-50',
            dropBefore && '[&>td]:shadow-[inset_0_2px_0_0_var(--brand)]',
            dropAfter && '[&>td]:shadow-[inset_0_-2px_0_0_var(--brand)]',
            rowClassName?.(row.original),
          )}
        >
          {reorderOn && (
            <TableCell data-no-row-click="" className="sticky left-0 z-[1] w-8 min-w-8 bg-card px-0 text-center group-hover/row:bg-muted group-data-[state=selected]/row:bg-muted">
              <DragHandle
                disabled={sorting.length > 0}
                name={nameOf(row)}
                onPointerDown={e => reorder.onPointerDown(e, row.id)}
                onKeyDown={e => reorder.onKeyDown(e, row.id)}
                onBlur={() => reorder.onBlur(row.id)}
                describedBy={hintId}
              />
            </TableCell>
          )}
          {selectable && (
            <TableCell
              data-no-row-click=""
              className="sticky z-[1] w-10 min-w-10 bg-card pr-0 group-hover/row:bg-muted group-data-[state=selected]/row:bg-muted"
              style={{ left: `${reorderOn ? DRAG_W : 0}rem` }}
            >
              <Checkbox checked={selected} onCheckedChange={v => row.toggleSelected(v === true)} aria-label={rowLabel ? t('ui.data.select_row_named', { name: nameOf(row), defaultValue: `Select ${nameOf(row)}` }) : t('ui.data.select_row', { n: index + 1 + (effectivePage - 1) * pageSize })} />
            </TableCell>
          )}
          {row.getVisibleCells().map((cell, ci) => {
            const meta = cell.column.columnDef.meta
            const first = ci === 0
            const p = pin(first)
            return (
              <TableCell
                key={cell.id}
                style={p.style}
                className={cn(
                  p.className && `${p.className} bg-card group-hover/row:bg-muted group-data-[state=selected]/row:bg-muted group-data-[active=true]/row:bg-[color-mix(in_oklab,var(--brand)_8%,var(--card))]`,
                  meta?.align === 'right' && 'text-right',
                  meta?.align === 'center' && 'text-center',
                  meta?.className,
                )}
              >
                {first && tree ? (
                  <div className="flex items-center gap-1" style={{ paddingLeft: `${row.depth * 1.25}rem` }}>
                    {row.getCanExpand() ? (
                      <TipButton
                        label={t(row.getIsExpanded() ? 'ui.data.collapse_row' : 'ui.data.expand_row', { name: nameOf(row) })}
                        size="icon-xs"
                        aria-expanded={row.getIsExpanded()}
                        data-no-row-click=""
                        onClick={() => row.toggleExpanded()}
                      >
                        <ChevronRightIcon className={cn('transition-transform', row.getIsExpanded() && 'rotate-90')} />
                      </TipButton>
                    ) : <span className="inline-block size-6 shrink-0" aria-hidden="true" />}
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </div>
                ) : flexRender(cell.column.columnDef.cell, cell.getContext())}
              </TableCell>
            )
          })}
        </TableRow>
      )
    })
  }

  return (
    <div data-slot="data-table" className={cn('flex min-w-0 flex-col', className)}>
      {(toolbar || columnMenu) && (
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <div className="min-w-0 flex-1">{toolbar}</div>
          {columnMenu && !cards && <ColumnMenu table={table} />}
        </div>
      )}

      <div className="overflow-hidden rounded-xl border bg-card">
        {cards ? (
          <ul aria-busy={loading || undefined} className="divide-y">
            {showError && <li className="list-none"><DefaultError error={error} onRetry={onRetry} /></li>}
            {skeleton && Array.from({ length: 4 }, (_, i) => <li key={i} className="p-3"><Skeleton className="h-12 w-full" /></li>)}
            {isEmpty && <li className="p-8 text-center text-sm text-muted-foreground">{empty ?? t('ui.data.empty')}</li>}
            {!showError && rows.map(row => {
              const selected = selection[row.id] === true
              const active = activeRowId === row.id
              const open = onRowOpen ? () => onRowOpen(row.original) : undefined
              return (
                <li
                  key={row.id}
                  data-id={row.id}
                  data-state={selected ? 'selected' : undefined}
                  data-active={active ? 'true' : undefined}
                  tabIndex={open ? 0 : undefined}
                  onClick={open ? e => onRowClick(e, row) : undefined}
                  onKeyDown={open ? e => { if (e.target === e.currentTarget && e.key === 'Enter') { e.preventDefault(); open() } } : undefined}
                  className={cn('flex items-start gap-2 p-3 outline-none focus-visible:bg-muted', open && 'cursor-pointer', selected && 'bg-muted', active && 'bg-brand/[0.07]')}
                >
                  {selectable && (
                    <Checkbox
                      data-no-row-click=""
                      className="mt-0.5"
                      checked={selected}
                      onCheckedChange={v => row.toggleSelected(v === true)}
                      aria-label={rowLabel ? t('ui.data.select_row_named', { name: nameOf(row), defaultValue: `Select ${nameOf(row)}` }) : t('ui.data.select_row', { n: rows.indexOf(row) + 1 })}
                    />
                  )}
                  <div className="min-w-0 flex-1">{renderCard(row.original, { selected, active, open })}</div>
                </li>
              )
            })}
          </ul>
        ) : (
          <div ref={frameRef} className="overflow-auto" style={maxHeight ? { maxHeight } : undefined}>
            <table data-slot="table" aria-label={props['aria-label']} aria-busy={loading || undefined} className="w-full caption-bottom text-sm">
              <TableHeader className="sticky top-0 z-10 bg-[color-mix(in_oklab,var(--muted),var(--card)_40%)] shadow-[0_1px_0_0_var(--border)] [&_tr]:border-b-0">
                <tr className="h-(--row-h)">
                  {reorderOn && <TableHead className="sticky left-0 z-20 w-8 min-w-8 bg-[color-mix(in_oklab,var(--muted),var(--card)_40%)] px-0"><span className="sr-only">{t('ui.data.drag_handle', { name: '' })}</span></TableHead>}
                  {selectable && (
                    <TableHead className="sticky z-20 w-10 min-w-10 bg-[color-mix(in_oklab,var(--muted),var(--card)_40%)]" style={{ left: `${reorderOn ? DRAG_W : 0}rem` }}>
                      <Checkbox
                        checked={allOnPage}
                        indeterminate={!allOnPage && someOnPage}
                        onCheckedChange={v => toggleAll(v === true)}
                        aria-label={t('ui.data.select_all')}
                      />
                    </TableHead>
                  )}
                  {table.getHeaderGroups()[0]?.headers.map((header, hi) => {
                    const col = header.column
                    const meta = col.columnDef.meta
                    const sorted = col.getIsSorted()
                    const canSort = col.getCanSort()
                    const p = pin(hi === 0)
                    const label = flexRender(col.columnDef.header, header.getContext())
                    return (
                      <TableHead
                        key={header.id}
                        scope="col"
                        aria-sort={canSort ? (sorted === 'asc' ? 'ascending' : sorted === 'desc' ? 'descending' : 'none') : undefined}
                        style={p.style}
                        className={cn(
                          p.className && `${p.className} z-20 bg-[color-mix(in_oklab,var(--muted),var(--card)_40%)]`,
                          meta?.align === 'right' && 'text-right',
                          meta?.align === 'center' && 'text-center',
                          meta?.headerClassName,
                        )}
                      >
                        {header.isPlaceholder ? null : canSort ? (
                          <button
                            type="button"
                            onClick={col.getToggleSortingHandler()}
                            title={t('ui.data.sort_by', { column: columnLabel(col) })}
                            className={cn(
                              '-mx-1.5 inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 font-medium outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50',
                              meta?.align === 'right' && 'flex-row-reverse',
                            )}
                          >
                            {label}
                            {sorted === 'asc' ? <ArrowUpIcon className="size-3.5" aria-hidden="true" />
                              : sorted === 'desc' ? <ArrowDownIcon className="size-3.5" aria-hidden="true" />
                                : <ArrowUpDownIcon className="size-3.5 opacity-40" aria-hidden="true" />}
                          </button>
                        ) : label}
                      </TableHead>
                    )
                  })}
                </tr>
              </TableHeader>
              <TableBody ref={bodyRef}>{bodyContent()}</TableBody>
            </table>
          </div>
        )}

        {pagination && !showError && (
          <DataTablePagination
            page={effectivePage}
            pageSize={pageSize}
            total={total}
            shown={topLevelShown}
            disabled={loading && data.length === 0}
            pageSizeOptions={pageSizeOptions}
            onPageChange={setPage}
            onPageSizeChange={n => { setPageSize(n); setPage(1) }}
          />
        )}
      </div>

      {reorderOn && (
        <>
          <p id={hintId} className="sr-only">{t('ui.data.reorder_hint')}</p>
          <p role="status" className="sr-only">{reorder.announcement}</p>
        </>
      )}
      {(bulkActions || selectable) && <BulkActionBar count={selectedIds.length} onClear={clear}>{bulkActions?.(bulkCtx)}</BulkActionBar>}
    </div>
  )
}

function DragHandle({ name, disabled, describedBy, ...handlers }: {
  name: string
  disabled?: boolean
  describedBy: string
  onPointerDown: (e: React.PointerEvent) => void
  onKeyDown: (e: React.KeyboardEvent) => void
  onBlur: () => void
}) {
  const { t } = useTranslation('common')
  return (
    <TipButton
      label={t('ui.data.drag_handle', { name })}
      size="icon-xs"
      disabled={disabled}
      aria-describedby={describedBy}
      aria-roledescription="sortable"
      // touch-action none: without it a finger drag on the handle scrolls the page instead.
      className="cursor-grab touch-none text-muted-foreground active:cursor-grabbing"
      {...handlers}
    >
      <GripVerticalIcon />
    </TipButton>
  )
}
