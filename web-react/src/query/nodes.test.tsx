// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { queryWrapper } from '@/test/queryTestUtils'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { sessionScope } from './session'
import { useAllNodes } from './nodes'

const api = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 1 })

describe('useAllNodes', () => {
  // The server caps a page at 200. The old list asked for 500 and silently got 200, so every node above that was
  // missing from search, drag and the join (design gap G1); every page must be read.
  it('reads every page of a fleet larger than one page', async () => {
    const all = Array.from({ length: 450 }, (_, i) => ({ id: i + 1 }))
    api.get.mockImplementation(async (_url: string, opts: { params: { page: number; page_size: number } }) => {
      const { page, page_size } = opts.params
      return { data: { items: all.slice((page - 1) * page_size, page * page_size), total: all.length, page, page_size } }
    })
    const { result } = renderHook(() => useAllNodes(scope), { wrapper: queryWrapper(makeTestQueryClient()) })
    await waitFor(() => expect(result.current.data).toHaveLength(450))
    expect(api.get.mock.calls.map(([, o]) => o.params.page)).toEqual([1, 2, 3])
    expect(api.get.mock.calls.every(([, o]) => o.params.page_size === 200)).toBe(true)
  })
})
