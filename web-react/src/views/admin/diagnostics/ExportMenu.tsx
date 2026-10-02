import { useState } from 'react'
import { CircularProgress, Divider, IconButton, ListItemIcon, ListItemText, Menu, MenuItem, Typography } from '@mui/material'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import DownloadIcon from '@mui/icons-material/Download'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import RestartAltIcon from '@mui/icons-material/RestartAlt'
import type { DiagFormat } from './useDiagFormat'

/**
 * The page's "⋮": copy or download everything it read, and clear the
 * statistics. The clear sits below a divider because it is the one action
 * here that changes anything: it restarts the window for every admin who
 * opens this page. Nothing else reads these counters (only the diagnostics
 * handler calls metrics.Take); the risk center and the node pages read the
 * database, which a clear does not touch.
 */
export default function ExportMenu({ fmt, onCopy, onDownload, onReset, resetting, resetDisabledReason }: {
  fmt: DiagFormat
  onCopy: () => void
  onDownload: () => void
  onReset: () => void
  /** A clear is in flight: the item stays disabled and shows its wait. */
  resetting: boolean
  /** Why clearing makes no sense right now, said under the item. */
  resetDisabledReason?: string
}) {
  const { t } = fmt
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const close = () => setAnchor(null)
  const run = (action: () => void) => () => { close(); action() }
  const disabled = resetting || resetDisabledReason !== undefined

  return (
    <>
      <IconButton aria-label={t('admin:diagnostics.actions.more')} aria-haspopup="menu" onClick={e => setAnchor(e.currentTarget)}>
        <MoreVertIcon />
      </IconButton>
      <Menu anchorEl={anchor} open={anchor !== null} onClose={close}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        slotProps={{ paper: { sx: { maxWidth: 340 } } }}>
        <MenuItem onClick={run(onCopy)}>
          <ListItemIcon><ContentCopyIcon fontSize="small" /></ListItemIcon>
          <ListItemText>{t('admin:diagnostics.actions.copy_json')}</ListItemText>
        </MenuItem>
        <MenuItem onClick={run(onDownload)}>
          <ListItemIcon><DownloadIcon fontSize="small" /></ListItemIcon>
          <ListItemText>{t('admin:diagnostics.actions.download_json')}</ListItemText>
        </MenuItem>
        <Typography variant="caption" component="li" role="note"
          sx={{ display: 'block', px: 2, pb: 1, color: 'text.secondary', whiteSpace: 'normal' }}>
          {t('admin:diagnostics.actions.export_note')}
        </Typography>
        <Divider />
        <MenuItem onClick={run(onReset)} disabled={disabled} aria-busy={resetting || undefined}>
          <ListItemIcon>
            {resetting ? <CircularProgress size={18} color="inherit" /> : <RestartAltIcon fontSize="small" />}
          </ListItemIcon>
          <ListItemText>{t('admin:diagnostics.actions.reset')}</ListItemText>
        </MenuItem>
        {!resetting && resetDisabledReason && (
          <Typography variant="caption" component="li" role="note"
            sx={{ display: 'block', px: 2, pb: 1, color: 'text.secondary', whiteSpace: 'normal' }}>
            {resetDisabledReason}
          </Typography>
        )}
      </Menu>
    </>
  )
}
