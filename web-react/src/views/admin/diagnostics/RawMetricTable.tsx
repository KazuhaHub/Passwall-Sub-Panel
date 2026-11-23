import { Fragment, useState, type ReactNode } from 'react'
import type { HistogramSnapshot } from '@/api/diagnostics'
import { CopyButton } from '@/components/common/CopyButton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { IconChevronDown } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { bucketRows, quantileUsable, ratePerHour, type RawFamily, type RawSeries } from '@/utils/diagnostics'
import { FAMILY_LABEL_GROUP } from '@/utils/diagnosticsCatalog'
import type { DiagFormat } from './useDiagFormat'

// EVERY SERIES THE SERVER RETURNED, EXACTLY. Values are the API's own,
// grouped but never rounded; a histogram opens on every quantile and every
// bucket; the server's help string is shown as it was sent, as data, and is
// never a fallback for missing copy. The table is shared by the raw area and
// each card's "detailed data", so the two can never show one series two ways.

// One column on a phone, four from the md breakpoint. Written out in full so Tailwind can see the class.
const COLS = 'md:grid-cols-[minmax(0,2.2fr)_88px_minmax(0,1.7fr)_minmax(0,2.3fr)]'
const GRID = `grid gap-x-4 gap-y-1 ${COLS}`

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
  const group = FAMILY_LABEL_GROUP[f.family]
  if (group && s.value !== undefined) return fmt.label(group, s.value)
  return `${s.label}=${s.value}`
}

function CopyName({ name }: { name: string }) {
  return <CopyButton value={name} name={name} size="icon-xs" />
}

