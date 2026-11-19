import type { ColumnDef, ExpandedState, RowData, SortingState, VisibilityState } from '@tanstack/react-table'
import type { ReactNode } from 'react'
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
 * so `ids` can name rows that are not on screen and `visibleRows` can be shorter.
 *
 * A DESTRUCTIVE action (delete, disable, reset) acts on `ids`, so it must confirm
 * with the total, `ids.length`, and say how many of those are hidden
 * (`hiddenCount`): "删除 12 项，其中 4 项不在当前页". Acting on `visibleRows` alone
 * would do less than the bar promised; acting on `ids` without saying so does more
 * than the person can see.
 */
export interface BulkContext<T> {
  /** Every selected id, including rows on other pages and rows a filter has hidden. */
  ids: string[]
  /** The selected rows `data` holds: in client mode all of them, in server mode only those on this page. */
  visibleRows: T[]
  /** How many selected ids are not among the rows shown now (another page, or filtered out). */
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

interface DataTableBase<T> {
  data: readonly T[]
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

  /** Makes rows a tree: children render under their parent, indented, expandable. */
  getSubRows?: (row: T) => T[] | undefined
  expanded?: ExpandedState
  onExpandedChange?: (next: ExpandedState) => void
  defaultExpanded?: boolean

  /** At phone width, render each row with this instead of the table. */
  renderCard?: (row: T, ctx: { selected: boolean; active: boolean; open: (() => void) | undefined }) => ReactNode
  /** Max height of the scrolling table area; the header stays put inside it. null removes the limit. */
  maxHeight?: string | null
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
