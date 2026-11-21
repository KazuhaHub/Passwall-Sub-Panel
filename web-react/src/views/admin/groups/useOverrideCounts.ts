import { useMemo } from 'react'
import { useQueries } from '@tanstack/react-query'

import type { Group } from '@/api/types'
import { getGroupScopeSettings } from '@/api/scopeSettings'
import { groupKeys } from '@/query/keys'
import type { QueryScope } from '@/query/session'
import { MATCH_COUNT_LIMIT } from './useMatchCounts'

/**
 * How many settings each group overrides, for the list's "Overrides" column: the size of the sparse
 * `overrides` map the group's scope-settings read returns (the same read the sheet's Overrides tab uses).
 * Admin-only on the server, so the caller passes `enabled` false for anyone else. No entry (not in yet, or
 * failed) means the column shows a dash, never a zero.
 */
export function useOverrideCounts(scope: QueryScope, groups: readonly Group[], enabled: boolean): ReadonlyMap<number, number> {
  const asked = enabled ? groups.slice(0, MATCH_COUNT_LIMIT) : []
  const results = useQueries({
    queries: asked.map(g => ({
      queryKey: [...groupKeys.all(scope), 'overrides', g.id] as const,
      queryFn: ({ signal }: { signal: AbortSignal }) => getGroupScopeSettings(g.id, signal),
      staleTime: 30_000,
      retry: false,
    })),
  })
  const key = asked.map((g, i) => `${g.id}:${results[i]?.dataUpdatedAt ?? 0}`).join(',')
  return useMemo(() => {
    const out = new Map<number, number>()
    results.forEach((r, i) => { if (r.data) out.set(asked[i].id, Object.keys(r.data.overrides ?? {}).length) })
    return out
    // `key` stands for the results' contents.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
}
