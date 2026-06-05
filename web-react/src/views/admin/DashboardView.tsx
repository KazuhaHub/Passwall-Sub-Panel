import { lazy, Suspense, useEffect, useState } from 'react'
import {
  Box,
  Card,
  CardContent,
  Chip,
  CircularProgress,
  Link as MuiLink,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Typography,
  useTheme,
} from '@mui/material'
import { Link as RouterLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import GroupIcon from '@mui/icons-material/Group'
import DnsIcon from '@mui/icons-material/Dns'
import WorkspacesIcon from '@mui/icons-material/Workspaces'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import ScheduleIcon from '@mui/icons-material/Schedule'
import CheckCircleIcon from '@mui/icons-material/CheckCircle'

import { dashboardSummary, type DashboardSummary } from '@/api/dashboard'
import { topTraffic, trafficHistory, type TrafficHistoryItem, type TrafficRow } from '@/api/traffic'
import PageHeader from '@/components/PageHeader'
import type { M3Tokens } from '@/theme'

const TrafficChart = lazy(() => import('@/components/TrafficChart'))

function formatBytes(n: number): string {
  if (n === 0) return '0'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let u = 0
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024
    u++
  }
  return `${v.toFixed(2)} ${units[u]}`
}

interface MetricCardProps {
  labelKey: string
  value: number | string
  subtitle?: string
  Icon: typeof DnsIcon
  tone: 'primary' | 'secondary' | 'tertiary' | 'error'
  loading: boolean
}

function toneContainer(md: M3Tokens, tone: MetricCardProps['tone']) {
  switch (tone) {
    case 'secondary': return { bg: md.secondaryContainer, fg: md.onSecondaryContainer }
    case 'tertiary': return { bg: md.tertiaryContainer, fg: md.onTertiaryContainer }
    case 'error': return { bg: md.errorContainer, fg: md.onErrorContainer }
    default: return { bg: md.primaryContainer, fg: md.onPrimaryContainer }
  }
}

function MetricCard({ labelKey, value, subtitle, Icon, tone, loading }: MetricCardProps) {
  const theme = useTheme()
  const md = theme.palette.md
  const t = toneContainer(md, tone)
  const { t: tr } = useTranslation('admin')
  return (
    <Card sx={{ bgcolor: md.surfaceContainerHighest }}>
      <CardContent sx={{ display: 'flex', alignItems: 'center', gap: 2, p: 2.5, '&:last-child': { pb: 2.5 } }}>
        <Box sx={{
          width: 56, height: 56, borderRadius: '12px',
          display: 'grid', placeItems: 'center', flexShrink: 0,
          bgcolor: t.bg, color: t.fg,
        }}>
          <Icon sx={{ fontSize: 28 }} />
        </Box>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{tr(labelKey)}</Typography>
          {loading
            ? <CircularProgress size={20} sx={{ mt: 0.5 }} />
            : <>
                <Typography sx={{ fontSize: 32, fontWeight: 500, lineHeight: 1.1, mt: 0.25 }}>{value}</Typography>
                {/* Always reserve the subtitle row's vertical space so all
                    four MetricCards in the dashboard row stay the same
                    height regardless of which ones have a breakdown line
                    — otherwise the icon on a subtitle-less card (Groups)
                    sits visibly higher than its neighbours. */}
                <Box sx={{ mt: 0.5, minHeight: 18 }}>
                  {subtitle && (
                    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{subtitle}</Typography>
                  )}
                </Box>
              </>}
        </Box>
      </CardContent>
    </Card>
  )
}

