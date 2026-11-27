import type { StageGroupTime } from '@/utils/diagnostics'
import type { StageGroup } from '@/utils/diagnosticsCatalog'
import type { DiagFormat } from './useDiagFormat'

/**
 * One fill per stage group, none of them a state colour: red, amber and green
 * mean something on this page, and the bar only says where the time of a poll
 * that may be perfectly healthy went. Brand and neutral fills only, each at a
 * different strength, so the five stay apart in both themes; the legend beside
 * the bar repeats every figure in words, so the fill is never the only way to
 * tell a segment.
 */
export const STAGE_GROUP_FILL: Record<StageGroup, string> = {
  db: 'bg-chart-2',
  panels: 'bg-brand',
  compute: 'bg-foreground/75',
  write: 'bg-brand/45',
  geo: 'bg-muted-foreground/40',
}

/**
 * Where an average poll's time goes: one bar, a segment per group in poll
 * order, each as wide as its share, with a legend that wraps on a phone
 * rather than squeezing labels into slivers.
 */
export default function StageBar({ groups, fmt }: { groups: StageGroupTime[]; fmt: DiagFormat }) {
  const shown = groups.filter(g => g.avgMs > 0)
  if (shown.length === 0) return null
  const { t } = fmt
  return (
    <div className="mt-3" data-testid="stage-bar">
      <p className="mb-1 text-xs text-muted-foreground">{t('admin:diagnostics.cards.poll.stages.title')}</p>
      <div className="flex h-2.5 gap-px overflow-hidden rounded-sm bg-muted">
        {shown.map(g => (
          <div key={g.group} className={`min-w-0.5 basis-0 ${STAGE_GROUP_FILL[g.group]}`} style={{ flexGrow: g.avgMs }} />
        ))}
      </div>
      <ul className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1">
        {shown.map(g => (
          <li key={g.group} className="flex min-w-0 items-center gap-1.5">
            <span aria-hidden className={`size-2.5 shrink-0 rounded-[2px] ${STAGE_GROUP_FILL[g.group]}`} />
            <span className="text-xs tabular-nums">
              {`${t(`admin:diagnostics.cards.poll.stages.${g.group}`)} ${fmt.latency(g.avgMs)}`}
            </span>
          </li>
        ))}
      </ul>
      <p className="mt-1 text-xs text-muted-foreground">{t('admin:diagnostics.cards.poll.stages.note')}</p>
    </div>
  )
}
