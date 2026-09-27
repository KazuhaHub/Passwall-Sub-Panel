import { useState } from 'react'
import {
  Box, CircularProgress, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, Typography, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import { PagedTableFooter } from '@/components/PagedTableFooter'
import { useConnectionHistory } from '@/query/riskCenter'
import { useQueryScope } from '@/query/useQueryScope'
import { AddressCell, JudgementChip, PanelCell, regionText } from './LiveConnectionList'

/**
 * domain.RiskDefaultConnectionRetentionDays: the intro states the SHIPPED
 * default ("kept 7 days by default"), which is what the sentence claims; the
 * value in effect is the settings page's to show.
 */
const RETENTION_DEFAULT_DAYS = 7

const when = (ms: number) => (ms ? new Date(ms).toLocaleString() : '—')

/**
 * One account's connection history: every source the location detector saw
 * it connected from, merged per (panel, node, source), newest sighting first.
 *
 * The one table in PSP that keeps IP addresses, so the intro says so, with
 * who can see it and how long it is kept.
 */
export default function ConnectionHistoryTable({ userId }: { userId: number }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(25)
  const { data, isPending, error } = useConnectionHistory(scope, { user_id: userId, page, page_size: pageSize })
  const rows = data?.items ?? []
  const err = error
    ? (isAxiosError(error) ? String(error.response?.data?.error ?? error.message) : String(error))
    : ''

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
        {t('admin:risk_center.lookup.history_intro', { days: RETENTION_DEFAULT_DAYS })}
      </Typography>
      {err && <Typography color="error" sx={{ fontSize: 13 }}>{err}</Typography>}
      <TableContainer>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>{t('admin:risk_center.live.col_ip')}</TableCell>
              <TableCell>{t('admin:risk_center.live.col_panel')}</TableCell>
              <TableCell>{t('admin:risk_center.live.col_region')}</TableCell>
              <TableCell>{t('admin:risk_center.live.col_status')}</TableCell>
              <TableCell>{t('admin:risk_center.lookup.col_first')}</TableCell>
              <TableCell>{t('admin:risk_center.lookup.col_last')}</TableCell>
              <TableCell align="right">{t('admin:risk_center.lookup.col_count')}</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {isPending && (
              <TableRow><TableCell colSpan={7}><CircularProgress size={20} /></TableCell></TableRow>
            )}
            {!isPending && !err && rows.length === 0 && (
              <TableRow><TableCell colSpan={7}>
                <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('admin:risk_center.flags.empty')}</Typography>
              </TableCell></TableRow>
            )}
            {rows.map(r => (
              <TableRow key={`${r.panel_id}/${r.node}/${r.source_key}`} hover>
                <TableCell><AddressCell ip={r.ip} sourceKey={r.source_key} /></TableCell>
                <TableCell><PanelCell name={r.panel_name} id={r.panel_id} node={r.node} /></TableCell>
                <TableCell sx={{ fontSize: 12 }}>{regionText(r.region)}</TableCell>
                <TableCell><JudgementChip exclusion={r.exclusion} /></TableCell>
                <TableCell sx={{ fontSize: 12, whiteSpace: 'nowrap' }}>{when(r.first_seen_ms)}</TableCell>
                <TableCell sx={{ fontSize: 12, whiteSpace: 'nowrap' }}>{when(r.last_seen_ms)}</TableCell>
                <TableCell align="right">{r.count}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TableContainer>
      {data && (
        <PagedTableFooter total={data.total} page={page} pageSize={pageSize}
          onPageChange={setPage} onPageSizeChange={n => { setPageSize(n); setPage(1) }} />
      )}
    </Box>
  )
}
