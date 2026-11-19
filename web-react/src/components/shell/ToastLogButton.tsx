
import { IconButton } from '@/components/common/IconButton'
import { openToastLog, useToastLogSummary } from '@/components/feedback/toasts'
import { useSpacedT } from '@/i18n/spacing'
import { IconLogs } from '@/lib/icons'
import { cn } from '@/lib/utils'

/**
 * The top-bar door to the message log: the messages that came too fast to be shown, errors
 * among them, stay reachable here after their summary toast is gone. The badge shows how
 * many errors are unseen when there are any (in the danger colour), and otherwise how many
 * messages are; it is capped at 99+ so it never outgrows the button, and it clears the
 * moment the log is opened.
 */
/**
 * `size="bar"` is for the old MUI app bars: the same 40px button and 24px icon as the MUI
 * icon buttons beside it, with a smaller badge tucked into the corner so the icon stays
 * recognisable. `className` lets such a bar hide it below the width where it no longer fits.
 */
export function ToastLogButton({ size = 'default', className }: { size?: 'default' | 'bar'; className?: string }) {
  const { t } = useSpacedT()
  const { unread, unreadErrors } = useToastLogSummary()
  const title = t('ui.feedback.log_title')
  const label = unread === 0 ? title
    : unreadErrors > 0 ? t('ui.feedback.log_unread_errors', { count: unread, errors: unreadErrors })
      : t('ui.feedback.log_unread', { count: unread })
  const shown = unreadErrors > 0 ? unreadErrors : unread
  return (
    <IconButton label={label} tooltip={title} className={cn('relative', size === 'bar' && "size-10 [&_svg:not([class*='size-'])]:size-6", className)} onClick={openToastLog}>
      <IconLogs />
      {unread > 0 && (
        <span
          aria-hidden
          data-slot="toast-log-badge"
          data-errors={unreadErrors > 0 ? '' : undefined}
          className={cn(
            'absolute flex items-center justify-center rounded-full font-semibold leading-none tabular-nums',
            size === 'bar' ? 'top-0.5 right-0.5 h-3.5 min-w-3.5 px-0.5 text-[9px]' : '-top-0.5 -right-0.5 h-4 min-w-4 px-1 text-[10px]',
            unreadErrors > 0 ? 'bg-destructive text-white dark:text-background' : 'bg-muted-foreground text-background',
          )}
        >
          {shown > 99 ? '99+' : shown}
        </span>
      )}
    </IconButton>
  )
}
