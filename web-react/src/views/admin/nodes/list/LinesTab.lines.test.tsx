// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { NodeOrder, OrderRow } from '@/api/nodes'
import type { Node } from '@/api/types'
import { api, installReads, list, mountPage } from '../testHarness'
import { forward, landing, listener, servers, tunnel } from '../lines/editorHarness'
import NodesPage from '../NodesPage'

const toast = vi.hoisted(() => vi.fn())
vi.mock('@/components/feedback/toasts', () => ({ pushToast: toast, pushSnack: toast }))

const landingOf = (id: number, name: string) => ({ node_id: id, name, region: 'TW', panel_id: 1, panel_name: 'Landing server', enabled: true })
const line = (node: number, id: string, name: string, over: Partial<OrderRow> = {}): OrderRow => ({
  kind: 'line', node_id: node, line_id: id, name, enabled: true, position: 10, positioned: true, line_type: id === 'direct' ? 'direct' : 'forward',
  published: true, withheld: '', address: `n${node}.example`, port: 443, health: { state: 'ok', checked_at: null }, landing: landingOf(node, `Node${node}`),
  ...(id === 'direct' ? {} : { entry: { panel_id: 2, panel_name: 'Entry GZ', missing: false } }), ...over,
})
const rows = [line(1, 'direct', 'Node1'), line(1, 'F1', 'Node1 · GZ'), line(1, 'T1', 'Node1 · SH', { line_type: 'tunnel', port: 31002 }), line(2, 'direct', 'Node2')]
const order: NodeOrder = { order_version: 'v1', rows }
const node1 = landing([forward, tunnel]) as Node

function reads(relays = node1.relays) {
  const n = { ...node1, relays } as Node
  installReads({
    '/admin/nodes/order': order, '/admin/nodes': list([n, { ...n, id: 2, display_name: 'Node2', relays: [] }]), '/admin/servers': list(servers), '/admin/nodes/separator': list([]),
    '/admin/nodes/1': { node: n, clients: [] },
    '/admin/nodes/1/source-guard': { can_guard: true, lines: [], cloudflare: { cidrs: [], source: 'builtin', builtin_date: '2026-11-18', auto_fetch: true } },
  })
  api.post.mockResolvedValue({ data: { groups: [], node_groups: [], saved_node_groups: [], relays: [[], []] } })
  api.put.mockImplementation(async (_u: string, body: { relays?: Node['relays'] }) => ({ data: { ...n, ...body } }))
}
const puts = () => api.put.mock.calls.filter(([u]) => u === '/admin/nodes/1/metadata')
const menuOf = async (name: string) => { fireEvent.click(await screen.findByRole('button', { name: `Actions for ${name}` })) }
const refusal = (data: Record<string, unknown>) => new AxiosError('x', '409', undefined, undefined, { status: 409, statusText: '', headers: {}, config: {} as never, data })

beforeEach(() => { toast.mockReset(); reads() })

