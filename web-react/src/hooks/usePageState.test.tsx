// @vitest-environment jsdom
import type { PropsWithChildren } from 'react'
import { act, renderHook } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import { usePageState } from './usePageState'

function wrapper(initialEntry = '/') {
  return function Wrapper({ children }: PropsWithChildren) {
    return <MemoryRouter initialEntries={[initialEntry]}>{children}</MemoryRouter>
  }
}

/** Reads the router's own search string — window.location is not updated by MemoryRouter. */
function useSearch() {
  const ps = usePageState({})
  const location = useLocation()
  return { ps, search: location.search }
}

beforeEach(() => localStorage.clear())

describe('usePageState', () => {
  it('derives the request object from the current paging state', () => {
    const { result } = renderHook(() => usePageState({ defaultSortBy: 'id' }), {
      wrapper: wrapper('/?q=tw&sort=id-desc'),
    })
    expect(result.current.request).toEqual({
      page: 1,
      page_size: 25,
      keyword: 'tw',
      sort_by: 'id',
      sort_dir: 'desc',
    })
  })

  it('returns to the first page on resetPage without touching the other params', () => {
    // A filter change must re-query from page 1. Doing it as a separate
    // refresh() effect (the old groupFilter patch) issued a throwaway request
    // for the new filter at the stale page before the page-1 request.
    const { result } = renderHook(() => usePageState({}), { wrapper: wrapper('/?page=4&q=tw') })
    expect(result.current.page).toBe(4)

    act(() => result.current.resetPage())

    expect(result.current.page).toBe(1)
    expect(result.current.keyword).toBe('tw')
  })

  it('keeps the page size preference out of the URL', () => {
    // Page size is a per-browser preference; putting it in the URL would make
    // every shared link carry one admin's row count.
    const { result } = renderHook(useSearch, { wrapper: wrapper('/') })
    act(() => result.current.ps.setPageSize(100))
    expect(result.current.ps.pageSize).toBe(100)
    expect(localStorage.getItem('psp_page_size')).toBe('100')
    expect(result.current.search).not.toContain('page_size')
  })

  it('omits default paging values from the URL and writes non-defaults', () => {
    const { result } = renderHook(useSearch, { wrapper: wrapper('/') })
    expect(result.current.search).toBe('')

    act(() => result.current.ps.setKeyword('alice'))
    // After the keyword: setKeyword deliberately restarts paging, so the page
    // must be set last for this to observe both params.
    act(() => result.current.ps.setPage(3))
    expect(result.current.search).toContain('q=alice')
    expect(result.current.search).toContain('page=3')

    act(() => result.current.ps.setPage(1))
    expect(result.current.search).not.toContain('page=')
  })
})
