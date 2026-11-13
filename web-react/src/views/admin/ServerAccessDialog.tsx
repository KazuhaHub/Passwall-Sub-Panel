import { Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, useMediaQuery, useTheme } from '@mui/material'
import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import { Link as RouterLink, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { DestNodeStatus } from '@/api/accessControl'
import NodePolicyStatusRow from '@/components/NodePolicyStatusRow'
import { useRetryDestAgent } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { reportDestError, reportRetry } from './accessControl/destErrors'

/**
 * One native node's access-control state, opened from the Servers page's
 * "Access control: …" line (batch 1: the state line and its problems; the
 * recording and hits blocks arrive with later batches). The state is drawn by
 * the one NodePolicyStatusRow the coverage sheet uses.
 */
export default function ServerAccessDialog({ node, serverName, onClose }: {
  /** The node's delivery state, or null (closed). */
  node: DestNodeStatus | null
  serverName: string
  onClose: () => void
}) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const fullScreen = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const navigate = useNavigate()
  const retry = useRetryDestAgent(scope)

  return (
    <Dialog open={node !== null} onClose={onClose} fullWidth maxWidth="sm" fullScreen={fullScreen}
      slotProps={{ paper: { sx: { bgcolor: theme.palette.md.surfaceContainerHigh, borderRadius: fullScreen ? 0 : 3 } } }}>
      <DialogTitle>{t('admin:servers.access.dialog_title', { name: serverName })}</DialogTitle>
      <DialogContent>
        {node && (
          <Box>
            <NodePolicyStatusRow node={node} variant="block"
              onOpenInbound={id => { onClose(); void navigate(`/admin/nodes?inbound=${id}`) }}
              onRetry={async id => { try { reportRetry(t, await retry.mutateAsync(id)) } catch (err) { reportDestError(t, err) } }}
              onUpgrade={onClose} onOpenLists={() => { onClose(); void navigate('/admin/access-control?tab=lists') }} />
          </Box>
        )}
      </DialogContent>
      <DialogActions>
        <Button component={RouterLink} to="/admin/access-control?sheet=nodes" endIcon={<ChevronRightIcon />} onClick={onClose}>
          {t('admin:servers.access.open_page')}
        </Button>
        <Button onClick={onClose}>{t('admin:servers.access.close')}</Button>
      </DialogActions>
    </Dialog>
  )
}
