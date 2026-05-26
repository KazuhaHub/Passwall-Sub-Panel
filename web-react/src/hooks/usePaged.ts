import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

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

/**
 * The request object the hook hands to the fetcher. Fetchers should
 * turn it into a query string (`?page=1&page_size=25&keyword=...&sort_by=...&sort_dir=...`)
 * and call the backend.
 */
export interface PageRequest {
  page: number
  page_size: number
  keyword: string
  sort_by: string
  sort_dir: 'asc' | 'desc'
}

export interface UsePagedOptions {
  /** Initial page size. Falls back to localStorage (psp_page_size) and
   * then to 25. */
  defaultPageSize?: number
  /** Initial sort_by column. Empty string defers to the backend's
   * resource-specific default order. */
  defaultSortBy?: string
  /** Initial sort_dir; defaults to "desc" since newest-first is the
   * common pattern for log-like tables. */
  defaultSortDir?: 'asc' | 'desc'
  /** Page-size options offered to the user. Defaults to [10, 25, 50, 100]. */
  pageSizeOptions?: number[]
  /** URL param namespace prefix. Useful when two paged tables share a
   * page (e.g. tabs) — prefix one with "tab1_" so their state doesn't
   * collide. Empty default keeps URLs short for the typical single-table
   * page. */
  paramPrefix?: string
}

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

const STORAGE_KEY = 'psp_page_size'

function readStoredPageSize(): number | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const n = parseInt(raw, 10)
    if (!Number.isFinite(n) || n < 1) return null
    return n
  } catch {
    return null
  }
}

function writeStoredPageSize(n: number) {
  try { localStorage.setItem(STORAGE_KEY, String(n)) } catch { /* localStorage may be disabled */ }
}

/**
 * usePaged manages page / page_size / keyword / sort state for one
 * paginated list view, syncs everything but page_size to the URL
 * (page_size lives in localStorage so admin's "show 100" preference
 * carries across every list), and re-fetches whenever any of those
 * change.
 *
 * The fetcher receives a fully-formed PageRequest and returns a
 * PagedResponse. Caller's responsibility to map the PageRequest into a
 * query string for its own API client.
 *
 * Aborts in-flight requests when params change rapidly (e.g. admin types
 * in the search box) so an older slow response can't overwrite a newer
 * fast one. The fetcher gets the AbortSignal as a second arg.
 */
export function usePaged<T>(
  fetcher: (req: PageRequest, signal: AbortSignal) => Promise<PagedResponse<T>>,
  opts: UsePagedOptions = {},
): UsePagedResult<T> {
  const [params, setParams] = useSearchParams()
  const prefix = opts.paramPrefix ?? ''
  const keyOf = useCallback((k: string) => prefix ? `${prefix}_${k}` : k, [prefix])

  // Initial values: URL > localStorage (page_size only) > defaults.
  const initialPage = Math.max(1, parseInt(params.get(keyOf('page')) || '1', 10) || 1)
  const initialKeyword = params.get(keyOf('q')) || ''
  const sortRaw = params.get(keyOf('sort')) || ''
  const sortParts = sortRaw.split('-')
  const initialSortBy = sortRaw ? sortParts[0] : (opts.defaultSortBy ?? '')
  const initialSortDir: 'asc' | 'desc' = sortRaw
    ? (sortParts[1] === 'asc' ? 'asc' : 'desc')
    : (opts.defaultSortDir ?? 'desc')
  const storedSize = readStoredPageSize()
  const initialPageSize = storedSize ?? opts.defaultPageSize ?? 25

  const [page, setPageState] = useState(initialPage)
  const [pageSize, setPageSizeState] = useState(initialPageSize)
  const [keyword, setKeywordState] = useState(initialKeyword)
  const [sortBy, setSortBy] = useState(initialSortBy)
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>(initialSortDir)

  const [items, setItems] = useState<T[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  // refreshTick is bumped by refresh() to force a re-fetch without
  // touching any other dep.
  const [refreshTick, setRefreshTick] = useState(0)
  const refresh = useCallback(() => setRefreshTick(t => t + 1), [])

  // URL sync. Default values (page=1, no keyword, no sort) stay omitted
  // so URLs stay short and bookmarks for the "default view" don't get
  // polluted with redundant query params.
  useEffect(() => {
    setParams(prev => {
      const next = new URLSearchParams(prev)
      if (page === 1) next.delete(keyOf('page')); else next.set(keyOf('page'), String(page))
      if (!keyword) next.delete(keyOf('q')); else next.set(keyOf('q'), keyword)
      if (!sortBy) next.delete(keyOf('sort')); else next.set(keyOf('sort'), `${sortBy}-${sortDir}`)
      return next
    }, { replace: true })
  }, [page, keyword, sortBy, sortDir, keyOf, setParams])

  // Fetch on any dep change. AbortController cancels in-flight requests
  // so a slow earlier response can't clobber a fast later one.
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher
  useEffect(() => {
    const ac = new AbortController()
    setLoading(true)
    setError(null)
    fetcherRef.current(
      { page, page_size: pageSize, keyword, sort_by: sortBy, sort_dir: sortDir },
      ac.signal,
    )
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
  }, [page, pageSize, keyword, sortBy, sortDir, refreshTick])

  const setPage = useCallback((n: number) => setPageState(Math.max(1, n)), [])
  const setPageSize = useCallback((n: number) => {
    setPageSizeState(n)
    writeStoredPageSize(n)
    // Reset to page 1 — staying on page 5 when shrinking from 100/page
    // to 25/page silently puts admin past the new last_page; the table
    // would then show empty.
    setPageState(1)
  }, [])
  const setKeyword = useCallback((s: string) => {
    setKeywordState(s)
    setPageState(1) // changing the filter set should restart paging
  }, [])
  const setSort = useCallback((col: string, initialDir: 'asc' | 'desc' = 'asc') => {
    setSortBy(prev => {
      if (prev === col) {
        // Same column — toggle direction.
        setSortDir(d => d === 'asc' ? 'desc' : 'asc')
        return prev
      }
      // New column — set the supplied initial direction.
      setSortDir(initialDir)
      return col
    })
    setPageState(1)
  }, [])

  const mutateItems = useCallback((updater: (prev: T[]) => T[]) => {
    setItems(prev => updater(prev))
  }, [])

  return {
    items, total, loading, error,
    page, pageSize, keyword, sortBy, sortDir,
    setPage, setPageSize, setKeyword, setSort, refresh, mutateItems,
  }
}
