import { useTranslation } from 'react-i18next'

import type { DestHits } from '@/api/accessControl'
import { useSiteStore } from '@/stores/site'
import { ActionBadge, R, RuleLink, UserCell, type Namers } from './cells'
import type { RecGroupBy } from './recordsParams'

/**
 * The folded views: one row per destination, account or policy with its count
 * as a bar (the longest is the whole width), and its last hit. The API gives
 * the sum and the span of the fold, not how many accounts or policies are
 * behind it, so there is no such column.
 */
export function GroupList({ data, view, folded, num, userName, ruleName, exitLabel, onOpenUser, onPickSource }: Namers & { data: DestHits; view: RecGroupBy; folded: boolean }) {
  const { t, i18n } = useTranslation(['admin'])
  const tz = useSiteStore(s => s.timezone)
  const max = Math.max(1, ...data.items.map(i => i.count))
  const last = (ms: number) => {
    try {
      return new Intl.DateTimeFormat(i18n.language, { timeZone: tz || undefined, month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(ms))
    } catch { return '' }
  }
  const objectCell = (h: DestHits['items'][number]) => {
    if (view === 'site') return <span className="truncate font-mono text-[13px]" title={h.dest}>{h.dest}</span>
    if (view === 'user') return <UserCell hit={h} userName={userName} onOpenUser={onOpenUser} />
    if (view === 'exit') return <span data-testid="hit-exit" className="truncate text-[13px]">{exitLabel(h)}</span>
    return <RuleLink hit={h} ruleName={ruleName} onPickSource={onPickSource} />
  }
  const cols = folded ? 'grid-cols-1 gap-1' : 'grid-cols-[minmax(0,1.4fr)_minmax(0,2fr)_7.5rem] gap-3'
  return (
    <div role="table" aria-label={t(`${R}title`)} className="grid">
      {!folded && (
        <div role="row" className={`grid ${cols} border-b px-3 py-2 text-[13px] text-muted-foreground`}>
          <span role="columnheader">{t(`${R}cols.object`)}</span><span role="columnheader">{t(`${R}cols.count`)}</span><span role="columnheader">{t(`${R}cols.last`)}</span>
        </div>
      )}
      {data.items.map((h, i) => (
        <div key={i} role="row" data-testid="hit-group-row" className={`grid ${cols} min-w-0 items-center border-b px-3 py-2`}>
          <div role="cell" className="flex min-w-0 items-center gap-1.5">
            {objectCell(h)}
            <ActionBadge action={h.action} />
          </div>
          <div role="cell" className="flex min-w-0 items-center gap-2">
            <div data-testid="hit-bar" aria-hidden className="h-2 flex-none rounded-full bg-primary" style={{ width: `${Math.max(2, (h.count / max) * 60)}%` }} />
            <span className="text-[13px] font-semibold tabular-nums">{num.format(h.count)}</span>
          </div>
          <span role="cell" className="text-sm text-muted-foreground">{last(h.last_ms)}</span>
        </div>
      ))}
    </div>
  )
}
