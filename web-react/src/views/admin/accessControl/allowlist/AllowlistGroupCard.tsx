import { useState } from 'react'
import { Box, Button, IconButton, Link, ListItemText, Menu, MenuItem, Typography, useTheme } from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutlineOutlined'
import HourglassEmptyIcon from '@mui/icons-material/HourglassEmpty'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useTranslation } from 'react-i18next'

import type { DestGroup } from '@/api/accessControl'
import FieldHint from '@/components/FieldHint'
import { amber } from '@/components/ToneBadge'
import StateBadge from './StateBadge'
import { stateDay } from './allowlistState'

const C = 'admin:access_control.allowlist.card.'

export interface AllowlistGroupCardProps {
  group: DestGroup
  now: number
  highlighted?: boolean
  /** Names of the nodes that left this group out for weak sniffing. */
  droppedOn: string[]
  onOpenEntries: (listId: number) => void
  onOpenNodes: () => void
  onReport: () => void
  onViewDenied: () => void
  onTurnOn: () => void
  onBackToReport: () => void
  onTurnOff: () => void
  onEditLists: () => void
  onOpenGroup: () => void
}

/**
 * ONE ALLOWLIST GROUP (S8): its state, what its list is made of, how many nodes
 * enforce it and what it did or would have done. A group that is off keeps its
 * lists and is a quiet card with one way back. The card is a summary; every
 * change is a button that opens the thing that explains it.
 */
