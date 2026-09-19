import { queryOptions, useQuery } from '@tanstack/react-query'
import { listGroups, type GroupListParams } from '@/api/groups'
import type { Group, ListResponse } from '@/api/types'
import { groupKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * User groups. A dictionary read — written rarely, consumed by several views —
 * so it is never polled; a write invalidates it instead.
 */
export function groupsListQuery(scope: QueryScope, params: GroupListParams = {}) {
  return queryOptions({
    queryKey: groupKeys.list(scope, params),
    queryFn: ({ signal }): Promise<ListResponse<Group>> => listGroups(params, signal),
    ...freshness(policies.dictionaries),
  })
}

export function useGroupsList(scope: QueryScope, params: GroupListParams = {}) {
  return useQuery(groupsListQuery(scope, params))
}