function Mono({ children }: { children: ReactNode }) {
  return <code className="font-mono text-[11.5px] break-all text-muted-foreground">{children}</code>
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
    <div data-testid={`hist-${h.name}`} className="col-span-full min-w-0 rounded-lg bg-muted/50 p-3">
      <div className="grid grid-cols-[repeat(auto-fill,minmax(120px,1fr))] gap-2">
        {stats.map(([k, v]) => (
          <div key={k} className="min-w-0">
            <div className="text-xs text-muted-foreground">{t(`admin:diagnostics.raw.stats.${k}`)}</div>
            <div className="text-sm tabular-nums [overflow-wrap:anywhere]">{v}</div>
          </div>
        ))}
      </div>
      <p className="mt-2 text-xs text-muted-foreground">{t('admin:diagnostics.raw.quantile_note')}</p>
      {h.buckets.length > 0 && (
        <>
          <p className="mt-2 text-xs font-semibold">{t('admin:diagnostics.raw.buckets')}</p>
          {/* The bucket table scrolls inside its own box (the shared Table's container); the page never does. */}
          <Table className="tabular-nums [&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
            <TableHeader>
              <TableRow>
                <TableHead>{t('admin:diagnostics.raw.bucket.le')}</TableHead>
                <TableHead className="text-right">{t('admin:diagnostics.raw.bucket.cumulative')}</TableHead>
                <TableHead className="text-right">{t('admin:diagnostics.raw.bucket.count')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {bucketRows(h).map((b, i) => (
                <TableRow key={i}>
                  <TableCell>{b.inf ? t('admin:diagnostics.raw.bucket.inf') : fmt.bound(b.le, h.unit)}</TableCell>
                  <TableCell className="text-right">{fmt.exact(b.cumulative)}</TableCell>
                  <TableCell className="text-right">{fmt.exact(b.count)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      )}
    </div>
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
      <div>
        <div className="text-sm tabular-nums">{fmt.exact(s.counter.value)}</div>
        {perHour !== null && (
          <span className="text-xs text-muted-foreground">{t('admin:diagnostics.fmt.per_hour', { rate: fmt.rate(perHour) })}</span>
        )}
      </div>
    )
  }
  if (s.gauge) {
    return (
      <span className="text-sm tabular-nums" title={t('admin:diagnostics.raw.peak_note')}>
        {t('admin:diagnostics.raw.gauge_value', { value: fmt.exact(s.gauge.value), peak: fmt.exact(s.gauge.peak) })}
      </span>
    )
  }
  if (s.histogram) {
    return (
      <button type="button" onClick={onToggle} aria-expanded={open}
        className="-mx-1 flex items-center gap-1 rounded px-1 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50 pointer-coarse:min-h-[2rem]">
        <span className="text-sm tabular-nums">{histValue(s.histogram, fmt)}</span>
        <IconChevronDown aria-hidden className={cn('size-4 shrink-0 text-muted-foreground transition-transform', open && 'rotate-180')} />
      </button>
    )
  }
  return null
}

function Row({ children, indent = false }: { children: ReactNode; indent?: boolean }) {
  return <div className={cn(GRID, 'min-w-0 border-t py-2', indent && 'pl-3 md:pl-6')}>{children}</div>
}

function TypeCell({ f, fmt }: { f: RawFamily; fmt: DiagFormat }) {
  return <span className="text-xs text-muted-foreground">{fmt.t(`admin:diagnostics.raw.type.${f.type}`)}</span>
}

function Desc({ f, fmt }: { f: RawFamily; fmt: DiagFormat }) {
  const desc = familyDesc(fmt, f.family)
  return (
    <div className="min-w-0 text-xs">
      {desc && <p>{desc}</p>}
      {f.help && (
        <div className={cn('text-muted-foreground', desc && 'mt-1')}>
          <p className="font-semibold">{fmt.t('admin:diagnostics.raw.server_help')}</p>
          <p className="[overflow-wrap:anywhere]">{f.help}</p>
        </div>
      )}
    </div>
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
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const toggle = (name: string) => setOpen(prev => {
    const next = new Set(prev)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    return next
  })
  const { t } = fmt

  const nameCell = (title: string, series: string, mono = false) => (
    <div className="min-w-0">
      {mono ? <Mono>{title}</Mono> : <div className="text-sm font-medium">{title}</div>}
      <div className="flex min-w-0 items-center gap-0.5">
        <Mono>{series}</Mono>
        <CopyName name={series} />
      </div>
    </div>
  )

  const seriesRow = (f: RawFamily, s: RawSeries, child: boolean) => (
    <Row key={s.name} indent={child}>
      {nameCell(child ? childLabel(fmt, f, s) : (familyLabel(fmt, f.family) ?? f.family), s.name)}
      {child ? <div className="hidden md:block" /> : <TypeCell f={f} fmt={fmt} />}
      <SeriesValue s={s} fmt={fmt} windowMs={windowMs} open={open.has(s.name)} onToggle={() => toggle(s.name)} />
      {child ? <div className="hidden md:block" /> : <Desc f={f} fmt={fmt} />}
      {s.histogram && open.has(s.name) && <HistogramDetails h={s.histogram} fmt={fmt} />}
    </Row>
  )

  return (
    <div className="min-w-0">
      <div className={cn('hidden gap-x-4 pb-1 text-xs font-semibold text-muted-foreground md:grid', COLS)}>
        {(['name', 'type', 'value', 'desc'] as const).map(k => <span key={k}>{t(`admin:diagnostics.raw.col.${k}`)}</span>)}
      </div>
      {families.map(f => {
        const title = familyLabel(fmt, f.family) ?? f.family
        if (f.absent) {
          return (
            <Row key={f.family}>
              {nameCell(title, f.family)}
              <TypeCell f={f} fmt={fmt} />
              <span className="text-sm text-muted-foreground">
                {t(f.labelled ? 'admin:diagnostics.raw.no_children' : 'admin:diagnostics.raw.absent')}
              </span>
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
              <span className="text-sm tabular-nums">{familyTotal(f, fmt)}</span>
              <Desc f={f} fmt={fmt} />
            </Row>
            {f.series.map(s => seriesRow(f, s, true))}
          </Fragment>
        )
      })}
    </div>
  )
}
