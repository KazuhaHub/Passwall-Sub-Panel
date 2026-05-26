import { TableCell, TableSortLabel, type TableCellProps } from '@mui/material'
import type { ReactNode } from 'react'

export interface SortableTableCellProps extends Omit<TableCellProps, 'sortDirection'> {
  /** Column identifier the backend's sort_by allowlist recognizes. */
  column: string
  /** Currently active sort column on this table. */
  activeColumn: string
  activeDir: 'asc' | 'desc'
  /** Called when admin clicks the header. The hook already toggles
   * direction when the same column is clicked twice. */
  onSort: (col: string, initialDir?: 'asc' | 'desc') => void
  /** Initial direction the first time admin clicks this header; usePaged
   * still toggles on repeated clicks of the same column. Defaults to
   * "asc" — useful to set "desc" for timestamp columns (newest first). */
  initialDir?: 'asc' | 'desc'
  children: ReactNode
}

/**
 * SortableTableCell wraps MUI's TableSortLabel + TableCell so a column
 * header becomes a click-target with the right arrow indicator. Pairs
 * with usePaged: pass `sortBy`/`sortDir`/`setSort` straight from the
 * hook.
 *
 * Non-sortable columns can stay as plain TableCell — only the columns
 * the backend's sort allowlist accepts should use this.
 */
export function SortableTableCell({
  column, activeColumn, activeDir, onSort, initialDir, children, ...rest
}: SortableTableCellProps) {
  const active = activeColumn === column
  return (
    <TableCell sortDirection={active ? activeDir : false} {...rest}>
      <TableSortLabel
        active={active}
        direction={active ? activeDir : 'asc'}
        onClick={() => onSort(column, initialDir)}
      >
        {children}
      </TableSortLabel>
    </TableCell>
  )
}
