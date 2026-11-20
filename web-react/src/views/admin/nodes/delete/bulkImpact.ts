import { useQuery } from '@tanstack/react-query'
import { getNodeDeleteImpact, type NodeDeleteImpact } from '@/api/nodes'
import { describeError } from '@/components/feedback/ErrorState'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { allSettledLimited } from '@/utils/promises'

/** One selected node as the bulk dialog sees it after reading its impact. */
export type BulkEntry =
  | { id: number; kind: 'ready'; impact: NodeDeleteImpact }
  | { id: number; kind: 'blocked'; impact: NodeDeleteImpact }
  | { id: number; kind: 'unread'; reason: string }

/** Four reads at a time: a selection can be a few hundred nodes, and each read walks that node's lines and the shared-port routes. */
const READ_CONCURRENCY = 4

/**
 * Reads the delete-impact of every selected node. A node whose impact cannot be read is NOT offered for deletion
 * (a bulk action has no room for "delete without checking"): it is listed as skipped with the reason, as is a node
 * behind shared-port routes.
 */
export function classifyImpacts(ids: readonly number[], results: readonly PromiseSettledResult<NodeDeleteImpact>[]): BulkEntry[] {
  return ids.map((id, i) => {
    const r = results[i]
    if (r.status === 'rejected') return { id, kind: 'unread', reason: describeError(r.reason) ?? '' }
    return { id, kind: r.value.blocked_by_routes.length > 0 ? 'blocked' : 'ready', impact: r.value }
  })
}

export function useBulkImpact(ids: readonly number[]) {
  const scope = useQueryScope()
  return useQuery({
    queryKey: [...nodeKeys.all(scope), 'bulk-delete-impact', ids.join(',')],
    queryFn: async ({ signal }) => classifyImpacts(ids, await allSettledLimited(ids, id => getNodeDeleteImpact(id, signal), READ_CONCURRENCY)),
    staleTime: 0, gcTime: 0, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
  })
}
