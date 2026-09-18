import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router'

/**
 * The request object a paged fetcher turns into a query string
 * (`?page=1&page_size=25&keyword=...&sort_by=...&sort_dir=...`).
 */
export interface PageRequest {
  page: number
  page_size: number
  keyword: string
  sort_by: string
  sort_dir: 'asc' | 'desc'
}

export interface UsePageStateOptions {
  /** Initial page size. Falls back to localStorage (psp_page_size) then 25. */
  defaultPageSize?: number
  /** Initial sort_by. Empty string defers to the resource's own default order. */
  defaultSortBy?: string
  /** Initial sort_dir; defaults to "desc" (newest-first is the common pattern). */
  defaultSortDir?: 'asc' | 'desc'
  /** URL param namespace, for two paged tables sharing one page. */
  paramPrefix?: string
}

export interface UsePageStateResult {
  page: number
  pageSize: number
  keyword: string
  sortBy: string
  sortDir: 'asc' | 'desc'
  /** Derived view of the five values above, for building a query key + request. */
  request: PageRequest
  setPage: (n: number) => void
  setPageSize: (n: number) => void
  setKeyword: (s: string) => void
  /** Toggles asc→desc→asc for the same column, or switches column with `initialDir`. */
  setSort: (col: string, initialDir?: 'asc' | 'desc') => void
  /**
   * Return to the first page. Call this in the same update that changes an
   * out-of-band filter, so the new filter is queried from page 1 — rather than
   * firing a throwaway request at the stale page and correcting afterwards.
   */
  resetPage: () => void
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
 * Owns the paging/filter/sort state for one list view: it syncs everything but
 * page_size to the URL (page_size is a per-browser preference in localStorage,
 * so a shared link doesn't carry one admin's row count) and re-derives that
 * state when the URL changes underneath it.
 *
 * This is the state half only — it performs no fetching. `usePaged` layers a
 * hand-rolled fetcher on top for callers not yet migrated to a query cache.
 */
export function usePageState(opts: UsePageStateOptions = {}): UsePageStateResult {
  const [params, setParams] = useSearchParams()
  const prefix = opts.paramPrefix ?? ''
  const keyOf = useCallback((k: string) => (prefix ? `${prefix}_${k}` : k), [prefix])

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

  // URL sync. Default values (page=1, no keyword, no sort) stay omitted so URLs
  // stay short and bookmarks for the "default view" aren't polluted.
  useEffect(() => {
    setParams(prev => {
      const next = new URLSearchParams(prev)
      if (page === 1) next.delete(keyOf('page')); else next.set(keyOf('page'), String(page))
      if (!keyword) next.delete(keyOf('q')); else next.set(keyOf('q'), keyword)
      if (!sortBy) next.delete(keyOf('sort')); else next.set(keyOf('sort'), `${sortBy}-${sortDir}`)
      return next
    }, { replace: true })
  }, [page, keyword, sortBy, sortDir, keyOf, setParams])

  // Reverse URL → state sync. Without this the browser Back/Forward moved the
  // address bar without updating the table (admin sees ?q=tw but no filter
  // applied). setState bails out when the value is unchanged, so the outgoing
  // effect above cannot loop with this one.
  useEffect(() => {
    const urlPage = Math.max(1, parseInt(params.get(keyOf('page')) || '1', 10) || 1)
    const urlKeyword = params.get(keyOf('q')) || ''
    const sortRawNow = params.get(keyOf('sort')) || ''
    const sortPartsNow = sortRawNow.split('-')
    const urlSortBy = sortRawNow ? sortPartsNow[0] : (opts.defaultSortBy ?? '')
    const urlSortDir: 'asc' | 'desc' = sortRawNow
      ? (sortPartsNow[1] === 'asc' ? 'asc' : 'desc')
      : (opts.defaultSortDir ?? 'desc')
    setPageState(urlPage)
    setKeywordState(urlKeyword)
    setSortBy(urlSortBy)
    setSortDir(urlSortDir)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params, keyOf])

  const setPage = useCallback((n: number) => setPageState(Math.max(1, n)), [])
  const resetPage = useCallback(() => setPageState(1), [])
  const setPageSize = useCallback((n: number) => {
    setPageSizeState(n)
    writeStoredPageSize(n)
    // Reset to page 1: shrinking from 100/page to 25/page while on page 5 puts
    // admin past the new last page, and the table would show empty.
    setPageState(1)
  }, [])
  const setKeyword = useCallback((s: string) => {
    setKeywordState(s)
    setPageState(1) // a changed filter set should restart paging
  }, [])
  const setSort = useCallback((col: string, initialDir: 'asc' | 'desc' = 'asc') => {
    if (sortBy === col) setSortDir(d => (d === 'asc' ? 'desc' : 'asc'))
    else { setSortBy(col); setSortDir(initialDir) }
    setPageState(1)
  }, [sortBy])

  const request = useMemo<PageRequest>(
    () => ({ page, page_size: pageSize, keyword, sort_by: sortBy, sort_dir: sortDir }),
    [page, pageSize, keyword, sortBy, sortDir],
  )

  return { page, pageSize, keyword, sortBy, sortDir, request, setPage, setPageSize, setKeyword, setSort, resetPage }
}
