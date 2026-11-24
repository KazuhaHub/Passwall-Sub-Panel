import { queryOptions, useQuery } from '@tanstack/react-query'
import { getGroup, listAllGroups, listGroups, type GroupListParams } from '@/api/groups'
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

/**
 * One group, by id, for its page. Silent: the page says what a failure means in its own frame. Disabled for an id
 * that is not a positive integer, so an unparsed address never asks for /admin/groups/0.
 */
export function groupDetailQuery(scope: QueryScope, id: number) {
  return queryOptions({
    queryKey: groupKeys.detail(scope, id),
    queryFn: ({ signal }): Promise<Group> => getGroup(id, { signal, silent: true }),
    enabled: id > 0,
    ...freshness(policies.groupDetail),
  })
}
