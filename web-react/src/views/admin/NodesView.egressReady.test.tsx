// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, editRow, installReads, list, mount, node } from '@/test/adminSaveHarness'
import NodesView from './NodesView'

/**
 * The node editor opens on the list's copy of a node, which says whether its
 * tunnel ran on both servers when the list was loaded. A tunnel that comes up
 * while the editor is open must turn the status to ready without a reload,
 * and an editor already showing ready must not keep asking.
 */

const entry = { id: 1, name: 'entry', url: '', panel_type: 'psp', capabilities: ['inbound.read', 'inbound.update', 'inbound.create', 'inbound.enable'], enabled: true }
const exit = { ...entry, id: 2, name: 'exit' }
const tunnelled = { ...node, panel_name: 'entry', egress_panel_id: 2 }
const link = { exists: true, protocol: 'ss2022', exit_address: '', reality_server_name: '', reality_target: '', port: 40000, server_address: '203.0.113.2', up_bytes: 0, down_bytes: 0 }

function reads(nodeDetail: () => unknown, listed: unknown) {
  installReads({
    '/admin/nodes': list([listed]),
    '/admin/servers': list([entry, exit]),
    '/admin/transit/links/1/2': link,
    '/admin/nodes/1/vless-route-editor': { lines: [], revision: 0 },
  })
  const listReads = api.get.getMockImplementation()!
  api.get.mockImplementation(async (url: string, ...rest: unknown[]) =>
    url === '/admin/nodes/1' ? { data: nodeDetail() } : listReads(url, ...rest))
}

const nodeReads = () => api.get.mock.calls.filter(([url]) => url === '/admin/nodes/1').length

describe('NodesView egress status', () => {
  beforeEach(() => { vi.useFakeTimers({ shouldAdvanceTime: true }) })
  afterEach(() => { vi.useRealTimers() })

  it('turns pending into ready once the tunnel runs on both servers', async () => {
    let ready = false
    reads(() => ({ node: { ...tunnelled, egress_ready: ready }, clients: [] }), tunnelled)
    mount(<NodesView />)
    await editRow()

    const status = await screen.findByTestId('egress-status')
    expect(status.textContent).toBe('admin:nodes.egress.pending')

    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    await waitFor(() => expect(nodeReads()).toBeGreaterThan(0))
    expect(screen.getByTestId('egress-status').textContent).toBe('admin:nodes.egress.pending')

    ready = true
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    await waitFor(() => expect(screen.getByTestId('egress-status').textContent).toBe('admin:nodes.egress.ready'))

    const asked = nodeReads()
    await act(async () => { await vi.advanceTimersByTimeAsync(15000) })
    expect(nodeReads()).toBe(asked)

    // The list row learned it too: reopening shows ready without asking.
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await editRow()
    expect((await screen.findByTestId('egress-status')).textContent).toBe('admin:nodes.egress.ready')
    await act(async () => { await vi.advanceTimersByTimeAsync(15000) })
    expect(nodeReads()).toBe(asked)
  })

  it('does not ask while the editor already shows ready', async () => {
    reads(() => ({ node: { ...tunnelled, egress_ready: true }, clients: [] }), { ...tunnelled, egress_ready: true })
    mount(<NodesView />)
    await editRow()

    expect((await screen.findByTestId('egress-status')).textContent).toBe('admin:nodes.egress.ready')
    await act(async () => { await vi.advanceTimersByTimeAsync(15000) })
    expect(nodeReads()).toBe(0)
  })
})
