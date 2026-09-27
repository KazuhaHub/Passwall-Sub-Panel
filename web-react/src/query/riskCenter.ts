import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  getLiveConnections, listConnectionHistory, listFlagRecords, refreshLiveConnections,
  type ConnectionHistoryParams, type FlagRecordParams, type LiveParams,
} from '@/api/riskCenter'
import { riskCenterKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * One page of the live view. `params` is both the key and the request, so a
 * filter or page change is its own entry. A 503 means the risk center is not
 * wired in this build: a deterministic answer the retry policy leaves alone.
 */
export function liveConnectionsQuery(scope: QueryScope, params: LiveParams) {
  return queryOptions({
    queryKey: riskCenterKeys.live(scope, params),
    queryFn: ({ signal }) => getLiveConnections(params, { signal }),
    ...freshness(policies.riskCenterLive),
  })
}

export function useLiveConnections(scope: QueryScope, params: LiveParams) {
  return useQuery(liveConnectionsQuery(scope, params))
}

/**
 * "Refresh now" as a MUTATION, not a refetch. A refetch re-reads the stored
 * snapshot; this asks every panel again and replaces it, rationed for the
 * whole fleet by the server. Settled either way, every live page is
 * invalidated: a refused refresh may still sit behind a newer poll.
 */
export function useRefreshLiveConnections(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: refreshLiveConnections,
    onSettled: () => qc.invalidateQueries({ queryKey: riskCenterKeys.lives(scope) }),
  })
}

export function connectionHistoryQuery(scope: QueryScope, params: ConnectionHistoryParams) {
  return queryOptions({
    queryKey: riskCenterKeys.history(scope, params),
    queryFn: ({ signal }) => listConnectionHistory(params, { signal }),
    ...freshness(policies.riskCenterHistory),
  })
}

export function useConnectionHistory(scope: QueryScope, params: ConnectionHistoryParams) {
  return useQuery(connectionHistoryQuery(scope, params))
}

export function flagRecordsQuery(scope: QueryScope, params: FlagRecordParams) {
  return queryOptions({
    queryKey: riskCenterKeys.flags(scope, params),
    queryFn: ({ signal }) => listFlagRecords(params, { signal }),
    ...freshness(policies.riskCenterFlags),
  })
}

export function useFlagRecords(scope: QueryScope, params: FlagRecordParams) {
  return useQuery(flagRecordsQuery(scope, params))
}
