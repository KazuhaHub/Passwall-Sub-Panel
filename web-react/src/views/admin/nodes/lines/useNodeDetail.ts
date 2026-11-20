import { useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getNode } from '@/api/nodes'
import type { Node } from '@/api/types'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import type { QueryScope } from '@/query/session'

/** One node as GET /nodes/:id answers it (its lines are the server's, settled); shared with the node page's own read. */
export const nodeDetailKey = (scope: QueryScope, id: number) => [...nodeKeys.all(scope), 'detail', id] as const

/**
 * The node an editor works on, read fresh whenever one opens: a line's state (listener, probe, CDN issues) is
 * what the server says NOW, and the list's copy may be a poll old. While an editor stays open the draft is the
 * person's; this read only feeds the server-decided facts beside it.
 */
export function useNodeDetail(id: number, enabled = true) {
  const scope = useQueryScope()
  return useQuery({
    queryKey: nodeDetailKey(scope, id),
    queryFn: ({ signal }) => getNode(id, { signal }),
    enabled,
    staleTime: 0,
    refetchOnMount: 'always',
  })
}

/**
 * Puts a node the server just answered into the caches that show it (this node's read and the list's join), and
 * marks the order stale: a line added, removed, enabled or disabled changes its rows and their publication state.
 */
export function useApplyNode() {
  const scope = useQueryScope()
  const client = useQueryClient()
  return useCallback((saved: Node) => {
    client.setQueryData<Awaited<ReturnType<typeof getNode>>>(nodeDetailKey(scope, saved.id), prev => (prev ? { ...prev, node: saved } : { node: saved, clients: [] }))
    client.setQueryData<Node[]>(nodeKeys.allNodes(scope), prev => prev?.map(n => (n.id === saved.id ? saved : n)))
    void client.invalidateQueries({ queryKey: nodeKeys.order(scope) })
  }, [client, scope])
}
