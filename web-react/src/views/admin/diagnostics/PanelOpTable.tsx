import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useMediaQuery } from '@/hooks/useMediaQuery'
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
    <div className="min-w-0">
      <div className="text-sm">{label}</div>
      {label !== op && <code className="block font-mono text-[11px] break-all text-muted-foreground">{op}</code>}
    </div>
  )
}

/**
 * The 3X-UI requests by operation, busiest first. A table on a wide screen;
 * on a phone each operation is a block, so the page never scrolls sideways.
 */
export default function PanelOpTable({ rows, fmt }: { rows: PanelOpRow[]; fmt: DiagFormat }) {
  const narrow = useMediaQuery('(max-width: 639px)')
  const [all, setAll] = useState(false)
  const { t } = fmt
  const shown = all ? rows : rows.slice(0, FIRST_ROWS)
  const head = (k: string) => t(`admin:diagnostics.cards.panel_api.table.${k}`)

  return (
    <div className="mt-3">
      {rows.length > 0 && (narrow ? (
        <ul className="space-y-2" data-testid="panel-op-blocks">
          {shown.map(row => {
            const l = latencies(row, fmt)
            return (
              <li key={row.op} className="rounded-lg bg-muted/50 p-2.5">
                <OpName op={row.op} fmt={fmt} />
                <p className="mt-1 text-xs tabular-nums text-muted-foreground">
                  {[
                    `${head('requests')} ${fmt.count(row.requests)}`,
                    `${head('errors')} ${errorsCell(row, fmt)}`,
                    `${head('p50')} ${l.p50}`,
                    `${head('p95')} ${l.p95}`,
                    `${head('max')} ${l.max}`,
                  ].join(' · ')}
                </p>
              </li>
            )
          })}
        </ul>
      ) : (
        <Table className="tabular-nums">
          <TableHeader>
            <TableRow>
              <TableHead>{head('op')}</TableHead>
              <TableHead className="text-right">{head('requests')}</TableHead>
              <TableHead className="text-right">{head('errors')}</TableHead>
              <TableHead className="text-right">{head('p50')}</TableHead>
              <TableHead className="text-right">{head('p95')}</TableHead>
              <TableHead className="text-right">{head('max')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map(row => {
              const l = latencies(row, fmt)
              return (
                <TableRow key={row.op}>
                  <TableCell><OpName op={row.op} fmt={fmt} /></TableCell>
                  <TableCell className="text-right">{fmt.count(row.requests)}</TableCell>
                  <TableCell className="text-right">{errorsCell(row, fmt)}</TableCell>
                  <TableCell className="text-right">{l.p50}</TableCell>
                  <TableCell className="text-right">{l.p95}</TableCell>
                  <TableCell className="text-right">{l.max}</TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      ))}
      {rows.length > FIRST_ROWS && (
        <Button variant="outline" size="sm" className="mt-1" onClick={() => setAll(a => !a)}>
          {all
            ? t('admin:diagnostics.cards.panel_api.show_less')
            : t('admin:diagnostics.cards.panel_api.show_all', { count: fmt.count(rows.length) })}
        </Button>
      )}
      <div className="mt-2 space-y-0.5">
        {(['note_scope', 'note_get_client', 'note_timeout'] as const).map(k => (
          <p key={k} className="text-xs text-muted-foreground">{t(`admin:diagnostics.cards.panel_api.${k}`)}</p>
        ))}
      </div>
    </div>
  )
}