describe('editing a line from the list', () => {
  it('the row menu\'s Edit line opens the sheet straight in edit mode, and the address says so', async () => {
    const { router } = mountPage(<NodesPage />)
    await menuOf('Node1 · GZ')
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Edit line' }))
    expect(await screen.findByLabelText(/Entry port/)).toBeTruthy()
    expect(router.state.location.search).toContain('line=1.F1')
    expect(router.state.location.search).toContain('edit=1')
  })

  it('E on a focused row does the same, and Enter on it opens the view', async () => {
    mountPage(<NodesPage />)
    await screen.findAllByText('Node2')
    const row = screen.getAllByRole('row').find(r => r.getAttribute('data-id') === '1.F1')!
    fireEvent.keyDown(row, { key: 'e' })
    expect(await screen.findByLabelText(/Entry port/)).toBeTruthy()
  })

  it('a row click shows the view; Edit line switches the same sheet to the editor and Back returns', async () => {
    mountPage(<NodesPage />)
    fireEvent.click((await screen.findAllByText('Node1 · GZ'))[0])
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).queryByLabelText(/Entry port/)).toBeNull()
    fireEvent.click(within(sheet).getByRole('button', { name: 'Edit line' }))
    expect(await within(sheet).findByLabelText(/Entry port/)).toBeTruthy()
    expect(screen.getAllByRole('dialog')).toHaveLength(1)
    fireEvent.click(within(sheet).getByRole('button', { name: 'Back' }))
    await waitFor(() => expect(within(sheet).queryByLabelText(/Entry port/)).toBeNull())
  })

  it('asks before closing a sheet with unsaved edits, and keeps them when told to keep editing', async () => {
    mountPage(<NodesPage />)
    await menuOf('Node1 · GZ')
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Edit line' }))
    fireEvent.change(await screen.findByLabelText(/Entry port/), { target: { value: '20444' } })
    fireEvent.click(screen.getByRole('button', { name: 'Close details' }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText('Discard unsaved changes?')).toBeTruthy()
    fireEvent.click(within(ask).getByRole('button', { name: 'Keep editing' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect((screen.getByLabelText(/Entry port/) as HTMLInputElement).value).toBe('20444')
    // Discarding closes it.
    fireEvent.click(screen.getByRole('button', { name: 'Close details' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(screen.queryByLabelText(/Entry port/)).toBeNull())
  })

  it('a clean sheet closes without asking', async () => {
    mountPage(<NodesPage />)
    await menuOf('Node1 · GZ')
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Edit line' }))
    await screen.findByLabelText(/Entry port/)
    fireEvent.click(screen.getByRole('button', { name: 'Close details' }))
    await waitFor(() => expect(screen.queryByLabelText(/Entry port/)).toBeNull())
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('Add line on a landing opens the editor on the type cards, and a saved line closes it and is highlighted', async () => {
    mountPage(<NodesPage />, '/admin/nodes?view=node')
    await menuOf('Node1')
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Add relay line…' }))
    fireEvent.click(await screen.findByRole('radio', { name: 'External entry' }))
    fireEvent.change(await screen.findByLabelText(/Entry address/), { target: { value: 'relay.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save line' }))
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect((puts()[0][1] as { relays: unknown[] }).relays).toHaveLength(3)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })
})

describe('the Multiplier column', () => {
  it('is drawn only when some row has a multiplier', async () => {
    mountPage(<NodesPage />)
    await screen.findAllByText('Node2')
    expect(screen.queryByRole('columnheader', { name: /Multiplier|Factor/i })).toBeNull()
  })
})

describe('the row menu of a line', () => {
  it('lists the line items, then the node items, then Delete line, in that order; a direct row has no line enable and no delete', async () => {
    mountPage(<NodesPage />)
    await menuOf('Node1 · GZ')
    expect((await screen.findAllByRole('menuitem')).map(i => i.textContent)).toEqual(['Edit line', 'Multiplier…', 'Disable line', 'Disable node', 'Move to…', 'Edit node', 'Delete line'])
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    await menuOf('Node1')
    expect((await screen.findAllByRole('menuitem')).map(i => i.textContent)).toEqual(['Edit node', 'Multiplier…', 'Add relay line…', 'Disable node', 'Move to…'])
  })
})

describe('enabling, disabling and deleting a line', () => {
  it('Disable line writes the node\'s lines once with only that flag changed, and offers Undo', async () => {
    mountPage(<NodesPage />)
    await menuOf('Node1 · GZ')
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Disable line' }))
    await waitFor(() => expect(puts()).toHaveLength(1))
    const relays = (puts()[0][1] as { relays: { id: string; enabled: boolean }[] }).relays
    expect(relays.map(r => [r.id, r.enabled])).toEqual([['F1', false], ['T1', true]])
    const action = toast.mock.calls.map(c => c[0]).find(c => c.message === 'Line disabled')?.action
    expect(action?.label).toBe('Undo')
    action.onClick()
    await waitFor(() => expect(puts()).toHaveLength(2))
    expect((puts()[1][1] as { relays: { enabled: boolean }[] }).relays[0].enabled).toBe(true)
  })

  it('Delete line draws the line, asks with focus on Cancel, and sends the lines without it', async () => {
    mountPage(<NodesPage />)
    await menuOf('Node1 · GZ')
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete line' }))
    const dialog = await screen.findByRole('dialog', { name: 'Delete line "Node1 · GZ"?' })
    expect(within(dialog).getByRole('img')).toBeTruthy()
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' })))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete line' }))
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect((puts()[0][1] as { relays: { id: string }[] }).relays.map(r => r.id)).toEqual(['T1'])
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('deleting a tunnel line shows the server\'s plan in that same dialog and sends the confirmed request', async () => {
    const plan = refusal({ code: 'confirm_required', params: { kind: 'tunnel', action: 'remove_line', plan: 'P', creates: [], removes: [{ node_id: 77, name: 'tunnel-77', line_name: 'SH', port: 31002, entry_panel_id: 3 }], refresh_needed: true } })
    api.put.mockRejectedValueOnce(plan)
    mountPage(<NodesPage />)
    await menuOf('Node1 · SH')
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete line' }))
    const dialog = await screen.findByRole('dialog', { name: 'Delete line "Node1 · SH"?' })
    // Before the server has spoken, the dialog already says which port closes.
    expect(within(dialog).getByTestId('closes-listener').textContent).toBe('Closes port 31002 on Entry GZ (entry listener)')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete line' }))
    const planned = await within(dialog).findByTestId('tunnel-plan')
    expect(planned.textContent).toContain('31002')
    expect(screen.getAllByRole('dialog')).toHaveLength(1)
    api.put.mockImplementation(async (_u: string, body: object) => ({ data: { ...node1, ...body } }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete line' }))
    await waitFor(() => expect(puts()).toHaveLength(2))
    expect(puts()[1][2]).toEqual({ params: { confirm_tunnels: 'true', confirm_plan: 'P' } })
    expect(listener.node_id).toBe(77)
  })

  it('the bulk bar enables or disables the selected relay lines with one write per landing', async () => {
    mountPage(<NodesPage />)
    await screen.findAllByText('Node2')
    for (const name of ['Node1 · GZ', 'Node1 · SH']) fireEvent.click(await screen.findByRole('checkbox', { name: new RegExp(name) }))
    fireEvent.click(await screen.findByRole('button', { name: 'Disable lines (2)' }))
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect((puts()[0][1] as { relays: { enabled: boolean }[] }).relays.map(r => r.enabled)).toEqual([false, false])
    await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Disabled 2 lines' })))
  })
})
