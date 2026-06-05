import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import {
  Autocomplete,
  Box,
  Button,
  Card,
  CircularProgress,
  MenuItem,
  Select,
  Tab,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tabs,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  Typography,
  useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import { listUsers } from '@/api/users'
import { listNodes } from '@/api/nodes'
import {
  nodeTrafficHistory,
  pollTrafficNow,
  topNodes,
  topTraffic,
  trafficHistory,
  userTrafficHistory,
  type NodeTrafficRow,
  type TrafficHistoryPeriod,
  type TrafficHistoryResponse,
  type TrafficRow,
} from '@/api/traffic'
import type { Node, User } from '@/api/types'
import { UserNodeUsage } from './UserNodeUsage'
import { getUISettings } from '@/api/settings'
import PageHeader from '@/components/PageHeader'
import { pushSnack } from '@/components/SnackbarHost'
import { useTabParam } from '@/hooks/useTabParam'
import { useSiteStore } from '@/stores/site'

// All tz options on the chart toolbar. Pulls from the browser's IANA
// database (Intl.supportedValuesOf) so the list covers everything go's
// time.LoadLocation can resolve — ~400 entries on modern engines, no
// manual upkeep. Browser tz + panel tz are pinned to the top of the
// dropdown by buildTzOptions below. Old browsers (no
// supportedValuesOf) fall back to a short hand-rolled list.
const COMMON_CHART_TIMEZONES: string[] = (() => {
  try {
    const fn = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf
    if (typeof fn === 'function') return fn('timeZone')
  } catch { /* fall through */ }
  return [
    'UTC', 'America/Los_Angeles', 'America/Denver', 'America/Chicago',
    'America/New_York', 'Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Taipei',
    'Asia/Tokyo', 'Asia/Seoul', 'Asia/Singapore', 'Europe/London',
    'Europe/Paris', 'Europe/Berlin', 'Europe/Moscow', 'Australia/Sydney',
  ]
})()

function browserTz(): string {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone } catch { return 'UTC' }
}

function buildTzOptions(panelTz: string): string[] {
  const bz = browserTz()
  // Browser first, panel tz second when distinct and non-empty, then
  // common IANA names with both already-pinned entries removed so the
  // list isn't visually duplicated.
  const head = [bz]
  if (panelTz && panelTz !== bz) head.push(panelTz)
  const tail = COMMON_CHART_TIMEZONES.filter(t => !head.includes(t))
  return [...head, ...tail]
}

const TrafficChart = lazy(() => import('@/components/TrafficChart'))

function bytesToHuman(n: number) {
  if (n === 0) return '0'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, u = 0
  while (v >= 1024 && u < units.length - 1) { v /= 1024; u++ }
  return `${v.toFixed(2)} ${units[u]}`
}

