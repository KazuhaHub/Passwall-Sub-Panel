import { memo, useEffect, useId, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import {
  flexRender, getCoreRowModel, getExpandedRowModel, getPaginationRowModel, getSortedRowModel, useReactTable,
  type ColumnDef, type ExpandedState, type Row, type SortingState, type VisibilityState,
} from '@tanstack/react-table'
import {
  ArrowDownIcon, ArrowUpDownIcon, ArrowUpIcon, ChevronRightIcon, GripVerticalIcon, SearchXIcon,
} from 'lucide-react'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { useSpacedT } from '@/i18n/spacing'
import { cn } from '@/lib/utils'
import { BulkActionBar } from './BulkActionBar'
import { ColumnMenu, columnLabel } from './ColumnMenu'
import { DataTablePagination } from './DataTablePagination'
import { DEFAULT_PAGE_SIZE, clampPage, compareCells, makeCollator } from './dataTableLogic'
import type { BulkContext, DataTableProps, PageChangeReason, RowOptions } from './dataTableTypes'
import { IconButton } from '@/components/common/IconButton'
import { TOUCH_HEIGHT } from '@/components/common/touchTarget'
import { useRowReorder } from './useRowReorder'

// Anything inside a row that does its own thing on click or on keys: a click or
// Enter there must not also open the row.
const OWN_CLICK = 'a,button,input,select,textarea,label,[role="checkbox"],[role="combobox"],[role="menuitem"],[role="switch"],[data-no-row-click]'

// Widths of the leading columns, so the first data column can stick right after them.
const DRAG_W = 2 // rem
const SELECT_W = 2.5 // rem

// A pinned cell must be opaque (scrolled content passes behind it), so it cannot
// inherit the row's translucent tint: each row state is repeated here as the
// equivalent opaque colour.
const PIN_BG = 'bg-card group-hover/row:bg-muted group-data-[state=selected]/row:bg-muted group-data-[row-state=pending]/row:bg-[color-mix(in_oklab,var(--warning)_7%,var(--card))] group-data-[highlight=true]/row:bg-[color-mix(in_oklab,var(--brand)_9%,var(--card))] group-data-[active=true]/row:bg-[color-mix(in_oklab,var(--brand)_8%,var(--card))]'
const HEAD_BG = 'bg-[color-mix(in_oklab,var(--muted),var(--card)_40%)]'
const EMPTY_OPTIONS: RowOptions = {}

function useControlled<V>(value: V | undefined, onChange: ((v: V) => void) | undefined, initial: V): [V, (v: V) => void] {
  const [own, setOwn] = useState(initial)
  const controlled = value !== undefined
  return [controlled ? value : own, (v: V) => { if (!controlled) setOwn(v); onChange?.(v) }]
}

function columnIdOf<T>(def: ColumnDef<T, never>): string | undefined {
  if (def.id) return def.id
  return 'accessorKey' in def && def.accessorKey !== undefined ? String(def.accessorKey) : undefined
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
 * nothing. Page numbers are 1-based, as in the URL and the API.
 *
 * PAGE CORRECTIONS. If the page asked for no longer exists (rows were deleted),
 * the table asks for the last one that does, with reason 'correction' so a
 * URL-backed page can REPLACE rather than push. It never corrects while loading or
 * while the total is unknown: a deep link to page 3 must still be page 3 when the
 * first response arrives.
 *
 * SELECTION is by row id and outlives paging and filtering. The bulk bar counts
 * every selected id and says how many are not on screen, with a button to drop
 * them; the bulk callback gets all the ids, and the rows `data` still holds.
 *
 * ROW OVERRIDES (rowOptions): a row can render as a full-width separator, be
 * excluded from selection or opening, and carry a dimmed / pending state, a class
 * and attributes. onRowHover and highlightedIds let a page light up related rows.
 */
export function DataTable<T>(props: DataTableProps<T>) {
  const {
    data, columns, getRowId, mode = 'client', total: serverTotal, pagination = true, pageSizeOptions,
    columnMenu = true, selectable, rowLabel, bulkActions, onRowOpen, activeRowId, rowClassName, toolbar,
    loading, error, onRetry, empty, onReorder, getSubRows, defaultExpanded, renderCard, maxHeight = '70dvh', className,
    rowOptions, onRowHover, highlightedIds,
  } = props
  const { t, i18n } = useSpacedT()
  const server = mode === 'server'

  // ---- state: each piece is the caller's if they pass it, the table's otherwise ----
  const [page, setPageRaw] = useControlled<number>(props.page, undefined, 1)
  const setPage = (p: number, reason: PageChangeReason = 'user') => {
    if (props.page === undefined) setPageRaw(p)
    props.onPageChange?.(p, reason)
  }
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
  // Until the first response there is no total to measure the page against.
  const settled = !loading && (!server || serverTotal !== undefined)
  const effectivePage = pagination && settled ? clampPage(page, total, pageSize) : page

  useEffect(() => {
    if (pagination && settled && page !== effectivePage) props.onPageChange?.(effectivePage, 'correction')
    if (pagination && settled && page !== effectivePage && props.page === undefined) setPageRaw(effectivePage)
    // The callbacks are recreated each render; the page numbers are the trigger.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, effectivePage, pagination, settled])

  // Text sorts with the UI language's collation (pinyin under zh), numbers as
  // numbers, empties last in both directions; see compareCells.
  const collator = useMemo(() => makeCollator(i18n.language), [i18n.language])
  const sortingRef = useRef(sorting)
  sortingRef.current = sorting

  const paginationState = useMemo(
    () => ({ pageIndex: Math.max(effectivePage, 1) - 1, pageSize: pagination ? pageSize : Math.max(data.length, 1) }),
    [effectivePage, pageSize, pagination, data.length],
  )
  const table = useReactTable<T>({
    data: data as T[],
    columns,
    getRowId,
    state: {
      sorting,
      columnVisibility: visibility,
      rowSelection: selection,
      expanded,
      pagination: paginationState,
    },
    defaultColumn: {
      // false: undefined is an ordinary value to compareCells, not tanstack's flip-with-direction default.
      sortUndefined: false,
      sortingFn: (a, b, id) => compareCells(a.getValue(id), b.getValue(id), sortingRef.current.find(x => x.id === id)?.desc === true, collator),
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
  const optsOf = (row: Row<T>): RowOptions => rowOptions?.(row.original) ?? EMPTY_OPTIONS
  const canSelect = (row: Row<T>) => selectable === true && optsOf(row).selectable !== false
  const canOpen = (row: Row<T>) => onRowOpen !== undefined && optsOf(row).openable !== false
  const nameOf = (row: Row<T>) => rowLabel?.(row.original) ?? row.id
  const highlighted = useMemo(() => new Set(highlightedIds ?? []), [highlightedIds])

  // ---- selection ----
  const selectedIds = Object.keys(selection).filter(id => selection[id])
  const shownIds = new Set(rowIds)
  const hiddenIds = selectedIds.filter(id => !shownIds.has(id))
  const coreById = table.getCoreRowModel().rowsById
  const selectableRows = rows.filter(canSelect)
  const allOnPage = selectableRows.length > 0 && selectableRows.every(r => selection[r.id])
  const someOnPage = selectableRows.some(r => selection[r.id])
  const toggleAll = (on: boolean) => {
    const next = { ...selection }
    for (const r of selectableRows) { if (on) next[r.id] = true; else delete next[r.id] }
    setSelection(next)
  }
  const clear = () => setSelection({})
  const clearHidden = () => {
    const next = { ...selection }
    for (const id of hiddenIds) delete next[id]
    setSelection(next)
  }
  const bulkCtx: BulkContext<T> = {
    ids: selectedIds,
    loadedRows: selectedIds.map(id => coreById[id]?.original).filter((r): r is T => r !== undefined),
    hiddenCount: hiddenIds.length,
    clear,
    clearHidden,
  }

  // ---- reorder ----
  const hintId = `${useId()}-reorder-hint`
  const reasonId = `${useId()}-reorder-reason`
  const bodyRef = useRef<HTMLTableSectionElement>(null)
  const frameRef = useRef<HTMLDivElement>(null)
  const reorderCol = onReorder !== undefined
  const blocked: 'sorted' | 'tree' | 'paged' | 'custom' | null = !reorderCol ? null
    : tree ? 'tree'
      : (pagination !== false || server) ? 'paged'
        : sorting.length > 0 ? 'sorted'
          : props.reorderBlockedReason ? 'custom' : null
  const blockedText = blocked === 'custom' ? props.reorderBlockedReason! : blocked ? t(`ui.data.reorder_blocked_${blocked}`) : ''
  useEffect(() => {
    if (reorderCol && (pagination !== false || server)) {
      console.error('DataTable: onReorder needs pagination={false} and client mode: a reorder is reported relative to the rows on screen, so on a page (or a server page) the last slot would move the row to the end of the whole list.')
    }
  }, [reorderCol, pagination, server])
  const reorder = useRowReorder({
    enabled: reorderCol && blocked === null,
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

  const onRowKeyDown = (e: KeyboardEvent<HTMLElement>, row: Row<T>) => {
    // Only when the row itself has focus: keys typed into an input or a menu in a cell are theirs.
    if (e.target !== e.currentTarget || e.defaultPrevented) return
    const el = e.currentTarget
    if (e.key === 'Enter' && canOpen(row)) { e.preventDefault(); onRowOpen!(row.original); return }
    if (e.key === ' ' && canSelect(row)) { e.preventDefault(); row.toggleSelected(); return }
    if (tree && e.key === 'ArrowRight' && row.getCanExpand() && !row.getIsExpanded()) { e.preventDefault(); row.toggleExpanded(true); return }
    if (tree && e.key === 'ArrowLeft') {
      e.preventDefault()
      if (row.getIsExpanded()) row.toggleExpanded(false)
      else if (row.getParentRow()) el.parentElement?.querySelector<HTMLElement>(`[data-id="${CSS.escape(row.getParentRow()!.id)}"]`)?.focus()
      return
    }
    let next: Element | null | undefined
    if (e.key === 'ArrowDown') next = el.nextElementSibling
    else if (e.key === 'ArrowUp') next = el.previousElementSibling
    else if (e.key === 'Home') next = el.parentElement?.querySelector('[data-row]')
    else if (e.key === 'End') { const all = el.parentElement?.querySelectorAll('[data-row]'); next = all?.[all.length - 1] }
    else return
    e.preventDefault()
    if (next instanceof HTMLElement && next.hasAttribute('data-row')) next.focus()
  }
  const onRowClick = (e: MouseEvent<HTMLElement>, row: Row<T>) => {
    if (!canOpen(row) || e.defaultPrevented || e.button !== 0) return
    // A click inside a popover rendered in a portal bubbles through React but is not in the row.
    if (!e.currentTarget.contains(e.target as Node)) return
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    if ((e.target as HTMLElement).closest(OWN_CLICK)) return
    const sel = window.getSelection()
    if (sel && !sel.isCollapsed && sel.anchorNode && e.currentTarget.contains(sel.anchorNode)) return
    onRowOpen!(row.original)
  }
  const rowEvents = (row: Row<T>) => ({
    'data-row': '',
    'data-id': row.id,
    tabIndex: interactive ? (row.id === tabStop ? 0 : -1) : undefined,
    onFocus: (e: React.FocusEvent<HTMLElement>) => { if (e.target === e.currentTarget) { setTabId(row.id); onRowHover?.(row.id) } },
    onBlur: (e: React.FocusEvent<HTMLElement>) => { if (e.target === e.currentTarget) onRowHover?.(null) },
    onMouseEnter: onRowHover ? () => onRowHover(row.id) : undefined,
    onMouseLeave: onRowHover ? () => onRowHover(null) : undefined,
    onClick: onRowOpen ? (e: MouseEvent<HTMLElement>) => onRowClick(e, row) : undefined,
    onKeyDown: interactive ? (e: KeyboardEvent<HTMLElement>) => onRowKeyDown(e, row) : undefined,
  })
  const rowStateClasses = (row: Row<T>, o: RowOptions) => cn(
    o.state === 'dimmed' && 'bg-muted/40 text-foreground/70 [&_.text-muted-foreground]:text-foreground/70',
    o.state === 'pending' && 'bg-warning/[0.07] [&>td:first-child]:shadow-[inset_3px_0_0_var(--warning)]',
    highlighted.has(row.id) && 'bg-brand/[0.09]',
    activeRowId === row.id && 'bg-brand/[0.07] hover:bg-brand/10',
    o.className,
    rowClassName?.(row.original),
  )
  const rowDataAttrs = (row: Row<T>, o: RowOptions, selected: boolean) => ({
    'data-state': selected ? 'selected' : undefined,
    'data-active': activeRowId === row.id ? 'true' : undefined,
    'data-row-state': o.state,
    'data-highlight': highlighted.has(row.id) ? 'true' : undefined,
    ...o.data,
  })

  // ---- layout bits ----
  const phone = useMediaQuery('(max-width: 767px)')
  const cards = phone && renderCard !== undefined
  const leadWidth = (reorderCol ? DRAG_W : 0) + (selectable ? SELECT_W : 0)
  const pin = (first: boolean): { className?: string; style?: CSSProperties } =>
    first ? { className: 'sticky z-[1]', style: { left: `${leadWidth}rem` } } : {}
  const colSpan = visibleColumns.length + (reorderCol ? 1 : 0) + (selectable ? 1 : 0)

  const showError = error !== undefined && error !== null && error !== false
  const skeleton = loading === true && data.length === 0 && !showError
  const isEmpty = !loading && !showError && rows.length === 0
  const checkLabel = (row: Row<T>, n: number) => rowLabel ? t('ui.data.select_row_named', { name: nameOf(row) }) : t('ui.data.select_row', { n })

  // Rows are memoised (DataRow): hovering one row must re-render that row and the
  // one it replaces, not all of them. The handlers rows need live behind one ref'd
  // object, so a row's props do not change just because the parent re-rendered.
  const apiRef = useRef<RowApi<T>>(null as unknown as RowApi<T>)
  apiRef.current = {
    focus: id => { setTabId(id); onRowHover?.(id) },
    blur: () => onRowHover?.(null),
    enter: id => onRowHover?.(id),
    leave: () => onRowHover?.(null),
    click: onRowClick,
    key: onRowKeyDown,
    pointerDown: (e, id) => reorder.onPointerDown(e, id),
    handleKey: (e, id) => reorder.onKeyDown(e, id),
    handleBlur: id => reorder.onBlur(id),
    hovering: onRowHover !== undefined,
    clickable: onRowOpen !== undefined,
  }
  const rowCtx = useMemo<RowCtx>(() => ({
    columns, visibility, tree, selectable: selectable === true, reorderCol, interactive,
    blockedText, hintId, reasonId, leadWidth,
  }), [columns, visibility, tree, selectable, reorderCol, interactive, blockedText, hintId, reasonId, leadWidth])

  const bodyContent = (): ReactNode => {
    if (showError) {
      return <TableRow className="hover:bg-transparent"><TableCell colSpan={colSpan} className="whitespace-normal"><ErrorState compact reason={error} retry={onRetry} /></TableCell></TableRow>
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
            {empty ?? <EmptyState compact icon={<SearchXIcon />} title={t('empty')} />}
          </TableCell>
        </TableRow>
      )
    }
    return rows.map((row, index) => {
      const o = optsOf(row)
      return (
        <DataRow
          key={row.id}
          api={apiRef}
          ctx={rowCtx}
          row={row}
          index={index}
          name={nameOf(row)}
          checkLabel={checkLabel(row, index + 1 + (effectivePage - 1) * pageSize)}
          selected={selection[row.id] === true}
          active={activeRowId === row.id}
          highlighted={highlighted.has(row.id)}
          tabStop={row.id === tabStop}
          expanded={tree ? row.getIsExpanded() : false}
          canSelect={canSelect(row)}
          canOpen={canOpen(row)}
          state={o.state}
          extraClass={cn(o.className, rowClassName?.(row.original))}
          dataKey={o.data ? JSON.stringify(o.data) : ''}
          dataAttrs={o.data}
          fullWidth={o.fullWidth}
          dropBefore={reorder.insertion === index}
          dropAfter={reorder.insertion === rows.length && index === rows.length - 1}
          moving={reorder.movingId === row.id}
        />
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
          <ul aria-label={props['aria-label']} aria-busy={loading || undefined} className="divide-y">
            {selectable && rows.length > 0 && (
              <li className="flex items-center gap-2 bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
                <CheckBox checked={allOnPage} indeterminate={!allOnPage && someOnPage} onCheckedChange={v => toggleAll(v === true)} aria-label={t('ui.data.select_all')} />
                <span aria-hidden="true">{t('ui.data.select_all')}</span>
              </li>
            )}
            {showError && <li className="list-none"><ErrorState compact reason={error} retry={onRetry} /></li>}
            {skeleton && Array.from({ length: 4 }, (_, i) => <li key={i} className="p-3"><Skeleton className="h-12 w-full" /></li>)}
            {isEmpty && <li className="list-none">{empty ?? <EmptyState compact icon={<SearchXIcon />} title={t('empty')} />}</li>}
            {!showError && rows.map((row, i) => {
              const o = optsOf(row)
              const selected = selection[row.id] === true
              const open = canOpen(row) ? () => onRowOpen!(row.original) : undefined
              return (
                <li
                  key={row.id}
                  {...rowEvents(row)}
                  {...rowDataAttrs(row, o, selected)}
                  className={cn('flex items-start gap-2 p-3 outline-none focus-visible:bg-muted', open && 'cursor-pointer', selected && 'bg-muted', rowStateClasses(row, o))}
                >
                  {canSelect(row) && (
                    <CheckBox data-no-row-click="" className="mt-0.5" checked={selected} onCheckedChange={v => row.toggleSelected(v === true)} aria-label={checkLabel(row, i + 1)} />
                  )}
                  <div className="min-w-0 flex-1">{o.fullWidth ?? renderCard(row.original, { selected, active: activeRowId === row.id, open })}</div>
                </li>
              )
            })}
          </ul>
        ) : (
          <div ref={frameRef} className="scroll-pt-(--row-h) overflow-auto" style={maxHeight ? { maxHeight } : undefined}>
            <table data-slot="table" aria-label={props['aria-label']} aria-busy={loading || undefined} className="w-full caption-bottom text-sm">
              <TableHeader className={cn('sticky top-0 z-10 shadow-[0_1px_0_0_var(--border)] [&_tr]:border-b-0', HEAD_BG)}>
                <tr className="h-(--row-h)">
                  {reorderCol && <TableHead className={cn('sticky left-0 z-20 w-8 min-w-8 px-0', HEAD_BG)}><span className="sr-only">{t('ui.data.reorder_column')}</span></TableHead>}
                  {selectable && (
                    <TableHead className={cn('sticky z-20 w-10 min-w-10', HEAD_BG)} style={{ left: `${reorderCol ? DRAG_W : 0}rem` }}>
                      <CheckBox
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
                    const headerDef = col.columnDef.header
                    // An empty header (a row-actions column) is still a column header: name it for readers.
                    const label = headerDef === undefined || headerDef === ''
                      ? <span className="sr-only">{meta?.label ?? t('ui.data.actions_column')}</span>
                      : flexRender(headerDef, header.getContext())
                    return (
                      <TableHead
                        key={header.id}
                        scope="col"
                        aria-sort={canSort ? (sorted === 'asc' ? 'ascending' : sorted === 'desc' ? 'descending' : 'none') : undefined}
                        style={p.style}
                        className={cn(
                          p.className && `${p.className} z-20 ${HEAD_BG}`,
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
                              TOUCH_HEIGHT,
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
            page={Math.max(effectivePage, 1)}
            pageSize={pageSize}
            total={total}
            shown={topLevelShown}
            disabled={loading && data.length === 0}
            pageSizeOptions={pageSizeOptions}
            onPageChange={p => setPage(p, 'user')}
            onPageSizeChange={n => { setPageSize(n); setPage(1, 'user') }}
          />
        )}
      </div>

      {reorderCol && (
        <>
          <p id={hintId} className="sr-only">{t('ui.data.reorder_hint')}</p>
          {blocked && <p id={reasonId} className="sr-only">{blockedText}</p>}
          <p role="status" className="sr-only">{reorder.announcement}</p>
        </>
      )}
      {(bulkActions || selectable) && (
        <BulkActionBar count={selectedIds.length} hidden={hiddenIds.length} onClear={clear} onClearHidden={clearHidden}>
          {bulkActions?.(bulkCtx)}
        </BulkActionBar>
      )}
    </div>
  )
}

/**
 * The row checkbox. On a coarse pointer its real box grows to 2rem inside a wrapper
 * (the checkbox's own invisible hit area, which reached into the drag handle beside
 * it, is switched off), so two controls in one row never overlap.
 */
function CheckBox({ className, ...props }: React.ComponentProps<typeof Checkbox>) {
  return (
    <span className="inline-flex items-center justify-center pointer-coarse:size-8">
      <Checkbox {...props} className={cn('after:hidden', className)} />
    </span>
  )
}

function DragHandle({ name, blockedText, reasonId, describedBy, ...handlers }: {
  name: string
  /** Empty while dragging works; otherwise why it does not, shown as the tooltip and described to readers. */
  blockedText: string
  reasonId: string
  describedBy: string
  onPointerDown: (e: React.PointerEvent) => void
  onKeyDown: (e: React.KeyboardEvent) => void
  onBlur: () => void
}) {
  const { t } = useSpacedT()
  const off = blockedText !== ''
  return (
    <IconButton
      label={t('ui.data.drag_handle', { name })}
      tooltip={off ? blockedText : undefined}
      size="icon-xs"
      aria-disabled={off || undefined}
      aria-describedby={off ? `${describedBy} ${reasonId}` : describedBy}
      aria-roledescription="sortable"
      // touch-action none: without it a finger drag on the handle scrolls the page instead.
      className="cursor-grab touch-none text-muted-foreground active:cursor-grabbing"
      {...handlers}
    >
      <GripVerticalIcon />
    </IconButton>
  )
}


interface RowApi<T> {
  focus: (id: string) => void
  blur: () => void
  enter: (id: string) => void
  leave: () => void
  click: (e: MouseEvent<HTMLElement>, row: Row<T>) => void
  key: (e: KeyboardEvent<HTMLElement>, row: Row<T>) => void
  pointerDown: (e: React.PointerEvent, id: string) => void
  handleKey: (e: React.KeyboardEvent, id: string) => void
  handleBlur: (id: string) => void
  hovering: boolean
  clickable: boolean
}

/** What every row shares and only a column, visibility or mode change alters. */
interface RowCtx {
  columns: unknown
  visibility: VisibilityState
  tree: boolean
  selectable: boolean
  reorderCol: boolean
  interactive: boolean
  blockedText: string
  hintId: string
  reasonId: string
  leadWidth: number
}

interface DataRowProps<T> {
  api: { current: RowApi<T> }
  ctx: RowCtx
  row: Row<T>
  index: number
  name: string
  checkLabel: string
  selected: boolean
  active: boolean
  highlighted: boolean
  tabStop: boolean
  expanded: boolean
  canSelect: boolean
  canOpen: boolean
  state: RowOptions['state']
  extraClass: string
  dataKey: string
  dataAttrs: Record<string, string> | undefined
  fullWidth: ReactNode
  dropBefore: boolean
  dropAfter: boolean
  moving: boolean
}

function DataRowInner<T>(p: DataRowProps<T>) {
  const { t } = useSpacedT()
  const { row, ctx, api, selected } = p
  const lead = `sticky z-[1] ${PIN_BG}`
  const { tree, selectable, reorderCol } = ctx

  return (
    <TableRow
      data-row=""
      data-id={row.id}
      data-depth={tree ? row.depth : undefined}
      data-state={selected ? 'selected' : undefined}
      data-active={p.active ? 'true' : undefined}
      data-row-state={p.state}
      data-highlight={p.highlighted ? 'true' : undefined}
      data-drop={p.dropBefore ? 'before' : p.dropAfter ? 'after' : undefined}
      {...p.dataAttrs}
      tabIndex={ctx.interactive ? (p.tabStop ? 0 : -1) : undefined}
      onFocus={e => { if (e.target === e.currentTarget) api.current.focus(row.id) }}
      onBlur={e => { if (e.target === e.currentTarget) api.current.blur() }}
      // Always attached and decided when the event fires: onRowHover / onRowOpen may only
      // become defined after the row was first rendered (a memoised row is not re-rendered for that).
      onMouseEnter={() => api.current.enter(row.id)}
      onMouseLeave={() => api.current.leave()}
      onClick={e => api.current.click(e, row)}
      onKeyDown={ctx.interactive ? e => api.current.key(e, row) : undefined}
      className={cn(
        'group/row outline-none focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring',
        p.canOpen && 'cursor-pointer',
        p.moving && 'opacity-50',
        p.dropBefore && '[&>td]:shadow-[inset_0_2px_0_0_var(--brand)]',
        p.dropAfter && '[&>td]:shadow-[inset_0_-2px_0_0_var(--brand)]',
        // Dimmed is colour, not opacity: opacity takes muted text to about 2:1. A page adds
        // its own cue (a tag) in the cells; the row only softens its background and text.
        p.state === 'dimmed' && 'bg-muted/40 text-foreground/70 [&_.text-muted-foreground]:text-foreground/70',
        p.state === 'pending' && 'bg-warning/[0.07] [&>td:first-child]:shadow-[inset_3px_0_0_var(--warning)]',
        p.highlighted && 'bg-brand/[0.09]',
        p.active && 'bg-brand/[0.07] hover:bg-brand/10',
        p.extraClass,
      )}
    >
      {reorderCol && (
        <TableCell data-no-row-click="" className={cn(lead, 'left-0 w-8 min-w-8 px-0 text-center')}>
          <DragHandle
            blockedText={ctx.blockedText}
            reasonId={ctx.reasonId}
            name={p.name}
            onPointerDown={e => api.current.pointerDown(e, row.id)}
            onKeyDown={e => api.current.handleKey(e, row.id)}
            onBlur={() => api.current.handleBlur(row.id)}
            describedBy={ctx.hintId}
          />
        </TableCell>
      )}
      {selectable && (
        <TableCell data-no-row-click="" className={cn(lead, 'w-10 min-w-10 pr-0')} style={{ left: `${reorderCol ? DRAG_W : 0}rem` }}>
          {p.canSelect && <CheckBox checked={selected} onCheckedChange={v => row.toggleSelected(v === true)} aria-label={p.checkLabel} />}
        </TableCell>
      )}
      {p.fullWidth !== undefined ? (
        <TableCell colSpan={row.getVisibleCells().length} className="whitespace-normal">{p.fullWidth}</TableCell>
      ) : row.getVisibleCells().map((cell, ci) => {
        const meta = cell.column.columnDef.meta
        const first = ci === 0
        const pinned = first ? { className: 'sticky z-[1]', style: { left: `${ctx.leadWidth}rem` } as CSSProperties } : {}
        return (
          <TableCell
            key={cell.id}
            style={pinned.style}
            className={cn(
              pinned.className && `${pinned.className} ${PIN_BG}`,
              meta?.align === 'right' && 'text-right',
              meta?.align === 'center' && 'text-center',
              meta?.className,
            )}
          >
            {first && tree ? (
              <div className="flex items-center gap-1" style={{ paddingLeft: `${row.depth * 1.25}rem` }}>
                {row.getCanExpand() ? (
                  <IconButton
                    label={t(p.expanded ? 'ui.data.collapse_row' : 'ui.data.expand_row', { name: p.name })}
                    size="icon-xs"
                    aria-expanded={p.expanded}
                    data-no-row-click=""
                    onClick={() => row.toggleExpanded()}
                  >
                    <ChevronRightIcon className={cn('transition-transform', p.expanded && 'rotate-90')} />
                  </IconButton>
                ) : <span className="inline-block size-6 shrink-0" aria-hidden="true" />}
                {flexRender(cell.column.columnDef.cell, cell.getContext())}
              </div>
            ) : flexRender(cell.column.columnDef.cell, cell.getContext())}
          </TableCell>
        )
      })}
    </TableRow>
  )
}

// A row re-renders only when something that shows on it changed. `api` is a stable
// ref. `fullWidth` (a separator's content) is compared by IDENTITY: a page builds that
// JSX again on every render, so a separator re-renders whenever its page does and can
// never show a stale count, a busy flag or a permission that has changed. Separators are
// few; the hundreds of ordinary rows are what the memo is for.
const DataRow = memo(DataRowInner, (a, b) => {
  const keys = Object.keys(a) as (keyof DataRowProps<unknown>)[]
  for (const k of keys) {
    // dataAttrs is compared through dataKey (its JSON), because the page builds a new object each render.
    if (k === 'dataAttrs') continue
    if (a[k] !== b[k]) return false
  }
  return true
}) as typeof DataRowInner
