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

export interface BulkContext<T> {
  selectedIds: string[]
  /** Only the selected rows that are in `data` now: with server paging, a selection can outlive its page. */
  selectedRows: T[]
  clear: () => void
}

export interface DataTableProps<T> {
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
  onPageChange?: (page: number) => void
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

  /** Adds a drag handle column. Reports (movedId, beforeId). Off while a column sort is on. */
  onReorder?: (call: ReorderCall) => void

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
