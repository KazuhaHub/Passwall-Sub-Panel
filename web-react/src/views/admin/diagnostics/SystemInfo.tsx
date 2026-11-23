import type { ReactNode } from 'react'
import type { DiagnosticsSnapshot } from '@/api/diagnostics'
import { CopyButton } from '@/components/common/CopyButton'
import { HelpTip } from '@/components/common/HelpTip'
import { formatMsDualTz } from '@/utils/datetime'
import { gauge } from '@/utils/diagnostics'
import { panelTime } from './panelTime'
import type { DiagFormat } from './useDiagFormat'

/** The response's own fields, which no card owns: build, clocks, the poll
 *  interval actually in use beside the one asked for, and the goroutines. */
export default function SystemInfo({ snap, settingsIntervalMs, panelTz, fmt }: {
  snap: DiagnosticsSnapshot
  settingsIntervalMs: number
  panelTz: string
  fmt: DiagFormat
}) {
  const { t } = fmt
  const m = snap.metrics
  const interval = gauge(m, 'psp_poll_interval_ms')
  const commit = snap.commit ?? ''

  const rows: Array<[string, ReactNode]> = [
    [t('admin:diagnostics.system.version'), snap.version],
    [t('admin:diagnostics.system.build'), commit ? (
      <span className="inline-flex items-center gap-1">
        <code className="font-mono text-[13px]" title={commit}>{commit.slice(0, 7)}</code>
        <CopyButton value={commit} name={t('admin:diagnostics.system.build')} size="icon-xs" />
      </span>
    ) : t('admin:diagnostics.system.build_local')],
    [t('admin:diagnostics.system.uptime'), fmt.duration(snap.uptime_ms)],
    [t('admin:diagnostics.system.since'), (
      <span title={`${formatMsDualTz(m.since_unix_ms, panelTz)} · ${m.since_unix_ms}`}>{panelTime(m.since_unix_ms, panelTz, fmt.lang)}</span>
    )],
    [t('admin:diagnostics.system.window'), fmt.duration(m.window_ms)],
    [t('admin:diagnostics.system.interval'), interval && interval.value > 0 ? (
      <>
        {fmt.duration(interval.value)}
        <span className="block text-xs text-muted-foreground">
          {t('admin:diagnostics.system.interval_peak', { peak: fmt.duration(interval.peak) })}
        </span>
      </>
    ) : '—'],
    [t('admin:diagnostics.system.interval_setting'), settingsIntervalMs > 0 ? fmt.duration(settingsIntervalMs) : '—'],
    [t('admin:diagnostics.system.goroutines'), (
      <span className="inline-flex items-center gap-1">
        {fmt.count(snap.goroutines)}
        <HelpTip text={t('admin:diagnostics.system.goroutines_note')}
          label={t('admin:diagnostics.help_label_card', { title: t('admin:diagnostics.system.goroutines') })} />
      </span>
    )],
  ]

  return (
    <section data-testid="diag-system" className="mt-6 rounded-xl bg-card p-4 ring-1 ring-foreground/10">
      <h3 className="mb-2 text-sm font-semibold">{t('admin:diagnostics.system.title')}</h3>
      <dl className="m-0 grid grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm sm:grid-cols-[repeat(4,minmax(0,auto)_minmax(0,1fr))]">
        {rows.map(([label, value]) => (
          <div key={label} className="contents">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="m-0 min-w-0 tabular-nums [overflow-wrap:anywhere]">{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}
