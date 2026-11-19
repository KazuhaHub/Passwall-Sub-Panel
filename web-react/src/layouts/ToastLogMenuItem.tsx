import { ListItemIcon, ListItemText, MenuItem, Typography } from '@mui/material'

import { openToastLog, useToastLogSummary } from '@/components/feedback/toasts'
import { useSpacedT } from '@/i18n/spacing'
import { IconLogs } from '@/lib/icons'

// The message log's way in on a phone, for the two OLD app bars. Their bars have no room for
// another icon below `sm` (the button that fits beside the bell from `sm` up is hidden
// there, or it pushes the account avatar off the screen), so below `sm` the log is an item
// in the account menu, with the unread count, and the persistent error summary toast is
// the other route. Goes away with the layouts when the new shell replaces them.
export default function ToastLogMenuItem({ onClose }: { onClose: () => void }) {
  const { t } = useSpacedT()
  const { unread, unreadErrors } = useToastLogSummary()
  const count = unreadErrors || unread
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
      sx={{ display: { xs: 'flex', sm: 'none' } }}
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
