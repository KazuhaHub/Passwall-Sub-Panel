// @vitest-environment jsdom

import type { PropsWithChildren } from 'react'
import { act, renderHook, waitFor } from '@testing-library/react'
import { MemoryRouter, useNavigate, type NavigateFunction } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { usePaged, type PagedResponse } from './usePaged'

interface Row { id: number; name: string }

function routerWrapper(initialEntry = '/') {
  let navigate: NavigateFunction | undefined
  function CaptureNavigate() {
    navigate = useNavigate()
    return null
  }
  function Wrapper({ children }: PropsWithChildren) {
    return (
      <MemoryRouter initialEntries={[initialEntry]}>
        <CaptureNavigate />
        {children}
      </MemoryRouter>
    )
  }
  return { Wrapper, navigate: () => navigate }
}

const response = (items: Row[], total = items.length): PagedResponse<Row> => ({
  items,
  total,
  page: 1,
  page_size: 25,
})

beforeEach(() => {
  localStorage.clear()
})

describe('usePaged', () => {
  it('initializes from URL and the shared page-size preference', async () => {
    localStorage.setItem('psp_page_size', '50')
    const fetcher = vi.fn().mockResolvedValue(response([{ id: 1, name: 'first' }], 12))
    const { Wrapper } = routerWrapper('/?page=3&q=alice&sort=name-asc')
    const { result } = renderHook(() => usePaged<Row>(fetcher), { wrapper: Wrapper })

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current).toMatchObject({
      page: 3,
      pageSize: 50,
      keyword: 'alice',
      sortBy: 'name',
      sortDir: 'asc',
      total: 12,
      items: [{ id: 1, name: 'first' }],
    })
    expect(fetcher).toHaveBeenLastCalledWith({
      page: 3,
      page_size: 50,
      keyword: 'alice',
      sort_by: 'name',
      sort_dir: 'asc',
    }, expect.any(AbortSignal))
  })

  it('resets paging for page-size, keyword, and sort changes', async () => {
    const fetcher = vi.fn().mockResolvedValue(response([]))
    const { Wrapper } = routerWrapper('/?page=5')
    const { result } = renderHook(() => usePaged<Row>(fetcher), { wrapper: Wrapper })
    await waitFor(() => expect(result.current.loading).toBe(false))

    act(() => result.current.setPageSize(10))
    await waitFor(() => expect(result.current.page).toBe(1))
    expect(result.current.pageSize).toBe(10)
    expect(localStorage.getItem('psp_page_size')).toBe('10')

    act(() => {
      result.current.setPage(4)
      result.current.setKeyword('new filter')
    })
    expect(result.current.page).toBe(1)
    expect(result.current.keyword).toBe('new filter')

    act(() => result.current.setSort('created_at', 'desc'))
    expect(result.current).toMatchObject({ page: 1, sortBy: 'created_at', sortDir: 'desc' })
    act(() => result.current.setSort('created_at'))
    expect(result.current.sortDir).toBe('asc')
  })

  it('aborts stale requests and prevents their response from clobbering new data', async () => {
    const pending: Array<{
      signal: AbortSignal
      resolve: (value: PagedResponse<Row>) => void
    }> = []
    const fetcher = vi.fn((_req, signal: AbortSignal) => new Promise<PagedResponse<Row>>(resolve => {
      pending.push({ signal, resolve })
    }))
    const { Wrapper } = routerWrapper()
    const { result } = renderHook(() => usePaged<Row>(fetcher), { wrapper: Wrapper })
    await waitFor(() => expect(pending).toHaveLength(1))

    act(() => result.current.setKeyword('new'))
    await waitFor(() => expect(pending).toHaveLength(2))
    expect(pending[0].signal.aborted).toBe(true)

    await act(async () => pending[1].resolve(response([{ id: 2, name: 'new' }])))
    await waitFor(() => expect(result.current.items).toEqual([{ id: 2, name: 'new' }]))
    await act(async () => pending[0].resolve(response([{ id: 1, name: 'stale' }])))
    expect(result.current.items).toEqual([{ id: 2, name: 'new' }])
  })

  it('surfaces failures and refreshes without changing parameters', async () => {
    const fetcher = vi.fn()
      .mockRejectedValueOnce('offline')
      .mockResolvedValueOnce(response([{ id: 3, name: 'recovered' }]))
    const { Wrapper } = routerWrapper()
    const { result } = renderHook(() => usePaged<Row>(fetcher), { wrapper: Wrapper })

    await waitFor(() => expect(result.current.error?.message).toBe('offline'))
    act(() => result.current.refresh())
    await waitFor(() => expect(result.current.items).toEqual([{ id: 3, name: 'recovered' }]))
    expect(result.current.error).toBeNull()
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('follows browser-history URL changes and supports local row mutation', async () => {
    const fetcher = vi.fn().mockResolvedValue(response([{ id: 1, name: 'one' }]))
    const router = routerWrapper('/?page=2')
    const { result } = renderHook(() => usePaged<Row>(fetcher), { wrapper: router.Wrapper })
    await waitFor(() => expect(result.current.loading).toBe(false))

    act(() => router.navigate()?.('/?page=4&q=back&sort=id-desc'))
    await waitFor(() => expect(result.current).toMatchObject({
      page: 4, keyword: 'back', sortBy: 'id', sortDir: 'desc',
    }))

    act(() => result.current.mutateItems(rows => rows.map(row => ({ ...row, name: 'updated' }))))
    expect(result.current.items).toEqual([{ id: 1, name: 'updated' }])
  })
})
