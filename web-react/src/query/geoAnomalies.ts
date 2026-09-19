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
