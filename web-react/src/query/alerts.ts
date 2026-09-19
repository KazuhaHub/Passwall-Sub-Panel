import { queryOptions, useQuery } from '@tanstack/react-query'
import { getAlerts, type AlertsResponse } from '@/api/alerts'
import { alertKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The notification feed. Keeps the 60s cadence the bell had before the
 * migration; the library pauses the interval while the tab is hidden, which
 * the hand-rolled setInterval never did.
 */
export function alertsQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: alertKeys.all(scope),
    queryFn: ({ signal }): Promise<AlertsResponse> => getAlerts({ signal }),
    ...freshness(policies.alerts),
  })
}

export function useAlerts(scope: QueryScope) {
  return useQuery(alertsQuery(scope))
}
