import { ListItemIcon, ListItemText, MenuItem, Typography } from '@mui/material'

import { openToastLog, useToastLogSummary } from '@/components/feedback/toasts'
import { useSpacedT } from '@/i18n/spacing'
import { IconLogs } from '@/lib/icons'

// The message log's way in on a narrow screen, for the two OLD app bars. Where the bar has
// no room for another icon (see useLogInBar) the layout renders THIS, an item in the account
// menu with the unread count, instead of the bar button; it is never rendered alongside the
// button and never hidden with CSS (a hidden item is still a menu child that MUI tries to
// focus). The persistent error summary toast is the other route. Goes away with the layouts
// when the new shell replaces them.
export default function ToastLogMenuItem({ onClose }: { onClose: () => void }) {
  const { t } = useSpacedT()
  const { unread, unreadErrors } = useToastLogSummary()
  // The total, so the number never goes down as messages arrive; errors show as colour and in the name.
  const count = unread
  const title = t('ui.feedback.log_title')
  const label = unread === 0 ? title
    : unreadErrors > 0 ? t('ui.feedback.log_unread_errors', { count: unread, errors: unreadErrors })
      : t('ui.feedback.log_unread', { count: unread })
  return (
    <MenuItem
      aria-label={label}
      onClick={() => {
        onClose()
        // After the menu has handed focus back, so the panel is what ends up holding it.
        setTimeout(openToastLog, 0)
      }}
    >
      <ListItemIcon><IconLogs className="size-5" /></ListItemIcon>
      <ListItemText primary={title} />
      {count > 0 && (
        <Typography component="span" data-slot="toast-log-menu-count" sx={{ ml: 1.5, fontSize: 12, fontWeight: 600, color: unreadErrors > 0 ? 'error.main' : 'text.secondary' }}>
          {count > 99 ? '99+' : count}
        </Typography>
      )}
    </MenuItem>
  )
}
