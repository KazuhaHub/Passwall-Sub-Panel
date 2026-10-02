import { useMemo, useState } from 'react'
import {
  Accordion, AccordionDetails, AccordionSummary, Box, Stack, TextField, ToggleButton, ToggleButtonGroup, Typography, useTheme,
} from '@mui/material'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import type { MetricsSnapshot } from '@/api/diagnostics'
import {
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
import RawMetricTable, { childLabel, familyDesc, familyLabel } from './RawMetricTable'
import { panelTime } from './StatusLine'
import type { DiagFormat } from './useDiagFormat'

// THE WHOLE REGISTRY, ONE CLICK AWAY. Folded by default: an operator reads the
// cards; support reads this. Grouped by the card each family belongs to, with
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
  const md = useTheme().palette.md
  const { t } = fmt
  return (
    <Box component="section" sx={{ mt: 2 }}>
      <Typography variant="subtitle2" component="h3">{t('admin:diagnostics.self_check.title')}</Typography>
      <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant, mb: 0.75 }}>
        {t('admin:diagnostics.self_check.note')}
      </Typography>
      <Stack spacing={0.5}>
        {checks.map(c => {
          const values = Object.fromEntries(Object.entries(c.values).map(([k, v]) => [k, fmt.count(v)]))
          return (
            <Box key={c.id} data-testid={`self-check-${c.id}`} data-pass={c.pass} sx={{ display: 'flex', gap: 1, alignItems: 'baseline', minWidth: 0 }}>
              {/* Never coloured: a mismatch questions the statistics, not the fleet. */}
              <Typography variant="caption" sx={{ fontWeight: 600, flex: '0 0 auto' }}>
                {c.pass ? t('admin:diagnostics.self_check.pass') : t('admin:diagnostics.self_check.fail')}
              </Typography>
              <Typography variant="caption" sx={{ minWidth: 0 }}>
                {t(`admin:diagnostics.self_check.${c.id}.${c.pass ? 'pass' : 'fail'}`, values)}
              </Typography>
            </Box>
          )
        })}
      </Stack>
    </Box>
  )
}

export default function RawMetrics({ current, previous, mode, selfChecks, panelTz, fmt }: {
  current: MetricsSnapshot
  previous: ClosedWindow | null
  mode: WindowMode
  selfChecks: SelfCheck[]
  panelTz: string
  fmt: DiagFormat
}) {
  const md = useTheme().palette.md
  const { t } = fmt
  const [query, setQuery] = useState('')
  const [nonZero, setNonZero] = useState(false)
  const [view, setView] = useState<'current' | 'previous'>('current')
  const [openGroups, setOpenGroups] = useState<ReadonlySet<Group>>(new Set())

  const showing = view === 'previous' && previous ? previous.metrics : current
  const families = useMemo(() => rawFamilies(showing), [showing])
  const summary = rawSummary(current)

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
    <Accordion data-testid="raw-metrics" disableGutters slotProps={{ transition: { unmountOnExit: true } }}
      sx={{ mt: 3, borderRadius: 3, bgcolor: md.surfaceContainerLow, '&::before': { display: 'none' } }}>
      <AccordionSummary expandIcon={<ExpandMoreIcon />}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={{ fontWeight: 600 }}>{t('admin:diagnostics.raw.title')}</Typography>
          <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>
            {t('admin:diagnostics.raw.summary', { families: fmt.count(summary.families), series: fmt.count(summary.series) })}
          </Typography>
        </Box>
      </AccordionSummary>
      <AccordionDetails sx={{ minWidth: 0 }}>
        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1, alignItems: 'center', mb: 1.5 }}>
          <TextField size="small" label={t('admin:diagnostics.raw.search')} value={query}
            onChange={e => setQuery(e.target.value)} sx={{ flex: '1 1 240px', minWidth: 0 }} />
          <ToggleButtonGroup size="small" exclusive value={nonZero ? 'nonzero' : 'all'}
            onChange={(_, v: string | null) => { if (v) setNonZero(v === 'nonzero') }}>
            <ToggleButton value="all">{t('admin:diagnostics.raw.filter_all')}</ToggleButton>
            <ToggleButton value="nonzero">{t('admin:diagnostics.raw.filter_nonzero')}</ToggleButton>
          </ToggleButtonGroup>
          {previous && (
            <ToggleButtonGroup size="small" exclusive value={view}
              onChange={(_, v: 'current' | 'previous' | null) => { if (v) setView(v) }}>
              <ToggleButton value="current">{t('admin:diagnostics.raw.view_current')}</ToggleButton>
              <ToggleButton value="previous">
                {t('admin:diagnostics.raw.view_previous', { window: fmt.duration(previous.metrics.window_ms) })}
              </ToggleButton>
            </ToggleButtonGroup>
          )}
        </Box>

        {view === 'previous' && previous ? (
          <Typography variant="body2" sx={{ mb: 1.5, color: md.onSurfaceVariant }}>
            {t('admin:diagnostics.raw.previous_note', { time: panelTime(previous.closedAt, panelTz, fmt.lang) })}
          </Typography>
        ) : mode === 'blackout' ? (
          <Typography variant="body2" sx={{ mb: 1.5, color: md.onSurfaceVariant }}>
            {t('admin:diagnostics.raw.blackout_note')}
          </Typography>
        ) : null}

        {filtered.length === 0 && (
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant, py: 2 }}>{t('admin:diagnostics.raw.no_match')}</Typography>
        )}

        {groups.map(g => {
          const inGroup = filtered.filter(f => f.card === g)
          if (inGroup.length === 0) return null
          const counts = groupCounts(families.filter(f => f.card === g))
          const title = g === 'other' ? t('admin:diagnostics.raw.group_other') : t(`admin:diagnostics.cards.${g}.title`)
          // A search opens every group it found something in; otherwise each
          // group opens on demand, and only an open one renders its rows.
          const expanded = searching || openGroups.has(g)
          return (
            <Accordion key={g} disableGutters expanded={expanded} onChange={() => toggleGroup(g)}
              slotProps={{ transition: { unmountOnExit: true } }}
              sx={{ bgcolor: 'transparent', boxShadow: 'none', '&::before': { display: 'none' } }}>
              <AccordionSummary expandIcon={<ExpandMoreIcon />} sx={{ px: 0 }}>
                <Typography variant="body2" sx={{ fontWeight: 600 }}>
                  {`${title} · ${t('admin:diagnostics.raw.group_count', { series: fmt.count(counts.series), nonzero: fmt.count(counts.nonzero) })}`}
                </Typography>
              </AccordionSummary>
              <AccordionDetails sx={{ px: 0, minWidth: 0 }}>
                <RawMetricTable families={inGroup} fmt={fmt} windowMs={showing.window_ms} />
              </AccordionDetails>
            </Accordion>
          )
        })}

        <Typography variant="caption" sx={{ display: 'block', mt: 1.5, color: md.onSurfaceVariant }}>
          {t('admin:diagnostics.raw.absence_note')}
        </Typography>
        <SelfChecks checks={selfChecks} fmt={fmt} />
      </AccordionDetails>
    </Accordion>
  )
}
