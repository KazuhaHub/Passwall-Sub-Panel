// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { getRouteLines, getRouteOutbounds, saveRouteLines, type RouteLinesView } from '@/api/vlessRoute'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useVlessRouteDraft } from './vlessRoute'
import type { QueryScope } from './session'

vi.mock('@/api/vlessRoute', async importOriginal => ({ ...await importOriginal<typeof import('@/api/vlessRoute')>(), getRouteLines: vi.fn(), getRouteOutbounds: vi.fn(), saveRouteLines: vi.fn() }))
const scope: QueryScope = { apiBase: '/api', userId: 1, role: 'admin', authEpoch: 1 }
const view = (): RouteLinesView => ({ node_id: 1, supported: true, warnings: [], state: { revision: 2, status: 'applied', lines: [{ id: 'stable', name: 'B', outbound_tag: 'direct', route: 1001, enabled: true }] } })
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getRouteLines).mockResolvedValue(view())
  vi.mocked(getRouteOutbounds).mockResolvedValue([{ tag: 'direct', protocol: 'freedom', source: 'template' }])
})

it('treats an omitted legacy option as false and detects display-only edits', async () => {
  const client = makeTestQueryClient()
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  expect(result.current.needsSave).toBe(false)
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, only_line_name: true }))))
  expect(result.current.needsSave).toBe(true)
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, only_line_name: false }))))
  expect(result.current.needsSave).toBe(false)
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, entry_relay_id: 'relay' }))))
  expect(result.current.needsSave).toBe(true)
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, entry_relay_id: '' }))))
  expect(result.current.needsSave).toBe(false)
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

it('treats empty match fields like old data and preserves edited conditions across status refreshes', async () => {
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(makeTestQueryClient()) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, network: '', protocols: [], ip: [], domain: [''], port: ' ' }))))
  expect(result.current.needsSave).toBe(false)
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, network: 'tcp', protocols: ['tls'], ip: ['192.0.2.0/24'], domain: ['domain:example.com'], port: '443' }))))
  const drift = view(); drift.state.status = 'drift'; drift.state.revision = 3
  vi.mocked(getRouteLines).mockResolvedValue(drift)
  await act(async () => { await result.current.refresh() })
  expect(result.current.lines[0]).toMatchObject({ network: 'tcp', protocols: ['tls'], ip: ['192.0.2.0/24'], domain: ['domain:example.com'], port: '443' })
  expect(result.current.needsSave).toBe(true)
  expect(result.current.baseline?.state.revision).toBe(3)
  expect(result.current.conflict).toBe(false)
  const changed = view(); changed.state.revision = 4; changed.state.lines[0].port = '80'
  vi.mocked(getRouteLines).mockResolvedValue(changed)
  await act(async () => { await result.current.refresh() })
  expect(result.current.conflict).toBe(true)
  expect(result.current.baseline?.state.revision).toBe(3)
  expect(result.current.lines[0].port).toBe('443')
})

it('rechecks status on every tab activation and preserves unsaved edits while adopting a status-only revision', async () => {
  const client = makeTestQueryClient()
  const { result, rerender } = renderHook(({ active }) => useVlessRouteDraft(scope, 1, 1, active), { initialProps: { active: true }, wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, name: 'edited' }))))
  const drift = view(); drift.state.status = 'drift'; drift.state.error = 'routing_or_outbound_changed'; drift.state.revision = 3
  vi.mocked(getRouteLines).mockResolvedValue(drift)
  rerender({ active: false })
  rerender({ active: true })
  await waitFor(() => expect(result.current.baseline?.state.status).toBe('drift'))
  expect(getRouteLines).toHaveBeenCalledTimes(2)
  expect(result.current.baseline?.state.revision).toBe(3)
  expect(result.current.lines[0].name).toBe('edited')
  expect(result.current.conflict).toBe(false)
  vi.mocked(saveRouteLines).mockResolvedValue({ ...drift, state: { ...drift.state, status: 'applied', revision: 4, lines: [{ ...drift.state.lines[0], name: 'edited' }] } })
  await act(async () => { await result.current.save() })
  expect(saveRouteLines).toHaveBeenCalledWith(1, 3, expect.arrayContaining([expect.objectContaining({ name: 'edited' })]))
})

it('refreshes both state and outbound catalog without issuing a save', async () => {
  const client = makeTestQueryClient()
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  const unknown = view(); unknown.state.status = 'unknown'; unknown.state.error = 'remote_check_unavailable'; unknown.state.revision = 3
  vi.mocked(getRouteLines).mockResolvedValue(unknown)
  vi.mocked(getRouteOutbounds).mockResolvedValue([{ tag: 'warp', protocol: 'socks', source: 'template' }])
  await act(async () => { await result.current.refresh() })
  await waitFor(() => expect(result.current.baseline?.state.status).toBe('unknown'))
  expect(result.current.outbounds[0].tag).toBe('warp')
  expect(saveRouteLines).not.toHaveBeenCalled()
})

