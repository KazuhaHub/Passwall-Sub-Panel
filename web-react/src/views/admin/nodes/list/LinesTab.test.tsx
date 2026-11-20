// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { NodeOrder, OrderRow } from '@/api/nodes'
import { useAuthStore } from '@/stores/auth'
import { api, installReads, list, mountPage } from '../testHarness'
import NodesPage from '../NodesPage'

const landing = (id: number, name: string) => ({ node_id: id, name, region: 'TW', panel_id: id * 10, panel_name: `srv${id}`, enabled: true })
const line = (node: number, id: string, name: string, over: Partial<OrderRow> = {}): OrderRow => ({
  kind: 'line', node_id: node, line_id: id, name, enabled: true, position: 10, positioned: true, line_type: id === 'direct' ? 'direct' : 'forward',
  published: true, withheld: '', address: `n${node}.example`, port: 443, health: { state: 'ok', checked_at: null }, landing: landing(node, `Node${node}`),
  ...(id === 'direct' ? {} : { entry: { panel_id: 7, panel_name: 'GZ', missing: false } }), ...over,
})
const sep: OrderRow = { kind: 'separator', id: 5, name: '---- HK ----', enabled: true, mode: 'global', position: 5, positioned: true }
const rows = [sep, line(1, 'direct', 'Node1'), line(1, 'a', 'Node1 · GZ', { published: false, withheld: 'waiting_entry' }), line(2, 'direct', 'Node2')]
const order: NodeOrder = { order_version: 'v1', rows }
const nodes = [{ id: 1, panel_id: 10, display_name: 'Node1', tags: [], relays: [], enabled: true }, { id: 2, panel_id: 20, display_name: 'Node2', tags: [], relays: [], enabled: true }]
const servers = [10, 20].map(id => ({ id, name: `srv${id / 10}`, panel_type: 'psp', capabilities: ['inbound.enable', 'inbound.delete'] }))

function reads(o: NodeOrder = order) {
  installReads({
    '/admin/nodes/order': o, '/admin/nodes': list(nodes), '/admin/servers': list(servers), '/admin/nodes/separator': list([{ id: 5, display_name: '---- HK ----', mode: 'global', node_ids: [], enabled: true, sort_order: 5 }]),
  })
}
const names = () => screen.getAllByRole('row').slice(1).map(r => r.getAttribute('data-id'))
const putBodies = () => api.put.mock.calls.filter(([u]) => u === '/admin/nodes/order').map(([, b]) => b)
const handle = (name: string) => screen.getByRole('button', { name: new RegExp(`Drag to reorder: ${name}`) })
async function keyboardMove(name: string, key: 'ArrowUp' | 'ArrowDown') {
  const h = handle(name)
  h.focus()
  fireEvent.keyDown(h, { key: ' ' })
  fireEvent.keyDown(h, { key })
  fireEvent.keyDown(h, { key: ' ' })
}
const staleError = (current: NodeOrder) => Object.assign(new Error('409'), { response: { status: 409, data: { code: 'order_stale', order: current } } })

describe('the subscription order list', () => {
  it('shows every line and separator in the server order, with the state in words and the KPI counts', async () => {
    reads()
    mountPage(<NodesPage />)
    await screen.findAllByText('Node2')
    expect(names()).toEqual(['sep:5', '1.direct', '1.a', '2.direct'])
    expect(screen.getByText('Waiting for entry server')).toBeTruthy()
    expect(within(screen.getByRole('button', { name: /^Published/ })).getByText('2')).toBeTruthy()
    expect(within(screen.getByRole('button', { name: /^Not published/ })).getByText('1')).toBeTruthy()
  })

  it('writes the COMPLETE list with the version it was read with after a keyboard move', async () => {
    reads()
    // The server now holds the new arrangement, as the refetch after the save reads it.
    const moved: NodeOrder = { order_version: 'v2', rows: [rows[0], rows[1], rows[3], rows[2]] }
    api.put.mockImplementation(async () => { reads(moved); return { data: { order_version: 'v2' } } })
    mountPage(<NodesPage />)
    await screen.findAllByText('Node2')
    await keyboardMove('Node2', 'ArrowUp')
    await waitFor(() => expect(putBodies()).toHaveLength(1))
    expect(putBodies()[0]).toEqual({
      base_version: 'v1',
      items: [
        { kind: 'separator', id: 5 }, { kind: 'line', node_id: 1, line_id: 'direct' }, { kind: 'line', node_id: 2, line_id: 'direct' }, { kind: 'line', node_id: 1, line_id: 'a' },
      ],
    })
    // The move shows at once and the Undo is on offer.
    await waitFor(() => expect(names()).toEqual(['sep:5', '1.direct', '2.direct', '1.a']))
    expect(await screen.findByTestId('undo-move')).toBeTruthy()
  })

  it('on a 409 shows what changed, then repeats the same move on the latest order', async () => {
    reads()
    const added = line(3, 'direct', 'Node3')
    const current: NodeOrder = { order_version: 'v2', rows: [...rows, added] }
    api.put.mockRejectedValueOnce(staleError(current)).mockResolvedValueOnce({ data: { order_version: 'v3' } })
    mountPage(<NodesPage />)
    await screen.findAllByText('Node2')
    await keyboardMove('Node2', 'ArrowUp')
    const dialog = await screen.findByRole('dialog', { name: 'The order has changed' })
    expect(within(dialog).getByText('Added: Node3')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move again on the latest order' }))
    await waitFor(() => expect(putBodies()).toHaveLength(2))
    // The second write carries the NEW version and the person's move on the new list.
    expect(putBodies()[1].base_version).toBe('v2')
    expect(putBodies()[1].items.map((i: { node_id?: number; line_id?: string }) => i.line_id && `${i.node_id}.${i.line_id}`)).toEqual([undefined, '1.direct', '2.direct', '1.a', '3.direct'])
  })

  it('drag is off while a filter is active, and Move to… still works', async () => {
    reads()
    api.put.mockResolvedValue({ data: { order_version: 'v2' } })
    mountPage(<NodesPage />, '/admin/nodes?q=Node')
    await screen.findAllByText('Node2')
    expect(handle('Node2').getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(screen.getByRole('button', { name: 'Actions for Node2' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Move to…' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.keyDown(within(dialog).getByRole('combobox'), { key: 'ArrowDown' })
    fireEvent.click(await screen.findByRole('option', { name: 'Move to the top' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move' }))
    await waitFor(() => expect(putBodies()).toHaveLength(1))
    expect(putBodies()[0].items[0]).toEqual({ kind: 'line', node_id: 2, line_id: 'direct' })
    expect(putBodies()[0].items).toHaveLength(4)
  })

  it('shows an operator the list without handles, Move to… or a way to change the order', async () => {
    useAuthStore.setState({ role: 'operator' })
    reads()
    mountPage(<NodesPage />)
    await screen.findAllByText('Node2')
    expect(screen.queryAllByRole('button', { name: /Drag to reorder/ })).toHaveLength(0)
    expect(screen.getByText('Only administrators can change the order')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Actions for Node2' }))
    expect(screen.queryByRole('menuitem', { name: 'Move to…' })).toBeNull()
    expect(screen.getByRole('menuitem', { name: 'Disable node' })).toBeTruthy()
  })

  it('says why the list is missing when the order cannot be read', async () => {
    installReads({ '/admin/nodes/order': new Error('offline'), '/admin/nodes': list([]), '/admin/servers': list([]), '/admin/nodes/separator': list([]) })
    mountPage(<NodesPage />)
    expect(await screen.findByText('Could not load lines')).toBeTruthy()
  })
})
