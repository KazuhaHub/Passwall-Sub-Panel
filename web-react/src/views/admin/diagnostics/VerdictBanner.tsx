import type { DiagnosticsSnapshot } from '@/api/diagnostics'
import { TONE_STYLE } from '@/components/common/tones'
import { IconError, IconPending, IconSuccess, IconWarning, type IconType } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { formatMsDualTz } from '@/utils/datetime'
import { wasReset, type PageVerdict } from '@/utils/diagnostics'
import { panelTime } from './panelTime'
import { VERDICT_TONE } from './StateBadge'
import type { DiagFormat } from './useDiagFormat'

// The page's one summary, in words, before anything else: whether the system
// looks healthy, which subsystems are the reason when it does not, and what
// window of statistics that judgement rests on. The tone only decorates; the
// headline carries the verdict.

const ICON: Record<PageVerdict['tone'], IconType> = {
  critical: IconError,
  action: IconError,
  attention: IconWarning,
  blackout: IconPending,
  measuring: IconPending,
  ok: IconSuccess,
}

export default function VerdictBanner({ verdict, snap, intervalMs, readAt, panelTz, fmt }: {
  verdict: PageVerdict
  snap: DiagnosticsSnapshot
  intervalMs: number
  readAt: number
  panelTz: string
  fmt: DiagFormat
}) {
  const { t } = fmt
  const m = snap.metrics
  const style = TONE_STYLE[VERDICT_TONE[verdict.tone]]
  const Icon = ICON[verdict.tone]
  const cards = verdict.cards.map(id => t(`admin:diagnostics.cards.${id}.title`)).join(t('admin:diagnostics.status.list_separator'))
  const headline = t(`admin:diagnostics.verdict.${verdict.key}`, {
    cards,
    count: fmt.count(verdict.notices),
    remaining: fmt.duration(verdict.remainingMs ?? 0),
    interval: fmt.duration(intervalMs),
  })
  const since = panelTime(m.since_unix_ms, panelTz, fmt.lang)
  // After a manual clear the window is shorter than the uptime, so the line
  // says so once instead of printing one span twice.
  const window = wasReset(snap)
    ? t('admin:diagnostics.status.window_after_reset', { since, window: fmt.duration(m.window_ms), uptime: fmt.duration(snap.uptime_ms) })
    : t('admin:diagnostics.status.window', { since, window: fmt.duration(m.window_ms) })
  const interval = intervalMs > 0
    ? t('admin:diagnostics.status.interval', { interval: fmt.duration(intervalMs) })
    : t('admin:diagnostics.status.interval_unknown')

  return (
    <section data-testid="diag-status" data-tone={verdict.tone}
      className={cn('mb-4 flex min-w-0 items-start gap-3 rounded-xl p-4 ring-1 ring-inset', style.tint, style.ring)}>
      <Icon aria-hidden className={cn('mt-0.5 size-5 shrink-0', style.ink)} />
      <div className="min-w-0">
        {/* Only the headline is a live region: the read-at time below changes
            every minute and would be announced on every refresh. */}
        <h2 role="status" className="text-base font-semibold">{headline}</h2>
        <p className="mt-1 text-sm text-muted-foreground" title={formatMsDualTz(m.since_unix_ms, panelTz)}>{`${window} · ${interval}`}</p>
        <p className="mt-0.5 text-xs text-muted-foreground">{t('admin:diagnostics.status.read_at', { time: panelTime(readAt, panelTz, fmt.lang, true) })}</p>
      </div>
    </section>
  )
}
