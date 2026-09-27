import { queryOptions, useQuery } from '@tanstack/react-query'
import { listRiskSignals, type RiskUserRow } from '@/api/riskSignals'
import { riskSignalKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * Every account's observe-only risk signals, with its concurrent-location
 * verdict beside them.
 *
 * Deliberately unpolled — see the policy note. A 503 means this build does
 * not compute the signals; that is a deterministic answer, which the query
 * retry policy leaves alone and the tab renders as its own message rather
 * than as an empty table.
 */
export function riskSignalsQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: riskSignalKeys.all(scope),
    queryFn: ({ signal }): Promise<RiskUserRow[]> => listRiskSignals(signal),
    ...freshness(policies.riskSignals),
  })
}

export function useRiskSignals(scope: QueryScope) {
  return useQuery(riskSignalsQuery(scope))
}

/** One account's signals, or null when it has none yet — read with
 *  `?user_id=`, for geoAnomalyForUserQuery's reason. */
export function riskSignalsForUserQuery(scope: QueryScope, userId: number) {
  return queryOptions({
    queryKey: riskSignalKeys.user(scope, userId),
    queryFn: async ({ signal }): Promise<RiskUserRow | null> =>
      (await listRiskSignals(signal, { user_id: userId })).find(r => r.user_id === userId) ?? null,
    ...freshness(policies.riskSignals),
  })
}

export function useRiskSignalsForUser(scope: QueryScope, userId: number) {
  return useQuery(riskSignalsForUserQuery(scope, userId))
}