export default function DashboardView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('admin')

  const [loading, setLoading] = useState(true)
  const [summary, setSummary] = useState<DashboardSummary | null>(null)
  const [topUsers, setTopUsers] = useState<TrafficRow[]>([])
  const [trend, setTrend] = useState<TrafficHistoryItem[]>([])
  const [trendLoading, setTrendLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        // The dashboard summary endpoint returns pre-aggregated counts
        // + the small "expiring" and "node alerts" lists the page
        // renders. Pre-fix this hook fetched listUsers({page_size:500})
        // + listNodes({page_size:500}) + listGroups() purely to compute
        // four counters and two five-row lists — the entire user and
        // node lists were downloaded on every dashboard open.
        const [s, top] = await Promise.all([
          dashboardSummary(),
          // silent: best-effort enrichment, axios global toast off so
          // a transient failure here doesn't surface "Network error"
          // on a dashboard whose primary data loaded fine.
          topTraffic(5, { silent: true }).catch(err => {
            // eslint-disable-next-line no-console
            console.warn('DashboardView: topTraffic(5) failed', err)
            return []
          }),
        ])
        if (cancelled) return
        setSummary(s)
        setTopUsers(top)
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    async function loadTrend() {
      try {
        const since = new Date()
        since.setHours(0, 0, 0, 0)
        since.setDate(since.getDate() - 6)
        const sStr = `${since.getFullYear()}-${String(since.getMonth() + 1).padStart(2, '0')}-${String(since.getDate()).padStart(2, '0')}`
        const today = new Date()
        const tStr = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`
        const res = await trafficHistory({ period: 'day', since: sStr, until: tStr })
        if (!cancelled) setTrend(res.items)
      } catch { /* ignore */ }
      finally { if (!cancelled) setTrendLoading(false) }
    }
    void load()
    void loadTrend()
    return () => { cancelled = true }
  }, [])

  // Aggregates now come pre-computed from /admin/dashboard/summary,
  // so the page no longer downloads + walks the full user / node lists
  // to render four counters.
  const stats = {
    enabled: summary?.user_enabled ?? 0,
    disabled: summary?.user_disabled ?? 0,
    emergency: summary?.user_emergency ?? 0,
    expiring: summary?.expiring_users ?? [],
  }
  const nodeAlerts = summary?.node_alerts ?? []
  const healthyCount = summary?.node_healthy ?? 0
  const enabledNodeCount = summary?.node_enabled ?? 0
  const userTotal = summary?.user_total ?? 0
  const groupCount = summary?.group_count ?? 0

  return (
    <Box sx={{ p: { xs: 2, sm: 3 } }}>
      <PageHeader title={t('dashboard.title')} />

      {/* Stat tiles — bigger numbers now carry a subtitle (e.g., 健康/总数) so
          a glance tells the admin "everything fine" vs "8/12 healthy". */}
      <Box sx={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))',
        gap: 2,
        mb: 3,
      }}>
        <MetricCard
          labelKey="dashboard.metric.users"
          value={userTotal}
          subtitle={loading ? undefined : t('dashboard.metric.users_breakdown', {
            enabled: stats.enabled,
            disabled: stats.disabled,
            defaultValue: `${stats.enabled} 启用 · ${stats.disabled} 停用`,
          })}
          Icon={GroupIcon} tone="primary" loading={loading}
        />
        <MetricCard
          labelKey="dashboard.metric.nodes"
          value={enabledNodeCount}
          // Suppress the breakdown subtitle when there are zero enabled
          // nodes — otherwise a fresh panel reads "0 / 0 健康" which is
          // technically true but reads as a broken or buggy state.
          subtitle={loading || enabledNodeCount === 0 ? undefined : t('dashboard.metric.nodes_breakdown', {
            healthy: healthyCount,
            total: enabledNodeCount,
            defaultValue: `${healthyCount} / ${enabledNodeCount} 健康`,
          })}
          Icon={DnsIcon}
          tone={nodeAlerts.length > 0 ? 'error' : 'tertiary'}
          loading={loading}
        />
        <MetricCard
          labelKey="dashboard.metric.groups"
          value={groupCount}
          Icon={WorkspacesIcon} tone="secondary" loading={loading}
        />
        <MetricCard
          labelKey="dashboard.metric.emergency_active"
          value={stats.emergency}
          subtitle={loading ? undefined : t('dashboard.metric.emergency_active_hint', { defaultValue: '正在使用紧急访问窗口' })}
          Icon={ScheduleIcon}
          tone={stats.emergency > 0 ? 'tertiary' : 'secondary'}
          loading={loading}
        />
      </Box>

      {/* Alerts row — only renders cards that have actual content, so a
          fully-healthy panel doesn't carry dead UI weight. */}
      <Box sx={{
        display: 'grid',
        gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' },
        gap: 2,
        mb: 3,
      }}>
        <AlertCard
          title={t('dashboard.alerts.node_health', { defaultValue: '节点健康警报' })}
          icon={<WarningAmberIcon />}
          emptyLabel={t('dashboard.alerts.node_health_empty', { defaultValue: '所有节点工作正常' })}
          empty={nodeAlerts.length === 0 && !loading}
          loading={loading}
          to="/admin/nodes"
          md={md}
        >
          {nodeAlerts.map(n => (
            <Box key={n.id} sx={{
              display: 'flex', alignItems: 'center', gap: 1.5,
              py: 1, borderBottom: `1px solid ${md.outlineVariant}`,
              '&:last-child': { borderBottom: 0 },
            }}>
              <Box sx={{ width: 8, height: 8, borderRadius: '50%', bgcolor: healthColor(md, n.health_state || '') }} />
              <Box sx={{ flex: 1, minWidth: 0 }}>
                <Typography sx={{ fontSize: 14, fontWeight: 500 }}>{n.display_name}</Typography>
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                  {n.panel_name} · {t(`admin:nodes.health.${n.health_state}`, { defaultValue: n.health_state })}
                </Typography>
              </Box>
            </Box>
          ))}
        </AlertCard>

        <AlertCard
          title={t('dashboard.alerts.expiring_soon', { defaultValue: '即将到期（7 天内）' })}
          icon={<ScheduleIcon />}
          emptyLabel={t('dashboard.alerts.expiring_empty', { defaultValue: '7 天内无到期用户' })}
          empty={stats.expiring.length === 0 && !loading}
          loading={loading}
          to="/admin/users"
          md={md}
        >
          {stats.expiring.map(u => {
            const diffDays = Math.ceil((new Date(u.expire_at).getTime() - Date.now()) / 86400000)
            const chipColor = diffDays <= 1 ? md.error : diffDays <= 3 ? md.tertiary : md.onSurfaceVariant
            return (
              <Box key={u.id} sx={{
                display: 'flex', alignItems: 'center', gap: 1.5,
                py: 1, borderBottom: `1px solid ${md.outlineVariant}`,
                '&:last-child': { borderBottom: 0 },
              }}>
                <Box sx={{ flex: 1, minWidth: 0 }}>
                  <Typography sx={{ fontSize: 14, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {u.display_name || u.upn}
                  </Typography>
                  <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                    {new Date(u.expire_at).toLocaleDateString()}
                  </Typography>
                </Box>
                <Chip
                  size="small"
                  label={diffDays <= 0
                    ? t('dashboard.alerts.expiring_today', { defaultValue: '今日' })
                    : t('dashboard.alerts.expiring_in_days', { days: diffDays, defaultValue: `${diffDays} 天后` })}
                  sx={{ bgcolor: 'transparent', color: chipColor, border: `1px solid ${chipColor}`, fontWeight: 500 }}
                />
              </Box>
            )
          })}
        </AlertCard>
      </Box>

      {/* 7-day traffic trend */}
      <Card sx={{ mb: 3, p: 2.5, bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1.5 }}>
          <Typography variant="h6">{t('dashboard.trend_title', { defaultValue: '近 7 天流量趋势' })}</Typography>
          <MuiLink component={RouterLink} to="/admin/traffic" underline="hover" sx={{ fontSize: 13, color: md.primary }}>
            {t('dashboard.traffic.view_all')} →
          </MuiLink>
        </Box>
        <Suspense fallback={<Box sx={{ height: 280, display: 'grid', placeItems: 'center' }}><CircularProgress size={24} /></Box>}>
          {trendLoading
            ? <Box sx={{ height: 280, display: 'grid', placeItems: 'center' }}><CircularProgress size={24} /></Box>
            : <TrafficChart items={trend} height={280} />}
        </Suspense>
      </Card>

      <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', px: 2.5, pt: 2.5, pb: 1.5 }}>
          <Typography variant="h6">{t('dashboard.traffic.title')}</Typography>
          <MuiLink component={RouterLink} to="/admin/traffic" underline="hover" sx={{ fontSize: 13, color: md.primary }}>
            {t('dashboard.traffic.view_all')} →
          </MuiLink>
        </Box>

        {loading ? (
          <Box sx={{ display: 'grid', placeItems: 'center', py: 6 }}>
            <CircularProgress size={24} />
          </Box>
        ) : topUsers.length === 0 ? (
          <Box sx={{ py: 6, textAlign: 'center', color: md.onSurfaceVariant, fontSize: 14 }}>
            {t('dashboard.traffic.empty')}
          </Box>
        ) : (
          <TableContainer>
            <Table>
              <TableHead>
                <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}` } }}>
                  <TableCell sx={{ width: 60 }}>{t('dashboard.traffic.table.rank')}</TableCell>
                  <TableCell>{t('dashboard.traffic.table.upn')}</TableCell>
                  <TableCell align="right">{t('dashboard.traffic.table.period_used')}</TableCell>
                  <TableCell align="right">{t('dashboard.traffic.table.today_used')}</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {topUsers.map((r, i) => (
                  <TableRow key={r.user_id} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}>
                    <TableCell sx={{ color: md.onSurfaceVariant, fontVariantNumeric: 'tabular-nums' }}>{i + 1}</TableCell>
                    <TableCell sx={{ fontWeight: 500 }}>{r.upn}</TableCell>
                    <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums', fontSize: 13 }}>
                      {formatBytes(r.period_used_bytes)}
                    </TableCell>
                    <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums', fontSize: 13 }}>
                      {formatBytes(r.today_used_bytes)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableContainer>
        )}
      </Card>
    </Box>
  )
}

