import type { RouteBudget as Budget } from '@/api/routes'
import { InfoTip } from '@/components/common/HelpTip'
import { Meter } from '@/components/common/Meter'
import { useNodesT } from '../../shared/useNodesT'

/** What the routes cost the engine they run on: entries and rules as bars that turn to a warning near the limit, and the size. */
export function RouteBudget({ budget }: { budget: Budget }) {
  const t = useNodesT()
  return (
    <div data-testid="routes-budget" className="grid gap-2">
      <div className="flex items-center gap-1">
        <span className="text-sm font-medium">{t('nodes.routes.budget.title', { engine: budget.engine })}</span>
        <InfoTip text={t('nodes.routes.help.xray')} />
      </div>
      {(['entries', 'rules'] as const).map(k => {
        const used = budget[k]
        const max = budget[k === 'entries' ? 'max_entries' : 'max_rules']
        const ratio = max > 0 ? Math.min(1, used / max) : 0
        const text = t(`nodes.routes.budget.${k}`, { used, max })
        return (
          <div key={k} data-testid={`routes-budget-${k}`} className="grid gap-1">
            <span className="text-xs text-muted-foreground">{text}</span>
            <Meter value={used} max={max} label={text} className="h-1.5" barClassName={ratio >= 0.9 ? 'bg-warning-ink' : 'bg-primary'} />
          </div>
        )
      })}
      <span data-testid="routes-budget-bytes" className="text-xs text-muted-foreground">{t('nodes.routes.budget.bytes', { used: budget.bytes, max: budget.max_bytes })}</span>
    </div>
  )
}
