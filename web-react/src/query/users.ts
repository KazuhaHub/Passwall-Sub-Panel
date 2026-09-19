import { queryOptions, useQuery } from '@tanstack/react-query'
import { listUsers, type UserListParams } from '@/api/users'
import type { ListResponse, User } from '@/api/types'
import { userKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The user list. `params` must be the complete request — it is both the query
 * key and what is sent — so anything that changes the response (page, sort,
 * keyword, group filter) has to be in it.
 */
export function usersListQuery(scope: QueryScope, params: UserListParams) {
  return queryOptions({
    queryKey: userKeys.list(scope, params),
    queryFn: ({ signal }): Promise<ListResponse<User>> => listUsers(params, signal),
    ...freshness(policies.usersList),
  })
}

export function useUsersList(scope: QueryScope, params: UserListParams) {
  return useQuery(usersListQuery(scope, params))
}
