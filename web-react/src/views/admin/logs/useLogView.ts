import { useTableViewState } from '@/hooks/useTableViewState'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { urlCodecs, useUrlStates } from '@/hooks/useUrlState'
import { legacyPageSize, parseRowId } from './shared'

/**
 * Where a log tab keeps its place. The page, the keyword and the opened record are in the URL, so a
 * filtered list or one record is a link and Back closes the sheet; the page size is the person's own
 * and is remembered once for every log tab (or per `pageKey` for another list built the same way). A tab switch clears the three (see LogsView), so a page
 * number never carries over into a list that is shorter.
 */
export function useLogView(pageKey = 'admin.logs') {
  const [url, setUrl] = useUrlStates({ page: urlCodecs.int(1, { min: 1 }), q: urlCodecs.string() })
  const view = useTableViewState(pageKey, { pageSize: legacyPageSize() })
  const sel = useUrlSelection<number>('row', { parse: parseRowId })
  return {
    page: url.page,
    q: url.q,
    pageSize: view.state.pageSize,
    // A new keyword starts from the first page.
    setQ: (q: string) => setUrl({ q, page: 1 }),
    // A page is a view of the list, not a place: it replaces, so Back leaves the logs.
    setPage: (page: number) => setUrl({ page }),
    setPageSize: (n: number) => { view.setPageSize(n); setUrl({ page: 1 }) },
    setUrl,
    sel,
  }
}

export type LogView = ReturnType<typeof useLogView>
