import { useCallback, useEffect, useRef, useState } from 'react'
import { usePageState, type PageRequest, type UsePageStateOptions } from './usePageState'

export type { PageRequest }

/**
 * Page response envelope returned by every paged list endpoint. Mirrors
 * the backend's handler.pagedEnvelope shape so the hook only needs to
 * know one type.
 */
export interface PagedResponse<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export type UsePagedOptions = UsePageStateOptions

export interface UsePagedResult<T> {
  items: T[]
  total: number
  loading: boolean
  error: Error | null
  page: number
  pageSize: number
  keyword: string
  sortBy: string
  sortDir: 'asc' | 'desc'
  setPage: (n: number) => void
  setPageSize: (n: number) => void
  setKeyword: (s: string) => void
  /** Toggles asc→desc→asc for the same column, or switches to a new
   * column with the supplied initial direction (default "asc"). */
  setSort: (col: string, initialDir?: 'asc' | 'desc') => void
  /** Force a re-fetch without changing any param (post-mutation reload). */
  refresh: () => void
  /** Patch the in-memory items list without a network round-trip. Used
   * when a sibling action (e.g. a connectivity probe) returns enriched
   * fields that should appear on the current page immediately. The
   * `total` count is not touched — this is for row-content updates
   * only, not insertions/deletions (use refresh() for those). */
  mutateItems: (updater: (prev: T[]) => T[]) => void
}

/**
 * Paging state plus a hand-rolled fetcher, for list views not yet migrated to
 * the shared query cache. The paging half lives in `usePageState`; this adds
 * only the request/replace cycle.
 *
 * It aborts in-flight requests when params change rapidly (e.g. admin types in
 * the search box) so an older slow response can't overwrite a newer fast one.
 * The fetcher gets the AbortSignal as a second arg.
 *
 * Migrated views should compose `usePageState` with `useQuery` instead — that
 * gives them cancellation, dedupe and invalidation for free.
 */
export function usePaged<T>(
  fetcher: (req: PageRequest, signal: AbortSignal) => Promise<PagedResponse<T>>,
  opts: UsePagedOptions = {},
): UsePagedResult<T> {
  const ps = usePageState(opts)

  const [items, setItems] = useState<T[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  // refreshTick is bumped by refresh() to force a re-fetch without touching
  // any other dep.
  const [refreshTick, setRefreshTick] = useState(0)
  const refresh = useCallback(() => setRefreshTick(t => t + 1), [])

  // Fetch on any dep change. `ps.request` is memoised, so it only changes when
  // one of the five paging values actually does.
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher
  useEffect(() => {
    const ac = new AbortController()
    setLoading(true)
    setError(null)
    fetcherRef.current(ps.request, ac.signal)
      .then(resp => {
        if (ac.signal.aborted) return
        setItems(resp.items ?? [])
        setTotal(resp.total ?? 0)
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        setError(e instanceof Error ? e : new Error(String(e)))
      })
      .finally(() => {
        if (!ac.signal.aborted) setLoading(false)
      })
    return () => ac.abort()
  }, [ps.request, refreshTick])

  const mutateItems = useCallback((updater: (prev: T[]) => T[]) => {
    setItems(prev => updater(prev))
  }, [])

  return {
    items, total, loading, error,
    page: ps.page, pageSize: ps.pageSize, keyword: ps.keyword, sortBy: ps.sortBy, sortDir: ps.sortDir,
    setPage: ps.setPage, setPageSize: ps.setPageSize, setKeyword: ps.setKeyword, setSort: ps.setSort,
    refresh, mutateItems,
  }
}
