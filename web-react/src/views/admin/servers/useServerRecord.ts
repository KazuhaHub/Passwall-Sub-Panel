import { useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Server } from '@/api/servers'
import { serverKeys } from '@/query/keys'
import { useAllServers } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import type { ServerListResponse } from '@/query/servers'

/**
 * One server, read from the list of every server: the panel has no read of a single record, and the list is a plain
 * database read (never an upstream request), so a page opened cold by its address costs one cheap request.
 * `missing` is true only once a read has succeeded, and none is under way, without the id: a record created a moment ago
 * is in the re-read the create asked for, and must not flash as "no such server" before it arrives.
 */
export function useServerRecord(id: number) {
  const scope = useQueryScope()
  const all = useAllServers(scope)
  const server = all.data?.find(s => s.id === id)
  return { server, query: all, missing: all.isSuccess && !all.isFetching && !server }
}

/**
 * Writes a changed record into every cached list (the paged one the Servers page shows and the one of every server), so
 * the list is right when the person goes back to it without a re-read, and invalidates them so it is re-read anyway.
 */
export function useServerCache() {
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const patch = useCallback((updater: (prev: Server[]) => Server[]) => {
    queryClient.setQueriesData<ServerListResponse | Server[]>({ queryKey: serverKeys.lists(scope) }, prev => {
      if (!prev) return prev
      return Array.isArray(prev) ? updater(prev) : { ...prev, items: updater(prev.items) }
    })
  }, [queryClient, scope])
  const invalidate = useCallback(() => queryClient.invalidateQueries({ queryKey: serverKeys.lists(scope) }), [queryClient, scope])
  return { patch, invalidate }
}
