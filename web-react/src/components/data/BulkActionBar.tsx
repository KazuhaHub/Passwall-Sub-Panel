import type { ReactNode } from 'react'
import { useSpacedT } from '@/i18n/spacing'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { TOUCH_HEIGHT } from '@/components/common/touchTarget'

/**
 * The bar that rises at the bottom of a list while rows are selected (replaces
 * BulkBar): how many, what can be done with them, and a way to let go. It is
 * sticky rather than fixed, so it never covers the last rows, and it wraps to
 * two lines on a phone.
 *
 * Selection is by id and outlives paging and filtering, so the count can include
 * rows nobody can see; `hidden` says how many, with a button to drop just those,
 * so a bulk action never acts on rows the person did not know were still picked.
 */
export function BulkActionBar({ count, hidden = 0, onClear, onClearHidden, children, className }: {
  count: number
  hidden?: number
  onClear: () => void
  onClearHidden?: () => void
  children?: ReactNode
  className?: string
}) {
  const { t } = useSpacedT()
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
      {hidden > 0 && (
        <span className="flex items-center gap-1.5 text-sm text-muted-foreground tabular-nums">
          {t('ui.data.selected_hidden', { count: hidden })}
          {onClearHidden && <Button variant="link" size="sm" className={cn(TOUCH_HEIGHT, 'h-auto p-0')} onClick={onClearHidden}>{t('ui.data.clear_hidden')}</Button>}
        </span>
      )}
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2 sm:justify-end">{children}</div>
      <Button variant="ghost" size="sm" className={TOUCH_HEIGHT} onClick={onClear}>{t('ui.data.clear_selection')}</Button>
    </div>
  )
}
