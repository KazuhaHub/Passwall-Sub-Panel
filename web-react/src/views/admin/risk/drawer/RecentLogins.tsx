import type { AuthEvent } from '@/api/authEvents'
import { StatusDot } from '@/components/common/StatusDot'
import { Button } from '@/components/ui/button'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { useUserActivity } from '@/query/authEvents'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { formatRegion } from '@/utils/geo'
import { useRiskT } from '../useRiskT'

/**
 * The account's latest panel sign-ins (a per-user slice of the authentication log), read-only. The risk
 * center's own copy of the list the Users page's edit dialog shows: the same read through the shared
 * query cache and the same words, drawn with the new components.
 *
 * A failed read is NOT an empty history. "No sign-in records" is a claim about the account, and a
 * security view is exactly where presenting an outage as a clean record does the most damage.
 */
export default function RecentLogins({ userId }: { userId: number }) {
  const { t } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  const scope = useQueryScope()
  const { data, isPending, isError, refetch } = useUserActivity(scope, userId)
  const items: AuthEvent[] | undefined = data?.items
  if (isPending) return <CardSkeleton lines={2} />
  if (isError) {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <p className="text-xs text-destructive">{t('admin:users.activity.unavailable')}</p>
        <Button variant="outline" size="sm" onClick={() => void refetch()}>{t('admin:risk_center.retry')}</Button>
      </div>
    )
  }
  if (!items || items.length === 0) return <p className="text-xs text-muted-foreground">{t('admin:users.activity.empty')}</p>
  return (
    <ul className="flex flex-col divide-y text-xs">
      {items.map(r => {
        const place = formatRegion(r.region)
        return (
          <li key={r.id} className="flex flex-col gap-0.5 py-1.5">
            <div className="flex items-baseline justify-between gap-2">
              <span className="flex min-w-0 items-baseline gap-2">
                <StatusDot tone={r.outcome === 'success' ? 'success' : 'danger'}
                  label={r.outcome === 'success' ? t('admin:users.activity.ok') : t('admin:users.activity.fail')} className="font-medium" />
                <span className="text-[11px] text-muted-foreground uppercase">{r.method}</span>
              </span>
              <span className="text-[11px] whitespace-nowrap text-muted-foreground">{formatDualTz(r.at, panelTz)}</span>
            </div>
            <span className="break-all text-muted-foreground">
              {r.ip}{place ? ` · ${place}` : ''}{r.reason ? ` · ${r.reason}` : ''}
            </span>
          </li>
        )
      })}
    </ul>
  )
}
