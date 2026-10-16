// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { getRouteLines, getRouteOutbounds, saveRouteLines, type RouteLinesView } from '@/api/vlessRoute'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useVlessRouteDraft } from './vlessRoute'
import type { QueryScope } from './session'

vi.mock('@/api/vlessRoute', () => ({ getRouteLines: vi.fn(), getRouteOutbounds: vi.fn(), saveRouteLines: vi.fn() }))
const scope: QueryScope = { apiBase: '/api', userId: 1, role: 'admin', authEpoch: 1 }
const view = (): RouteLinesView => ({ node_id: 1, supported: true, warnings: [], state: { revision: 2, status: 'applied', lines: [{ id: 'stable', name: 'B', outbound_tag: 'direct', route: 1001, enabled: true }] } })
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getRouteLines).mockResolvedValue(view())
  vi.mocked(getRouteOutbounds).mockResolvedValue([{ tag: 'direct', protocol: 'freedom', source: 'template' }])
})

it('does not overwrite an unsaved line when background data is invalidated', async () => {
  const client = makeTestQueryClient()
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, name: 'edited' }))))
  await act(async () => { await client.invalidateQueries() })
  expect(result.current.lines[0].name).toBe('edited')
  expect(result.current.needsSave).toBe(true)
})

it('clears local draft state when the authenticated session changes', async () => {
  const client = makeTestQueryClient()
  const { result, rerender } = renderHook(({ currentScope, active }) => useVlessRouteDraft(currentScope, 1, 1, active), {
    initialProps: { currentScope: scope, active: true }, wrapper: queryWrapper(client),
  })
  await waitFor(() => expect(result.current.lines).toHaveLength(1))
  rerender({ currentScope: { ...scope, userId: 2, authEpoch: 2 }, active: false })
  expect(result.current.lines).toEqual([])
  expect(result.current.baseline).toBeNull()
})

it('preserves user input when a rejected save did not change the remote target', async () => {
  const client = makeTestQueryClient()
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, name: 'edited' }))))
  const error = Object.assign(new Error('validation rejected'), { isAxiosError: true, response: { status: 400 } })
  vi.mocked(saveRouteLines).mockRejectedValue(error)
  await act(async () => { await expect(result.current.save()).rejects.toBe(error) })
  expect(result.current.lines[0].name).toBe('edited')
  expect(saveRouteLines).toHaveBeenCalledOnce()
})

it('reads back an ambiguous committed target without automatically issuing another write', async () => {
  const client = makeTestQueryClient()
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, name: 'edited' }))))
  const latest = view(); latest.state.revision = 3; latest.state.status = 'unknown'; latest.state.lines[0].name = 'edited'
  vi.mocked(getRouteLines).mockResolvedValue(latest)
  vi.mocked(saveRouteLines).mockRejectedValue(Object.assign(new Error('timeout'), { isAxiosError: true }))
  await act(async () => { await expect(result.current.save()).rejects.toThrow('timeout') })
  expect(result.current.baseline?.state.revision).toBe(3)
  expect(result.current.lines[0].id).toBe('stable')
  expect(saveRouteLines).toHaveBeenCalledOnce()
})
