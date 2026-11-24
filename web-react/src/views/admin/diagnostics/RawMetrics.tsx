import { useMemo, useState } from 'react'
import type { MetricsSnapshot } from '@/api/diagnostics'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Input } from '@/components/ui/input'
import {
  deriveSelfChecks,
  filterRawFamilies,
  rawFamilies,
  rawSummary,
  seriesNonZero,
  type RawFamily,
  type RawSeries,
  type SelfCheck,
  type WindowMode,
} from '@/utils/diagnostics'
import { CARD_ORDER, type CardId } from '@/utils/diagnosticsCatalog'
import { Disclosure } from '@/components/common/Disclosure'
import { panelTime } from './panelTime'
import RawMetricTable, { childLabel, familyDesc, familyLabel } from './RawMetricTable'
import type { DiagFormat } from './useDiagFormat'

// THE WHOLE REGISTRY, ONE CLICK AWAY. Folded by default: an operator reads the
// subsystems; support reads this. Grouped by the card each family belongs to, with
// families Go added after this page was built under "other", searchable by
// what the page calls a row as well as by its metric name, and switchable to
// the window a clear on this page closed.

export interface ClosedWindow {
  metrics: MetricsSnapshot
  /** When the cleared window closed, by the server's clock. */
  closedAt: number
}

type Group = CardId | 'other'

function groupCounts(families: RawFamily[]): { series: number; nonzero: number } {
  const series: RawSeries[] = families.flatMap(f => f.series)
  return { series: series.length, nonzero: series.filter(seriesNonZero).length }
}

function SelfChecks({ checks, fmt }: { checks: SelfCheck[]; fmt: DiagFormat }) {
  const { t } = fmt
  return (
    <section className="mt-4">
      <h3 className="text-sm font-semibold">{t('admin:diagnostics.self_check.title')}</h3>
      <p className="mb-1.5 text-xs text-muted-foreground">{t('admin:diagnostics.self_check.note')}</p>
      <ul className="space-y-1">
        {checks.map(c => {
          const values = Object.fromEntries(Object.entries(c.values).map(([k, v]) => [k, fmt.count(v)]))
          return (
            <li key={c.id} data-testid={`self-check-${c.id}`} data-pass={c.pass} className="flex min-w-0 items-baseline gap-2 text-xs">
              {/* Never coloured: a mismatch questions the statistics, not the fleet. */}
              <span className="shrink-0 font-semibold">
                {c.pass ? t('admin:diagnostics.self_check.pass') : t('admin:diagnostics.self_check.fail')}
              </span>
              <span className="min-w-0">{t(`admin:diagnostics.self_check.${c.id}.${c.pass ? 'pass' : 'fail'}`, values)}</span>
            </li>
          )
        })}
      </ul>
    </section>
  )
}

