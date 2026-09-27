import { queryOptions, useQuery } from '@tanstack/react-query'
import { listGeoAnomalies, type GeoAnomaly } from '@/api/geoAnomalies'
import { geoAnomalyKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * Every judged user's concurrent-location verdict.
 *
 * Deliberately unpolled — see the policy note. A 503 from this endpoint means
 * the detector is not wired in this build; that is a deterministic answer, so
 * the query retry policy leaves it alone and the component renders it as its
 * own message rather than as an empty table.
 */
export function geoAnomaliesQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: geoAnomalyKeys.all(scope),
    queryFn: ({ signal }): Promise<GeoAnomaly[]> => listGeoAnomalies(signal),
    ...freshness(policies.geoAnomalies),
  })
}

export function useGeoAnomalies(scope: QueryScope) {
  return useQuery(geoAnomaliesQuery(scope))
}

/**
 * One account's verdict, or null when the detector has none. Read with
 * `?user_id=` into its own entry: the lookup must never load (or wait on) the
 * fleet list to show one row, and a row the server did not send is "no
 * record", not a filtering accident here.
 */
export function geoAnomalyForUserQuery(scope: QueryScope, userId: number) {
  return queryOptions({
    queryKey: geoAnomalyKeys.user(scope, userId),
    queryFn: async ({ signal }): Promise<GeoAnomaly | null> =>
      (await listGeoAnomalies(signal, { user_id: userId })).find(r => r.user_id === userId) ?? null,
    ...freshness(policies.geoAnomalies),
  })
}

export function useGeoAnomalyForUser(scope: QueryScope, userId: number) {
  return useQuery(geoAnomalyForUserQuery(scope, userId))
}
