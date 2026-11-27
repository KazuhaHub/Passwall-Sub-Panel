import type { ColumnDef, ExpandedState, RowData, SortingState, VisibilityState } from '@tanstack/react-table'
import type { ReactNode, Ref } from 'react'
import type { ReorderCall } from './dataTableLogic'

declare module '@tanstack/react-table' {
  // The names and the parameter list must match the library's declaration for the
  // merge to type-check; the parameters are not otherwise used here.
  // oxlint-disable-next-line no-unused-vars
  interface ColumnMeta<TData extends RowData, TValue> {
    /** Name in the column menu and for sorting announcements, when `header` is not plain text. */
    label?: string
    /** Classes for the body cells of this column (width, wrapping, alignment). */
    className?: string
    headerClassName?: string
    align?: 'left' | 'right' | 'center'
    /** false keeps the column out of the column menu (the name column, the actions column). */
    hideable?: boolean
    /** Starts hidden, until the person turns it on or a remembered choice says otherwise. */
    defaultHidden?: boolean
  }
}

export type { ColumnDef, ExpandedState, RowData, SortingState, VisibilityState, ReorderCall }

/**
 * What a bulk action is handed. Selection is by id and outlives paging and filtering,
 * so `ids` can name rows that are not on screen and `loadedRows` can be shorter.
 *
 * A DESTRUCTIVE action (delete, disable, reset) acts on `ids`, so it must confirm
 * with the total, `ids.length`, and say how many of those are not in the current
 * list (`hiddenCount`): "删除 12 项，其中 4 项不在当前列表中". Acting on `loadedRows` alone
 * would do less than the bar promised; acting on `ids` without saying so does more
 * than the person can see.
 */
export interface BulkContext<T> {
  /** Every selected id, including rows on other pages and rows a filter has hidden. */
  ids: string[]
  /**
   * The selected rows the table has LOADED, i.e. that `data` holds: in client mode all of
   * them whichever page they are on, in server mode only those on the current page. Not
   * "the rows on screen".
   */
  loadedRows: T[]
  /** How many selected ids are not among the rows shown now (on another page, or filtered out of the list). */
  hiddenCount: number
  clear: () => void
  /** Drop only the selected ids that are not shown now. */
  clearHidden: () => void
}

/** Why a page number changed: a person asked, or the table corrected a page that no longer exists. */
export type PageChangeReason = 'user' | 'correction'

export interface RowOptions {
  /**
   * Render this across the whole row instead of the cells: a separator line in a
   * mixed list. The row keeps its place in the order and may still be dragged.
   */
  fullWidth?: ReactNode
  /** false: no checkbox, left out of select-all and Space. Default true when the table is selectable. */
  selectable?: boolean
  /** false: a click or Enter does not open it. Default true when onRowOpen is set. */
  openable?: boolean
  /** Styling states a page needs without writing its own row: dimmed (inactive) and pending (not live yet). */
  state?: 'dimmed' | 'pending'
  className?: string
  /** Extra attributes on the row element, for a page's own hooks (data-kind, ...). */
  data?: Record<string, string>
}

/** What a page can ask of a mounted table (DataTableProps.handleRef). */
export interface DataTableHandle {
  /**
   * Scroll the row with this id into view: a search hit, or the row a move just landed on.
   * In a windowed table the row need not be drawn yet; it is drawn by the time the scroll
   * settles. Returns false when no such row is in the list. The index comes from the LAST
   * RENDER: after a reorder (or any data change) call it from an effect once the new data
   * has rendered, not synchronously inside `onReorder`, or it scrolls to the old position. 'nearest' (default) scrolls
   * only if the row is hidden, and stops it landing under the sticky header.
   */
  scrollToRow: (id: string, opts?: { align?: 'nearest' | 'center' }) => boolean
}

interface DataTableBase<T> {
  data: readonly T[]
  /**
   * MUST BE REFERENTIALLY STABLE: define the array at module level, or memoise it
   * (`useMemo`) with dependencies that really change the columns. TanStack rebuilds
   * its column model, and DataTable its row context, whenever this identity changes,
   * and the rows are memoised on that context. An array literal written in the render
   * body therefore works, but every render re-derives every column, header and cell
   * and redraws every row: a list that re-renders often (typing in the search box,
   * a poll) gets slow as it grows. Selection, sorting and paging live in state, not in
   * the columns, so they are not lost. Only the FIRST render's `meta.defaultHidden` flags decide the
   * starting column visibility, whatever the identity does later.
   */
  // The column helper produces differently-typed columns in one array.
  columns: ColumnDef<T, any>[]
  getRowId: (row: T) => string
  /**
   * `client`: `data` is every row and the table sorts and pages it. `server`:
   * `data` is the current page, the caller sorted and paged it, and `total`
   * is the count across all pages.
   */
  mode?: 'client' | 'server'
  total?: number
  /** Accessible name for the table. */
  'aria-label'?: string
  /**
   * A stable id for this table ("admin.users"), under which the widths an admin dragged the columns to are kept in
   * the browser (`psp.colWidths.v1.<tableId>`). Give every table one; two tables must not share one. Left out, a
   * resize lasts for the visit only.
   */
  tableId?: string

