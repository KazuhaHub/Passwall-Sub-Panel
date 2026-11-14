import type { ReactNode } from 'react'
import { Box, Button, Skeleton, Typography, useTheme } from '@mui/material'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { DestGroup } from '@/api/accessControl'
import StateBadge from '@/views/admin/accessControl/allowlist/StateBadge'
import { groupHasAllowlist, stateDay } from '@/views/admin/accessControl/allowlist/allowlistState'
import { useDestGroups } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'

const D = 'admin:groups.destination.'

function Row({ label, children }: { label: string; children: ReactNode }) {
  const md = useTheme().palette.md
  return (
    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '96px minmax(0, 1fr)' }, columnGap: 2, rowGap: 0.25, alignItems: 'baseline' }}>
      <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{label}</Typography>
      <Box sx={{ minWidth: 0, overflowWrap: 'anywhere' }}>{children}</Box>
    </Box>
  )
}

/**
 * A group's destination access in the group dialog (S17): a summary and the way
 * to the access-control page, never an editor. The allowlist lives in one place
 * (its tab on the access-control page), so the dialog's save cannot overwrite
 * it with a stale copy. A group with no allowlist is unrestricted and can be
 * given one from here.
 */
export default function GroupDestinationTab({ groupId, onLeave }: {
  /** Null for a group that is not saved yet. */
  groupId: number | null
  /** Called before leaving for the access-control page (the dialog closes). */
  onLeave: () => void
}) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const navigate = useNavigate()
  const scope = useQueryScope()
  const read = useDestGroups(scope, { enabled: groupId !== null })

  if (groupId === null) return <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${D}save_first`)}</Typography>
  if (read.isPending) return <Box sx={{ display: 'grid', gap: 1 }}>{[0, 1, 2].map(i => <Skeleton key={i} variant="rounded" height={24} />)}</Box>
  if (read.isError) {
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Typography variant="body2" sx={{ color: md.error }}>{t(`${D}read_failed`)}</Typography>
        <Button size="small" onClick={() => void read.refetch()}>{t(`${D}retry`)}</Button>
      </Box>
    )
  }

  const g: DestGroup | undefined = read.data?.find(x => x.group_id === groupId)
  const go = (search: string) => { onLeave(); void navigate(`/admin/access-control?tab=allowlist&${search}`) }
  const enforced = g !== undefined && g.state !== 'off'
  const kept = g !== undefined && g.state === 'off' && groupHasAllowlist(g)

  return (
    <Box sx={{ display: 'grid', gap: 1.5 }}>
      <Row label={t(`${D}mode`)}>
        {enforced && g
          ? <StateBadge scope="group" state={g.state} day={stateDay(g.state_changed_at, Date.now())} />
          : <Typography component="span" sx={{ fontWeight: 500 }}>{t(`${D}open`)}</Typography>}
      </Row>
      {enforced && g && (
        <>
          <Row label={t(`${D}lists`)}>
            <Typography variant="body2">{t(`${D}lists_line`, {
              lists: g.lists.length > 0 ? g.lists.map(l => t(`${D}list_item`, { name: l.name, entries: l.entry_count })).join(t(`${D}list_separator`)) : t(`${D}lists_none`),
              base: g.base_count,
            })}</Typography>
          </Row>
          <Row label={t(`${D}nodes`)}>
            <Typography variant="body2" component="span">{t(`${D}nodes_line`, { ok: g.eligible_nodes, total: g.total_nodes })}</Typography>
            {g.ineligible.length > 0 && <Typography variant="body2" component="span" sx={{ ml: 1.5, color: md.onSurfaceVariant }}>{t(`${D}not_open`, { count: g.ineligible.length })}</Typography>}
          </Row>
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${D}summary_note`)}</Typography>
          <Box sx={{ display: 'flex', justifyContent: { sm: 'flex-end' } }}>
            <Button variant="outlined" onClick={() => go(`al_group=${groupId}`)}>{t(`${D}edit`)}</Button>
          </Box>
        </>
      )}
      {!enforced && (
        <>
          <Box>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${D}open_hint`)}</Typography>
            {kept && <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${D}open_kept`)}</Typography>}
          </Box>
          <Box sx={{ display: 'flex', justifyContent: { sm: 'flex-end' } }}>
            <Button variant="outlined" onClick={() => go(`al_enable=${groupId}`)}>{t(`${D}enable`)}</Button>
          </Box>
        </>
      )}
    </Box>
  )
}
