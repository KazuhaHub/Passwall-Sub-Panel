import { useId, useState } from 'react'
import { Box, Button, List, ListItem, Popover, Stack, Typography } from '@mui/material'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import type { DestinationReference } from '@/api/accessControl'
const P = 'admin:access_control.lists.'
interface Props {
  name: string
  references: DestinationReference[]
  ownerGroupId: number
  onOpenPolicy: (id: number) => void
  onOpenGroup?: (id: number) => void
}
export default function UsedByPopover({ name, references, ownerGroupId, onOpenPolicy, onOpenGroup }: Props) {
  const { t } = useAccessTranslation('admin'), titleId = useId()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const policies = references.filter(ref => ref.kind === 'policy').length
  const groups = references.filter(ref => ref.kind === 'group').length
  const summary = ownerGroupId
    ? t(`${P}owner`, { name: references.find(ref => ref.kind === 'group' && ref.id === ownerGroupId)?.name ?? `#${ownerGroupId}` })
    : [policies && t(`${P}policy_count`, { count: policies }), groups && t(`${P}group_count`, { count: groups })].filter(Boolean).join(' · ') || t(`${P}unused`)
  if (!references.length) return <Typography variant="body2">{summary}</Typography>
  const open = (ref: DestinationReference) => { setAnchor(null); if (ref.kind === 'policy') onOpenPolicy(ref.id); else onOpenGroup?.(ref.id) }
  return <>
    <Button aria-label={t(`${P}open_references`, { name })} aria-haspopup="dialog" aria-expanded={!!anchor} onClick={event => setAnchor(event.currentTarget)} sx={{ p: 0, minWidth: 44, minHeight: 44, justifyContent: 'flex-start', textAlign: 'left', overflowWrap: 'anywhere' }}>{summary}</Button>
    <Popover open={!!anchor} anchorEl={anchor} onClose={() => setAnchor(null)} anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }} slotProps={{ paper: { role: 'dialog', 'aria-labelledby': titleId, sx: { width: 360, maxWidth: 'calc(100vw - 32px)' } } }}>
      <Box sx={{ p: 2 }}><Typography id={titleId} variant="subtitle2" sx={{ overflowWrap: 'anywhere' }}>{t(`${P}references_title`, { name })}</Typography>
        <List disablePadding>{references.map(ref => <ListItem key={`${ref.kind}-${ref.id}`} disableGutters>
          <Stack sx={{ minWidth: 0, width: '100%' }}>
            <Typography variant="caption" color="text.secondary">{t(`${P}reference_${ref.kind}`)}</Typography>
            {ref.kind === 'policy' || onOpenGroup ? <Button onClick={() => open(ref)} aria-label={t(`${P}open_${ref.kind}`, { name: ref.name })} sx={{ justifyContent: 'flex-start', minWidth: 44, minHeight: 44, textAlign: 'left', overflowWrap: 'anywhere' }}>{ref.name}</Button> : <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{ref.name}</Typography>}
          </Stack>
        </ListItem>)}</List>
      </Box>
    </Popover>
  </>
}
