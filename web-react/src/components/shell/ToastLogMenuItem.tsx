import { DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { openToastLog, useToastLogSummary } from '@/components/feedback/toasts'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { useSpacedT } from '@/i18n/spacing'
import { IconLogs } from '@/lib/icons'

// Below this width the bar's seven controls do not fit side by side (measured at 320px in
// both densities: the log button overlapped the search button, the phone's only way to the
// command palette), so the log moves into the account menu. Decided in JavaScript so the bar
// renders the button OR the menu item, never both and never a CSS-hidden menu item.
export const LOG_IN_MENU_MAX_WIDTH = 359

export function useLogInMenu(): boolean {
  return useMediaQuery(`(max-width: ${LOG_IN_MENU_MAX_WIDTH}px)`)
}

/** The message log's entry in the account menu, with the unread count; see ToastLogButton. */
export function ToastLogMenuItem() {
  const { t } = useSpacedT()
  const { unread, unreadErrors } = useToastLogSummary()
  const title = t('ui.feedback.log_title')
  const label = unread === 0 ? title
    : unreadErrors > 0 ? t('ui.feedback.log_unread_errors', { count: unread, errors: unreadErrors })
      : t('ui.feedback.log_unread', { count: unread })
  return (
    // After the menu has handed focus back, so the panel is what ends up holding it.
    <DropdownMenuItem aria-label={label} onClick={() => { setTimeout(openToastLog, 0) }}>
      <IconLogs />
      {title}
      {unread > 0 && (
        <span data-slot="toast-log-menu-count" className={unreadErrors > 0 ? 'ml-auto text-xs font-semibold text-destructive' : 'ml-auto text-xs font-semibold text-muted-foreground'}>
          {unread > 99 ? '99+' : unread}
        </span>
      )}
    </DropdownMenuItem>
  )
}
