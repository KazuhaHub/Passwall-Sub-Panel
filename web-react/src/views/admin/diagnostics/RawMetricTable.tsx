import { Fragment, useState, type ReactNode } from 'react'
import {
  Box, ButtonBase, IconButton, Table, TableBody, TableCell, TableHead, TableRow, Tooltip, Typography, useTheme,
} from '@mui/material'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import type { HistogramSnapshot } from '@/api/diagnostics'
import { copyToClipboard } from '@/utils/clipboard'
import { bucketRows, quantileUsable, ratePerHour, type RawFamily, type RawSeries } from '@/utils/diagnostics'
import { FAMILY_LABEL_GROUP, pairLabelFor } from '@/utils/diagnosticsCatalog'
import type { DiagFormat } from './useDiagFormat'

// EVERY SERIES THE SERVER RETURNED, EXACTLY. Values are the API's own,
// grouped but never rounded; a histogram opens on every quantile and every
// bucket; the server's help string is shown as it was sent, as data, and is
// never a fallback for missing copy. The table is shared by the raw area and
// each card's "detailed data", so the two can never show one series two ways.

const COLUMNS = { xs: 'minmax(0, 1fr)', md: 'minmax(0, 2.2fr) 88px minmax(0, 1.7fr) minmax(0, 2.3fr)' }

/** The translated name of a family, when the bundles have one. */
export function familyLabel(fmt: DiagFormat, family: string): string | undefined {
  const key = `admin:diagnostics.metric.${family}.label`
  return fmt.has(key) ? fmt.t(key) : undefined
}

export function familyDesc(fmt: DiagFormat, family: string): string | undefined {
  const key = `admin:diagnostics.metric.${family}.desc`
  return fmt.has(key) ? fmt.t(key) : undefined
}

/** A child's name: its label group's translation, or the raw label=value. */
export function childLabel(fmt: DiagFormat, f: RawFamily, s: RawSeries): string {
  const pair = pairLabelFor(fmt.label, f.family, s.label, s.value)
  if (pair !== undefined) return pair
  const group = FAMILY_LABEL_GROUP[f.family]
  if (group && s.value !== undefined) return fmt.label(group, s.value)
  return `${s.label}=${s.value}`
}

function CopyName({ name, fmt }: { name: string; fmt: DiagFormat }) {
  return (
    <IconButton size="small" aria-label={fmt.t('admin:diagnostics.raw.copy_name')} onClick={() => void copyToClipboard(name)}
      sx={{ p: 0.25 }}>
      <ContentCopyIcon sx={{ fontSize: 14 }} />
    </IconButton>
  )
}

function Mono({ children }: { children: ReactNode }) {
  return (
    <Typography component="code" sx={{ fontFamily: 'monospace', fontSize: 11.5, color: 'text.secondary', wordBreak: 'break-all' }}>
      {children}
    </Typography>
  )
}

function histValue(h: HistogramSnapshot, fmt: DiagFormat): string {
  if (h.count === 0) return fmt.t('admin:diagnostics.fmt.no_samples')
  if (!quantileUsable(h)) {
    return fmt.t('admin:diagnostics.fmt.few_samples', { count: fmt.count(h.count), max: fmt.unit(h.max, h.unit) })
  }
  return fmt.t('admin:diagnostics.raw.hist_value', {
    count: fmt.count(h.count), p50: fmt.unit(h.p50, h.unit), p95: fmt.unit(h.p95, h.unit),
  })
}

