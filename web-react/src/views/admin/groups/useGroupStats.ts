import { useQuery } from '@tanstack/react-query'

import { previewGroupNodes } from '@/api/groups'
import { getGroupScopeSettings } from '@/api/scopeSettings'
import type { Group } from '@/api/types'
import { groupKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import type { MatchCounts } from './model'

/** At most this many of the list's per-group reads are in flight at once (each node preview is a server-side match). */
export const MAX_IN_FLIGHT = 4
let active = 0
const waiting: (() => void)[] = []

/** Runs `task` when fewer than MAX_IN_FLIGHT of these reads are running; the rest queue in order. */
async function limited<T>(task: () => Promise<T>): Promise<T> {
  if (active >= MAX_IN_FLIGHT) await new Promise<void>(resolve => waiting.push(resolve))
  active++
  try { return await task() } finally {
    active--
    waiting.shift()?.()
  }
}

// Background decoration of the list: cheap by construction. Kept for minutes, never refetched on window focus,
// and only asked for rows that are drawn (the cells call these), so a page of 25 never becomes 200 requests.
const BACKGROUND = { staleTime: 10 * 60_000, refetchOnWindowFocus: false, retry: false } as const

export const matchKey = (scope: Parameters<typeof groupKeys.all>[0], id: number) => [...groupKeys.all(scope), 'match', id] as const
export const overridesKey = (scope: Parameters<typeof groupKeys.all>[0], id: number) => [...groupKeys.all(scope), 'overrides', id] as const

/**
 * How many nodes and lines a SAVED group's filter reaches, asked of the same read-only endpoint the sheet's live preview
 * uses, so the number cannot disagree with it. undefined until answered (or if it failed): a dash, never a guessed zero.
 * A save re-reads it (the sheet invalidates `matchKey`); the key carries the filter, so a changed filter is a new read.
 */
export function useMatchCount(g: Group): MatchCounts | undefined {
  const scope = useQueryScope()
  const q = useQuery({
    queryKey: [...matchKey(scope, g.id), g.tag_filter] as const,
    queryFn: ({ signal }) => limited(() => previewGroupNodes(g.id, g.tag_filter, signal)),
    ...BACKGROUND,
  })
  const nodes = q.data?.nodes
  if (!nodes) return undefined
  return {
    nodes: nodes.length,
    visibleLines: nodes.reduce((n, x) => n + x.visible_lines, 0),
    totalLines: nodes.reduce((n, x) => n + x.total_lines, 0),
  }
}

/** How many settings the group overrides: the size of the sparse map its scope-settings read returns. undefined until answered. */
export function useOverrideCount(g: Group): number | undefined {
  const scope = useQueryScope()
  const q = useQuery({
    queryKey: overridesKey(scope, g.id),
    queryFn: ({ signal }) => limited(() => getGroupScopeSettings(g.id, signal)),
    ...BACKGROUND,
  })
  return q.data ? Object.keys(q.data.overrides ?? {}).length : undefined
}