  /** false hides the footer and shows every row (a short list, a reorderable list). */
  pagination?: boolean
  page?: number
  /** `reason` is 'correction' when the table moved to the last page that exists; a URL-backed page should REPLACE then, not push. */
  onPageChange?: (page: number, reason: PageChangeReason) => void
  pageSize?: number
  onPageSizeChange?: (size: number) => void
  pageSizeOptions?: readonly number[]

  sorting?: SortingState
  onSortingChange?: (next: SortingState) => void

  columnVisibility?: VisibilityState
  onColumnVisibilityChange?: (next: VisibilityState) => void
  /** Show the column menu at the end of the toolbar. Default true. */
  columnMenu?: boolean

  selectable?: boolean
  selection?: Record<string, boolean>
  onSelectionChange?: (next: Record<string, boolean>) => void
  /** Text for a row's checkbox, so it is announced as "Select Taiwan HiNet" and not "Select row 3". */
  rowLabel?: (row: T) => string
  /** Rendered in the bar that appears at the bottom while rows are selected. */
  bulkActions?: (ctx: BulkContext<T>) => ReactNode

  /** Click or Enter on a row. Left out, rows are not interactive. */
  onRowOpen?: (row: T) => void
  /** The row whose detail is open, highlighted. */
  activeRowId?: string | null
  rowClassName?: (row: T) => string | undefined

  /** The search / facets / actions row above the table (a FilterBar). The column menu follows it. */
  toolbar?: ReactNode
  loading?: boolean
  /** true for the built-in message; a string or Error for its detail. Shows instead of the rows. */
  error?: boolean | string | Error | null
  onRetry?: () => void
  /** Shown when there are no rows. Defaults to a plain sentence. */
  empty?: ReactNode

  /**
   * Why reordering is off right now, from the caller (a filter is active, a save is
   * running). Shown on the handles like the built-in reasons. Undefined: no extra block.
   */
  reorderBlockedReason?: string
  /** Per-row overrides; see RowOptions. */
  rowOptions?: (row: T) => RowOptions | undefined
  /** The row under the pointer or holding keyboard focus (null when it leaves): for highlighting related rows elsewhere. */
  onRowHover?: (id: string | null) => void
  /** Rows to mark highlighted from outside (rows sharing the hovered row's landing, say). */
  highlightedIds?: readonly string[]
  /**
   * Rows related to the hovered one (same landing, same entry). A QUIET hint: the row only gets `data-related="true"`
   * and no background, so the row under the pointer stays the most prominent one; the page decides what the mark
   * changes inside a cell (the landing bar widens, say). `highlightedIds` is the loud, timed signal (a moved row).
   */
  relatedIds?: readonly string[]

  /** Makes rows a tree: children render under their parent, indented, expandable. */
  getSubRows?: (row: T) => T[] | undefined
  expanded?: ExpandedState
  onExpandedChange?: (next: ExpandedState) => void
  defaultExpanded?: boolean

  /** At phone width, render each row with this instead of the table. */
  renderCard?: (row: T, ctx: { selected: boolean; active: boolean; open: (() => void) | undefined }) => ReactNode
  /**
   * Max height of the scrolling table area; the header stays put inside it. null removes
   * the limit. Left out, it is 70dvh, EXCEPT in a windowed table (see DataTable), where it
   * is null: the page scrolls. Pass a height there to give the table its own scroll box.
   */
  maxHeight?: string | null
  /** Imperative access: scrollToRow. */
  handleRef?: Ref<DataTableHandle>
  className?: string
}

/**
 * Reordering needs the whole list on screen, because the report is (movedId,
 * beforeId) relative to the rows shown: on a page, the last slot would read as
 * "after everything" and move the row to the end of the whole list. So onReorder
 * requires `pagination={false}` AND client mode (a server page is only part of the
 * list); the types reject the rest, and at runtime the handles are shown off with
 * the reason and a developer error is logged. A FILTERED list has the same problem
 * and the table cannot know about the caller's filter: pass `reorderBlockedReason`
 * while one is active.
 */
type ReorderProps =
  | { onReorder?: undefined; pagination?: boolean }
  | { /** Adds a drag handle column. Reports (movedId, beforeId). */ onReorder: (call: ReorderCall) => void; pagination: false; mode?: 'client' }

export type DataTableProps<T> = DataTableBase<T> & ReorderProps
