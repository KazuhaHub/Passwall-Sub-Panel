import { queryOptions, useQuery } from '@tanstack/react-query'
import { listAuthEvents, type AuthEvent } from '@/api/authEvents'
import { authEventKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

export interface AuthEventPage {
  items: AuthEvent[]
  total: number
}

/**
 * One user's recent sign-in events, as shown in the edit dialog. Keyed by user
 * id so switching targets cannot serve one user's history under another's name
 * — the failure mode that makes a security view actively misleading.
 */
export function userActivityQuery(scope: QueryScope, userId: number, pageSize = 8) {
  return queryOptions({
    queryKey: authEventKeys.forUser(scope, userId, pageSize),
    queryFn: ({ signal }): Promise<AuthEventPage> =>
      listAuthEvents({ user_id: userId, page_size: pageSize }, { signal }),
    ...freshness(policies.userActivity),
  })
}

export function useUserActivity(scope: QueryScope, userId: number, pageSize = 8) {
  return useQuery(userActivityQuery(scope, userId, pageSize))
}
