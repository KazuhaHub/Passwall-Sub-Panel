import { queryOptions, useQuery } from '@tanstack/react-query'
import {
  listNodes,
  listSeparators,
  listUnmanagedInbounds,
  type NodeListParams,
  type Separator,
} from '@/api/nodes'
import type { ListResponse, Node, UnmanagedInbound } from '@/api/types'
import { nodeKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The node list, used as a picker by the traffic views. A dictionary read: it
 * changes only when nodes are written, so it is never polled and is invalidated
 * by those writes rather than re-read on every filter change.
 */
export function nodesListQuery(scope: QueryScope, params: NodeListParams) {
  return queryOptions({
    queryKey: nodeKeys.list(scope, params),
    queryFn: ({ signal }): Promise<ListResponse<Node>> => listNodes(params, signal),
    ...freshness(policies.dictionaries),
  })
}

export function useNodesList(scope: QueryScope, params: NodeListParams) {
  return useQuery(nodesListQuery(scope, params))
}

/**
 * Separator rows, which interleave into the node table by sort_order. Loaded in
 * full — a fleet has a handful — and a dictionary in nature: written rarely.
 */
export function separatorsQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: nodeKeys.separators(scope),
    queryFn: (): Promise<Separator[]> => listSeparators(),
    ...freshness(policies.dictionaries),
  })
}

export function useSeparators(scope: QueryScope) {
  return useQuery(separatorsQuery(scope))
}

/**
 * Inbounds on a panel that PSP does not manage. Keyed by panel id, so switching
 * the panel is a different query rather than a race between overlapping reads.
 */
export function unmanagedInboundsQuery(scope: QueryScope, panelId: number) {
  return queryOptions({
    queryKey: nodeKeys.unmanaged(scope, panelId),
    queryFn: ({ signal }): Promise<ListResponse<UnmanagedInbound>> =>
      listUnmanagedInbounds(panelId, { signal }),
    ...freshness(policies.nodeUnmanaged),
  })
}

export function useUnmanagedInbounds(scope: QueryScope, panelId: number | null) {
  return useQuery({
    ...unmanagedInboundsQuery(scope, panelId ?? 0),
    enabled: panelId != null,
  })
}
