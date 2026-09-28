import { queryOptions, useQuery } from '@tanstack/react-query'
import { listAllGroups, listGroups, type GroupListParams } from '@/api/groups'
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

/**
 * The whole group catalogue, every page: for a picker that must offer group
 * 201+, where the list above is one page the backend clamps to 200.
 *
 * Read again on every mount, cached or not: the groups page patches and
 * refetches its own list after a write, never this key, so a group made
 * there would otherwise be missing from the picker its own link opens. The
 * cached copy shows meanwhile.
 */
export function allGroupsQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: groupKeys.catalogue(scope),
    queryFn: ({ signal }): Promise<Group[]> => listAllGroups(signal),
    ...freshness(policies.dictionaries),
    refetchOnMount: 'always',
  })
}

export function useAllGroups(scope: QueryScope) {
  return useQuery(allGroupsQuery(scope))
}