export default function AllowlistGroupCard(p: AllowlistGroupCardProps) {
  const { group: g, now } = p
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const [menu, setMenu] = useState<HTMLElement | null>(null)
  const num = new Intl.NumberFormat(i18n.language)
  const off = g.state === 'off'
  const noNodes = !off && g.eligible_nodes === 0
  const sep = t(`${C}list_separator`)
  const link = (label: string, onClick: () => void) => (
    <Link component="button" type="button" underline="hover" onClick={onClick} sx={{ font: 'inherit', textAlign: 'left', verticalAlign: 'baseline' }}>{label}</Link>
  )
  const listLinks = g.lists.map((l, i) => (
    <Box component="span" key={l.id}>{i > 0 && sep}{link(t(`${C}list_item`, { name: l.name, entries: num.format(l.entry_count) }), () => p.onOpenEntries(l.id))}</Box>
  ))
  if (g.extra_list_id > 0) {
    listLinks.push(<Box component="span" key="extra">{listLinks.length > 0 && sep}{link(t(`${C}extra_list`), () => p.onOpenEntries(g.extra_list_id))}</Box>)
  }
  const menuItem = (key: string, label: string, onClick: () => void) => (
    <MenuItem key={key} onClick={() => { setMenu(null); onClick() }}><ListItemText>{label}</ListItemText></MenuItem>
  )

  return (
    <Box data-testid="allowlist-card" data-group={g.group_id} sx={{
      borderRadius: 3, border: `1px solid ${p.highlighted ? md.primary : md.outlineVariant}`, bgcolor: off ? md.surfaceContainerLow : md.surfaceContainer,
      overflow: 'hidden', minWidth: 0,
    }}>
      {noNodes && (
        <Box data-testid="allowlist-no-nodes" sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', px: 2, py: 1, bgcolor: md.errorContainer, color: md.onErrorContainer }}>
          <ErrorOutlineIcon aria-hidden sx={{ fontSize: 18 }} />
          <Typography variant="body2" sx={{ flex: 1, minWidth: 0 }}>{t(`${C}no_nodes`)}</Typography>
          <Button size="small" color="inherit" onClick={p.onOpenNodes}>{t(`${C}no_nodes_action`)}</Button>
        </Box>
      )}
      <Box sx={{ p: 2, display: 'grid', gap: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
          <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600, minWidth: 0, overflowWrap: 'anywhere', flex: { xs: '1 1 0', sm: '0 1 auto' } }}>{g.group_name}</Typography>
          <Box sx={{ order: { xs: 3, sm: 2 }, flex: { xs: '1 0 100%', sm: '0 0 auto' } }}>
            <StateBadge state={g.state} day={stateDay(g.state_changed_at, now)} />
          </Box>
          <IconButton size="small" aria-label={t(`${C}menu`, { name: g.group_name })} aria-haspopup="menu" onClick={e => setMenu(e.currentTarget)}
            sx={{ ml: { sm: 'auto' }, order: { xs: 2, sm: 3 }, minWidth: 44, minHeight: 44 }}>
            <MoreVertIcon />
          </IconButton>
        </Box>

        <Typography variant="body2" sx={{ color: md.onSurfaceVariant, overflowWrap: 'anywhere' }}>
          {t(`${C}members`, { members: num.format(g.members) })}{' · '}{t(`${C}lists_label`)}{listLinks.length > 0 ? listLinks : t(`${C}lists_none`)}{' · '}{t(`${C}base`, { count: g.base_count })}
        </Typography>

        {!off && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, flexWrap: 'wrap' }}>
            <Typography variant="body2">{t(`${C}nodes`, { ok: g.eligible_nodes, total: g.total_nodes })}</Typography>
            {g.ineligible.length > 0 && (
              <Typography variant="body2" component="span">
                <FieldHint tone="muted" summary={t(`${C}not_open`, { count: g.ineligible.length })}
                  detail={g.ineligible.map(n => t(`${C}not_open_item`, { name: n.name, reason: t(`admin:access_control.allowlist.reason.${n.reason}`) })).join(t(`${C}detail_separator`))} />
              </Typography>
            )}
          </Box>
        )}
        {!off && g.pending_nodes.length > 0 && (
          <Typography variant="body2" data-testid="allowlist-applying" sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <HourglassEmptyIcon aria-hidden sx={{ fontSize: 16, color: md.onSecondaryContainer }} />
            {t(`${C}applying`, { count: g.pending_nodes.length })}
          </Typography>
        )}
        {!off && g.not_ready_lists.length > 0 && (
          <Typography variant="body2" data-testid="allowlist-list-not-ready" sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <WarningAmberIcon aria-hidden sx={{ fontSize: 16, color: amber(theme).fg }} />
            {t(`${C}list_not_ready`, { list: g.not_ready_lists.map(l => l.name).join(sep) })}
          </Typography>
        )}
        {p.droppedOn.length > 0 && (
          <Typography variant="body2" data-testid="allowlist-sniffing-dropped" sx={{ display: 'flex', alignItems: 'flex-start', gap: 0.5 }}>
            <WarningAmberIcon aria-hidden sx={{ fontSize: 16, mt: '2px', color: amber(theme).fg }} />
            <span>{t(`${C}sniffing_dropped`, { nodes: p.droppedOn.join(sep) })}</span>
          </Typography>
        )}
        {g.state === 'report' && g.would_deny_recent && (
          <Typography variant="body2">{t(`${C}would_deny`, {
            days: g.would_deny_recent.days, sites: num.format(g.would_deny_recent.sites), times: num.format(g.would_deny_recent.count),
          })}</Typography>
        )}
        {g.state === 'on' && g.denied_24h !== null && (
          <Typography variant="body2">{t(`${C}denied_24h`, { times: num.format(g.denied_24h) })}</Typography>
        )}

        <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap', justifyContent: { sm: 'flex-end' }, '& > button': { flex: { xs: '1 1 0', sm: '0 0 auto' } } }}>
          {g.state === 'on' && <Button size="small" onClick={p.onViewDenied}>{t(`${C}view_denied`)}</Button>}
          {g.state === 'report' && <Button size="small" variant="outlined" onClick={p.onReport}>{t(`${C}view_report`)}</Button>}
          {g.state === 'report' && <Button size="small" variant="contained" onClick={p.onTurnOn}>{t(`${C}turn_on`)}</Button>}
          {g.state === 'on' && <Button size="small" variant="outlined" onClick={p.onBackToReport}>{t(`${C}back_to_report`)}</Button>}
          {off && <Button size="small" variant="outlined" onClick={p.onEditLists}>{t(`${C}reenable`)}</Button>}
        </Box>
      </Box>

      <Menu anchorEl={menu} open={menu !== null} onClose={() => setMenu(null)}>
        {menuItem('edit', t(`${C}edit_lists`), p.onEditLists)}
        {g.state === 'report' && menuItem('on', t(`${C}turn_on`), p.onTurnOn)}
        {g.state === 'on' && menuItem('report', t(`${C}back_to_report`), p.onBackToReport)}
        {!off && menuItem('off', t(`${C}turn_off`), p.onTurnOff)}
        {menuItem('group', t(`${C}open_group`), p.onOpenGroup)}
      </Menu>
    </Box>
  )
}
