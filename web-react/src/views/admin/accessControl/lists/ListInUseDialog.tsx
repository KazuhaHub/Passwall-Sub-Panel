import { useId, useState } from 'react'
import { Button, Dialog, DialogActions, DialogContent, DialogTitle, List, ListItem, Stack, Typography } from '@mui/material'
import type { DestinationReference } from '@/api/accessControl'
import { useAccessTranslation } from '../useAccessTranslation'
import { listInUseCopy } from '../confirmCopy'

const P = 'admin:access_control.lists.'
export default function ListInUseDialog({ references, onClose, onOpenPolicy }: { references: DestinationReference[]; onClose: () => void; onOpenPolicy: (id: number) => void }) {
  const { t } = useAccessTranslation(['admin', 'common']), titleId = useId(), copy = listInUseCopy(t)
  const [open, setOpen] = useState(true)
  const close = () => setOpen(false)
  return <Dialog open={open} fullWidth maxWidth="sm" onClose={close} disableRestoreFocus aria-labelledby={titleId}
    slotProps={{ transition: { onExited: onClose }, paper: { sx: { width: 480, maxWidth: 'calc(100vw - 32px)', m: 2, '& button': { minWidth: 44, minHeight: 44 } } } }}>
    <DialogTitle id={titleId}>{copy.title}</DialogTitle>
    <DialogContent>
      <Typography variant="body2">{copy.message}</Typography>
      {references.length ? <List disablePadding>{references.map(ref => <ListItem key={`${ref.kind}-${ref.id}`} disableGutters>
        <Stack sx={{ minWidth: 0, width: '100%' }}>
          <Typography variant="caption" color="text.secondary">{t(`${P}reference_${ref.kind}`)}</Typography>
          {ref.kind === 'policy'
            ? <Button aria-label={t(`${P}open_policy`, { name: ref.name })} sx={{ justifyContent: 'flex-start', textAlign: 'left', overflowWrap: 'anywhere' }} onClick={() => onOpenPolicy(ref.id)}>{ref.name}</Button>
            : <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{ref.name}</Typography>}
        </Stack>
      </ListItem>)}</List> : <Typography variant="body2" sx={{ mt: 2 }} color="text.secondary">{t(`${P}in_use_unknown`)}</Typography>}
    </DialogContent>
    <DialogActions><Button autoFocus onClick={close}>{copy.confirmText}</Button></DialogActions>
  </Dialog>
}
