import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import type { QueueCounts } from '@/api/riskCenter'
import { useSiteStore } from '@/stores/site'
import { cn } from '@/lib/utils'
import { formatDualTz } from '@/utils/datetime'
import { useRiskT } from '../useRiskT'

/** The cards that filter the queue; 在线 is a way to the live tab instead. */
export type MetricCard = 'flagged' | 'suspect' | 'auto' | 'dismissed'

/**
 * The queue's headline numbers, each a click-through filter (D4): 已标记 and
 * 疑似 narrow the open queue to one level, 异地自动暂停 lists every held
 * account whatever its review state (D17), 已忽略 lists the dismissed. Each
 * is a toggle button (aria-pressed) — pressed while its filter is the one in
 * force, and a second click takes the filter away. 在线账号 opens the live
 * tab, so it is a plain button, and says whose snapshot its number is.
 */
export default function QueueMetricCards({ counts, active, onToggle, onOpenLive }: {
  /** Undefined before the first read: every number reads "—". */
  counts: QueueCounts | undefined
  active: MetricCard | null
  onToggle: (card: MetricCard) => void
  onOpenLive: () => void
}) {
  const { t } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  const n = (v: number | null | undefined) => (typeof v === 'number' ? v : undefined)

  // The online count is the live snapshot's, and says whose: the time under the number, and a stale
  // snapshot is a warning tone rather than a quiet one.
  const takenAt = counts?.online_taken_at
  const onlineHint = takenAt
    ? t('admin:risk_center.queue.metric_online_hint', { time: formatDualTz(takenAt, panelTz) })
    : t('admin:risk_center.queue.metric_online_none')

  const toggle = (card: MetricCard, label: string, value: number | undefined, tone?: 'danger' | 'warning') => (
    <KpiTile key={card} label={label} value={n(value)} tone={tone} pressed={active === card} onToggle={() => onToggle(card)} />
  )

  return (
    <KpiGrid className="my-0 md:grid-cols-5">
      {/* A navigation, not a filter: a plain button (no aria-pressed) around a static tile. */}
      <button type="button" onClick={onOpenLive}
        className={cn('rounded-lg text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50 [&>[data-slot=kpi-tile]]:hover:bg-muted')}>
        <KpiTile label={t('admin:risk_center.queue.metric_online')} value={n(counts?.online)} caption={onlineHint}
          tone={counts?.online_stale ? 'warning' : undefined} className="h-full" />
      </button>
      {toggle('flagged', t('admin:risk_center.queue.metric_flagged'), counts?.flagged, 'danger')}
      {toggle('suspect', t('admin:risk_center.queue.metric_suspect'), counts?.suspect, 'warning')}
      {toggle('auto', t('admin:risk_center.queue.metric_auto'), counts?.auto_suspended, 'danger')}
      {toggle('dismissed', t('admin:risk_center.queue.metric_dismissed'), counts?.dismissed)}
    </KpiGrid>
  )
}
