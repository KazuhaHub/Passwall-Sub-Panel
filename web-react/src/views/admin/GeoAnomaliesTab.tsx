import { useEffect, useState } from 'react'
import {
  Box, Chip, CircularProgress, Table, TableBody, TableCell,
  TableContainer, TableHead, TableRow, Tooltip, Typography, useTheme,
} from '@mui/material'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useTranslation } from 'react-i18next'

import { listGeoAnomalies, type GeoAnomaly } from '@/api/geoAnomalies'

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

export default function GeoAnomaliesTab() {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const [rows, setRows] = useState<GeoAnomaly[] | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    const ac = new AbortController()
    listGeoAnomalies(ac.signal)
      .then(setRows)
      .catch(e => {
        if (ac.signal.aborted) return
        // 503 means the detector is not wired in this build. Reporting that as
        // "no anomalies" would be the worst possible answer, so it surfaces as
        // its own message rather than an empty table.
        setErr(e?.response?.status === 503
          ? t('admin:geo_anomalies.unwired', { defaultValue: '本部署未启用异地并发检测。' })
          : String(e?.response?.data?.error ?? e))
        setRows([])
      })
    return () => ac.abort()
  }, [t])

  if (rows === null) return <Box sx={{ p: 3 }}><CircularProgress size={24} /></Box>

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
        {t('admin:geo_anomalies.intro', {
          defaultValue: '按用户合并全部面板的并发源 IP 后的判定。只有「已标记」代表持续超限；「疑似」是尚未达到次数的爬升过程。这是嫌疑不是证据——分割隧道、公司 VPN、境外家人都会诚实产生这个信号。目前不做任何自动处置。',
        })}
      </Typography>
      {err && <Typography color="error" sx={{ fontSize: 13 }}>{err}</Typography>}

      <TableContainer>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>{t('admin:geo_anomalies.col_user', { defaultValue: '用户' })}</TableCell>
              <TableCell>{t('admin:geo_anomalies.col_state', { defaultValue: '判定' })}</TableCell>
              <TableCell>{t('admin:geo_anomalies.col_places', { defaultValue: '同时所在' })}</TableCell>
              <TableCell align="right">{t('admin:geo_anomalies.col_ips', { defaultValue: '并发 IP' })}</TableCell>
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
            {rows.map(r => (
              <TableRow key={r.user_id} hover>
                <TableCell>{r.upn || r.display_name || `#${r.user_id}`}</TableCell>
                <TableCell>
                  <Chip size="small" color={stateColor(r.state)}
                    label={t(`admin:geo_anomalies.state_${r.state}`, { defaultValue: r.state })} />
                </TableCell>
                <TableCell>{r.places.length ? r.places.join(' · ') : '—'}</TableCell>
                <TableCell align="right">
                  <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.5 }}>
                    {r.live_ips}
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
            ))}
          </TableBody>
        </Table>
      </TableContainer>
    </Box>
  )
}
