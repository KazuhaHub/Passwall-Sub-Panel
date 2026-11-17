import { useMemo, useState } from 'react'
import {
  Box, CircularProgress, Table, TableBody, TableCell,
  TableContainer, TableFooter, TableHead, TableRow, TableSortLabel, Typography, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { UserServerUsageRow } from '@/api/traffic'
import { TrafficFactorBadge } from '@/components/TrafficFactorBadge'
import { UsageBasisToggle, type UsageBasis } from './UsageBasisToggle'
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
  const [basis, setBasis] = useState<UsageBasis>('charged')
  const charged = basis === 'charged'

  const grand = useMemo(() => (rows ?? []).reduce((a, r) => ({
    lifetime: a.lifetime + r.lifetime_total_bytes, lifeUp: a.lifeUp + r.lifetime_up_bytes, lifeDown: a.lifeDown + r.lifetime_down_bytes,
    period: a.period + r.period_total_bytes, periodUp: a.periodUp + r.period_up_bytes, periodDown: a.periodDown + r.period_down_bytes,
    chargedLifetime: a.chargedLifetime + (r.charged_lifetime_bytes ?? 0), chargedPeriod: a.chargedPeriod + (r.charged_period_bytes ?? 0),
    today: a.today + r.today_total_bytes, todayUp: a.todayUp + r.today_up_bytes, todayDown: a.todayDown + r.today_down_bytes,
  }), { lifetime: 0, lifeUp: 0, lifeDown: 0, period: 0, periodUp: 0, periodDown: 0, chargedLifetime: 0, chargedPeriod: 0, today: 0, todayUp: 0, todayDown: 0 }), [rows])

  const sorted = useMemo(() => {
    const metric = (r: UserServerUsageRow) =>
      orderBy === 'lifetime' ? (charged ? r.charged_lifetime_bytes : r.lifetime_total_bytes)
        : orderBy === 'today' ? r.today_total_bytes
          : (charged ? r.charged_period_bytes : r.period_total_bytes)
    return [...(rows ?? [])].sort((a, b) => orderDir === 'desc' ? metric(b) - metric(a) : metric(a) - metric(b))
  }, [rows, orderBy, orderDir, charged])

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

  // Charged mode: the charged total over the raw amount. The charged figure has
  // no up/down split, so the subline says what the raw bytes were instead.
  const chargedCell = (chargedTotal: number, rawTotal: number) => (
    <Box sx={{ fontVariantNumeric: 'tabular-nums' }}>
      <Box sx={{ fontSize: 13, fontWeight: 500 }}>{fmt(chargedTotal)}</Box>
      <Box sx={{ fontSize: 11, color: md.onSurfaceVariant, whiteSpace: 'nowrap' }}>
        {t('users.nodeusage.raw_amount', { amount: fmt(rawTotal), defaultValue: '原始 {{amount}}' })}
      </Box>
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
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, mb: 1, flexWrap: 'wrap' }}>
        {title}
        <UsageBasisToggle value={basis} onChange={setBasis} />
      </Box>
      {charged && (
        <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant, mb: 1 }}>
          {t('users.nodeusage.charged_hint', { defaultValue: '计费合计已按各服务器倍率换算；上行/下行拆分与今日为原始字节。' })}
        </Typography>
      )}
      <TableContainer>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell sx={{ fontWeight: 600 }}>{t('users.serverusage.server', { defaultValue: '服务器' })}</TableCell>
              {headCell('lifetime', t('users.nodeusage.lifetime', { defaultValue: '累计' }))}
              {headCell('period', t('users.nodeusage.period', { defaultValue: '本周期' }))}
              {headCell('today', charged ? t('users.nodeusage.today_raw', { defaultValue: '今日（原始）' }) : t('users.nodeusage.today', { defaultValue: '今日' }))}
            </TableRow>
          </TableHead>
          <TableBody>
            {sorted.map((r, i) => (
              <TableRow key={r.panel_id || i}>
                <TableCell>
                  <Typography noWrap sx={{ fontSize: 13, fontWeight: 500 }}>
                    {r.server_name || `#${r.panel_id}`}
                    <TrafficFactorBadge milli={r.traffic_factor_milli} sx={{ ml: 0.75 }} />
                  </Typography>
                  <Typography noWrap sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
                    {t('users.serverusage.node_count', { n: r.node_count, defaultValue: '{{n}} 节点' })}
                  </Typography>
                </TableCell>
                <TableCell align="right">{charged ? chargedCell(r.charged_lifetime_bytes, r.lifetime_total_bytes) : cell(r.lifetime_total_bytes, r.lifetime_up_bytes, r.lifetime_down_bytes)}</TableCell>
                <TableCell align="right">{charged ? chargedCell(r.charged_period_bytes, r.period_total_bytes) : cell(r.period_total_bytes, r.period_up_bytes, r.period_down_bytes)}</TableCell>
                <TableCell align="right">{cell(r.today_total_bytes, r.today_up_bytes, r.today_down_bytes)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
          <TableFooter>
            <TableRow>
              <TableCell sx={{ fontWeight: 700, color: md.onSurface, borderTop: `2px solid ${md.outlineVariant}` }}>
                {t('users.nodeusage.total', { defaultValue: '合计' })}
              </TableCell>
              <TableCell align="right" sx={{ borderTop: `2px solid ${md.outlineVariant}` }}>{charged ? chargedCell(grand.chargedLifetime, grand.lifetime) : cell(grand.lifetime, grand.lifeUp, grand.lifeDown)}</TableCell>
              <TableCell align="right" sx={{ borderTop: `2px solid ${md.outlineVariant}` }}>{charged ? chargedCell(grand.chargedPeriod, grand.period) : cell(grand.period, grand.periodUp, grand.periodDown)}</TableCell>
              <TableCell align="right" sx={{ borderTop: `2px solid ${md.outlineVariant}` }}>{cell(grand.today, grand.todayUp, grand.todayDown)}</TableCell>
            </TableRow>
          </TableFooter>
        </Table>
      </TableContainer>
    </Box>
  )
}
