import { keepPreviousData, queryOptions, useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import {
  dismissRiskUser, getLiveConnections, getRiskLevels, getRiskQueue, getRiskUser, listConnectionHistory,
  listFlagRecords, refreshLiveConnections, trustRiskUser, undismissRiskUser, untrustRiskUser,
  type ConnectionHistoryParams, type FlagRecordParams, type LiveParams, type QueueParams, type ReviewResult,
  type TrustResult,
} from '@/api/riskCenter'
import { alertKeys, riskCenterKeys, userKeys } from './keys'
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

/**
 * One page of the attention queue. The previous page stays on screen while a
 * new filter or page loads (a table that blanks on every click reads as "no
 * accounts"), and the page re-reads on the bell's cadence while visible.
 */
export function riskQueueQuery(scope: QueryScope, params: QueueParams) {
  return queryOptions({
    queryKey: riskCenterKeys.queue(scope, params),
    queryFn: ({ signal }) => getRiskQueue(params, { signal }),
    placeholderData: keepPreviousData,
    ...freshness(policies.riskCenterQueue),
  })
}

export function useRiskQueue(scope: QueryScope, params: QueueParams) {
  return useQuery(riskQueueQuery(scope, params))
}

/** One account's drawer. Never asks for an id that is not a positive
 *  integer, and asks nothing while the drawer is closed. */
export function riskUserQuery(scope: QueryScope, userId: number, enabled = true) {
  return queryOptions({
    queryKey: riskCenterKeys.user(scope, userId),
    queryFn: ({ signal }) => getRiskUser(userId, { signal }),
    enabled: enabled && userId > 0,
    ...freshness(policies.riskCenterUser),
  })
}

export function useRiskUser(scope: QueryScope, userId: number, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(riskUserQuery(scope, userId, enabled))
}

export function riskLevelsQuery(scope: QueryScope, enabled = true) {
  return queryOptions({
    queryKey: riskCenterKeys.levels(scope),
    queryFn: ({ signal }) => getRiskLevels({ signal }),
    enabled,
    ...freshness(policies.riskCenterLevels),
  })
}

export function useRiskLevels(scope: QueryScope, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(riskLevelsQuery(scope, enabled))
}

/**
 * What every risk action changes, refreshed after it settles — on success
 * AND on a refusal, since a 409 means this view was stale: the risk center
 * (queue pages, summaries, levels, records, the live view), the users (the
 * list and the edit dialog's snapshot read from it) and the bell's count.
 */
export function invalidateAfterRiskAction(qc: QueryClient, scope: QueryScope) {
  return Promise.all([
    qc.invalidateQueries({ queryKey: riskCenterKeys.all(scope) }),
    qc.invalidateQueries({ queryKey: userKeys.all(scope) }),
    qc.invalidateQueries({ queryKey: alertKeys.all(scope) }),
  ])
}

export type ReviewAction =
  | { kind: 'dismiss'; userId: number; note?: string; expected?: Record<string, string> }
  | { kind: 'undismiss'; userId: number }
  | { kind: 'trust'; userId: number; resume: boolean }
  | { kind: 'untrust'; userId: number }

function runReview(a: ReviewAction): Promise<ReviewResult | TrustResult> {
  switch (a.kind) {
    case 'dismiss': return dismissRiskUser(a.userId, { note: a.note, expected: a.expected })
    case 'undismiss': return undismissRiskUser(a.userId)
    case 'trust': return trustRiskUser(a.userId, a.resume)
    case 'untrust': return untrustRiskUser(a.userId)
  }
}

/** The four review actions as one mutation: they share their refusals, their
 *  reporting and what they invalidate. */
export function useReviewAction(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: runReview,
    onSettled: () => { void invalidateAfterRiskAction(qc, scope) },
  })
}