it('keeps a verified drift status when the outbound catalog cannot be loaded', async () => {
  const drift = view(); drift.state.status = 'drift'; drift.state.error = 'unsupported_or_stopped_core'
  vi.mocked(getRouteLines).mockResolvedValue(drift)
  vi.mocked(getRouteOutbounds).mockRejectedValue(Object.assign(new Error('core stopped'), { isAxiosError: true, response: { status: 400 } }))
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(makeTestQueryClient()) })
  await waitFor(() => expect(result.current.baseline?.state.status).toBe('drift'))
  expect(result.current.baseline?.supported).toBe(true)
  expect(result.current.catalogError).toBe(true)
})

it('preserves the old CAS revision and blocks saving when another writer changes the target of a dirty draft', async () => {
  const client = makeTestQueryClient()
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, name: 'my-edit' }))))
  const changed = view(); changed.state.lines[0].name = 'another-admin'; changed.state.revision = 9
  vi.mocked(getRouteLines).mockResolvedValue(changed)
  await act(async () => { await result.current.refresh() })
  await waitFor(() => expect(result.current.conflict).toBe(true))
  expect(result.current.lines[0].name).toBe('my-edit')
  expect(result.current.baseline?.state.revision).toBe(2)
  await act(async () => { await expect(result.current.save()).rejects.toThrow('Routing status must be refreshed') })
  expect(saveRouteLines).not.toHaveBeenCalled()
})

it('updates a clean draft after another writer changes its target', async () => {
  const client = makeTestQueryClient()
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  const changed = view(); changed.state.lines[0].name = 'another-admin'; changed.state.revision = 9
  vi.mocked(getRouteLines).mockResolvedValue(changed)
  await act(async () => { await result.current.refresh() })
  await waitFor(() => expect(result.current.lines[0].name).toBe('another-admin'))
  expect(result.current.baseline?.state.revision).toBe(9)
  expect(result.current.conflict).toBe(false)
})

it('reports a failed refresh and retains the unsaved draft for a retry', async () => {
  const client = makeTestQueryClient()
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, name: 'edited' }))))
  vi.mocked(getRouteLines).mockRejectedValue(new Error('unreachable'))
  await act(async () => { await result.current.refresh() })
  await waitFor(() => expect(result.current.readError).toBe(true))
  expect(result.current.lines[0].name).toBe('edited')
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

it('does not discard edits when an ambiguous save readback advanced only the status revision', async () => {
  const client = makeTestQueryClient()
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  act(() => result.current.setLines(previous => previous.map(line => ({ ...line, name: 'edited' }))))
  const latest = view(); latest.state.revision = 3; latest.state.status = 'drift'
  vi.mocked(getRouteLines).mockResolvedValue(latest)
  vi.mocked(saveRouteLines).mockRejectedValue(Object.assign(new Error('timeout'), { isAxiosError: true }))
  await act(async () => { await expect(result.current.save()).rejects.toThrow('timeout') })
  expect(result.current.lines[0].name).toBe('edited')
  expect(result.current.baseline?.state.revision).toBe(3)
  expect(result.current.conflict).toBe(false)
})

it('recognizes server-assigned IDs when reading back an ambiguous new-line save', async () => {
  const empty = view(); empty.state.lines = []
  vi.mocked(getRouteLines).mockResolvedValue(empty)
  const client = makeTestQueryClient()
  const { result } = renderHook(() => useVlessRouteDraft(scope, 1, 1, true), { wrapper: queryWrapper(client) })
  await waitFor(() => expect(result.current.baseline).not.toBeNull())
  act(() => result.current.setLines([{ name: 'New', outbound_tag: 'direct', enabled: true }]))
  const latest = view(); latest.state.revision = 3; latest.state.status = 'unknown'; latest.state.lines[0].name = 'New'
  vi.mocked(getRouteLines).mockResolvedValue(latest)
  vi.mocked(saveRouteLines).mockRejectedValue(Object.assign(new Error('timeout'), { isAxiosError: true }))
  await act(async () => { await expect(result.current.save()).rejects.toThrow('timeout') })
  expect(result.current.lines[0].id).toBe('stable')
  expect(result.current.lines[0].name).toBe('New')
  expect(result.current.conflict).toBe(false)
  expect(saveRouteLines).toHaveBeenCalledOnce()
})
