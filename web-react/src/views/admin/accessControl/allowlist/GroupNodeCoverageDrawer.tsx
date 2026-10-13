import { useId } from 'react'
import { Alert, Box, DialogContent, DialogTitle, Drawer, IconButton, Skeleton, Stack, Typography, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { AsyncButton } from '@/components/AsyncButton'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { useDestinationGroup } from '@/query/destinationGroups'
import type { QueryScope } from '@/query/session'
import { useAccessTranslation } from '../useAccessTranslation'

const P = 'admin:access_control.group_coverage.'
export default function GroupNodeCoverageDrawer({ scope, groupID, onClose }: { scope: QueryScope; groupID: number; onClose: () => void }) {
  const { t, number } = useAccessTranslation(['admin', 'common']), theme = useTheme(), titleID = useId()
  const query = useDestinationGroup(scope, groupID), group = query.error ? undefined : query.data
  return <Drawer open anchor="right" onClose={onClose} slotProps={{ paper: { role: 'dialog', 'aria-modal': true, 'aria-labelledby': titleID, sx: { width: { xs: '100vw', sm: 560 }, maxWidth: '100vw', bgcolor: theme.palette.md.surfaceContainerLow, borderTopLeftRadius: 16 } } }}>
    <Box sx={{ display: 'flex', alignItems: 'center' }}><DialogTitle id={titleID} sx={{ flex: 1 }}>{t(`${P}title`, { name: group?.group_name ?? '' })}</DialogTitle><IconButton aria-label={t('common:actions.close')} onClick={onClose} sx={{ width: 44, height: 44, mr: 2 }}><CloseIcon /></IconButton></Box>
    <DialogContent><Stack spacing={2}>
      {query.error ? <Alert severity="error" action={<AsyncButton pending={query.isFetching} onClick={() => query.refetch()}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}read_failed`)}</Alert> : !group ? <Skeleton height={240} aria-busy="true" /> : <>
        <KpiGrid><KpiTile label={t(`${P}matched`)} value={number(group.total_nodes)} /><KpiTile label={t(`${P}eligible`)} value={number(group.eligible_nodes)} /><KpiTile label={t(`${P}applied`)} value={number(group.applied_nodes)} /><KpiTile label={t(`${P}excluded`)} value={number(group.ineligible.length)} /></KpiGrid>
        <Typography variant="body2" color="text.secondary">{t(`${P}scope`)}</Typography>
        {group.ineligible.map(node => <Box key={node.panel_id} sx={{ p: 2, borderRadius: 2, bgcolor: theme.palette.md.errorContainer, color: theme.palette.md.onErrorContainer }}><Typography sx={{ overflowWrap: 'anywhere' }}>{node.name}</Typography><Typography variant="body2">{t(`admin:access_control.allowlist.reason.${node.reason}`)}</Typography></Box>)}
        {group.readiness.unapplied_nodes.map(node => <Box key={node.panel_id} sx={{ p: 2, borderRadius: 2, bgcolor: theme.palette.md.secondaryContainer, color: theme.palette.md.onSecondaryContainer }}><Typography sx={{ overflowWrap: 'anywhere' }}>{node.name}</Typography><Typography variant="body2">{t(`${P}pending`)}</Typography></Box>)}
        {group.mode === 'allowlist' && group.eligible_nodes > 0 && group.applied_nodes === group.eligible_nodes && group.ineligible.length === 0 && group.readiness.unapplied_nodes.length === 0 && <Typography>{t(`${P}ready`)}</Typography>}
      </>}
    </Stack></DialogContent>
  </Drawer>
}
