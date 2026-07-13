import { useEffect, useMemo, useState } from 'react'
import {
  Box, CircularProgress, InputAdornment, Table, TableBody, TableCell,
  TableContainer, TableFooter, TableHead, TablePagination, TableRow,
  TableSortLabel, TextField, Typography, useTheme,
} from '@mui/material'
import SearchIcon from '@mui/icons-material/Search'
import { useTranslation } from 'react-i18next'

import { getUserNodeUsage, type UserNodeUsageRow } from '@/api/traffic'

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

/** UserNodeUsage shows one user's usage broken down per node (lifetime /
 *  current-period / today, each split ↑up ↓down). Lives on the Traffic page's
 *  Trend tab (below the chart) when a single user is selected. All rows are
 *  fetched once and paginated / searched / sorted CLIENT-SIDE — a user's node
 *  count is bounded by their group coverage, and the backend already batches
 *  the per-node lookup, so this never hammers the DB. The "合计" total always
 *  reflects ALL nodes (not the filtered or current page). */
export function UserNodeUsage({ userId }: { userId: number }) {
  const { t } = useTranslation('admin')
  const md = useTheme().palette.md
  const [rows, setRows] = useState<UserNodeUsageRow[] | null>(null)
  const [keyword, setKeyword] = useState('')
  const [orderBy, setOrderBy] = useState<SortKey>('period')
  const [orderDir, setOrderDir] = useState<'asc' | 'desc'>('desc')
  const [page, setPage] = useState(0)
  const [rowsPerPage, setRowsPerPage] = useState(10)

  useEffect(() => {
    let alive = true
    setRows(null)
    setPage(0)
    setKeyword('')
    getUserNodeUsage(userId)
      .then(r => { if (alive) setRows(r) })
      .catch(() => { if (alive) setRows([]) })
    return () => { alive = false }
  }, [userId])

  // Grand total = EVERY node, always — independent of search / paging so it
  // keeps matching the user-level period/lifetime figures shown elsewhere.
  const grand = useMemo(() => (rows ?? []).reduce((a, r) => ({
    lifetime: a.lifetime + r.lifetime_total_bytes, lifeUp: a.lifeUp + r.lifetime_up_bytes, lifeDown: a.lifeDown + r.lifetime_down_bytes,
    period: a.period + r.period_total_bytes, periodUp: a.periodUp + r.period_up_bytes, periodDown: a.periodDown + r.period_down_bytes,
    today: a.today + r.today_total_bytes, todayUp: a.todayUp + r.today_up_bytes, todayDown: a.todayDown + r.today_down_bytes,
  }), { lifetime: 0, lifeUp: 0, lifeDown: 0, period: 0, periodUp: 0, periodDown: 0, today: 0, todayUp: 0, todayDown: 0 }), [rows])

  const filtered = useMemo(() => {
    const all = rows ?? []
    const kw = keyword.trim().toLowerCase()
    const f = kw
      ? all.filter(r =>
          (r.display_name || '').toLowerCase().includes(kw) ||
          (r.region || '').toLowerCase().includes(kw) ||
          (r.client_email || '').toLowerCase().includes(kw))
      : all
    const metric = (r: UserNodeUsageRow) =>
      orderBy === 'lifetime' ? r.lifetime_total_bytes
        : orderBy === 'today' ? r.today_total_bytes
          : r.period_total_bytes
    return [...f].sort((a, b) => orderDir === 'desc' ? metric(b) - metric(a) : metric(a) - metric(b))
  }, [rows, keyword, orderBy, orderDir])

  const paged = filtered.slice(page * rowsPerPage, page * rowsPerPage + rowsPerPage)

  const setSort = (k: SortKey) => {
    if (orderBy === k) setOrderDir(d => d === 'asc' ? 'desc' : 'asc')
    else { setOrderBy(k); setOrderDir('desc') }
    setPage(0)
  }

  const title = (
    <Typography sx={{ fontSize: 13, fontWeight: 600, color: md.onSurfaceVariant, mb: 1 }}>
      {t('users.nodeusage.title', { defaultValue: '按节点用量' })}
    </Typography>
  )

  if (rows === null) {
    return <Box>{title}<CircularProgress size={20} /></Box>
  }
  if (rows.length === 0) {
    return (
      <Box>
        {title}
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('users.nodeusage.empty', { defaultValue: '该用户暂无节点' })}
        </Typography>
      </Box>
    )
  }

  // One metric cell: prominent total over a small ↑up ↓down subline.
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
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, mb: 1, flexWrap: 'wrap' }}>
        {title}
        <TextField
          size="small"
          value={keyword}
          onChange={e => { setKeyword(e.target.value); setPage(0) }}
          placeholder={t('users.nodeusage.search', { defaultValue: '搜索节点 / 地区' })}
          sx={{ width: 240 }}
          slotProps={{
            input: { startAdornment: <InputAdornment position="start"><SearchIcon fontSize="small" /></InputAdornment> }
          }}
        />
      </Box>
      <TableContainer>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell sx={{ fontWeight: 600 }}>{t('users.nodeusage.node', { defaultValue: '节点' })}</TableCell>
              {headCell('lifetime', t('users.nodeusage.lifetime', { defaultValue: '累计' }))}
              {headCell('period', t('users.nodeusage.period', { defaultValue: '本周期' }))}
              {headCell('today', t('users.nodeusage.today', { defaultValue: '今日' }))}
            </TableRow>
          </TableHead>
          <TableBody>
            {paged.length === 0 && (
              <TableRow>
                <TableCell colSpan={4} sx={{ color: md.onSurfaceVariant, fontSize: 12 }}>
                  {t('users.nodeusage.no_match', { defaultValue: '无匹配节点' })}
                </TableCell>
              </TableRow>
            )}
            {paged.map((r, i) => (
              <TableRow key={r.node_id || r.client_email || i}>
                <TableCell>
                  <Typography noWrap sx={{ fontSize: 13, fontWeight: 500 }}>
                    {r.display_name || r.client_email || `#${r.node_id}`}
                  </Typography>
                  {r.region && <Typography noWrap sx={{ fontSize: 11, color: md.onSurfaceVariant }}>{r.region}</Typography>}
                </TableCell>
                <TableCell align="right">{cell(r.lifetime_total_bytes, r.lifetime_up_bytes, r.lifetime_down_bytes)}</TableCell>
                <TableCell align="right">{cell(r.period_total_bytes, r.period_up_bytes, r.period_down_bytes)}</TableCell>
                <TableCell align="right">{cell(r.today_total_bytes, r.today_up_bytes, r.today_down_bytes)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
          <TableFooter>
            {/* Always the grand total of EVERY node — not affected by search/page. */}
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
      <TablePagination
        component="div"
        count={filtered.length}
        page={page}
        onPageChange={(_, p) => setPage(p)}
        rowsPerPage={rowsPerPage}
        onRowsPerPageChange={e => { setRowsPerPage(parseInt(e.target.value, 10)); setPage(0) }}
        rowsPerPageOptions={[10, 25, 50]}
      />
    </Box>
  );
}
