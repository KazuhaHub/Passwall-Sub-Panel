import { queryOptions, useQuery } from '@tanstack/react-query'
import { getMyProfile, type MeProfile } from '@/api/me'
import { getMyUsage, type UsageReport } from '@/api/traffic'
import { meKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/** The caller's own profile. Scoped like every private read, so an operator's
 *  cached profile can never be served to an admin session. */
export function myProfileQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: meKeys.profile(scope),
    queryFn: ({ signal }): Promise<MeProfile> => getMyProfile({ signal }),
    ...freshness(policies.myProfile),
  })
}

export function useMyProfile(scope: QueryScope) {
  return useQuery(myProfileQuery(scope))
}

/** The caller's own usage counters. Auxiliary to the profile — the page works
 *  without it, so a failure here must not take the page down. */
export function myUsageQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: meKeys.usage(scope),
    queryFn: ({ signal }): Promise<UsageReport> => getMyUsage({ signal }),
    ...freshness(policies.myProfile),
  })
}

export function useMyUsage(scope: QueryScope) {
  return useQuery(myUsageQuery(scope))
}
