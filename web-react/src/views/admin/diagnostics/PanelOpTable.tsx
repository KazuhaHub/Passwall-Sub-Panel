import { useState } from 'react'
import {
  Box, Button, Stack, Table, TableBody, TableCell, TableHead, TableRow, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import { quantileReading, type PanelOpRow } from '@/utils/diagnostics'
import type { DiagFormat } from './useDiagFormat'

const FIRST_ROWS = 8

/** p50, p95 and max of one operation, honest about too few samples: under ten
 *  the quantiles are withheld and only the longest is given. */
function latencies(row: PanelOpRow, fmt: DiagFormat) {
  const r = quantileReading(row.rtt)
  const none = '—'
  if (r.kind === 'none') return { p50: none, p95: none, max: none }
  if (r.kind === 'few') return { p50: none, p95: none, max: fmt.latency(r.max) }
  return {
    p50: fmt.latency(r.p50),
    // Beyond the last finite bucket the estimate is pulled toward the max;
    // state the ceiling instead of a number.
    p95: r.over ? `> ${fmt.latency(r.ceiling)}` : fmt.latency(r.p95),
    max: fmt.latency(row.rtt?.max ?? 0),
  }
}

function errorsCell(row: PanelOpRow, fmt: DiagFormat): string {
  return row.errors > 0 ? `${fmt.count(row.errors)} (${fmt.pct(row.errors, row.requests)})` : fmt.count(0)
}

function OpName({ op, fmt }: { op: string; fmt: DiagFormat }) {
  const label = fmt.label('op', op)
  return (
    <Box sx={{ minWidth: 0 }}>
      <Typography variant="body2">{label}</Typography>
      {label !== op && (
        <Typography component="code" sx={{ display: 'block', fontFamily: 'monospace', fontSize: 11, color: 'text.secondary', wordBreak: 'break-all' }}>
          {op}
        </Typography>
      )}
    </Box>
  )
}

/**
 * The 3X-UI requests by operation, busiest first. A table on a wide screen;
 * on a phone each operation is a block, so the page never scrolls sideways.
 */
export default function PanelOpTable({ rows, fmt }: { rows: PanelOpRow[]; fmt: DiagFormat }) {
  const theme = useTheme()
  const md = theme.palette.md
  const narrow = useMediaQuery(theme.breakpoints.down('sm'))
  const [all, setAll] = useState(false)
  const { t } = fmt
  const shown = all ? rows : rows.slice(0, FIRST_ROWS)
  const head = (k: string) => t(`admin:diagnostics.cards.panel_api.table.${k}`)

  return (
    <Box sx={{ mt: 1.5 }}>
      {rows.length > 0 && (narrow ? (
        <Stack spacing={1} data-testid="panel-op-blocks">
          {shown.map(row => {
            const l = latencies(row, fmt)
            return (
              <Box key={row.op} sx={{ p: 1.25, borderRadius: 2, bgcolor: md.surfaceContainer }}>
                <OpName op={row.op} fmt={fmt} />
                <Typography variant="caption" sx={{ display: 'block', mt: 0.5, fontVariantNumeric: 'tabular-nums', color: md.onSurfaceVariant }}>
                  {[
                    `${head('requests')} ${fmt.count(row.requests)}`,
                    `${head('errors')} ${errorsCell(row, fmt)}`,
                    `${head('p50')} ${l.p50}`,
                    `${head('p95')} ${l.p95}`,
                    `${head('max')} ${l.max}`,
                  ].join(' · ')}
                </Typography>
              </Box>
            )
          })}
        </Stack>
      ) : (
        <Box sx={{ overflowX: 'auto' }}>
          <Table size="small" sx={{ '& td, & th': { fontVariantNumeric: 'tabular-nums', px: 1 } }}>
            <TableHead>
              <TableRow>
                <TableCell>{head('op')}</TableCell>
                <TableCell align="right">{head('requests')}</TableCell>
                <TableCell align="right">{head('errors')}</TableCell>
                <TableCell align="right">{head('p50')}</TableCell>
                <TableCell align="right">{head('p95')}</TableCell>
                <TableCell align="right">{head('max')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {shown.map(row => {
                const l = latencies(row, fmt)
                return (
                  <TableRow key={row.op}>
                    <TableCell><OpName op={row.op} fmt={fmt} /></TableCell>
                    <TableCell align="right">{fmt.count(row.requests)}</TableCell>
                    <TableCell align="right">{errorsCell(row, fmt)}</TableCell>
                    <TableCell align="right">{l.p50}</TableCell>
                    <TableCell align="right">{l.p95}</TableCell>
                    <TableCell align="right">{l.max}</TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </Box>
      ))}
      {rows.length > FIRST_ROWS && (
        <Button size="small" sx={{ mt: 0.5 }} onClick={() => setAll(a => !a)}>
          {all
            ? t('admin:diagnostics.cards.panel_api.show_less')
            : t('admin:diagnostics.cards.panel_api.show_all', { count: fmt.count(rows.length) })}
        </Button>
      )}
      <Stack spacing={0.25} sx={{ mt: 1 }}>
        {(['note_scope', 'note_get_client', 'note_timeout'] as const).map(k => (
          <Typography key={k} variant="caption" sx={{ color: md.onSurfaceVariant }}>
            {t(`admin:diagnostics.cards.panel_api.${k}`)}
          </Typography>
        ))}
      </Stack>
    </Box>
  )
}
