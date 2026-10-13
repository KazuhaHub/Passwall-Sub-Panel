import { useId, useState } from 'react'
import { Box, Button, IconButton, Menu, MenuItem, Paper, Stack, Typography, useTheme } from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import ScienceOutlinedIcon from '@mui/icons-material/ScienceOutlined'
import LockOutlinedIcon from '@mui/icons-material/LockOutlined'
import HourglassEmptyIcon from '@mui/icons-material/HourglassEmpty'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import type { DestinationGroupSummary } from '@/api/destinationGroups'
import type { DestinationListSummary } from '@/api/accessControl'
import FieldHint from '@/components/FieldHint'
import { stateTone } from '@/components/ToneBadge'
import { useAccessTranslation } from '../useAccessTranslation'

export interface AllowlistCardActions {
  onList: (id: number) => void
  onReport: (group: DestinationGroupSummary) => void
  onDenied: (group: DestinationGroupSummary) => void
  onEdit: (group: DestinationGroupSummary) => void
  onChangeMode: (group: DestinationGroupSummary, target: 'trial' | 'enforce' | 'open') => void
  onOpenGroup: (group: DestinationGroupSummary) => void
}
const P = 'admin:access_control.allowlist.'
export default function AllowlistGroupCard({ group, lists, ...actions }: AllowlistCardActions & { group: DestinationGroupSummary; lists?: DestinationListSummary[] }) {
  const { t } = useAccessTranslation(['admin', 'common']), theme = useTheme(), titleID = useId(), menuID = useId()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const trial = group.stage === 'trial', elapsed = group.stage_changed_at === null ? -1 : Date.now() - group.stage_changed_at
  const trialDay = elapsed < 0 ? null : Math.floor(elapsed / 86400000) + 1
  const byID = new Map(lists?.map(list => [list.id, list]))
  const selected = [...new Set([...group.list_ids, group.extra_list_id])].map(id => byID.get(id))
  const known = lists !== undefined && selected.every(Boolean) && byID.has(group.base_list_id)
  const notReady = known ? selected.filter((list): list is DestinationListSummary => !!list && list.kind !== 'custom' && list.last_fetched_at === null) : []
  const pending = group.eligible_nodes - group.applied_nodes
  const measuring = stateTone(theme, 'measuring'), attention = stateTone(theme, 'attention')
  const menuAction = (run: () => void) => { setAnchor(null); run() }
  return <Paper component="article" aria-labelledby={titleID} variant="outlined" sx={{ borderRadius: 3, p: { xs: 2, sm: 2.5 }, minWidth: 0 }}>
    <Stack direction="row" spacing={1} sx={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ minWidth: 0, alignItems: { xs: 'flex-start', sm: 'center' } }}>
        <Typography id={titleID} component="h3" variant="h6" sx={{ overflowWrap: 'anywhere' }}>{group.group_name}</Typography>
        <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: .5, px: 1, py: .5, borderRadius: 2, bgcolor: trial ? theme.palette.md.secondaryContainer : theme.palette.md.surfaceContainerHigh, color: trial ? theme.palette.md.onSecondaryContainer : theme.palette.md.onSurface }}>
          {trial ? <ScienceOutlinedIcon fontSize="small" aria-hidden /> : <LockOutlinedIcon fontSize="small" aria-hidden />}
          <Typography variant="body2">{trial ? trialDay === null ? t(`${P}trial`) : t(`${P}trial_day`, { days: trialDay }) : t(`${P}enforced`)}</Typography>
        </Box>
      </Stack>
      <IconButton aria-label={t(`${P}menu`, { name: group.group_name })} aria-controls={anchor ? menuID : undefined} aria-haspopup="menu" aria-expanded={!!anchor} onClick={event => setAnchor(event.currentTarget)} sx={{ minWidth: 44, minHeight: 44 }}><MoreVertIcon /></IconButton>
    </Stack>
    <Stack spacing={1.25} sx={{ mt: 1.5 }}>
      {group.eligible_nodes === 0 && <Box sx={{ bgcolor: theme.palette.md.errorContainer, color: theme.palette.md.onErrorContainer, borderRadius: 2, p: 1.5 }}><Typography sx={{ fontWeight: 600 }}>{t(`${P}zero_nodes`)}</Typography></Box>}
      <Typography variant="body2">{t(`${P}members`, { count: group.members })}</Typography>
      {known ? <Stack direction="row" sx={{ flexWrap: 'wrap', alignItems: 'center', gap: .5 }}>
        <Typography variant="body2">{t(`${P}lists_label`)}</Typography>
        {selected.map(list => list && <Button key={list.id} size="small" onClick={() => actions.onList(list.id)} sx={{ minHeight: 44, textTransform: 'none', overflowWrap: 'anywhere' }}>{t(`${P}list_count`, { name: list.name, count: list.entry_count })}</Button>)}
        <Button size="small" onClick={() => actions.onList(group.base_list_id)} sx={{ minHeight: 44, textTransform: 'none' }}>{t(`${P}base_count`, { count: byID.get(group.base_list_id)!.entry_count })}</Button>
      </Stack> : <Typography variant="body2" color="text.secondary">{t(`${P}lists_unknown`)}</Typography>}
      <Typography variant="body2">{t(`${P}nodes`, { ok: group.eligible_nodes, total: group.total_nodes })}</Typography>
      {group.ineligible.length > 0 && <FieldHint tone="muted" summary={t(`${P}excluded`, { count: group.ineligible.length })} detail={group.ineligible.map(node => `${node.name} · ${t(`${P}reason.${node.reason}`)}`).join('; ')} />}
      {pending > 0 && <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, borderRadius: 2, p: 1.25, bgcolor: measuring.bg, color: measuring.fg }}><HourglassEmptyIcon fontSize="small" aria-hidden /><Typography variant="body2">{t(`${P}pending`, { count: pending })}</Typography></Box>}
      {notReady.map(list => <Box key={list.id} sx={{ display: 'flex', alignItems: 'center', gap: 1, borderRadius: 2, p: 1.25, bgcolor: attention.bg, color: attention.fg }}><WarningAmberIcon fontSize="small" aria-hidden sx={{ color: attention.iconColor }} /><Typography variant="body2">{t(`${P}not_ready`, { name: list.name })}</Typography></Box>)}
      <Typography variant="body2">{trial ? t(`${P}would_deny`, { days: group.would_deny_recent.days, sites: group.would_deny_recent.sites, count: group.would_deny_recent.count }) : t(`${P}denied`, { count: group.denied_24h })}</Typography>
      <Stack direction="row" sx={{ gap: 1, justifyContent: 'flex-end', '& > button': { minHeight: 44, flex: { xs: 1, sm: '0 1 auto' } } }}>
        {trial ? <><Button onClick={() => actions.onReport(group)}>{t(`${P}report`)}</Button><Button variant="outlined" onClick={() => actions.onChangeMode(group, 'enforce')}>{t(`${P}enforce`)}</Button></> : <Button onClick={() => actions.onDenied(group)}>{t(`${P}view_denied`)}</Button>}
      </Stack>
    </Stack>
    <Menu id={menuID} anchorEl={anchor} open={!!anchor} onClose={() => setAnchor(null)} slotProps={{ list: { 'aria-label': t(`${P}menu`, { name: group.group_name }) } }}>
      <MenuItem sx={{ minHeight: 44 }} onClick={() => menuAction(() => actions.onEdit(group))}>{t(`${P}edit_lists`)}</MenuItem>
      {!trial && <MenuItem sx={{ minHeight: 44 }} onClick={() => menuAction(() => actions.onChangeMode(group, 'trial'))}>{t(`${P}return_trial`)}</MenuItem>}
      <MenuItem sx={{ minHeight: 44 }} onClick={() => menuAction(() => actions.onChangeMode(group, 'open'))}>{t(`${P}close_mode`)}</MenuItem>
      <MenuItem sx={{ minHeight: 44 }} onClick={() => menuAction(() => actions.onOpenGroup(group))}>{t(`${P}open_group`)}</MenuItem>
    </Menu>
  </Paper>
}
