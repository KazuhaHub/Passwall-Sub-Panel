import { useState } from 'react'
import { Link } from 'react-router'
import type { MetricsSnapshot } from '@/api/diagnostics'
import { HelpTip } from '@/components/common/HelpTip'
import { Button, buttonVariants } from '@/components/ui/button'
import {
  FINDING_LOGS,
  breakdownInterpolation,
  findingInterpolation,
  findingParts,
  sessionDelta,
  sessionLineKey,
  type Finding,
  type FindingId,
  type SessionBase,
} from '@/utils/diagnostics'
import { LINK_TARGET, type CardId } from '@/utils/diagnosticsCatalog'
import { Disclosure } from '@/components/common/Disclosure'
import { SeverityBadge } from './StateBadge'
import type { DiagFormat } from './useDiagFormat'

// The page's problems, each written the same way: what happened, with its
// denominator and window; what that does; any sentence that applies to this
// reading; what to do; where to go; what to search the log for; and whether
// it has grown since the page was opened. Notices, standing conditions
// rather than incidents, sit below on one line each and open on demand.
// A notice's open state lives in its own row, keyed by the finding id, so the
// minute refresh (which re-derives the findings) leaves an open one open.

const ACTIONABLE = new Set(['critical', 'error', 'warn'])

function interpolation(f: Finding, fmt: DiagFormat): Record<string, string> {
  return {
    ...findingInterpolation(f, { count: fmt.count, duration: fmt.duration, pct: fmt.pct }),
    ...breakdownInterpolation(f, fmt.label, fmt.count),
  }
}

function SessionLine({ f, base, cur, fmt }: { f: Finding; base?: SessionBase; cur: MetricsSnapshot; fmt: DiagFormat }) {
  if (f.series.length === 0) return <span />
  const d = sessionDelta(base?.metrics, cur, f.series)
  const text = fmt.t(`admin:diagnostics.problems.${sessionLineKey(d.kind, base?.rebuilt ?? false)}`, {
    count: fmt.count(d.kind === 'grew' ? d.count : 0),
    minutes: fmt.count(d.kind === 'grew' || d.kind === 'flat' ? d.minutes : 0),
  })
  return <span data-testid="session-delta" data-kind={d.kind} className="text-xs text-muted-foreground">{text}</span>
}

/** Impact, the sentences this reading carries, the action and the log line. */
function FindingBody({ f, fmt }: { f: Finding; fmt: DiagFormat }) {
  const { t } = fmt
  const values = interpolation(f, fmt)
  const base = `admin:diagnostics.findings.${f.key}`
  const logs = FINDING_LOGS[f.id as FindingId] ?? []
  return (
    <div className="mt-2 space-y-1.5 text-sm">
      <p>{t(`${base}.impact`, values)}</p>
      {findingParts(f).map(part => (
        <p key={part} className="text-muted-foreground">{t(`${base}.${part}`, values)}</p>
      ))}
      <p className="font-medium">{t(`${base}.action`, values)}</p>
      {logs.includes('log') && (
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="text-xs text-muted-foreground">{t('admin:diagnostics.problems.log_label')}</span>
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs break-all">{t(`${base}.log`)}</code>
          {logs.includes('log_note') && (
            <HelpTip text={t(`${base}.log_note`)} label={t('admin:diagnostics.problems.log_label')} />
          )}
        </div>
      )}
    </div>
  )
}

interface RowProps {
  f: Finding; fmt: DiagFormat; base?: SessionBase; cur: MetricsSnapshot; onShowData: (card: CardId) => void
}

function FindingFooter({ f, fmt, base, cur, onShowData }: RowProps) {
  const link = f.link ? LINK_TARGET[f.link] : undefined
  return (
    <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
      <SessionLine f={f} base={base} cur={cur} fmt={fmt} />
      <div className="flex flex-wrap gap-2">
        {link && (
          // A real link (role link), drawn like a button: it navigates.
          <Link to={link.path} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
            {fmt.t('admin:diagnostics.links.open', { page: fmt.t(link.nav) })}
          </Link>
        )}
        <Button variant="outline" size="sm" onClick={() => onShowData(f.card)}>{fmt.t('admin:diagnostics.problems.show_data')}</Button>
      </div>
    </div>
  )
}

function ProblemRow(props: RowProps) {
  const { f, fmt } = props
  return (
    <div data-testid={`finding-${f.id}`} className="rounded-xl bg-card p-4 ring-1 ring-foreground/10">
      <div className="flex flex-wrap items-start gap-2">
        <SeverityBadge severity={f.severity} />
        <h3 className="min-w-0 flex-1 basis-60 font-semibold">
          {fmt.t(`admin:diagnostics.findings.${f.key}.title`, interpolation(f, fmt))}
        </h3>
      </div>
      <FindingBody f={f} fmt={fmt} />
      <FindingFooter {...props} />
    </div>
  )
}

function NoticeRow(props: RowProps) {
  const { f, fmt } = props
  const [open, setOpen] = useState(false)
  return (
    <div data-testid={`finding-${f.id}`} className="rounded-xl bg-card ring-1 ring-foreground/10">
      <Disclosure open={open} onToggle={() => setOpen(o => !o)} buttonClassName="px-3 py-2.5"
        summary={
          <span className="flex min-w-0 items-center gap-2">
            <SeverityBadge severity={f.severity} />
            <span className="min-w-0 flex-1 text-sm">{fmt.t(`admin:diagnostics.findings.${f.key}.title`, interpolation(f, fmt))}</span>
          </span>
        }>
        <div className="px-4 pb-4">
          <FindingBody f={f} fmt={fmt} />
          <FindingFooter {...props} />
        </div>
      </Disclosure>
    </div>
  )
}

export default function FindingList({ findings, fmt, base, cur, onShowData }: {
  findings: Finding[]
  fmt: DiagFormat
  /** The reading taken when the page opened, for "since you opened this page". */
  base?: SessionBase
  cur: MetricsSnapshot
  onShowData: (card: CardId) => void
}) {
  const problems = findings.filter(f => ACTIONABLE.has(f.severity))
  const notices = findings.filter(f => f.severity === 'notice')
  return (
    <>
      {problems.length > 0 && (
        <section className="mb-6">
          <h2 className="mb-2 text-sm font-medium text-muted-foreground">{fmt.t('admin:diagnostics.problems.title')}</h2>
          <div className="space-y-3">
            {problems.map(f => <ProblemRow key={f.id} f={f} fmt={fmt} base={base} cur={cur} onShowData={onShowData} />)}
          </div>
        </section>
      )}
      {notices.length > 0 && (
        <section className="mb-6">
          <h2 className="mb-2 text-sm font-medium text-muted-foreground">{fmt.t('admin:diagnostics.problems.notices_title')}</h2>
          <div className="space-y-2">
            {notices.map(f => <NoticeRow key={f.id} f={f} fmt={fmt} base={base} cur={cur} onShowData={onShowData} />)}
          </div>
        </section>
      )}
    </>
  )
}
