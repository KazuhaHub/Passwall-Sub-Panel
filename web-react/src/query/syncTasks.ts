import { queryOptions, useQuery } from '@tanstack/react-query'
import { listSyncTasks, type SyncTaskListParams } from '@/api/syncTasks'
import type { ListResponse, SyncTask } from '@/api/types'
import { syncTaskKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The upstream sync-task queue. Not polled — see the policy note — so the page
 * revalidates on focus and on the operator's explicit Refresh.
 */
export function syncTasksQuery(scope: QueryScope, params: SyncTaskListParams) {
  return queryOptions({
    queryKey: syncTaskKeys.list(scope, params),
    queryFn: ({ signal }): Promise<ListResponse<SyncTask>> => listSyncTasks(params, { signal }),
    ...freshness(policies.syncTasks),
  })
}

export function useSyncTasks(scope: QueryScope, params: SyncTaskListParams) {
  return useQuery(syncTasksQuery(scope, params))
}
