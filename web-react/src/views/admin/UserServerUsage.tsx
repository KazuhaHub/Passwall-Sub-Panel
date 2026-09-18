import { useMemo, useState } from 'react'
import {
  Box, CircularProgress, Table, TableBody, TableCell,
  TableContainer, TableFooter, TableHead, TableRow, TableSortLabel, Typography, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { UserServerUsageRow } from '@/api/traffic'
import { useUserServerUsage } from '@/query/traffic'
import { useQueryScope } from '@/query/useQueryScope'

// fmt renders a byte count compactly (single-letter unit).
function fmt(n: number): string {
  if (!n) return '0'
  const units = ['B', 'K', 'M', 'G', 'T']
  let v = n
  let u = 0
  while (v >= 1024 && u < units.length - 1) { v /= 1024; u++ }
  return `${v >= 100 || u === 0 ? Math.round(v) : v.toFixed(1)}${units[u]}`
}

type SortKey = 'lifetime' | 'period' | 'today'

/** UserServerUsage shows one user's usage broken down per SERVER (3X-UI panel)
 *  — the per-node rows aggregated by server. This is the per-(user, server)
 *  view the admin uses to see "how much did this user pull from each server".
 *  Servers per user are few, so the whole list renders without search/paging;
 *  the "合计" footer is the user's grand total across servers. Mirrors
 *  UserNodeUsage's look so the two read the same. */
export function UserServerUsage({ userId }: { userId: number }) {
  const { t } = useTranslation('admin')
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const { data, isPending, isError } = useUserServerUsage(scope, userId)
  const rows = data
  const [orderBy, setOrderBy] = useState<SortKey>('period')
  const [orderDir, setOrderDir] = useState<'asc' | 'desc'>('desc')

  const grand = useMemo(() => (rows ?? []).reduce((a, r) => ({
    lifetime: a.lifetime + r.lifetime_total_bytes, lifeUp: a.lifeUp + r.lifetime_up_bytes, lifeDown: a.lifeDown + r.lifetime_down_bytes,
    period: a.period + r.period_total_bytes, periodUp: a.periodUp + r.period_up_bytes, periodDown: a.periodDown + r.period_down_bytes,
    today: a.today + r.today_total_bytes, todayUp: a.todayUp + r.today_up_bytes, todayDown: a.todayDown + r.today_down_bytes,
  }), { lifetime: 0, lifeUp: 0, lifeDown: 0, period: 0, periodUp: 0, periodDown: 0, today: 0, todayUp: 0, todayDown: 0 }), [rows])

  const sorted = useMemo(() => {
    const metric = (r: UserServerUsageRow) =>
      orderBy === 'lifetime' ? r.lifetime_total_bytes
        : orderBy === 'today' ? r.today_total_bytes
          : r.period_total_bytes
    return [...(rows ?? [])].sort((a, b) => orderDir === 'desc' ? metric(b) - metric(a) : metric(a) - metric(b))
  }, [rows, orderBy, orderDir])

  const setSort = (k: SortKey) => {
    if (orderBy === k) setOrderDir(d => d === 'asc' ? 'desc' : 'asc')
    else { setOrderBy(k); setOrderDir('desc') }
  }

  const title = (
    <Typography sx={{ fontSize: 13, fontWeight: 600, color: md.onSurfaceVariant, mb: 1 }}>
      {t('users.serverusage.title', { defaultValue: '按服务器用量' })}
    </Typography>
  )

  if (isPending) {
    return <Box>{title}<CircularProgress size={20} /></Box>
  }
  // A failed read is NOT "this user has no servers" — that claim would send an
  // admin looking for a coverage problem that may not exist.
  if (isError) {
    return (
      <Box>
        {title}
        <Typography sx={{ fontSize: 12, color: md.error }}>
          {t('users.serverusage.unavailable', { defaultValue: '暂时无法获取服务器用量' })}
        </Typography>
      </Box>
    )
  }
  if (!rows || rows.length === 0) {
    return (
      <Box>
        {title}
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('users.serverusage.empty', { defaultValue: '该用户暂无服务器' })}
        </Typography>
      </Box>
    )
  }

  const cell = (total: number, up: number, down: number) => (
    <Box sx={{ fontVariantNumeric: 'tabular-nums' }}>
      <Box sx={{ fontSize: 13, fontWeight: 500 }}>{fmt(total)}</Box>
      <Box sx={{ fontSize: 11, color: md.onSurfaceVariant, whiteSpace: 'nowrap' }}>↑{fmt(up)} ↓{fmt(down)}</Box>
    </Box>
  )

  const headCell = (k: SortKey, label: string) => (
    <TableCell align="right" sortDirection={orderBy === k ? orderDir : false} sx={{ fontWeight: 600 }}>
      <TableSortLabel active={orderBy === k} direction={orderBy === k ? orderDir : 'desc'} onClick={() => setSort(k)}>
        {label}
      </TableSortLabel>
    </TableCell>
  )

  return (
    <Box>
      {title}
      <TableContainer>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell sx={{ fontWeight: 600 }}>{t('users.serverusage.server', { defaultValue: '服务器' })}</TableCell>
              {headCell('lifetime', t('users.nodeusage.lifetime', { defaultValue: '累计' }))}
              {headCell('period', t('users.nodeusage.period', { defaultValue: '本周期' }))}
              {headCell('today', t('users.nodeusage.today', { defaultValue: '今日' }))}
            </TableRow>
          </TableHead>
          <TableBody>
            {sorted.map((r, i) => (
              <TableRow key={r.panel_id || i}>
                <TableCell>
                  <Typography noWrap sx={{ fontSize: 13, fontWeight: 500 }}>
                    {r.server_name || `#${r.panel_id}`}
                  </Typography>
                  <Typography noWrap sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
                    {t('users.serverusage.node_count', { n: r.node_count, defaultValue: '{{n}} 节点' })}
                  </Typography>
                </TableCell>
                <TableCell align="right">{cell(r.lifetime_total_bytes, r.lifetime_up_bytes, r.lifetime_down_bytes)}</TableCell>
                <TableCell align="right">{cell(r.period_total_bytes, r.period_up_bytes, r.period_down_bytes)}</TableCell>
                <TableCell align="right">{cell(r.today_total_bytes, r.today_up_bytes, r.today_down_bytes)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
          <TableFooter>
            <TableRow>
              <TableCell sx={{ fontWeight: 700, color: md.onSurface, borderTop: `2px solid ${md.outlineVariant}` }}>
                {t('users.nodeusage.total', { defaultValue: '合计' })}
              </TableCell>
              <TableCell align="right" sx={{ borderTop: `2px solid ${md.outlineVariant}` }}>{cell(grand.lifetime, grand.lifeUp, grand.lifeDown)}</TableCell>
              <TableCell align="right" sx={{ borderTop: `2px solid ${md.outlineVariant}` }}>{cell(grand.period, grand.periodUp, grand.periodDown)}</TableCell>
              <TableCell align="right" sx={{ borderTop: `2px solid ${md.outlineVariant}` }}>{cell(grand.today, grand.todayUp, grand.todayDown)}</TableCell>
            </TableRow>
          </TableFooter>
        </Table>
      </TableContainer>
    </Box>
  )
}
