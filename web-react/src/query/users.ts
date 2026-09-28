import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getUser, listUsers, setServiceStatus, type UserListParams } from '@/api/users'
import type { ListResponse, User } from '@/api/types'
import { userKeys } from './keys'
import { freshness, policies } from './policies'
import { invalidateAfterRiskAction } from './riskCenter'
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

/**
 * One account, by id. Silent: its reader (the risk center's lookup) answers a
 * 404 with its own "not found", and a toast beside it would say it twice.
 * Disabled for an id that is not a positive integer, so an unparsed URL never
 * asks for /admin/users/0.
 */
export function userDetailQuery(scope: QueryScope, userId: number) {
  return queryOptions({
    queryKey: userKeys.detail(scope, userId),
    queryFn: ({ signal }): Promise<User> => getUser(userId, { signal, silent: true }),
    enabled: userId > 0,
    ...freshness(policies.userDetail),
  })
}

export function useUserDetail(scope: QueryScope, userId: number) {
  return useQuery(userDetailQuery(scope, userId))
}

export interface SetServiceStatusVars {
  userId: number
  enabled: boolean
  reason?: string
  detail?: string
  /** Resume only while the hold still carries this reason (409 otherwise). */
  expectReason?: string
}

/**
 * Pause or resume the proxy service from the risk center. Every outcome is
 * the caller's to report (the global toast is off), and a service change
 * moves what the risk center shows as much as the Users list — the hold
 * chip, the queue's hold card, the bell — so it refreshes all three.
 */
export function useSetServiceStatus(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: SetServiceStatusVars) => setServiceStatus(v.userId, v.enabled, v.reason, v.detail, {
      expectReason: v.expectReason, skipErrorToast: true,
    }),
    onSettled: () => { void invalidateAfterRiskAction(qc, scope) },
  })
}
