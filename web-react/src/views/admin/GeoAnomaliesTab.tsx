import { useMemo } from 'react'
import { Alert, Box, Chip, CircularProgress, Table, TableBody, TableCell,
  TableContainer, TableHead, TableRow, Tooltip, Typography, useTheme,
} from '@mui/material'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import type { GeoAnomaly } from '@/api/geoAnomalies'
import { useGeoAnomalies } from '@/query/geoAnomalies'
import { useGeoIPStatus } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { countryFlag } from '@/utils/geo'
import { activeDbIsCountryOnly, groupSpots, sortBySeverity, tierLabelKey, type SpotTree } from '@/utils/geoAnomaly'

/**
 * Colour carries meaning here, so it is assigned by what the operator should
 * DO rather than by how alarming the word sounds.
 *
 * Only `flagged` is actionable. `suspect` is the visible ramp and must look
 * different from both a flag and a clean row — hiding it would make the
 * eventual flag appear out of nowhere. Everything else means the detector is
 * not in a position to judge, and those states are deliberately NOT green:
 * `unknown` on a fleet whose geo database has stopped working looks exactly
 * like a clean fleet if it is coloured like one.
 */
export function stateColor(state: GeoAnomaly['state']): 'error' | 'warning' | 'success' | 'info' | 'default' {
  switch (state) {
    case 'flagged': return 'error'
    case 'suspect': return 'warning'
    case 'clean': return 'success'
    case 'unknown': return 'info'
    default: return 'default'   // exempt / disabled / idle
  }
}

const TIER_DEFAULT: Record<string, string> = { country: '跨国', region: '跨省', city: '跨城' }

/**
 * One line per country: "🇨🇳 CN 3: Guangdong 2 (Shenzhen 2) · Hunan 1".
 * Counts are judged sources, so the line reads as "how many were where". A
 * name the database did not resolve prints as "?" — it is a real source that
 * could only be placed as far as its parent, and dropping it would make the
 * counts stop adding up. Country-only evidence prints as the country alone.
 */
function spotLine(c: SpotTree): string {
  const name = (s: string) => s || '?'
  const named = c.regions.some(r => r.region !== '')
  const regions = c.regions.map(r => {
    const cities = r.cities.some(ci => ci.city !== '')
      ? ` (${r.cities.map(ci => `${name(ci.city)} ${ci.n}`).join(', ')})`
      : ''
    return `${name(r.region)} ${r.n}${cities}`
  })
  const head = [countryFlag(c.cc), c.cc, String(c.n)].filter(Boolean).join(' ')
  return named ? `${head}: ${regions.join(' · ')}` : head
}

