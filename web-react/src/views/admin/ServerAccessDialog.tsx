import { useRef, useState } from 'react'
import { Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Skeleton, Stack, useMediaQuery, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { Link as RouterLink } from 'react-router'
import type { Server } from '@/api/servers'
import { AsyncButton } from '@/components/AsyncButton'
import { pushSnack } from '@/components/SnackbarHost'
import { useDestinationStatus, useRetryDestinationPolicy } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { useAccessTranslation } from './accessControl/useAccessTranslation'
import { destinationError } from './accessControl/errors'
import NodePolicyStatusRow from './accessControl/NodePolicyStatusRow'

const P = 'admin:access_control.'
export default function ServerAccessDialog({ server, onClose }: { server: Server; onClose: () => void }) {
  const theme = useTheme()
  const narrow = useMediaQuery(theme.breakpoints.down('sm'))
  const { t } = useAccessTranslation(['admin', 'common'])
  const canView = useCan('access.view')
  const scope = useQueryScope()
  const status = useDestinationStatus(scope, canView && server.panel_type === 'psp')
  const retry = useRetryDestinationPolicy(scope)
  const node = status.data?.nodes.find(node => node.panel_id === server.id && node.kind === 'psp')
  const [error, setError] = useState('')
  const admission = useRef(false)
  const retryNode = async (agent: string) => {
    if (admission.current || !canView) return
    admission.current = true; setError('')
    try { await retry.mutateAsync(agent); pushSnack(t(`${P}coverage.retry_requested`), 'success') }
    catch (error) { setError(destinationError(error).error) }
    finally { admission.current = false }
  }
  if (!canView || server.panel_type !== 'psp') return null
  const retryRead = <AsyncButton pending={status.isFetching} onClick={() => status.refetch()} sx={{ minHeight: 44 }}>{t('common:actions.retry')}</AsyncButton>
  return <Dialog open fullScreen={narrow} maxWidth="sm" fullWidth onClose={onClose} aria-labelledby="server-access-title"
    slotProps={{ paper: { sx: { bgcolor: theme.palette.md.surfaceContainerLow } } }}>
    <Box sx={{ display: 'flex', alignItems: 'center' }}>
      <DialogTitle id="server-access-title" sx={{ flex: 1, minWidth: 0, overflowWrap: 'anywhere' }}>{t(`${P}server.title`, { name: server.name })}</DialogTitle>
      <IconButton aria-label={t('common:actions.close')} onClick={onClose} sx={{ width: 44, height: 44, mr: 2 }}><CloseIcon /></IconButton>
    </Box>
    <DialogContent><Stack spacing={2}>
      {error && <Alert severity="error">{error}</Alert>}
      {!status.data && status.isPending ? <Stack role="progressbar" aria-label={t(`${P}coverage.loading`)} spacing={1}><Skeleton variant="rounded" height={40} /><Skeleton variant="rounded" height={96} /></Stack>
        : !node ? <Alert severity="error" action={retryRead}>{t(`${P}coverage.unavailable`)}</Alert>
          : <>
            {status.isError && <Alert severity="warning" action={retryRead}>{t(`${P}coverage.read_stale`)}</Alert>}
            {status.data!.generation !== status.data!.published_generation && <Alert severity="info">{t(`${P}server.unpublished`)}</Alert>}
            <NodePolicyStatusRow node={node} now={Date.now()} onRetry={retryNode} retryPending={retry.isPending} />
          </>}
    </Stack></DialogContent>
    <DialogActions sx={{ flexWrap: 'wrap' }}>
      <Button component={RouterLink} to="/admin/access-control?sheet=nodes" sx={{ minHeight: 44 }}>{t(`${P}server.open_access`)}</Button>
      <Button onClick={onClose} sx={{ minHeight: 44 }}>{t('common:actions.close')}</Button>
    </DialogActions>
  </Dialog>
}
