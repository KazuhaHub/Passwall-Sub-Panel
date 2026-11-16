import { queryOptions, useQuery } from '@tanstack/react-query'
import { getAdminLegal, getAffectedUsers, type AdminLegal, type AffectedUsers, type LegalKind } from '@/api/legal'
import { legalKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/** One kind's newest version per language, its history and the generated list. */
export function adminLegalQuery(scope: QueryScope, kind: LegalKind) {
  return queryOptions({
    queryKey: legalKeys.admin(scope, kind),
    queryFn: ({ signal }): Promise<AdminLegal> => getAdminLegal(kind, { signal }),
    ...freshness(policies.legalAdmin),
  })
}

export function useAdminLegal(scope: QueryScope, kind: LegalKind) {
  return useQuery(adminLegalQuery(scope, kind))
}

/** Always re-read when asked for: the number is about to be shown beside a button. */
export function useAffectedUsers(scope: QueryScope, enabled: boolean) {
  return useQuery({
    queryKey: legalKeys.affected(scope),
    queryFn: ({ signal }): Promise<AffectedUsers> => getAffectedUsers({ signal }),
    ...freshness(policies.legalAdmin),
    staleTime: 0,
    enabled,
  })
}
