import { queryOptions, useQuery } from '@tanstack/react-query'
import { listLocales, type LocaleMeta } from '@/api/locales'
import { localeKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * Uploaded runtime language packs. Disk-backed and written only by an upload or
 * a delete, so it is a dictionary: no polling, and the writes invalidate it.
 */
export function localesQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: localeKeys.list(scope),
    queryFn: ({ signal }): Promise<LocaleMeta[]> => listLocales(signal),
    ...freshness(policies.dictionaries),
  })
}

export function useLocales(scope: QueryScope) {
  return useQuery(localesQuery(scope))
}
