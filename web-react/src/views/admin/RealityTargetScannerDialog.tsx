import { useCallback, useEffect, useState } from 'react'
import {
  Box,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import SearchIcon from '@mui/icons-material/Search'
import { useTranslation } from 'react-i18next'

import {
  scanRealityTargets,
  type RealityScanResponse,
  type RealityScanResult,
} from '@/api/nodes'

interface Props {
  open: boolean
  panelId: number
  sourceName?: string
  onClose: () => void
  onUse: (result: RealityScanResult) => void
}

/**
 * REALITY target discovery UI. Every scan is deliberately routed through the
 * selected 3X-UI panel; this component never attempts browser-side or PSP-host
 * TLS probing. The returned source label makes that network vantage explicit.
 */
export default function RealityTargetScannerDialog({
  open,
  panelId,
  sourceName,
  onClose,
  onUse,
}: Props) {
  const { t } = useTranslation(['admin', 'common'])
  const theme = useTheme()
  const md = theme.palette.md
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(false)
  const [response, setResponse] = useState<RealityScanResponse | null>(null)

  const runScan = useCallback(async (targets?: string) => {
    if (panelId <= 0) return
    setLoading(true)
    try {
      setResponse(await scanRealityTargets(panelId, targets))
    } catch {
      // The shared Axios interceptor owns the error toast. Clear stale results
      // so a failed rescan can never be mistaken for fresh node output.
      setResponse(null)
    } finally {
      setLoading(false)
    }
  }, [panelId])

  useEffect(() => {
    if (!open) return
    setQuery('')
    setResponse(null)
    void runScan()
  }, [open, runScan])

  const source = response?.source_panel_name || sourceName || `#${panelId}`
  const items = response?.items ?? []

  return (
    <Dialog
      open={open}
      onClose={onClose}
      fullWidth
      maxWidth="lg"
      slotProps={{
        paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh } }
      }}
    >
      <DialogTitle sx={{ pb: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
          <Typography component="span" sx={{ fontSize: 18, fontWeight: 500 }}>
            {t('admin:nodes.create_dialog.reality_scan_title')}
          </Typography>
          <Chip
            size="small"
            variant="outlined"
            label={t('admin:nodes.create_dialog.reality_scan_source', { source })}
          />
        </Box>
      </DialogTitle>
      <DialogContent>
        <Typography sx={{ mb: 1.5, fontSize: 13, color: md.onSurfaceVariant }}>
          {t('admin:nodes.create_dialog.reality_scan_desc')}
        </Typography>
        <Box sx={{ display: 'flex', gap: 1, mb: 1.5, alignItems: 'flex-start' }}>
          <TextField
            size="small"
            fullWidth
            value={query}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={e => {
              if (e.key !== 'Enter') return
              e.preventDefault()
              void runScan(query.trim() || undefined)
            }}
            placeholder={t('admin:nodes.create_dialog.reality_scan_placeholder')}
          />
          <Button
            type="button"
            variant="contained"
            disabled={loading}
            startIcon={loading ? <CircularProgress size={15} color="inherit" /> : <SearchIcon />}
            onClick={() => void runScan(query.trim() || undefined)}
            sx={{ whiteSpace: 'nowrap' }}
          >
            {t('admin:nodes.create_dialog.reality_scan')}
          </Button>
        </Box>

        <TableContainer sx={{ maxHeight: 420, border: `1px solid ${md.outlineVariant}`, borderRadius: 2 }}>
          <Table stickyHeader size="small" sx={{ minWidth: 900 }}>
            <TableHead>
              <TableRow>
                <TableCell>{t('admin:nodes.create_dialog.reality_scan_target')}</TableCell>
                <TableCell>{t('admin:nodes.create_dialog.reality_scan_status')}</TableCell>
                <TableCell>TLS</TableCell>
                <TableCell>ALPN</TableCell>
                <TableCell>{t('admin:nodes.create_dialog.reality_scan_key_exchange')}</TableCell>
                <TableCell>{t('admin:nodes.create_dialog.reality_scan_certificate')}</TableCell>
                <TableCell align="right">{t('admin:nodes.create_dialog.reality_scan_latency')}</TableCell>
                <TableCell align="right" />
              </TableRow>
            </TableHead>
            <TableBody>
              {loading && items.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} align="center" sx={{ py: 5 }}>
                    <CircularProgress size={24} />
                  </TableCell>
                </TableRow>
              ) : items.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} align="center" sx={{ py: 5, color: md.onSurfaceVariant }}>
                    {t('admin:nodes.create_dialog.reality_scan_empty')}
                  </TableCell>
                </TableRow>
              ) : items.map(row => (
                <TableRow key={row.target} hover>
                  <TableCell>
                    <Tooltip title={row.ip ? `${row.target} — ${row.ip}` : row.target}>
                      <Box>
                        <Typography sx={{ fontSize: 13, whiteSpace: 'nowrap' }}>{row.target}</Typography>
                        {row.ip && <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant }}>{row.ip}</Typography>}
                      </Box>
                    </Tooltip>
                  </TableCell>
                  <TableCell>
                    <Tooltip title={row.feasible ? '' : row.reason || t('admin:nodes.create_dialog.reality_scan_not_feasible')}>
                      <Chip
                        size="small"
                        color={row.feasible ? 'success' : 'warning'}
                        label={row.feasible
                          ? t('admin:nodes.create_dialog.reality_scan_feasible')
                          : t('admin:nodes.create_dialog.reality_scan_not_feasible')}
                      />
                    </Tooltip>
                  </TableCell>
                  <TableCell>{row.tlsVersion || '—'}</TableCell>
                  <TableCell>{row.alpn || '—'}</TableCell>
                  <TableCell>{row.curveID || '—'}</TableCell>
                  <TableCell>
                    {row.certValid ? (
                      <Tooltip title={[row.certSubject, row.certIssuer].filter(Boolean).join(' — ')}>
                        <Typography sx={{ fontSize: 13, maxWidth: 180 }} noWrap>{row.certSubject || '—'}</Typography>
                      </Tooltip>
                    ) : (
                      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                        {t('admin:nodes.create_dialog.reality_scan_cert_invalid')}
                      </Typography>
                    )}
                  </TableCell>
                  <TableCell align="right">{row.latencyMs > 0 ? `${row.latencyMs} ms` : '—'}</TableCell>
                  <TableCell align="right">
                    <Button
                      type="button"
                      size="small"
                      onClick={() => {
                        onUse(row)
                        onClose()
                      }}
                    >
                      {t('admin:nodes.create_dialog.reality_scan_use')}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      </DialogContent>
      <DialogActions>
        <Button type="button" onClick={() => void runScan(query.trim() || undefined)} disabled={loading}>
          {t('admin:nodes.create_dialog.reality_scan_rescan')}
        </Button>
        <Button type="button" variant="contained" onClick={onClose}>
          {t('common:actions.close')}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
