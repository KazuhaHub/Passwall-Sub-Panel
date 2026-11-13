import { useState } from 'react'
import {
  Box, Divider, IconButton, ListItemText, Menu, MenuItem, Switch, Tooltip, Typography, useTheme,
} from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import { useTranslation } from 'react-i18next'

import type { DestPolicy } from '@/api/accessControl'
import ToneBadge from '@/components/ToneBadge'
import { accessTone } from '@/utils/accessControl'
import { matchSummary, summaryText } from './matchSummary'

export type RowMove = 'up' | 'down' | 'top' | 'bottom'

/**
 * One policy: two lines. The first is the name and its badges, with the
 * switch and the menu at the right; the second says what it matches and for
 * whom. A click anywhere else opens the editor. A policy that is off is
 * drawn at 0.6 and says "paused" in words as well.
 */
export default function PolicyRow({
  policy, index, count, listNames, scopeText, busy, moving, highlighted, onOpen, onToggle, onMove, onDuplicate, onDelete,
}: {
  policy: DestPolicy
  /** 0-based, within its action's section. */
  index: number
  count: number
  listNames: Record<number, string>
  scopeText: string
  /** This row's switch is saving. */
  busy: boolean
  /** A reorder is in flight: no other move may start. */
  moving: boolean
  highlighted: boolean
  onOpen: () => void
  onToggle: () => void
  onMove: (m: RowMove) => void
  onDuplicate: () => void
  onDelete: () => void
}) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const [menu, setMenu] = useState<HTMLElement | null>(null)
  const off = policy.state !== 'on'

  const summary = summaryText(t, matchSummary({
    listNames: policy.list_ids.map(id => listNames[id] ?? t('admin:access_control.policies.row.deleted_list')),
    cidrs: policy.inline.cidrs?.length ?? 0,
    private: policy.inline.private === true,
    bittorrent: (policy.inline.protocols ?? []).includes('bittorrent'),
    ports: policy.inline.ports ?? '',
    network: policy.inline.network ?? '',
  }))
  const notReady = policy.list_states.some(s => s.state === 'not_ready')
  const emptyList = policy.list_states.some(s => s.state === 'empty')
  const over = policy.note?.state === 'empty' && !notReady

  return (
    <Box data-policy-id={policy.id} data-testid="policy-row" onClick={onOpen}
      sx={{
        p: 1.5, borderRadius: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${highlighted ? md.primary : md.outlineVariant}`,
        cursor: 'pointer', transition: 'border-color .2s', minWidth: 0,
      }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, opacity: off ? 0.6 : 1 }}>
        <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 0.75, flex: 1, minWidth: 0 }}>
          <Typography sx={{ fontSize: 14, fontWeight: 600, overflowWrap: 'anywhere' }}>{policy.name}</Typography>
          <ToneBadge tone={accessTone(theme, policy.action === 'block' ? 'block' : 'allow')} label={t(`admin:access_control.policies.action.${policy.action}`)} />
          {off && <ToneBadge tone={accessTone(theme, 'paused')} label={t('admin:access_control.policies.row.paused')} />}
          {notReady && (
            <Tooltip title={t('admin:access_control.policies.row.not_ready_hint')}>
              <span><ToneBadge tone={accessTone(theme, 'attention')} label={t('admin:access_control.policies.row.not_ready')} /></span>
            </Tooltip>
          )}
          {!notReady && emptyList && <ToneBadge tone={accessTone(theme, 'none')} label={t('admin:access_control.policies.row.empty_list')} />}
          {policy.scope_missing && <ToneBadge tone={accessTone(theme, 'failing')} label={t('admin:access_control.policies.row.scope_missing_badge')} />}
          {over && <ToneBadge tone={accessTone(theme, 'none')} label={t('admin:access_control.policies.row.nothing_left')} />}
        </Box>
        <Box onClick={e => e.stopPropagation()} sx={{ display: 'flex', alignItems: 'center', flex: '0 0 auto' }}>
          {/* Disabled while saving, never swapped for a spinner: a swapped-out control drops keyboard focus. */}
          <Switch checked={!off} disabled={busy} onChange={onToggle}
            slotProps={{ input: { 'aria-label': t('admin:access_control.policies.row.toggle', { name: policy.name }), 'aria-busy': busy || undefined } }} />
          <IconButton aria-label={t('admin:access_control.policies.row.menu', { name: policy.name })} aria-haspopup="menu"
            onClick={e => setMenu(e.currentTarget)} sx={{ width: 44, height: 44 }}>
            <MoreVertIcon />
          </IconButton>
          <Menu anchorEl={menu} open={menu !== null} onClose={() => setMenu(null)}>
            {([
              ['edit', () => onOpen(), false],
              ['duplicate', onDuplicate, false],
              ['up', () => onMove('up'), moving || index === 0],
              ['down', () => onMove('down'), moving || index === count - 1],
              ['top', () => onMove('top'), moving || index === 0],
              ['bottom', () => onMove('bottom'), moving || index === count - 1],
            ] as const).map(([key, fn, disabled]) => (
              <MenuItem key={key} disabled={disabled} onClick={() => { setMenu(null); fn() }}>
                <ListItemText>{t(`admin:access_control.policies.row.menu_${key}`)}</ListItemText>
              </MenuItem>
            ))}
            <Divider />
            <MenuItem onClick={() => { setMenu(null); onDelete() }} sx={{ color: md.error }}>
              <ListItemText>{t('admin:access_control.policies.row.menu_delete')}</ListItemText>
            </MenuItem>
          </Menu>
        </Box>
      </Box>
      <Typography variant="body2" sx={{ mt: 0.25, color: md.onSurfaceVariant, opacity: off ? 0.6 : 1, overflowWrap: 'anywhere' }}>
        {[summary || t('admin:access_control.policies.row.no_condition'), scopeText].join(' · ')}
        {policy.scope_missing ? ` · ${t('admin:access_control.policies.row.scope_missing')}` : ''}
        {notReady ? ` · ${t('admin:access_control.policies.row.not_ready_detail')}` : ''}
      </Typography>
    </Box>
  )
}
