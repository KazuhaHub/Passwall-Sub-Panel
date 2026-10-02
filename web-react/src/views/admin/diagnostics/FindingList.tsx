import { useState } from 'react'
import { Box, Button, ButtonBase, Collapse, Stack, Typography, useTheme } from '@mui/material'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { Link as RouterLink } from 'react-router'
import type { MetricsSnapshot } from '@/api/diagnostics'
import HelpTip from '@/components/HelpTip'
import {
  FINDING_LOGS,
  breakdownInterpolation,
  findingInterpolation,
  findingParts,
  sessionDelta,
  type Finding,
  type FindingId,
} from '@/utils/diagnostics'
import { LINK_TARGET, type CardId } from '@/utils/diagnosticsCatalog'
import { SeverityBadge } from './StatusBadge'
import type { DiagFormat } from './useDiagFormat'

// The page's problems, each written the same way: what happened, with its
// denominator and window; what that does; any sentence that applies to this
// reading; what to do; where to go; what to search the log for; and whether
// it has grown since the page was opened. Notices, standing conditions
// rather than incidents, sit below on one line each and open on demand.

const ACTIONABLE = new Set(['critical', 'error', 'warn'])

function interpolation(f: Finding, fmt: DiagFormat): Record<string, string> {
  return {
    ...findingInterpolation(f, { count: fmt.count, duration: fmt.duration, pct: fmt.pct }),
    ...breakdownInterpolation(f, fmt.label, fmt.count),
  }
}

function SessionLine({ f, base, cur, fmt }: { f: Finding; base?: MetricsSnapshot; cur: MetricsSnapshot; fmt: DiagFormat }) {
  if (f.series.length === 0) return null
  const d = sessionDelta(base, cur, f.series)
  const text = d.kind === 'grew'
    ? fmt.t('admin:diagnostics.problems.session_new', { count: fmt.count(d.count), minutes: fmt.count(d.minutes) })
    : d.kind === 'flat'
      ? fmt.t('admin:diagnostics.problems.session_none', { minutes: fmt.count(d.minutes) })
      : fmt.t('admin:diagnostics.problems.session_wait')
  return <Typography variant="caption" data-testid="session-delta" data-kind={d.kind} sx={{ color: 'text.secondary' }}>{text}</Typography>
}