export default function GeoAnomaliesTab() {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const scope = useQueryScope()
  const { data, isPending, error } = useGeoAnomalies(scope)
  // Advisory only. A failed read yields no banner rather than an error: no
  // evidence of a coarse database is not evidence of one.
  const { data: dbStatus } = useGeoIPStatus(scope)
  // The server answers newest first; the admin needs what to act on first. A
  // sorted copy, so the cached array every other reader shares stays intact.
  const rows = useMemo(() => sortBySeverity(data ?? []), [data])

  if (isPending) return <Box sx={{ p: 3 }}><CircularProgress size={24} /></Box>

  // 503 means the detector is not wired in this build. Reporting that as "no
  // anomalies" would be the worst possible answer, so it surfaces as its own
  // message rather than as an empty table.
  const err = (() => {
    if (!error) return ''
    if (isAxiosError(error) && error.response?.status === 503) {
      return t('admin:geo_anomalies.unwired', { defaultValue: '本部署未启用异地并发检测。' })
    }
    return isAxiosError(error)
      ? String(error.response?.data?.error ?? error)
      : String(error)
  })()

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
        {t('admin:geo_anomalies.intro', {
          defaultValue: '按用户合并全部面板上「此刻仍在连接」的源地址后的分级判定（跨国 / 跨省 / 跨城）。只有「已标记」代表持续超限；「疑似」是尚未达到次数的爬升。共享出口、忽略名单、本机节点与中转、内网地址不计入。这是嫌疑不是证据——分割隧道、公司 VPN、境外家人都会诚实产生这个信号。默认只观测并在通知铃提醒；开启自动临时暂停后，被暂停的账号会在此标出。',
        })}
      </Typography>
      {/* A country-only database leaves two of the three tiers unable to
          fire. Without saying so, a quiet region/city column reads as "nobody
          is spread across provinces" when the panel simply cannot tell. */}
      {activeDbIsCountryOnly(dbStatus) && (
        <Alert severity="info" sx={{ fontSize: 13 }}>
          {t('admin:geo_anomalies.coarse_db', { defaultValue: '当前地区库只能定位到国家，跨省、跨城两级不会触发。' })}
        </Alert>
      )}
      {err && <Typography color="error" sx={{ fontSize: 13 }}>{err}</Typography>}

      <TableContainer>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>{t('admin:geo_anomalies.col_user', { defaultValue: '用户' })}</TableCell>
              <TableCell>{t('admin:geo_anomalies.col_state', { defaultValue: '判定' })}</TableCell>
              <TableCell>{t('admin:geo_anomalies.col_places', { defaultValue: '同时所在' })}</TableCell>
              <TableCell align="right">{t('admin:geo_anomalies.col_ips', { defaultValue: '并发 / 窗口 IP' })}</TableCell>
              <TableCell>{t('admin:geo_anomalies.col_reason', { defaultValue: '依据' })}</TableCell>
              <TableCell>{t('admin:geo_anomalies.col_updated', { defaultValue: '最后判定' })}</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {rows.length === 0 && !err && (
              <TableRow><TableCell colSpan={6}>
                <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
                  {t('admin:geo_anomalies.empty', { defaultValue: '还没有判定记录——流量轮询跑过一轮后才会出现。' })}
                </Typography>
              </TableCell></TableRow>
            )}
            {rows.map(r => {
              const tierKey = tierLabelKey(r.tier)
              // evidence.v 0 is a row an older build wrote: nothing recorded,
              // so its places (whatever that build stored) are all there is.
              const spots = r.evidence?.v > 0 && r.evidence.spots.length ? groupSpots(r.evidence.spots) : null
              const ex = r.evidence?.excluded ?? { shared: 0, listed: 0, infra: 0, internal: 0 }
              const since = r.service_disabled_at_ms ? new Date(r.service_disabled_at_ms).toLocaleString() : '—'
              return (
                <TableRow key={r.user_id} hover>
                  <TableCell>{r.upn || r.display_name || `#${r.user_id}`}</TableCell>
                  <TableCell>
                    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, alignItems: 'center' }}>
                      <Chip size="small" color={stateColor(r.state)}
                        label={t(`admin:geo_anomalies.state_${r.state}`, { defaultValue: r.state })} />
                      {/* The tier is WHY: cross-border, cross-region or
                          cross-city. Coloured by the latch, not the state, so
                          a flag that went idle still reads as a flag. */}
                      {tierKey && (
                        <Chip size="small" variant="outlined"
                          color={r.flagged ? 'error' : r.state === 'suspect' ? 'warning' : 'default'}
                          label={t(tierKey, { defaultValue: TIER_DEFAULT[r.tier] ?? r.tier })} />
                      )}
                      {/* Idle or unreadable samples freeze the streak, so the
                          latch outlives the state. Without this chip a flagged
                          account that disconnected looks cleared. */}
                      {r.flagged && r.state !== 'flagged' && (
                        <Chip size="small" variant="outlined" color="error"
                          label={t('admin:geo_anomalies.latched', { defaultValue: '仍在标记中' })} />
                      )}
                      {/* Only the detector's own hold. Another reason is a
                          person's (or another policy's) decision, and marking it
                          here would credit the automation with it. */}
                      {r.service_disabled_reason === 'geo_auto' && (
                        <Tooltip title={t('admin:geo_anomalies.auto_suspended_since', {
                          time: since, defaultValue: `自 ${since} 起自动暂停，到期自动恢复`,
                        })}>
                          <Chip size="small" color="error"
                            label={t('admin:geo_anomalies.auto_suspended', { defaultValue: '自动暂停中' })} />
                        </Tooltip>
                      )}
                    </Box>
                  </TableCell>
                  <TableCell sx={{ fontSize: 12 }}>
                    {spots
                      ? spots.map(c => <Box key={c.cc} sx={{ whiteSpace: 'nowrap' }}>{spotLine(c)}</Box>)
                      : r.places.length ? r.places.join(' · ') : '—'}
                  </TableCell>
                  <TableCell align="right">
                    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.5 }}>
                      {/* Concurrent first: it is what was judged. The window is
                          what the upstream remembers for 30 minutes — a
                          commuter's whole morning — and shown alone it made one
                          phone look like several places at once. */}
                      <Tooltip title={t('admin:geo_anomalies.ips_hint', {
                        concurrent: r.concurrent_ips, window: r.live_ips, excluded: r.excluded_ips,
                        shared: ex.shared, listed: ex.listed, infra: ex.infra, internal: ex.internal,
                        defaultValue: `并发 ${r.concurrent_ips}：此刻仍在连接的地址（IPv6 按 /64 合并）。窗口 ${r.live_ips}：上游最近 30 分钟见过的地址。另有 ${r.excluded_ips} 个已排除：共享出口 ${ex.shared}、忽略名单 ${ex.listed}、本机节点/中转 ${ex.infra}、内网 ${ex.internal}。`,
                      })}>
                        <span>{`${r.concurrent_ips} / ${r.live_ips}`}</span>
                      </Tooltip>
                      {/* A count taken while some panel was unreadable is a FLOOR.
                          Rendering it as a plain number would let a partial count
                          read as a clean bill of health, which is the failure this
                          whole area keeps producing. */}
                      {!r.complete && (
                        <Tooltip title={t('admin:geo_anomalies.incomplete', {
                          defaultValue: '有面板读取失败，这个数字是下限而不是总数。',
                        })}>
                          <WarningAmberIcon fontSize="inherit" color="warning" />
                        </Tooltip>
                      )}
                    </Box>
                  </TableCell>
                  <TableCell sx={{ fontSize: 12, color: md.onSurfaceVariant, maxWidth: 420 }}>{r.reason}</TableCell>
                  <TableCell sx={{ fontSize: 12, whiteSpace: 'nowrap' }}>
                    {r.updated_at_ms ? new Date(r.updated_at_ms).toLocaleString() : '—'}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </TableContainer>
    </Box>
  )
}