export default function RawMetrics({ current, previous, mode, panelTz, fmt, open, onToggle }: {
  current: MetricsSnapshot
  previous: ClosedWindow | null
  mode: WindowMode
  panelTz: string
  fmt: DiagFormat
  /** Kept by the page, so the minute refresh does not fold it. */
  open: boolean
  onToggle: () => void
}) {
  const { t } = fmt
  const [query, setQuery] = useState('')
  const [nonZero, setNonZero] = useState(false)
  const [view, setView] = useState<'current' | 'previous'>('current')
  const [openGroups, setOpenGroups] = useState<ReadonlySet<Group>>(new Set())

  // Everything below describes the window shown: the rows, the summary and
  // the consistency checks. Checks taken from the current window beside the
  // closed window's rows would compare one window's polls with another's.
  const showing = view === 'previous' && previous ? previous.metrics : current
  const families = useMemo(() => rawFamilies(showing), [showing])
  const selfChecks = useMemo(() => deriveSelfChecks(showing), [showing])
  const summary = rawSummary(showing)

  const filtered = useMemo(() => filterRawFamilies(families, {
    query,
    nonZero,
    words: (f, s) => s
      ? [childLabel(fmt, f, s)]
      : [familyLabel(fmt, f.family), familyDesc(fmt, f.family)].filter((x): x is string => !!x),
  }), [families, query, nonZero, fmt])

  const groups: Group[] = [...CARD_ORDER, 'other']
  const searching = query.trim() !== ''
  const toggleGroup = (g: Group) => setOpenGroups(prev => {
    const next = new Set(prev)
    if (next.has(g)) next.delete(g)
    else next.add(g)
    return next
  })

  return (
    <section data-testid="raw-metrics" className="mt-6 rounded-xl bg-card ring-1 ring-foreground/10">
      <Disclosure open={open} onToggle={onToggle} buttonClassName="px-3 py-3"
        summary={
          <>
            <span className="block font-semibold">{t('admin:diagnostics.raw.title')}</span>
            <span className="block text-xs text-muted-foreground">
              {t('admin:diagnostics.raw.summary', { families: fmt.count(summary.families), series: fmt.count(summary.series) })}
            </span>
          </>
        }>
        <div className="min-w-0 px-4 pb-4">
          <div className="mb-3 flex flex-wrap items-center gap-2">
            <Input aria-label={t('admin:diagnostics.raw.search')} placeholder={t('admin:diagnostics.raw.search')}
              value={query} onChange={e => setQuery(e.target.value)} className="min-w-0 flex-1 basis-60" />
            <SegmentedControl size="sm" value={nonZero ? 'nonzero' : 'all'} onChange={v => setNonZero(v === 'nonzero')}
              aria-label={t('admin:diagnostics.raw.title')}
              options={[
                { value: 'all', label: t('admin:diagnostics.raw.filter_all') },
                { value: 'nonzero', label: t('admin:diagnostics.raw.filter_nonzero') },
              ]} />
            {previous && (
              <SegmentedControl size="sm" value={view} onChange={setView} aria-label={t('admin:diagnostics.raw.title')}
                options={[
                  { value: 'current', label: t('admin:diagnostics.raw.view_current') },
                  { value: 'previous', label: t('admin:diagnostics.raw.view_previous', { window: fmt.duration(previous.metrics.window_ms) }) },
                ]} />
            )}
          </div>

          {view === 'previous' && previous ? (
            <p className="mb-3 text-sm text-muted-foreground">
              {t('admin:diagnostics.raw.previous_note', { time: panelTime(previous.closedAt, panelTz, fmt.lang) })}
            </p>
          ) : mode === 'blackout' ? (
            <p className="mb-3 text-sm text-muted-foreground">{t('admin:diagnostics.raw.blackout_note')}</p>
          ) : null}

          {filtered.length === 0 && <p className="py-4 text-sm text-muted-foreground">{t('admin:diagnostics.raw.no_match')}</p>}

          {groups.map(g => {
            const inGroup = filtered.filter(f => f.card === g)
            if (inGroup.length === 0) return null
            const counts = groupCounts(families.filter(f => f.card === g))
            const title = g === 'other' ? t('admin:diagnostics.raw.group_other') : t(`admin:diagnostics.cards.${g}.title`)
            // A search opens every group it found something in; otherwise each
            // group opens on demand, and only an open one renders its rows.
            const expanded = searching || openGroups.has(g)
            return (
              <Disclosure key={g} open={expanded} onToggle={() => toggleGroup(g)} className="border-t" buttonClassName="py-2.5 text-sm font-semibold"
                summary={`${title} · ${t('admin:diagnostics.raw.group_count', { series: fmt.count(counts.series), nonzero: fmt.count(counts.nonzero) })}`}>
                <div className="min-w-0 pb-2"><RawMetricTable families={inGroup} fmt={fmt} windowMs={showing.window_ms} /></div>
              </Disclosure>
            )
          })}

          <p className="mt-3 text-xs text-muted-foreground">{t('admin:diagnostics.raw.absence_note')}</p>
          <SelfChecks checks={selfChecks} fmt={fmt} />
        </div>
      </Disclosure>
    </section>
  )
}
