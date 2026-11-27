
import { IconButton } from '@/components/common/IconButton'
import { openToastLog, useToastLogSummary } from '@/components/feedback/toasts'
import { useSpacedT } from '@/i18n/spacing'
import { IconLogs } from '@/lib/icons'
import { cn } from '@/lib/utils'

/**
 * The top-bar door to the message log: the messages that came too fast to be shown, errors
 * among them, stay reachable here after their summary toast is gone. The badge counts the
 * messages not yet seen, so the number never goes DOWN as more arrive (it is the total,
 * not the error count); errors are conveyed by the badge turning red and by the accessible
 * name ("N not yet seen, M of them errors"). Capped at 99+ so it never outgrows the button,
 * and it clears the moment the log is opened.
 */
export function ToastLogButton({ className }: { className?: string }) {
  const { t } = useSpacedT()
  const { unread, unreadErrors } = useToastLogSummary()
  const title = t('ui.feedback.log_title')
  const label = unread === 0 ? title
    : unreadErrors > 0 ? t('ui.feedback.log_unread_errors', { count: unread, errors: unreadErrors })
      : t('ui.feedback.log_unread', { count: unread })
  const shown = unread
  return (
    <IconButton label={label} tooltip={title} className={cn('relative', className)} onClick={openToastLog}>
      <IconLogs />
      {unread > 0 && (
        <span
          aria-hidden
          data-slot="toast-log-badge"
          data-errors={unreadErrors > 0 ? '' : undefined}
          className={cn(
            'absolute flex items-center justify-center rounded-full font-semibold leading-none tabular-nums',
            '-top-0.5 -right-0.5 h-4 min-w-4 px-1 text-[10px]',
            unreadErrors > 0 ? 'bg-destructive text-white dark:text-background' : 'bg-muted-foreground text-background',
          )}
        >
          {shown > 99 ? '99+' : shown}
        </span>
      )}
    </IconButton>
  )
}
