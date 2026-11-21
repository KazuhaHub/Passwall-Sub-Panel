import { useCallback, useMemo } from 'react'
import type { SortingState } from '@tanstack/react-table'
import type { UserListParams } from '@/api/users'
import { useTableViewState } from '@/hooks/useTableViewState'
import { urlCodecs, useUrlStates } from '@/hooks/useUrlState'
import { legacyPageSize } from '../logs/shared'
import { DEFAULT_SORT, SORT_KEYS, parseSort } from './model'

/**
 * Where the Users list keeps its place. The page, the keyword, the order and the group filter are in the URL,
 * so a filtered list is a link and Back behaves; the page size and the hidden columns are the person's own and
 * are remembered (the page size starts from the one the old list stored under `psp_page_size`). The URL keeps
 * the spelling it always had: `?page=2&q=alice&sort=upn-asc`, with the group filter as `?group=<id>`.
 */
export function useUserListView() {
  const [url, setUrl] = useUrlStates({
    page: urlCodecs.int(1, { min: 1 }),
    q: urlCodecs.string(),
    sort: urlCodecs.string(DEFAULT_SORT),
    group: urlCodecs.int(0, { min: 0 }),
  })
  const view = useTableViewState('admin.users', { pageSize: legacyPageSize() })
  const { by, dir } = parseSort(url.sort)

  // The default order (newest first) is not a column the table shows, so it reads as "no column sorted".
  const sorting = useMemo<SortingState>(() => ((SORT_KEYS as readonly string[]).includes(by) && by !== 'id' ? [{ id: by, desc: dir === 'desc' }] : []), [by, dir])
  const setSorting = useCallback((next: SortingState) => {
    const first = next[0]
    setUrl({ sort: first ? `${first.id}-${first.desc ? 'desc' : 'asc'}` : DEFAULT_SORT, page: 1 })
  }, [setUrl])

  // The complete request: it is both the query key and what is sent, so everything that changes the response is in it.
  const params = useMemo<UserListParams>(() => ({
    page: url.page,
    page_size: view.state.pageSize,
    keyword: url.q || undefined,
    sort_by: by,
    sort_dir: dir,
    ...(url.group > 0 ? { group_id: url.group } : {}),
  }), [url.page, view.state.pageSize, url.q, by, dir, url.group])

  return {
    params,
    page: url.page,
    pageSize: view.state.pageSize,
    q: url.q,
    group: url.group,
    sorting,
    columnVisibility: view.state.columnVisibility,
    setColumnVisibility: view.setColumnVisibility,
    setSorting,
    setPage: (page: number) => setUrl({ page }),
    setPageSize: (n: number) => { view.setPageSize(n); setUrl({ page: 1 }) },
    setQ: (q: string) => setUrl({ q, page: 1 }),
    setGroup: (group: number) => setUrl({ group, page: 1 }),
    clearFilters: () => setUrl({ q: '', group: 0, page: 1 }),
    narrowed: url.q !== '' || url.group > 0,
  }
}

export type UserListView = ReturnType<typeof useUserListView>
