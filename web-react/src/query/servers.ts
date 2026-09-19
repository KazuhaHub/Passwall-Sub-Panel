import { queryOptions, useQuery } from '@tanstack/react-query'
import { listServers, type Server, type ServerListParams } from '@/api/servers'
import { serverKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/** Shape the backend returns for the paged server list. */
export interface ServerListResponse {
  items: Server[]
  total: number
  page?: number
  page_size?: number
}

/**
 * The server list — a plain database read.
 *
 * Deliberately read-only and unpolled: the connection/version probe is a
 * separate concern driven by the current page's row-id set, so nothing here
 * may trigger an upstream request. A refresh of this query must stay a
 * refresh of the list.
 */
export function serversListQuery(scope: QueryScope, params: ServerListParams) {
  return queryOptions({
    queryKey: serverKeys.list(scope, params),
    queryFn: ({ signal }): Promise<ServerListResponse> => listServers(params, signal),
    ...freshness(policies.serversList),
  })
}

export function useServersList(scope: QueryScope, params: ServerListParams) {
  return useQuery(serversListQuery(scope, params))
}
