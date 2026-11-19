import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/**
 * The bar that rises at the bottom of a list while rows are selected (replaces
 * BulkBar): how many, what can be done with them, and a way to let go. It is
 * sticky rather than fixed, so it never covers the last rows, and it wraps to
 * two lines on a phone.
 */
export function BulkActionBar({ count, onClear, children, className }: { count: number; onClear: () => void; children?: ReactNode; className?: string }) {
  const { t } = useTranslation('common')
  if (count <= 0) return null
  return (
    <div
      role="region"
      aria-label={t('ui.data.bulk_actions')}
      data-slot="bulk-bar"
      className={cn(
        'sticky bottom-3 z-20 mt-3 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border bg-popover px-4 py-2 text-popover-foreground shadow-lg',
        className,
      )}
    >
      <span className="text-sm font-medium tabular-nums">{t('ui.data.selected_count', { count })}</span>
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2 sm:justify-end">{children}</div>
      <Button variant="ghost" size="sm" onClick={onClear}>{t('ui.data.clear_selection')}</Button>
    </div>
  )
}
