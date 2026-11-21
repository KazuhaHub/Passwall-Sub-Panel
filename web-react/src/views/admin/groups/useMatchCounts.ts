import { useMemo } from 'react'
import { useQueries } from '@tanstack/react-query'

import { previewGroupNodes } from '@/api/groups'
import type { Group } from '@/api/types'
import { groupKeys } from '@/query/keys'
import type { QueryScope } from '@/query/session'
import type { MatchCounts } from './model'

/**
 * Past this many groups the list stops asking: one read per row is fine for the handful of groups a panel
 * has, and a reason to stop (the column shows a dash) for a catalogue nobody has.
 */
export const MATCH_COUNT_LIMIT = 100

/**
 * How many nodes and lines each SAVED group's filter reaches, for the list's "Nodes and lines" column.
 *
 * It asks the same read-only, unaudited endpoint the sheet's live preview uses (POST /admin/groups/:id/node-preview
 * with the group's own saved filter), so the number cannot disagree with the preview, and it follows the server's
 * rules (enabled nodes, line visibility, allowlist eligibility) rather than a second copy of them here. A group
 * whose answer is not in yet, or failed, has no entry: the column shows a dash, never a guessed zero. The
 * preview is admin-only on the server, so the caller passes `enabled` false for anyone else.
 */
export function useMatchCounts(scope: QueryScope, groups: readonly Group[], enabled: boolean): ReadonlyMap<number, MatchCounts> {
  const asked = enabled ? groups.slice(0, MATCH_COUNT_LIMIT) : []
  const results = useQueries({
    queries: asked.map(g => ({
      queryKey: [...groupKeys.all(scope), 'match', g.id, g.tag_filter] as const,
      queryFn: ({ signal }: { signal: AbortSignal }) => previewGroupNodes(g.id, g.tag_filter, signal),
      staleTime: 30_000,
      retry: false,
    })),
  })
  // useQueries returns a new array each render; the map is rebuilt only when an answer changes.
  const key = asked.map((g, i) => `${g.id}:${results[i]?.dataUpdatedAt ?? 0}`).join(',')
  return useMemo(() => {
    const out = new Map<number, MatchCounts>()
    results.forEach((r, i) => {
      if (!r.data) return
      out.set(asked[i].id, {
        nodes: r.data.nodes.length,
        visibleLines: r.data.nodes.reduce((n, x) => n + x.visible_lines, 0),
        totalLines: r.data.nodes.reduce((n, x) => n + x.total_lines, 0),
      })
    })
    return out
    // `key` stands for the results' contents.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
}
