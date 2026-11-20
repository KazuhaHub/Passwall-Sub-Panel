// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { NodeOrder, OrderRow } from '@/api/nodes'
import { api, installReads, list, mountPage } from '../testHarness'
import NodesPage from '../NodesPage'

const landing = (id: number) => ({ node_id: id, name: `Node${id}`, region: 'TW', panel_id: id * 10, panel_name: `srv${id}`, enabled: true })
const line = (node: number, id: string, over: Partial<OrderRow> = {}): OrderRow => ({
  kind: 'line', node_id: node, line_id: id, name: id === 'direct' ? `Node${node}` : `Node${node} · ${id}`, enabled: true, position: 1, positioned: true,
  line_type: id === 'direct' ? 'direct' : 'forward', published: true, withheld: '', address: 'x', health: { state: 'ok', checked_at: null }, landing: landing(node),
  ...(id === 'direct' ? {} : { entry: { panel_id: 7, panel_name: 'GZ', missing: false } }), ...over,
})
const order: NodeOrder = { order_version: 'v1', rows: [line(1, 'direct'), line(1, 'a'), line(2, 'direct')] }
const nodes = [1, 2].map(id => ({ id, panel_id: id * 10, display_name: `Node${id}`, tags: [], relays: [], enabled: true, egress_panel_id: id === 2 ? 30 : 0 }))
const servers = [10, 20, 30].map(id => ({ id, name: `srv${id / 10}`, panel_type: 'psp', capabilities: ['inbound.enable', 'inbound.delete'] }))
const impact = (id: number, lines: number) => ({
  node: { id, name: `Node${id}` }, tunnels: [], blocked_by_routes: [],
  lines: Array.from({ length: lines }, (_, i) => ({ node_id: id, line_id: i === 0 ? 'direct' : 'a', landing_name: `Node${id}`, line_name: i === 0 ? `Node${id}` : `Node${id} · a`, line_type: i === 0 ? 'direct' : 'forward' })),
})
const reads = () => installReads({
  '/admin/nodes/order': order, '/admin/nodes': list(nodes), '/admin/servers': list(servers), '/admin/nodes/separator': list([]),
  '/admin/nodes/1/delete-impact': impact(1, 2), '/admin/nodes/2/delete-impact': impact(2, 1),
})

describe('the topology view of the list', () => {
  it('is the third view; a landing that leaves through another server gets an exit column, and "view in list" narrows the list', async () => {
    reads()
    const { router } = mountPage(<NodesPage />, '/admin/nodes?view=graph')
    await screen.findByTestId('topology')
    // srv3 is only an exit (Node2 leaves through it), so its box exists only because the exit column does.
    expect(screen.getByRole('button', { name: /^srv3/ })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /^GZ/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'View the 1 line through "GZ" in the list' }))
    await waitFor(() => expect(router.state.location.search).not.toContain('view=graph'))
    // The list is back, narrowed to that entry server.
    await waitFor(() => expect(screen.queryByText('Node2')).toBeNull())
    expect(screen.getAllByText('Node1 · a').length).toBeGreaterThan(0)
  })
})
