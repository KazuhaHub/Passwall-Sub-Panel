import { useQuery } from '@tanstack/react-query'
import { getNodeDeleteImpact } from '@/api/nodes'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'

/**
 * What deleting a node takes with it, read when its dialog opens. It is never served from a cache and never kept
 * (staleTime and gcTime 0, no retry on its own): the answer is a statement about right now that the dialog asks the
 * admin to act on, so a remembered one is worse than none. A failed read stays failed until the admin retries.
 */
export function useNodeDeleteImpact(nodeId: number) {
  const scope = useQueryScope()
  return useQuery({
    queryKey: [...nodeKeys.all(scope), 'delete-impact', nodeId],
    queryFn: ({ signal }) => getNodeDeleteImpact(nodeId, signal),
    staleTime: 0, gcTime: 0, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
  })
}
