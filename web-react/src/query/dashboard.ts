import { queryOptions, useQuery } from '@tanstack/react-query'
import { dashboardSummary, type DashboardSummary } from '@/api/dashboard'
import { trafficHistory, type TrafficHistoryItem, type TrafficHistoryParams } from '@/api/traffic'
import { dashboardKeys, trafficKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The dashboard's pre-aggregated counters. This read has no fallback: every
 * figure on the page is `summary?.x ?? 0`, so an unhandled failure used to
 * render a zeroed all-clear. Through the cache the failure is a value the page
 * can see and report.
 */
export function dashboardSummaryQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: dashboardKeys.summary(scope),
    queryFn: ({ signal }): Promise<DashboardSummary> => dashboardSummary({ signal }),
    ...freshness(policies.dashboardSummary),
  })
}

export function useDashboardSummary(scope: QueryScope) {
  return useQuery(dashboardSummaryQuery(scope))
}

/** A panel-wide traffic trend window. */
export function trafficTrendQuery(scope: QueryScope, params: TrafficHistoryParams) {
  return queryOptions({
    queryKey: trafficKeys.history(scope, params),
    queryFn: ({ signal }): Promise<TrafficHistoryItem[]> =>
      trafficHistory(params, { signal }).then(res => res.items),
    ...freshness(policies.trafficTrend),
  })
}

export function useTrafficTrend(scope: QueryScope, params: TrafficHistoryParams) {
  return useQuery(trafficTrendQuery(scope, params))
}
