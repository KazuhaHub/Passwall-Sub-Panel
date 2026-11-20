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

describe('deleting from the nodes list', () => {
  it('the row menu opens the impact dialog; nothing is deleted until its confirm', async () => {
    reads()
    mountPage(<NodesPage />)
    await screen.findAllByText('Node2')
    fireEvent.click(screen.getByRole('button', { name: 'Actions for Node1' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete node' }))
    const dialog = await screen.findByRole('alertdialog')
    fireEvent.click(await within(dialog).findByRole('button', { name: 'Delete node and 2 lines' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/nodes/1', undefined))
  })

  it('the bulk bar asks once for the whole selection, with the lines that go', async () => {
    reads()
    mountPage(<NodesPage />)
    await screen.findAllByText('Node2')
    fireEvent.click(screen.getByRole('checkbox', { name: /Node1 · a/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: /^Select Node2|Node2$/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Delete nodes (2)' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(await within(dialog).findByRole('button', { name: 'Delete 2 nodes and 3 lines' })).toBeTruthy()
    expect(api.delete).not.toHaveBeenCalled()
  })
})
