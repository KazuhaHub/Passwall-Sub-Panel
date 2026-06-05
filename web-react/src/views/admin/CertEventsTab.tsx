import { useCallback, useEffect, useState } from 'react'
import {
  Card,
  Chip,
  CircularProgress,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import { PagedTableFooter } from '@/components/PagedTableFooter'
import { listCertEvents, type CertEvent } from '@/api/certs'
import { formatDualTz } from '@/utils/datetime'
import { useSiteStore } from '@/stores/site'

// CertEventsTab is the Logs page's "Certificates" tab: the issuance/renewal
// activity log. Self-contained (own paging/state) so it doesn't touch LogsView's
// load orchestration. Deploy isn't shown here — it's a node sync-task (Sync tasks).
export default function CertEventsTab() {
  const theme = useTheme()
  const md = theme.palette.md as unknown as Record<string, string>
  const { t } = useTranslation(['admin', 'common'])
  const panelTz = useSiteStore(s => s.timezone)

  const [events, setEvents] = useState<CertEvent[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(25)
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const r = await listCertEvents(page, pageSize)
      setEvents(r.events)
      setTotal(r.total)
    } catch {
      /* error toast via the axios interceptor */
    } finally {
      setLoading(false)
    }
  }, [page, pageSize])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
      <TableContainer>
        <Table>
          <TableHead>
            <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
              <TableCell>{t('admin:logs.cert_table.at')}</TableCell>
              <TableCell>{t('admin:logs.cert_table.cert')}</TableCell>
              <TableCell>{t('admin:logs.cert_table.kind')}</TableCell>
              <TableCell>{t('admin:logs.cert_table.result')}</TableCell>
              <TableCell>{t('admin:logs.cert_table.message')}</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {loading && events.length === 0 && (
              <TableRow><TableCell colSpan={5} sx={{ textAlign: 'center', py: 6 }}><CircularProgress size={24} /></TableCell></TableRow>
            )}
            {!loading && events.length === 0 && (
              <TableRow><TableCell colSpan={5} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
            )}
            {events.map(e => (
              <TableRow key={e.id} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}>
                <TableCell sx={{ fontSize: 13, whiteSpace: 'nowrap' }}>{formatDualTz(e.created_at, panelTz)}</TableCell>
                <TableCell sx={{ fontWeight: 500 }}>{e.cert_name || `#${e.cert_id}`}</TableCell>
                <TableCell sx={{ fontSize: 13 }}>{t(`admin:logs.cert_kind.${e.kind}`, { defaultValue: e.kind })}</TableCell>
                <TableCell>
                  <Chip
                    size="small"
                    label={e.success ? t('admin:logs.cert_result.ok') : t('admin:logs.cert_result.fail')}
                    sx={{ height: 22, bgcolor: e.success ? md.primary : md.error, color: md.surface ?? '#fff' }}
                  />
                </TableCell>
                <TableCell sx={{ fontSize: 12, fontFamily: 'monospace', color: md.onSurfaceVariant, maxWidth: 480, wordBreak: 'break-word' }}>
                  {e.message || '—'}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TableContainer>
      <PagedTableFooter total={total} page={page} pageSize={pageSize} onPageChange={setPage} onPageSizeChange={s => { setPageSize(s); setPage(1) }} />
    </Card>
  )
}
