import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getRoutes, saveRoutes, type RouteInput } from '@/api/routes'
import { accessControlKeys, routeKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/** While the server has been sent the routes but has not confirmed them, ask again so "running" appears without a reload. */
const PENDING_POLL_MS = 5000

export function routesQuery(scope: QueryScope, nodeId: number) {
  return queryOptions({
    queryKey: routeKeys.list(scope, nodeId),
    queryFn: ({ signal }) => getRoutes(nodeId, { signal }),
    ...freshness(policies.routes),
    refetchInterval: q => (q.state.data?.sent && !q.state.data.running ? PENDING_POLL_MS : false),
  })
}

export const useNodeRoutes = (scope: QueryScope, nodeId: number, enabled = true) =>
  useQuery({ ...routesQuery(scope, nodeId), enabled })

/** A save makes the routes stale, and the lists' "used by" (a list a route names cannot be deleted). */
export function useSaveRoutes(scope: QueryScope, nodeId: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (routes: RouteInput[]) => saveRoutes(nodeId, routes),
    onSuccess: view => { qc.setQueryData(routeKeys.list(scope, nodeId), view) },
    onSettled: () => Promise.all([
      qc.invalidateQueries({ queryKey: routeKeys.list(scope, nodeId) }),
      qc.invalidateQueries({ queryKey: accessControlKeys.lists(scope) }),
    ]),
  })
}