interface AlertCardProps {
  title: string
  icon: React.ReactNode
  emptyLabel: string
  empty: boolean
  loading: boolean
  to: string
  md: M3Tokens
  children: React.ReactNode
}

function AlertCard({ title, icon, emptyLabel, empty, loading, to, md, children }: AlertCardProps) {
  const { t } = useTranslation('admin')
  return (
    <Card sx={{ p: 2.5, bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, color: md.onSurface }}>
          {icon}
          <Typography variant="h6">{title}</Typography>
        </Box>
        <MuiLink component={RouterLink} to={to} underline="hover" sx={{ fontSize: 13, color: md.primary }}>
          {t('dashboard.traffic.view_all')} →
        </MuiLink>
      </Box>
      {loading ? (
        <Box sx={{ display: 'grid', placeItems: 'center', py: 3 }}><CircularProgress size={20} /></Box>
      ) : empty ? (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, py: 1.5, color: md.onSurfaceVariant, fontSize: 14 }}>
          <CheckCircleIcon sx={{ fontSize: 18, color: '#22c55e' }} />
          {emptyLabel}
        </Box>
      ) : (
        <Box>{children}</Box>
      )}
    </Card>
  )
}

function healthColor(md: M3Tokens, state: string): string {
  switch (state) {
    case 'panel_unreachable': return md.error
    case 'inbound_missing': return '#f97316'
    case 'inbound_disabled': return '#9ca3af'
    default: return md.outlineVariant
  }
}
