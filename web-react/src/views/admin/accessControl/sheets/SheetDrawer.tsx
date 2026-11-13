import type { ReactNode } from 'react'
import { Box, Drawer, IconButton, Typography, useMediaQuery, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useTranslation } from 'react-i18next'

/**
 * The page-level right drawer (plan §7.2): 560 wide, 100vw on a phone, the
 * low surface, rounded on the left. At the DEFAULT zIndex.drawer, so a dialog
 * it opens (zIndex.modal) is above it; only the risk drawer is raised.
 */
export default function SheetDrawer({ open, onClose, title, children, headingId }: {
  open: boolean
  onClose: () => void
  title: ReactNode
  children: ReactNode
  headingId: string
}) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  return (
    <Drawer anchor="right" open={open} onClose={onClose}
      slotProps={{
        paper: {
          role: 'dialog', 'aria-labelledby': headingId,
          sx: {
            width: phone ? '100vw' : 560, maxWidth: '100vw', bgcolor: md.surfaceContainerLow, display: 'flex', flexDirection: 'column',
            ...(phone ? {} : { borderTopLeftRadius: 16, borderBottomLeftRadius: 16 }),
          },
        },
      }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, p: 2, pb: 1 }}>
        <Typography id={headingId} component="h2" sx={{ flex: 1, fontSize: 16, fontWeight: 600, minWidth: 0 }}>{title}</Typography>
        <IconButton aria-label={t('admin:access_control.sheet.close')} onClick={onClose}><CloseIcon /></IconButton>
      </Box>
      {/* pt: an outlined field's shrunk label sits above its border and would be clipped by the scroll area. */}
      <Box sx={{ flex: 1, overflowY: 'auto', px: 2, pt: 1, pb: 2, minWidth: 0 }}>{children}</Box>
    </Drawer>
  )
}
