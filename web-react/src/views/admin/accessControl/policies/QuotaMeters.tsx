import { useTranslation } from 'react-i18next'

import type { DestBudget } from '@/api/accessControl'
import { Meter } from '@/components/common/Meter'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { anyBudgetOver, budgetLevel, visibleBudget, type BudgetLevel } from '@/utils/accessControl'

const Q = 'admin:access_control.policies.quota.'

/** Amber from 80%, the danger colour over 100%; the figure is also bold then, so colour is not the only cue. */
const levelText = (level: BudgetLevel): string => (level === 'over' ? 'text-destructive' : level === 'near' ? 'text-warning-ink' : '')
const levelBar = (level: BudgetLevel): string => (level === 'over' ? 'bg-destructive' : level === 'near' ? 'bg-warning-ink' : 'bg-primary')

/**
 * The wire quotas the policies use. Compact: one line above the rail (the
 * four always shown, subjects and bytes only from 80%). Full: the editor's
 * bars for the budget as it would be after saving. At 80% the figure is amber,
 * over 100% it is the danger colour and says why that matters: a new version
 * over the quota is not applied, nodes keep the previous one.
 */
export default function QuotaMeters({ budget, variant, loading = false, failed = false, onRetry, suffix }: {
  budget: DestBudget | undefined
  variant: 'compact' | 'full'
  loading?: boolean
  failed?: boolean
  onRetry?: () => void
  /** Compact only: a quiet closing note ("no node enforces right now"). */
  suffix?: string
}) {
  const { t, i18n } = useTranslation(['admin'])
  const num = new Intl.NumberFormat(i18n.language)

  if (failed) {
    return (
      <p className="text-sm text-muted-foreground">
        {t(`${Q}failed`)} {onRetry && <Button size="sm" variant="ghost" onClick={onRetry}>{t('admin:access_control.retry')}</Button>}
      </p>
    )
  }
  const kinds = budget ? visibleBudget(budget) : (['rules', 'domains', 'regexps', 'cidrs'] as (keyof DestBudget)[])
  const over = anyBudgetOver(budget)

  if (variant === 'compact') {
    return (
      <div>
        <p className="text-sm text-muted-foreground tabular-nums">
          {t(`${Q}label`)}{'  '}
          {kinds.map((k, i) => {
            const item = budget?.[k]
            const level = budgetLevel(item)
            return (
              <span key={k}>
                {i > 0 && ' · '}
                {t(`${Q}kind.${k}`)}{' '}
                <span className={cn('text-foreground', levelText(level), level !== 'ok' && 'font-semibold')}>
                  {item ? `${num.format(item.used)} / ${num.format(item.limit)}` : '…'}
                </span>
              </span>
            )
          })}
          {suffix ? ` · ${suffix}` : ''}
        </p>
        {over && <p className="text-xs text-destructive">{t(`${Q}over_note`)}</p>}
      </div>
    )
  }

  return (
    <div>
      <h3 className="mb-1 text-sm font-semibold">{t(`${Q}after_save`)}</h3>
      {kinds.map(k => {
        const item = budget?.[k]
        const level = budgetLevel(item)
        return (
          <div key={k} className="mb-1 grid items-center gap-x-3 sm:grid-cols-[4.5rem_minmax(0,1fr)_7.5rem]">
            <span className="text-sm text-muted-foreground">{t(`${Q}kind.${k}`)}</span>
            <Meter value={item?.used ?? 0} max={item?.limit ?? 0} label={t(`${Q}kind.${k}`)} className="h-1" barClassName={levelBar(level)} />
            <span className={cn('text-sm tabular-nums sm:text-right', levelText(level), level !== 'ok' && 'font-semibold')}>
              {loading || !item ? '…' : `${num.format(item.used)} / ${num.format(item.limit)}`}
            </span>
          </div>
        )
      })}
      {over && <p className="text-xs text-destructive">{t(`${Q}over_note`)}</p>}
    </div>
  )
}
