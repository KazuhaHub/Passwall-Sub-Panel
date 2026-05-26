import { TablePagination, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

export interface PagedTableFooterProps {
  total: number
  page: number             // 1-indexed
  pageSize: number
  onPageChange: (n: number) => void
  onPageSizeChange: (n: number) => void
  /** Allowed page sizes. Defaults to [10, 25, 50, 100]. */
  rowsPerPageOptions?: number[]
}

/**
 * PagedTableFooter is the shared bottom-of-table strip every paged
 * list view uses. Wraps MUI's TablePagination so we get the standard
 * "1–25 of 3,421 < >" layout + size selector + first/last buttons with
 * one consistent style.
 *
 * Internal API mirrors usePaged's outputs so call sites are just
 * `<PagedTableFooter {...paged} />`-ish — see view migrations.
 */
export function PagedTableFooter(props: PagedTableFooterProps) {
  const theme = useTheme()
  const { t } = useTranslation('common')
  return (
    <TablePagination
      component="div"
      count={props.total}
      // MUI TablePagination is 0-indexed for the page prop; we expose
      // 1-indexed externally to match the URL + backend convention.
      page={Math.max(0, props.page - 1)}
      rowsPerPage={props.pageSize}
      rowsPerPageOptions={props.rowsPerPageOptions ?? [10, 25, 50, 100]}
      onPageChange={(_, p) => props.onPageChange(p + 1)}
      onRowsPerPageChange={(e) => props.onPageSizeChange(parseInt(e.target.value, 10))}
      labelRowsPerPage={t('pagination.rows_per_page', { defaultValue: '每页：' })}
      labelDisplayedRows={({ from, to, count }) =>
        t('pagination.range', {
          from, to,
          count: count !== -1 ? count : `> ${to}`,
          defaultValue: '{{from}}–{{to}} / 共 {{count}}',
        })
      }
      showFirstButton
      showLastButton
      sx={{
        borderTop: `1px solid ${theme.palette.md.outlineVariant}`,
        '& .MuiTablePagination-toolbar': { minHeight: 52 },
      }}
    />
  )
}