function dateString(d: Date) {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function daysAgo(n: number) {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  d.setDate(d.getDate() - n)
  return d
}

export default function TrafficView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('admin')

  const [tab, setTab] = useTabParam<'trend' | 'rank'>('tab', 'trend', ['trend', 'rank'])
  const [scope, setScope] = useTabParam<'user' | 'node'>('scope', 'user', ['user', 'node'])
  const [items, setItems] = useState<TrafficRow[]>([])
  const [nodeItems, setNodeItems] = useState<NodeTrafficRow[]>([])
  const [users, setUsers] = useState<User[]>([])
  const [nodes, setNodes] = useState<Node[]>([])
  const [history, setHistory] = useState<TrafficHistoryResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [chartLoading, setChartLoading] = useState(false)
  const [pollLoading, setPollLoading] = useState(false)

  const [limit, setLimit] = useState(20)
  // Pre-select a user from the deep-link (?user=N) the user-edit dialog opens —
  // "View usage" jumps here with that user already chosen. One-shot read on
  // mount; 0 = all users.
  const [selectedUserId, setSelectedUserId] = useState<number>(() => {
    const u = Number(new URLSearchParams(window.location.search).get('user'))
    return Number.isFinite(u) && u > 0 ? u : 0
  })
  const [selectedNodeId, setSelectedNodeId] = useState<number>(0)
  const [period, setPeriod] = useState<TrafficHistoryPeriod>('day')
  const [rangeDays, setRangeDays] = useState(30)
  // Server-side history retention (TrafficHistoryDays admin setting). Drives
  // which range options the dropdown exposes: a 30-day retention hides the
  // 90-day option, etc. 0 in the setting means "keep everything" — we treat
  // that as effectively unlimited (Infinity below). Loaded once on mount.
  const [historyDays, setHistoryDays] = useState<number>(365)
  useEffect(() => {
    void getUISettings().then(s => {
      const d = Number(s.traffic_history_days) || 0
      setHistoryDays(d > 0 ? d : Number.POSITIVE_INFINITY)
    }).catch(() => { /* leave default */ })
  }, [])

  // Range options filtered by retention. Day/Week/Month read the hourly
  // rollup (HistoryFor), so those ranges work out to the full
  // traffic_history_days (historyDays). Hour granularity is additionally
  // capped to a short recent window: thousands of hourly points (e.g. 30d×24)
  // are unreadable, and hour-level detail is only useful for recent inspection.
  //
  // "Today" (1d) is always available regardless of retention and only renders
  // Hour granularity.
  const hourGranularityMaxDays = 7
  const rangeOptions = useMemo(() => {
    const all = [1, 7, 30, 90, 180, 365]
    const cap = period === 'hour' ? Math.min(historyDays, hourGranularityMaxDays) : historyDays
    return all.filter(d => d <= cap || d === 1)
  }, [period, historyDays])
  // Clamp rangeDays whenever the option set changes: pick the largest
  // available option that isn't bigger than the current selection.
  useEffect(() => {
    if (rangeOptions.length === 0) return
    if (!rangeOptions.includes(rangeDays)) {
      const fallback = rangeOptions[rangeOptions.length - 1]
      setRangeDays(fallback)
    }
  }, [rangeOptions, rangeDays])
  // Period ↔ Range coupling: range=1d forces Hour; range≥30d forbids Hour.
  // Snap to the closest valid Period when the user picks a Range that the
  // current Period doesn't support, so the chart never queries an invalid
  // combo (which would otherwise show empty or duplicate data).
  useEffect(() => {
    if (rangeDays === 1 && period !== 'hour') setPeriod('hour')
    else if (rangeDays >= 30 && period === 'hour') setPeriod('day')
  }, [rangeDays, period])
  // Admin's effective chart timezone. Defaults to the panel-configured tz
  // (so the chart aligns with the rest of the panel's calendar math by
  // default) and falls back to the browser tz when panel tz is unset.
  // Not persisted across reloads — the admin reselects per session if they
  // want a non-default view, matching the spec's "don't remember" rule.
  const panelTz = useSiteStore(s => s.timezone)
  // selectedTz is the COMMITTED timezone the chart fetch depends on; tzInput
  // is the live text in the Autocomplete. Keeping them separate stops a
  // fetch firing on every keystroke — the committed value only changes on
  // select / Enter / blur.
  const [selectedTz, setSelectedTz] = useState<string>(() => panelTz || browserTz())
  const [tzInput, setTzInput] = useState<string>(() => panelTz || browserTz())
  // When panel tz finishes loading (site store is async), realign the
  // default once. Skip when admin has already typed something distinct.
  useEffect(() => {
    if (!panelTz) return
    const realign = (prev: string) => (prev === '' || prev === browserTz()) ? panelTz : prev
    setSelectedTz(realign)
    setTzInput(realign)
  }, [panelTz])

  const historyItems = history?.items ?? []
  const summary = useMemo(() => {
    let total = 0, up = 0, down = 0
    for (const it of historyItems) { total += it.total_bytes; up += it.up_bytes; down += it.down_bytes }
    return { total, up, down }
  }, [historyItems])

  useEffect(() => {
    void Promise.all([loadRank(), loadUsers(), loadNodes(), loadHistory()])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => { void loadRank()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [limit, scope])

  useEffect(() => { void loadHistory()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope, selectedUserId, selectedNodeId, period, rangeDays, selectedTz])

  // Last-wins guards: rapid scope/filter/range changes fire overlapping
  // requests; without a sequence check a slow earlier response can land after
  // a newer one and paint stale data under the current selection.
  const rankSeq = useRef(0)
  const historySeq = useRef(0)

  async function loadRank() {
    const seq = ++rankSeq.current
    setLoading(true)
    try {
      if (scope === 'node') {
        const res = await topNodes(limit)
        if (seq === rankSeq.current) setNodeItems(res)
      } else {
        const res = await topTraffic(limit)
        if (seq === rankSeq.current) setItems(res)
      }
    } finally { if (seq === rankSeq.current) setLoading(false) }
  }

  async function loadUsers() {
    const res = await listUsers({ page: 1, page_size: 200 })
    setUsers(res.items)
  }

  async function loadNodes() {
    try {
      const res = await listNodes({ page: 1, page_size: 500 })
      setNodes(res.items)
    } catch { /* toasted by client */ }
  }

  async function loadHistory() {
    const seq = ++historySeq.current
    setChartLoading(true)
    try {
      const params = {
        period, since: dateString(daysAgo(rangeDays - 1)), until: dateString(new Date()),
        tz: selectedTz || undefined,
      }
      let res: TrafficHistoryResponse
      if (scope === 'node') {
        res = await nodeTrafficHistory(selectedNodeId > 0 ? { ...params, node_id: selectedNodeId } : params)
      } else {
        res = selectedUserId > 0
          ? await userTrafficHistory(selectedUserId, params)
          : await trafficHistory(params)
      }
      if (seq === historySeq.current) setHistory(res)
    } finally { if (seq === historySeq.current) setChartLoading(false) }
  }

  async function pollNow() {
    setPollLoading(true)
    try {
      await pollTrafficNow()
      pushSnack(t('traffic.poll_done'), 'success')
      await Promise.all([loadRank(), loadHistory()])
    } finally { setPollLoading(false) }
  }

  function userLabel(u: User) {
    return u.display_name ? `${u.display_name} (${u.upn})` : u.upn
  }

  function nodeLabel(n: Node) {
    return `${n.display_name} · ${n.region}`
  }

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader
        title={t('traffic.title')}
        actions={
          <Button variant="contained" disabled={pollLoading} onClick={pollNow}
            startIcon={pollLoading ? <CircularProgress size={14} color="inherit" /> : null}>
            {t('traffic.poll_now')}
          </Button>
        }
      />

      <Box sx={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', flexWrap: 'wrap', gap: 1, mt: 2, mb: 2, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Tabs value={tab} onChange={(_, v) => setTab(v)}>
          <Tab value="trend" label={t('traffic.tab_trend')} />
          <Tab value="rank" label={t('traffic.tab_rank')} />
        </Tabs>
        <ToggleButtonGroup value={scope} exclusive size="small"
          onChange={(_, v) => v && setScope(v as 'user' | 'node')}
          sx={{ mb: 1, '& .MuiToggleButton-root': { px: 2, height: 36 } }}>
          <ToggleButton value="user">{t('traffic.scope_user')}</ToggleButton>
          <ToggleButton value="node">{t('traffic.scope_node')}</ToggleButton>
        </ToggleButtonGroup>
      </Box>

      {tab === 'rank' && (
        <>
          <Box sx={{ display: 'flex', gap: 1.5, mb: 2, alignItems: 'center', flexWrap: 'wrap' }}>
            <Select size="small" value={limit} onChange={e => setLimit(Number(e.target.value))} sx={{ width: 140, height: 40 }}>
              <MenuItem value={10}>{t('traffic.limit.10')}</MenuItem>
              <MenuItem value={20}>{t('traffic.limit.20')}</MenuItem>
              <MenuItem value={50}>{t('traffic.limit.50')}</MenuItem>
            </Select>
            <Button variant="outlined" onClick={loadRank} disabled={loading}>{t('traffic.refresh')}</Button>
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, ml: 1 }}>
              {t('traffic.rank_note')}
            </Typography>
          </Box>
          <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
            <TableContainer>
              <Table>
                {scope === 'user' ? (
                  <>
                    <TableHead>
                      <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                        <TableCell sx={{ width: 60 }}>{t('traffic.rank_table.rank')}</TableCell>
                        <TableCell>{t('traffic.rank_table.upn')}</TableCell>
                        <TableCell align="right">{t('traffic.rank_table.period_used')}</TableCell>
                        <TableCell align="right">{t('traffic.rank_table.today_used')}</TableCell>
                        <TableCell align="right">{t('traffic.rank_table.permanent')}</TableCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {loading && items.length === 0 && (
                        <TableRow><TableCell colSpan={5} sx={{ textAlign: 'center', py: 6 }}><CircularProgress size={24} /></TableCell></TableRow>
                      )}
                      {!loading && items.length === 0 && (
                        <TableRow><TableCell colSpan={5} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
                      )}
                      {items.map((r, i) => (
                        <TableRow key={r.user_id} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}>
                          <TableCell sx={{ color: md.onSurfaceVariant, fontVariantNumeric: 'tabular-nums' }}>{i + 1}</TableCell>
                          <TableCell sx={{ fontWeight: 500 }}>{r.upn}</TableCell>
                          <TableCell align="right" sx={{ fontSize: 13 }}>{bytesToHuman(r.period_used_bytes)}</TableCell>
                          <TableCell align="right" sx={{ fontSize: 13 }}>{bytesToHuman(r.today_used_bytes)}</TableCell>
                          <TableCell align="right" sx={{ fontSize: 13 }}>{bytesToHuman(r.permanent_total_bytes)}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </>
                ) : (
                  <>
                    <TableHead>
                      <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                        <TableCell sx={{ width: 60 }}>{t('traffic.rank_table.rank')}</TableCell>
                        <TableCell>{t('traffic.node_table.name')}</TableCell>
                        <TableCell>{t('traffic.node_table.panel')}</TableCell>
                        <TableCell>{t('traffic.node_table.region')}</TableCell>
                        <TableCell align="right">{t('traffic.node_table.month_used')}</TableCell>
                        <TableCell align="right">{t('traffic.rank_table.today_used')}</TableCell>
                        <TableCell align="right">{t('traffic.rank_table.permanent')}</TableCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {loading && nodeItems.length === 0 && (
                        <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6 }}><CircularProgress size={24} /></TableCell></TableRow>
                      )}
                      {!loading && nodeItems.length === 0 && (
                        <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
                      )}
                      {nodeItems.map((r, i) => (
                        <TableRow key={r.node_id} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}>
                          <TableCell sx={{ color: md.onSurfaceVariant, fontVariantNumeric: 'tabular-nums' }}>{i + 1}</TableCell>
                          <TableCell sx={{ fontWeight: 500 }}>{r.display_name}</TableCell>
                          <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{r.panel_name}</TableCell>
                          <TableCell sx={{ fontSize: 13 }}>{r.region}</TableCell>
                          <TableCell align="right" sx={{ fontSize: 13 }}>{bytesToHuman(r.period_used_bytes)}</TableCell>
                          <TableCell align="right" sx={{ fontSize: 13 }}>{bytesToHuman(r.today_used_bytes)}</TableCell>
                          <TableCell align="right" sx={{ fontSize: 13 }}>{bytesToHuman(r.permanent_total_bytes)}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </>
                )}
              </Table>
            </TableContainer>
          </Card>
        </>
      )}

      {tab === 'trend' && (
        <>
          <Box sx={{ display: 'flex', gap: 1.5, mb: 2, alignItems: 'center', flexWrap: 'wrap' }}>
            {scope === 'user' ? (
              (() => {
                // Build a sentinel "all users" option so Autocomplete can model
                // it the same way as a real user pick. id=0 → server returns
                // the all-users history.
                const allOpt = { id: 0, label: t('traffic.trend.all_users') }
                const opts = [allOpt, ...users.map(u => ({ id: u.id, label: userLabel(u) }))]
                const selected = opts.find(o => o.id === selectedUserId) ?? allOpt
                return (
                  <Autocomplete
                    size="small"
                    options={opts}
                    value={selected}
                    onChange={(_, v) => setSelectedUserId(v?.id ?? 0)}
                    isOptionEqualToValue={(a, b) => a.id === b.id}
                    disableClearable
                    sx={{ width: 280 }}
                    renderInput={(params) => <TextField {...params} placeholder={t('traffic.trend.search_user')} />}
                  />
                )
              })()
            ) : (
              (() => {
                const allOpt = { id: 0, label: t('traffic.trend.all_nodes') }
                const opts = [allOpt, ...nodes.map(n => ({ id: n.id, label: nodeLabel(n) }))]
                const selected = opts.find(o => o.id === selectedNodeId) ?? allOpt
                return (
                  <Autocomplete
                    size="small"
                    options={opts}
                    value={selected}
                    onChange={(_, v) => setSelectedNodeId(v?.id ?? 0)}
                    isOptionEqualToValue={(a, b) => a.id === b.id}
                    disableClearable
                    sx={{ width: 280 }}
                    renderInput={(params) => <TextField {...params} placeholder={t('traffic.trend.search_node')} />}
                  />
                )
              })()
            )}
            <Select size="small" value={rangeDays} onChange={e => setRangeDays(Number(e.target.value))}
              sx={{ width: 140, height: 40 }}>
              {rangeOptions.includes(1) && <MenuItem value={1}>{t('traffic.trend.range_1')}</MenuItem>}
              {rangeOptions.includes(7) && <MenuItem value={7}>{t('traffic.trend.range_7')}</MenuItem>}
              {rangeOptions.includes(30) && <MenuItem value={30}>{t('traffic.trend.range_30')}</MenuItem>}
              {rangeOptions.includes(90) && <MenuItem value={90}>{t('traffic.trend.range_90')}</MenuItem>}
              {rangeOptions.includes(180) && <MenuItem value={180}>{t('traffic.trend.range_180')}</MenuItem>}
              {rangeOptions.includes(365) && <MenuItem value={365}>{t('traffic.trend.range_365')}</MenuItem>}
            </Select>
            <ToggleButtonGroup value={period} exclusive size="small"
              onChange={(_, v) => v && setPeriod(v as TrafficHistoryPeriod)}
              sx={{ '& .MuiToggleButton-root': { px: 2, height: 40 } }}>
              {/* Hour only meaningful within the raw retention window (≤7d).
                  For Today (1d) it's the *only* sensible granularity, and
                  for ≥30d ranges the raw table doesn't hold enough buckets,
                  so we hide the button entirely instead of disabling it —
                  fewer dead UI affordances. */}
              {rangeDays <= 7 && (
                <ToggleButton value="hour">{t('traffic.trend.period_hour')}</ToggleButton>
              )}
              <ToggleButton value="day" disabled={rangeDays === 1}>{t('traffic.trend.period_day')}</ToggleButton>
              <ToggleButton value="week" disabled={rangeDays === 1}>{t('traffic.trend.period_week')}</ToggleButton>
              <ToggleButton value="month" disabled={rangeDays === 1}>{t('traffic.trend.period_month')}</ToggleButton>
            </ToggleButtonGroup>
            <Autocomplete freeSolo size="small"
              options={buildTzOptions(panelTz)}
              value={selectedTz}
              inputValue={tzInput}
              onInputChange={(_, v) => setTzInput(v)}
              onChange={(_, v) => { const tz = (v as string) ?? ''; setTzInput(tz); setSelectedTz(tz) }}
              sx={{ width: 220 }}
              renderInput={(params) => (
                <TextField {...params} label={t('traffic.trend.timezone')}
                  onBlur={() => setSelectedTz(tzInput)} />
              )} />
            <Button variant="outlined" onClick={loadHistory} disabled={chartLoading}>{t('traffic.refresh')}</Button>
          </Box>

          <Box sx={{
            display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))',
            gap: 1.5, mb: 2,
          }}>
            <SummaryTile label={t('traffic.trend.summary_total')} value={bytesToHuman(summary.total)} md={md} />
            <SummaryTile label={t('traffic.trend.summary_up')} value={bytesToHuman(summary.up)} md={md} />
            <SummaryTile label={t('traffic.trend.summary_down')} value={bytesToHuman(summary.down)} md={md} />
            <SummaryTile label={t('traffic.trend.summary_range')} value={`${history?.since || '-'} → ${history?.until || '-'}`} md={md} small />
          </Box>

          <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', p: 2 }}>
            <Suspense fallback={<Box sx={{ height: 360, display: 'grid', placeItems: 'center' }}><CircularProgress size={24} /></Box>}>
              {chartLoading
                ? <Box sx={{ height: 360, display: 'grid', placeItems: 'center' }}><CircularProgress size={24} /></Box>
                : <TrafficChart items={historyItems} height={360} />}
            </Suspense>
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: 1.5 }}>
              {t('traffic.trend.chart_note')}
            </Typography>
          </Card>

          {/* Per-node usage breakdown — only when ONE specific user is
              selected (not "all users", and not the by-node scope). Moved here
              from the user-edit dialog so a growing node list can't bloat the
              modal. */}
          {scope === 'user' && selectedUserId !== 0 && (
            <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', p: 2, mt: 2 }}>
              <UserNodeUsage userId={selectedUserId} />
            </Card>
          )}
        </>
      )}
    </Box>
  )
}

interface TileProps { label: string; value: string; md: { onSurfaceVariant: string; outlineVariant: string; surface: string }; small?: boolean }
function SummaryTile({ label, value, md, small }: TileProps) {
  return (
    <Box sx={{ p: 2, borderRadius: 2, border: `1px solid ${md.outlineVariant}`, bgcolor: md.surface }}>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>{label}</Typography>
      <Typography sx={{ fontSize: small ? 13 : 18, fontWeight: 500, fontVariantNumeric: 'tabular-nums', wordBreak: 'break-all' }}>
        {value}
      </Typography>
    </Box>
  )
}