/** Impact, the sentences this reading carries, the action and the log line. */
function FindingBody({ f, fmt }: { f: Finding; fmt: DiagFormat }) {
  const md = useTheme().palette.md
  const { t } = fmt
  const values = interpolation(f, fmt)
  const base = `admin:diagnostics.findings.${f.key}`
  const logs = FINDING_LOGS[f.id as FindingId] ?? []
  return (
    <Stack spacing={0.75} sx={{ mt: 0.75 }}>
      <Typography variant="body2">{t(`${base}.impact`, values)}</Typography>
      {findingParts(f).map(part => (
        <Typography key={part} variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${base}.${part}`, values)}</Typography>
      ))}
      <Typography variant="body2" sx={{ fontWeight: 500 }}>{t(`${base}.action`, values)}</Typography>
      {logs.includes('log') && (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', minWidth: 0 }}>
          <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>{t('admin:diagnostics.problems.log_label')}</Typography>
          <Box component="code" sx={{
            fontFamily: 'monospace', fontSize: 12, px: 0.75, py: 0.25, borderRadius: 1,
            bgcolor: md.surfaceContainerHighest, wordBreak: 'break-all',
          }}>{t(`${base}.log`)}</Box>
          {logs.includes('log_note') && (
            <HelpTip textKey={`${base}.log_note`} labelKey="admin:diagnostics.problems.log_label" />
          )}
        </Box>
      )}
    </Stack>
  )
}

function FindingActions({ f, fmt, onShowData }: { f: Finding; fmt: DiagFormat; onShowData: (card: CardId) => void }) {
  const link = f.link ? LINK_TARGET[f.link] : undefined
  return (
    <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
      {link && (
        <Button size="small" variant="outlined" component={RouterLink} to={link.path}>
          {fmt.t('admin:diagnostics.links.open', { page: fmt.t(link.nav) })}
        </Button>
      )}
      <Button size="small" onClick={() => onShowData(f.card)}>{fmt.t('admin:diagnostics.problems.show_data')}</Button>
    </Box>
  )
}

function ProblemRow({ f, fmt, base, cur, onShowData }: {
  f: Finding; fmt: DiagFormat; base?: MetricsSnapshot; cur: MetricsSnapshot; onShowData: (card: CardId) => void
}) {
  const md = useTheme().palette.md
  return (
    <Box data-testid={`finding-${f.id}`} sx={{ p: 2, borderRadius: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
      <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start', flexWrap: 'wrap' }}>
        <SeverityBadge severity={f.severity} />
        <Typography sx={{ fontWeight: 600, flex: '1 1 240px', minWidth: 0 }}>
          {fmt.t(`admin:diagnostics.findings.${f.key}.title`, interpolation(f, fmt))}
        </Typography>
      </Box>
      <FindingBody f={f} fmt={fmt} />
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, flexWrap: 'wrap', mt: 1.5 }}>
        <SessionLine f={f} base={base} cur={cur} fmt={fmt} />
        <FindingActions f={f} fmt={fmt} onShowData={onShowData} />
      </Box>
    </Box>
  )
}

function NoticeRow({ f, fmt, base, cur, onShowData }: {
  f: Finding; fmt: DiagFormat; base?: MetricsSnapshot; cur: MetricsSnapshot; onShowData: (card: CardId) => void
}) {
  const md = useTheme().palette.md
  const [open, setOpen] = useState(false)
  return (
    <Box data-testid={`finding-${f.id}`} sx={{ borderRadius: 3, bgcolor: md.surfaceContainerLow }}>
      <ButtonBase onClick={() => setOpen(o => !o)} aria-expanded={open}
        sx={{ width: '100%', justifyContent: 'flex-start', gap: 1, px: 2, py: 1.25, borderRadius: 3, textAlign: 'left' }}>
        <SeverityBadge severity={f.severity} />
        <Typography variant="body2" sx={{ flex: 1, minWidth: 0 }}>
          {fmt.t(`admin:diagnostics.findings.${f.key}.title`, interpolation(f, fmt))}
        </Typography>
        <ExpandMoreIcon aria-hidden sx={{ transform: open ? 'rotate(180deg)' : 'none', transition: 'transform 150ms' }} />
      </ButtonBase>
      <Collapse in={open} unmountOnExit>
        <Box sx={{ px: 2, pb: 2 }}>
          <FindingBody f={f} fmt={fmt} />
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, flexWrap: 'wrap', mt: 1.5 }}>
            <SessionLine f={f} base={base} cur={cur} fmt={fmt} />
            <FindingActions f={f} fmt={fmt} onShowData={onShowData} />
          </Box>
        </Box>
      </Collapse>
    </Box>
  )
}

export default function FindingList({ findings, fmt, base, cur, onShowData }: {
  findings: Finding[]
  fmt: DiagFormat
  /** The reading taken when the page opened, for "since you opened this page". */
  base?: MetricsSnapshot
  cur: MetricsSnapshot
  onShowData: (card: CardId) => void
}) {
  const problems = findings.filter(f => ACTIONABLE.has(f.severity))
  const notices = findings.filter(f => f.severity === 'notice')
  return (
    <>
      {problems.length > 0 && (
        <Box component="section" sx={{ mb: 3 }}>
          <Typography variant="h6" component="h2" sx={{ mb: 1 }}>{fmt.t('admin:diagnostics.problems.title')}</Typography>
          <Stack spacing={1.5}>
            {problems.map(f => <ProblemRow key={f.id} f={f} fmt={fmt} base={base} cur={cur} onShowData={onShowData} />)}
          </Stack>
        </Box>
      )}
      {notices.length > 0 && (
        <Box component="section" sx={{ mb: 3 }}>
          <Typography variant="subtitle1" component="h2" sx={{ mb: 1 }}>{fmt.t('admin:diagnostics.problems.notices_title')}</Typography>
          <Stack spacing={1}>
            {notices.map(f => <NoticeRow key={f.id} f={f} fmt={fmt} base={base} cur={cur} onShowData={onShowData} />)}
          </Stack>
        </Box>
      )}
    </>
  )
}