/** Every field of a histogram as the API sent it, then its buckets. */
function HistogramDetails({ h, fmt }: { h: HistogramSnapshot; fmt: DiagFormat }) {
  const md = useTheme().palette.md
  const { t } = fmt
  const stats: Array<[string, string]> = [
    ['unit', h.unit],
    ['count', fmt.exact(h.count)],
    ['sum', fmt.exact(h.sum)],
    ['mean', fmt.exact(h.mean)],
    ['max', fmt.exact(h.max)],
    ['p50', fmt.exact(h.p50)],
    ['p90', fmt.exact(h.p90)],
    ['p95', fmt.exact(h.p95)],
    ['p99', fmt.exact(h.p99)],
  ]
  return (
    <Box data-testid={`hist-${h.name}`} sx={{ gridColumn: '1 / -1', p: 1.5, borderRadius: 2, bgcolor: md.surfaceContainer, minWidth: 0 }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(120px, 1fr))', gap: 1 }}>
        {stats.map(([k, v]) => (
          <Box key={k} sx={{ minWidth: 0 }}>
            <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant }}>{t(`admin:diagnostics.raw.stats.${k}`)}</Typography>
            <Typography variant="body2" sx={{ fontVariantNumeric: 'tabular-nums', overflowWrap: 'anywhere' }}>{v}</Typography>
          </Box>
        ))}
      </Box>
      <Typography variant="caption" sx={{ display: 'block', mt: 1, color: md.onSurfaceVariant }}>
        {t('admin:diagnostics.raw.quantile_note')}
      </Typography>
      {h.buckets.length > 0 && (
        <>
          <Typography variant="caption" sx={{ display: 'block', mt: 1, fontWeight: 600 }}>{t('admin:diagnostics.raw.buckets')}</Typography>
          {/* The bucket table scrolls inside its own box; the page never does. */}
          <Box sx={{ overflowX: 'auto', maxWidth: '100%' }}>
            <Table size="small" sx={{ '& td, & th': { fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap', px: 1 } }}>
              <TableHead>
                <TableRow>
                  <TableCell>{t('admin:diagnostics.raw.bucket.le')}</TableCell>
                  <TableCell align="right">{t('admin:diagnostics.raw.bucket.cumulative')}</TableCell>
                  <TableCell align="right">{t('admin:diagnostics.raw.bucket.count')}</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {bucketRows(h).map((b, i) => (
                  <TableRow key={i}>
                    <TableCell>{b.inf ? t('admin:diagnostics.raw.bucket.inf') : fmt.bound(b.le, h.unit)}</TableCell>
                    <TableCell align="right">{fmt.exact(b.cumulative)}</TableCell>
                    <TableCell align="right">{fmt.exact(b.count)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Box>
        </>
      )}
    </Box>
  )
}

function SeriesValue({ s, fmt, windowMs, open, onToggle }: {
  s: RawSeries; fmt: DiagFormat; windowMs: number; open: boolean; onToggle: () => void
}) {
  const { t } = fmt
  if (s.counter) {
    // Unit-neutral: a raw counter may count rows, addresses or sources, not
    // only events, so the rate carries no unit of its own.
    const perHour = s.counter.value > 0 ? ratePerHour(s.counter.value, windowMs) : null
    return (
      <Box>
        <Typography variant="body2" sx={{ fontVariantNumeric: 'tabular-nums' }}>{fmt.exact(s.counter.value)}</Typography>
        {perHour !== null && (
          <Typography variant="caption" sx={{ color: 'text.secondary' }}>{t('admin:diagnostics.fmt.per_hour', { rate: fmt.rate(perHour) })}</Typography>
        )}
      </Box>
    )
  }
  if (s.gauge) {
    return (
      <Tooltip title={t('admin:diagnostics.raw.peak_note')}>
        <Typography variant="body2" sx={{ fontVariantNumeric: 'tabular-nums' }}>
          {t('admin:diagnostics.raw.gauge_value', { value: fmt.exact(s.gauge.value), peak: fmt.exact(s.gauge.peak) })}
        </Typography>
      </Tooltip>
    )
  }
  if (s.histogram) {
    return (
      <ButtonBase onClick={onToggle} aria-expanded={open}
        sx={{ justifyContent: 'flex-start', textAlign: 'left', gap: 0.5, borderRadius: 1, px: 0.5, mx: -0.5 }}>
        <Typography variant="body2" sx={{ fontVariantNumeric: 'tabular-nums' }}>{histValue(s.histogram, fmt)}</Typography>
        <ExpandMoreIcon aria-hidden sx={{ fontSize: 18, transform: open ? 'rotate(180deg)' : 'none' }} />
      </ButtonBase>
    )
  }
  return null
}

function Row({ children, indent = false, divider = true }: { children: ReactNode; indent?: boolean; divider?: boolean }) {
  const md = useTheme().palette.md
  return (
    <Box sx={{
      display: 'grid', gridTemplateColumns: COLUMNS, columnGap: 2, rowGap: 0.5, py: 1,
      pl: indent ? { xs: 1.5, md: 3 } : 0, minWidth: 0,
      borderTop: divider ? `1px solid ${md.outlineVariant}` : 'none',
    }}>
      {children}
    </Box>
  )
}

function TypeCell({ f, fmt }: { f: RawFamily; fmt: DiagFormat }) {
  return <Typography variant="caption" sx={{ color: 'text.secondary' }}>{fmt.t(`admin:diagnostics.raw.type.${f.type}`)}</Typography>
}

function Desc({ f, fmt }: { f: RawFamily; fmt: DiagFormat }) {
  const desc = familyDesc(fmt, f.family)
  return (
    <Box sx={{ minWidth: 0 }}>
      {desc && <Typography variant="caption" sx={{ display: 'block' }}>{desc}</Typography>}
      {f.help && (
        <Box sx={{ mt: desc ? 0.5 : 0 }}>
          <Typography variant="caption" sx={{ display: 'block', color: 'text.secondary', fontWeight: 600 }}>
            {fmt.t('admin:diagnostics.raw.server_help')}
          </Typography>
          <Typography variant="caption" component="span" sx={{ display: 'block', color: 'text.secondary', overflowWrap: 'anywhere' }}>
            {f.help}
          </Typography>
        </Box>
      )}
    </Box>
  )
}

/**
 * What a labelled family's parent row shows. A counter family's children add
 * up to a count; a distribution family's add up only as samples (stages x
 * polls, requests timed), so it is labelled as such, never left bare beside a
 * family whose unit is a time or a distance; gauges do not add up at all.
 */
function familyTotal(f: RawFamily, fmt: DiagFormat): string {
  if (f.type === 'gauge') return ''
  const total = f.series.reduce((sum, s) => sum + (s.counter?.value ?? s.histogram?.count ?? 0), 0)
  return fmt.t(f.type === 'histogram' ? 'admin:diagnostics.raw.family_total_samples' : 'admin:diagnostics.raw.family_total',
    { value: fmt.exact(total) })
}

export default function RawMetricTable({ families, fmt, windowMs }: {
  families: RawFamily[]
  fmt: DiagFormat
  windowMs: number
}) {
  const md = useTheme().palette.md
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const toggle = (name: string) => setOpen(prev => {
    const next = new Set(prev)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    return next
  })
  const { t } = fmt

  const nameCell = (title: string, series: string, mono = false) => (
    <Box sx={{ minWidth: 0 }}>
      {mono
        ? <Mono>{title}</Mono>
        : <Typography variant="body2" sx={{ fontWeight: 500 }}>{title}</Typography>}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.25, minWidth: 0 }}>
        <Mono>{series}</Mono>
        <CopyName name={series} fmt={fmt} />
      </Box>
    </Box>
  )

  const seriesRow = (f: RawFamily, s: RawSeries, child: boolean) => (
    <Fragment key={s.name}>
      <Row indent={child}>
        {nameCell(child ? childLabel(fmt, f, s) : (familyLabel(fmt, f.family) ?? f.family), s.name)}
        {child ? <Box sx={{ display: { xs: 'none', md: 'block' } }} /> : <TypeCell f={f} fmt={fmt} />}
        <SeriesValue s={s} fmt={fmt} windowMs={windowMs} open={open.has(s.name)} onToggle={() => toggle(s.name)} />
        {child ? <Box sx={{ display: { xs: 'none', md: 'block' } }} /> : <Desc f={f} fmt={fmt} />}
        {s.histogram && open.has(s.name) && <HistogramDetails h={s.histogram} fmt={fmt} />}
      </Row>
    </Fragment>
  )

  return (
    <Box sx={{ minWidth: 0 }}>
      <Box sx={{
        display: { xs: 'none', md: 'grid' }, gridTemplateColumns: COLUMNS.md, columnGap: 2, pb: 0.5,
        color: md.onSurfaceVariant,
      }}>
        {(['name', 'type', 'value', 'desc'] as const).map(k => (
          <Typography key={k} variant="caption" sx={{ fontWeight: 600 }}>{t(`admin:diagnostics.raw.col.${k}`)}</Typography>
        ))}
      </Box>
      {families.map(f => {
        const title = familyLabel(fmt, f.family) ?? f.family
        if (f.absent) {
          return (
            <Row key={f.family}>
              {nameCell(title, f.family)}
              <TypeCell f={f} fmt={fmt} />
              <Typography variant="body2" sx={{ color: 'text.secondary' }}>
                {t(f.labelled ? 'admin:diagnostics.raw.no_children' : 'admin:diagnostics.raw.absent')}
              </Typography>
              <Desc f={f} fmt={fmt} />
            </Row>
          )
        }
        if (!f.labelled) return seriesRow(f, f.series[0], false)
        return (
          <Fragment key={f.family}>
            <Row>
              {nameCell(title, f.family)}
              <TypeCell f={f} fmt={fmt} />
              <Typography variant="body2" sx={{ fontVariantNumeric: 'tabular-nums' }}>
                {familyTotal(f, fmt)}
              </Typography>
              <Desc f={f} fmt={fmt} />
            </Row>
            {f.series.map(s => seriesRow(f, s, true))}
          </Fragment>
        )
      })}
    </Box>
  )
}
