import { queryOptions, useQuery } from '@tanstack/react-query'
import {
  getUserNodeUsage,
  getUserServerUsage,
  nodeTrafficHistory,
  topNodes,
  topTraffic,
  trafficHistory,
  userTrafficHistory,
  type NodeTrafficRow,
  type TrafficHistoryParams,
  type TrafficHistoryResponse,
  type TrafficRow,
  type UserNodeUsageRow,
  type UserServerUsageRow,
} from '@/api/traffic'
import { trafficKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The global Top-N usage leaderboard.
 *
 * This is an expensive read — the backend walks every user to build it — so it
 * is deliberately not tied to the list refresh: it loads per `limit` and is
 * invalidated only by writes that actually change usage. Errors stay silent
 * because this only feeds a best-effort column; a blip here must not raise a
 * toast over a page whose primary data loaded fine.
 */
export function topTrafficQuery(scope: QueryScope, limit: number) {
  return queryOptions({
    queryKey: trafficKeys.top(scope, limit),
    queryFn: ({ signal }): Promise<TrafficRow[]> => topTraffic(limit, { silent: true, signal }),
    ...freshness(policies.topTraffic),
  })
}

export function useTopTraffic(scope: QueryScope, limit: number) {
  return useQuery(topTrafficQuery(scope, limit))
}

/** One user's per-node usage breakdown. Keyed by user id so switching the
 *  selected user cannot show one user's nodes under another's heading. */
export function userNodeUsageQuery(scope: QueryScope, userId: number) {
  return queryOptions({
    queryKey: trafficKeys.userNodes(scope, userId),
    queryFn: ({ signal }): Promise<UserNodeUsageRow[]> => getUserNodeUsage(userId, { signal }),
    ...freshness(policies.userBreakdown),
  })
}

export function useUserNodeUsage(scope: QueryScope, userId: number) {
  return useQuery(userNodeUsageQuery(scope, userId))
}

/** One user's per-server usage breakdown. */
export function userServerUsageQuery(scope: QueryScope, userId: number) {
  return queryOptions({
    queryKey: trafficKeys.userServers(scope, userId),
    queryFn: ({ signal }): Promise<UserServerUsageRow[]> => getUserServerUsage(userId, { signal }),
    ...freshness(policies.userBreakdown),
  })
}

export function useUserServerUsage(scope: QueryScope, userId: number) {
  return useQuery(userServerUsageQuery(scope, userId))
}

/** The node-scoped rank leaderboard. */
export function topNodesQuery(scope: QueryScope, limit: number) {
  return queryOptions({
    queryKey: trafficKeys.topNodes(scope, limit),
    queryFn: ({ signal }): Promise<NodeTrafficRow[]> => topNodes(limit, { signal }),
    ...freshness(policies.topTraffic),
  })
}

export function useTopNodes(scope: QueryScope, limit: number) {
  return useQuery(topNodesQuery(scope, limit))
}

/**
 * Which series a history window is drawn from. Modelled explicitly so the
 * panel-wide, per-user and per-node answers cannot share a cache entry — they
 * come from three different endpoints and mean different things.
 */
export type HistoryTarget =
  | { kind: 'panel' }
  | { kind: 'user'; userId: number }
  | { kind: 'node'; nodeId: number }

export function historyTargetQuery(scope: QueryScope, target: HistoryTarget, params: TrafficHistoryParams) {
  return queryOptions({
    queryKey: [...trafficKeys.history(scope, params), target] as const,
    // The whole envelope, not just `items`: the range display reads the
    // server-resolved since/until back off it.
    queryFn: ({ signal }): Promise<TrafficHistoryResponse> => {
      if (target.kind === 'node') {
        // nodeId 0 means "every node", which the endpoint expresses by omitting it.
        const p = target.nodeId > 0 ? { ...params, node_id: target.nodeId } : params
        return nodeTrafficHistory(p, { signal })
      }
      if (target.kind === 'user') {
        return userTrafficHistory(target.userId, params, { signal })
      }
      return trafficHistory(params, { signal })
    },
    ...freshness(policies.trafficTrend),
  })
}

export function useHistoryTarget(scope: QueryScope, target: HistoryTarget, params: TrafficHistoryParams) {
  return useQuery(historyTargetQuery(scope, target, params))
}
