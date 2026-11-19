import { useCallback, useState } from 'react'
import { PAGE_SIZE_OPTIONS } from '@/components/data/dataTableLogic'

export interface TableSort { id: string; desc: boolean }

/**
 * What a list remembers between visits (plan 3.2): how it is ordered, how many
 * rows a page shows, which optional columns are hidden and which facets are on.
 * The keyword and the page number are NOT here: they belong in the URL, where a
 * shared link should carry them, and a remembered search would greet someone
 * with a list that looks empty.
 */
export interface TableViewState {
  sort: TableSort | null
  pageSize: number
  /** column id -> visible; absent means the column's own default. */
  columnVisibility: Record<string, boolean>
  /** facet id -> chosen values. */
  filters: Record<string, string[]>
}

const VERSION = 1

/** The key carries a version so a changed shape is ignored rather than misread. */
export function tableViewKey(pageKey: string): string {
  return `psp.tableView.v${VERSION}.${pageKey}`
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/**
 * Stored text -> a valid view. Every field is checked on its own and falls back
 * to the default alone, so one bad field (a page size the footer no longer
 * offers, a column id renamed away) never discards the rest of the saved view.
 */
export function readTableView(raw: string | null, defaults: TableViewState): TableViewState {
  if (!raw) return defaults
  let data: unknown
  try { data = JSON.parse(raw) } catch { return defaults }
  if (!isRecord(data) || data.v !== VERSION) return defaults

  const out: TableViewState = { ...defaults }
  const sort = data.sort
  if (isRecord(sort) && typeof sort.id === 'string' && typeof sort.desc === 'boolean') {
    out.sort = { id: sort.id, desc: sort.desc }
  }
  if (typeof data.pageSize === 'number' && (PAGE_SIZE_OPTIONS as readonly number[]).includes(data.pageSize)) {
    out.pageSize = data.pageSize
  }
  if (isRecord(data.columns)) {
    out.columnVisibility = Object.fromEntries(Object.entries(data.columns).filter(([, v]) => typeof v === 'boolean')) as Record<string, boolean>
  }
  if (isRecord(data.filters)) {
    const filters: Record<string, string[]> = {}
    for (const [k, v] of Object.entries(data.filters)) {
      if (Array.isArray(v)) {
        const values = v.filter((x): x is string => typeof x === 'string')
        if (values.length) filters[k] = values
      }
    }
    out.filters = filters
  }
  return out
}

function load(pageKey: string, defaults: TableViewState): TableViewState {
  try { return readTableView(localStorage.getItem(tableViewKey(pageKey)), defaults) } catch { return defaults }
}

function save(pageKey: string, s: TableViewState) {
  try {
    localStorage.setItem(tableViewKey(pageKey), JSON.stringify({
      v: VERSION, sort: s.sort, pageSize: s.pageSize, columns: s.columnVisibility, filters: s.filters,
    }))
  } catch { /* storage may be disabled: the view then lasts for this visit only */ }
}

export interface UseTableViewState {
  state: TableViewState
  setSort: (sort: TableSort | null) => void
  setPageSize: (n: number) => void
  setColumnVisibility: (v: Record<string, boolean>) => void
  setFilters: (f: Record<string, string[]>) => void
  /** Back to the page's defaults, and forget what was stored. */
  reset: () => void
}

export function useTableViewState(pageKey: string, defaults: Partial<TableViewState> = {}): UseTableViewState {
  const [base] = useState<TableViewState>(() => ({
    sort: null, pageSize: 25, columnVisibility: {}, filters: {}, ...defaults,
  }))
  const [state, setState] = useState(() => load(pageKey, base))

  const update = useCallback((patch: Partial<TableViewState>) => {
    setState(prev => {
      const next = { ...prev, ...patch }
      save(pageKey, next)
      return next
    })
  }, [pageKey])

  const reset = useCallback(() => {
    try { localStorage.removeItem(tableViewKey(pageKey)) } catch { /* nothing stored to forget */ }
    setState(base)
  }, [pageKey, base])

  return {
    state,
    setSort: useCallback((sort: TableSort | null) => update({ sort }), [update]),
    setPageSize: useCallback((pageSize: number) => update({ pageSize }), [update]),
    setColumnVisibility: useCallback((columnVisibility: Record<string, boolean>) => update({ columnVisibility }), [update]),
    setFilters: useCallback((filters: Record<string, string[]>) => update({ filters }), [update]),
    reset,
  }
}
