import { ChevronLeftIcon, ChevronRightIcon, ChevronsLeftIcon, ChevronsRightIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { NativeSelect } from '@/components/ui/native-select'
import { cn } from '@/lib/utils'
import { PAGE_SIZE_OPTIONS, pageCount, pageRange } from './dataTableLogic'
import { TipButton } from './TipButton'

export interface DataTablePaginationProps {
  page: number
  pageSize: number
  total: number
  /** Rows on this page, for the range text. */
  shown: number
  onPageChange: (page: number) => void
  onPageSizeChange: (size: number) => void
  pageSizeOptions?: readonly number[]
  disabled?: boolean
  className?: string
}

/**
 * The strip under a table (replaces PagedTableFooter): "1–25 of 3,421", a page
 * size choice and first / previous / next / last. Page numbers are 1-based, the
 * same convention as the URL and the API.
 */
export function DataTablePagination({
  page, pageSize, total, shown, onPageChange, onPageSizeChange, pageSizeOptions = PAGE_SIZE_OPTIONS, disabled, className,
}: DataTablePaginationProps) {
  const { t } = useTranslation('common')
  const pages = pageCount(total, pageSize)
  const { from, to } = pageRange(page, pageSize, total, shown)
  // A remembered or caller-chosen size the footer does not list must still be selectable.
  const sizes = pageSizeOptions.includes(pageSize) ? pageSizeOptions : [...pageSizeOptions, pageSize].sort((a, b) => a - b)
  const atStart = page <= 1
  const atEnd = page >= pages

  return (
    <div data-slot="data-table-pagination" className={cn('flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t px-3 py-2 text-sm', className)}>
      <div className="flex items-center gap-2 text-muted-foreground">
        <span>{t('pagination.rows_per_page')}</span>
        <div className="w-20">
          <NativeSelect
            aria-label={t('ui.data.page_size')}
            value={pageSize}
            disabled={disabled}
            onChange={e => onPageSizeChange(Number(e.target.value))}
            className="h-[calc(var(--control-h)-0.25rem)] text-sm"
          >
            {sizes.map(n => <option key={n} value={n}>{n}</option>)}
          </NativeSelect>
        </div>
      </div>
      <div className="flex items-center gap-2">
        <span className="text-muted-foreground tabular-nums">{t('pagination.range', { from, to, count: total })}</span>
        <div className="flex items-center gap-0.5">
          <TipButton label={t('ui.data.first_page')} variant="outline" disabled={disabled || atStart} onClick={() => onPageChange(1)}><ChevronsLeftIcon /></TipButton>
          <TipButton label={t('ui.data.prev_page')} variant="outline" disabled={disabled || atStart} onClick={() => onPageChange(page - 1)}><ChevronLeftIcon /></TipButton>
          <TipButton label={t('ui.data.next_page')} variant="outline" disabled={disabled || atEnd} onClick={() => onPageChange(page + 1)}><ChevronRightIcon /></TipButton>
          <TipButton label={t('ui.data.last_page')} variant="outline" disabled={disabled || atEnd} onClick={() => onPageChange(pages)}><ChevronsRightIcon /></TipButton>
        </div>
      </div>
    </div>
  )
}
