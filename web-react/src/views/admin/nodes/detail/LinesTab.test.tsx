// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { beforeEach, expect, it, vi } from 'vitest'
import ConfirmHost from '@/components/ConfirmHost'
import type { NodeOrder, OrderRow } from '@/api/nodes'
import type { Node } from '@/api/types'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { api, installReads, list } from '../testHarness'
import { forward, landing, servers, tunnel } from '../lines/editorHarness'
import NodeDetailRoute from '../NodeDetailRoute'

vi.mock('@/components/feedback/toasts', () => ({ pushToast: vi.fn(), pushSnack: vi.fn() }))

const row = (id: string, name: string, over: Partial<OrderRow> = {}): OrderRow => ({
  kind: 'line', node_id: 1, line_id: id, name, enabled: true, position: 10, positioned: true, line_type: id === 'direct' ? 'direct' : 'forward', published: true, withheld: '',
  address: 'x.example', port: 443, health: { state: 'ok', checked_at: null },
  landing: { node_id: 1, name: 'Landing', region: 'TW', panel_id: 1, panel_name: 'Landing server', enabled: true },
  ...(id === 'direct' ? {} : { entry: { panel_id: 2, panel_name: 'Entry GZ', missing: false } }), ...over,
})
const order: NodeOrder = { order_version: 'v1', rows: [row('direct', 'Landing'), row('F1', 'Landing · GZ'), row('T1', 'Landing · SH', { line_type: 'tunnel' })] }
const node = landing([forward, tunnel]) as Node

function mount(path: string) {
  installReads({
    '/admin/nodes/order': order, '/admin/nodes': list([node]), '/admin/servers': list(servers), '/admin/nodes/separator': list([]), '/admin/nodes/1': { node, clients: [] },
    '/admin/nodes/1/source-guard': { can_guard: true, lines: [], cloudflare: { cidrs: [], source: 'builtin', builtin_date: '2026-11-18', auto_fetch: true } },
  })
  api.post.mockResolvedValue({ data: { groups: [], node_groups: [], saved_node_groups: [], relays: [[], []] } })
  api.put.mockImplementation(async (_u: string, body: object) => ({ data: { ...node, ...body } }))
  const router = createMemoryRouter([{ path: '/admin/nodes/:id', element: <NodeDetailRoute /> }], { initialEntries: [path] })
  render(<QueryClientProvider client={makeTestQueryClient()}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
  return router
}

beforeEach(() => vi.clearAllMocks())

it('the Lines tab is the default: one card per line with its path, and the published count', async () => {
  mount('/admin/nodes/1')
  const cards = await screen.findAllByTestId('node-line-card')
  expect(cards.map(c => c.getAttribute('data-line-id'))).toEqual(['direct', 'F1', 'T1'])
  expect(within(cards[1]).getByRole('img')).toBeTruthy()
  expect(await screen.findByText(/3 of 3 lines published/)).toBeTruthy()
})

it('Add line opens the editor in a sheet, addressed by ?edit=new, and saving appends the line', async () => {
  const router = mount('/admin/nodes/1?tab=lines')
  fireEvent.click(await screen.findByRole('button', { name: 'Add line' }))
  await waitFor(() => expect(router.state.location.search).toContain('edit=new'))
  fireEvent.click(await screen.findByRole('radio', { name: 'External entry' }))
  fireEvent.change(await screen.findByLabelText(/Entry address/), { target: { value: 'relay.example.com' } })
  fireEvent.click(screen.getByRole('button', { name: 'Save line' }))
  await waitFor(() => expect(api.put.mock.calls.some(([u]) => u === '/admin/nodes/1/metadata')).toBe(true))
  await waitFor(() => expect(router.state.location.search).not.toContain('edit='))
})

it('a link with ?edit=<line id> opens that line\'s editor', async () => {
  mount('/admin/nodes/1?edit=F1')
  expect(await screen.findByLabelText(/Entry port/)).toBeTruthy()
})
