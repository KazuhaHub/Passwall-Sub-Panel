import { queryOptions, useQuery } from '@tanstack/react-query'
import { getDiagnostics, type DiagnosticsSnapshot } from '@/api/diagnostics'
import { diagnosticsKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The metrics registry as the diagnostics page reads it. Polled once a minute
 * while the page is visible (see the policy note), so a problem's growth since
 * the page opened is measured against readings nobody had to ask for; the
 * page's Refresh is a refetch of this same entry, and a reset invalidates it.
 */
export function diagnosticsQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: diagnosticsKeys.metrics(scope),
    queryFn: ({ signal }): Promise<DiagnosticsSnapshot> => getDiagnostics({ signal }),
    ...freshness(policies.diagnostics),
  })
}

export function useDiagnostics(scope: QueryScope) {
  return useQuery(diagnosticsQuery(scope))
}
