import type { AuthEvent } from '@/api/authEvents'
import { StatusBadge } from '@/components/common/StatusBadge'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { TableSkeleton } from '@/components/feedback/LoadingState'
import { useUserActivity } from '@/query/authEvents'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { formatRegion } from '@/utils/geo'
import { useAdminT } from './users/useAdminT'

/** UserActivity shows a user's recent sign-in events (a per-user slice of the authentication log).
 *  Read-only; reads the latest few auth_events for the user through the shared query cache. It is
 *  also rendered inside the risk center's drawer, so it brings no page
 *  frame or padding of its own. */
export function UserActivity({ userId, showTitle = true }: {
  userId: number
  /** false where the caller already heads the list (the risk center's drawer), so the same words do
   *  not stand twice in a row. */
  showTitle?: boolean
}) {
  const t = useAdminT()
  const panelTz = useSiteStore(s => s.timezone)
  const scope = useQueryScope()
  const { data, isPending, isError, error, refetch } = useUserActivity(scope, userId)
  const items: AuthEvent[] | undefined = data?.items

  return (
    <div className="mt-2 space-y-2">
      {showTitle && <h3 className="text-sm font-medium">{t('users.activity.title')}</h3>}
      {isPending && <TableSkeleton rows={3} cols={2} />}
      {/* A failed read is NOT an empty history. "No sign-in records" is a claim about the account,
          and a security view is exactly where presenting an outage as a clean record does the most
          damage. */}
      {isError && <ErrorState compact title={t('users.activity.unavailable')} reason={error} retry={() => refetch()} />}
      {!isError && items !== undefined && items.length === 0 && <EmptyState compact title={t('users.activity.empty')} />}
      {!isError && items !== undefined && items.length > 0 && (
        <ul className="divide-y rounded-lg border">
          {items.map(r => {
            const where = [r.ip, formatRegion(r.region), r.reason].filter(Boolean).join(' · ')
            return (
              <li key={r.id} className="flex flex-col gap-0.5 px-3 py-2 text-sm">
                <div className="flex items-baseline justify-between gap-2">
                  <div className="flex min-w-0 items-center gap-2">
                    {r.outcome === 'success'
                      ? <StatusBadge tone="success" label={t('users.activity.ok')} />
                      : <StatusBadge tone="danger" label={t('users.activity.fail')} />}
                    <span className="text-xs text-muted-foreground uppercase">{r.method}</span>
                  </div>
                  <span className="text-xs whitespace-nowrap text-muted-foreground tabular-nums">{formatDualTz(r.at, panelTz)}</span>
                </div>
                <div className="truncate text-xs text-muted-foreground" title={where}>{where}</div>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
