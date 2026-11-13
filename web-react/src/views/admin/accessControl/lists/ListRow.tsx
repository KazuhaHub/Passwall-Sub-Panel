import { useState } from 'react'
import { Box, IconButton, ListItemText, Menu, MenuItem, TableCell, TableRow, Tooltip, Typography, useTheme } from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import { useTranslation } from 'react-i18next'

import type { DestList } from '@/api/accessControl'
import ToneBadge from '@/components/ToneBadge'
import { accessTone } from '@/utils/accessControl'
import { formatMsDualTz } from '@/utils/datetime'
import { listErrorKey } from './listError'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { listStatus } from './listStatus'
import UsedByPopover from './UsedByPopover'

const L = 'admin:access_control.lists.'

export interface ListRowProps {
  list: DestList
  /** An enabled policy names this list. */
  usedByEnabled: boolean
  /** The folded layout (a phone): one block per list. */
  block?: boolean
  onOpenEntries: (id: number) => void
  onEdit: (id: number) => void
  onRefresh: (list: DestList) => void
  onDelete: (list: DestList) => void
  onOpenPolicy: (id: number) => void
}

const mono = { fontFamily: 'ui-monospace, monospace' }

/** One list: a table row from md up, a block below. */
export default function ListRow(props: ListRowProps) {
  const { list, usedByEnabled, block, onOpenEntries, onEdit, onRefresh, onDelete, onOpenPolicy } = props
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const st = listStatus(list, usedByEnabled)
  const num = new Intl.NumberFormat(i18n.language)
  const ago = list.last_fetched_at ? formatRelativeTimeShort(Date.now() - list.last_fetched_at, t) : ''
  const abs = list.last_fetched_at ? formatMsDualTz(list.last_fetched_at, '') : ''
  const used = list.used_by.length
  // Only a remote list refreshes on its own say; a community category moves with the rule data.
  const canRefresh = list.kind === 'remote'

  const kindLine = list.kind === 'remote'
    ? <>{t(`${L}kind.remote`)} · <Box component="span" sx={mono}>{safeHost(list.source_url)}</Box></>
    : list.kind === 'geosite'
      ? <>{t(`${L}kind.geosite`)} · <Box component="span" sx={mono}>{list.geosite_category}{list.geosite_attrs?.length ? `@${list.geosite_attrs.join('@')}` : ''}</Box></>
      : t(`${L}kind.custom`)

  const errWords = list.last_error ? (() => { const e = listErrorKey(list.last_error); return t(`${L}error.${e.key}`, e.params) })() : ''
  const second = (st.key === 'failed_old' || st.key === 'failed_never') && list.last_error
    ? (
      <Tooltip title={errWords}>
        <Typography component="span" sx={{ fontSize: 12, color: md.error, display: 'block', overflowWrap: 'anywhere' }}>
          {errWords}{ago ? ` · ${ago}` : ''}
        </Typography>
      </Tooltip>
    )
    : ago ? <Tooltip title={abs}><Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{ago}</Typography></Tooltip> : null

  const badge = <ToneBadge tone={accessTone(theme, st.tone)} label={t(`${L}state.${st.key}`)} data={st.key} testId="list-state" />
  const entries = list.state === 'pending' ? '—' : num.format(list.entry_count)

  const menu = (
    <>
      <IconButton size="small" aria-label={t(`${L}menu.label`, { name: list.name })} aria-haspopup="menu"
        onClick={e => { e.stopPropagation(); setAnchor(e.currentTarget) }} sx={{ minWidth: 44, minHeight: 44 }}>
        <MoreVertIcon />
      </IconButton>
      <Menu anchorEl={anchor} open={anchor !== null} onClose={() => setAnchor(null)}>
        <MenuItem onClick={() => { setAnchor(null); onOpenEntries(list.id) }}><ListItemText>{t(`${L}menu.view`)}</ListItemText></MenuItem>
        {list.kind !== 'geosite' && <MenuItem onClick={() => { setAnchor(null); onEdit(list.id) }}><ListItemText>{t(`${L}menu.edit`)}</ListItemText></MenuItem>}
        {canRefresh && <MenuItem onClick={() => { setAnchor(null); onRefresh(list) }}><ListItemText>{t(`${L}menu.refresh`)}</ListItemText></MenuItem>}
        <Tooltip title={used > 0 ? t(`${L}menu.delete_in_use`, { count: used }) : ''}>
          <span>
            <MenuItem disabled={used > 0} onClick={() => { setAnchor(null); onDelete(list) }} sx={{ color: md.error }}>
              <ListItemText>{t(`${L}menu.delete`)}</ListItemText>
            </MenuItem>
          </span>
        </Tooltip>
      </Menu>
    </>
  )

  if (block) {
    return (
      <Box data-testid="list-block" sx={{ p: 1.5, borderRadius: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, justifyContent: 'space-between' }}>
          <Box sx={{ minWidth: 0 }}>
            <Typography component="button" onClick={() => onOpenEntries(list.id)}
              sx={{ all: 'unset', cursor: 'pointer', fontWeight: 600, overflowWrap: 'anywhere' }}>{list.name}</Typography>
          </Box>
          <Box sx={{ display: 'flex', alignItems: 'center', flex: '0 0 auto' }}>{badge}{menu}</Box>
        </Box>
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, overflowWrap: 'anywhere' }}>
          {kindLine} · <Box component="span" sx={{ position: 'absolute', width: 1, height: 1, overflow: 'hidden', clip: 'rect(0 0 0 0)' }}>{t(`${L}label.entries`)} </Box>
          {entries} · <Box component="span" sx={{ position: 'absolute', width: 1, height: 1, overflow: 'hidden', clip: 'rect(0 0 0 0)' }}>{t(`${L}label.usage`)} </Box>
          <UsedByPopover usedBy={list.used_by} onOpenPolicy={onOpenPolicy} />
        </Typography>
        {second}
      </Box>
    )
  }

  return (
    <TableRow hover data-testid="list-row" sx={{ cursor: 'pointer' }} onClick={() => onOpenEntries(list.id)}>
      <TableCell>
        <Typography sx={{ fontWeight: 600, overflowWrap: 'anywhere' }}>{list.name}</Typography>
        <Typography component="div" sx={{ fontSize: 13, color: md.onSurfaceVariant, overflowWrap: 'anywhere' }}>{kindLine}</Typography>
      </TableCell>
      <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums' }}>{entries}</TableCell>
      <TableCell>{badge}<Box>{second}</Box></TableCell>
      <TableCell onClick={e => e.stopPropagation()}>
        <UsedByPopover usedBy={list.used_by} onOpenPolicy={onOpenPolicy} />
      </TableCell>
      <TableCell align="right" onClick={e => e.stopPropagation()}>{menu}</TableCell>
    </TableRow>
  )
}

function safeHost(url?: string): string {
  if (!url) return ''
  try { return new URL(url).host } catch { return url }
}
