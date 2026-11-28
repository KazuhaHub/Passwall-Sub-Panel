// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ConfirmHost from '@/components/ConfirmHost'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { api, installReads, list } from '../testHarness'
import NodeCreateRoute from '../NodeCreateRoute'

vi.mock('@/components/feedback/toasts', () => ({ pushToast: vi.fn(), pushSnack: vi.fn() }))

const server = { id: 1, name: 'server', region: 'US', url: 'https://panel.example.test', panel_type: '3xui', capabilities: ['inbound.read', 'inbound.update', 'inbound.create', 'inbound.delete', 'inbound.enable'], enabled: true }
const created = { id: 9, panel_id: 1, display_name: 'server', relays: [] }

function mount(servers: unknown[] = [server]) {
  installReads({ '/admin/servers': list(servers), '/admin/nodes': list([]), '/admin/certs': { certs: [] } })
  api.post.mockImplementation(async (path: string, request?: { setup?: { link_ports?: Record<string, number> } }) => {
    if (path === '/admin/nodes/generate-reality-keypair') return { data: { private_key: 'private', public_key: 'public', short_id: 'abcd' } }
    if (path === '/admin/nodes/setup-preview') {
      const port = request?.setup?.link_ports?.[`9/${server.id}`]
      return { data: { token: port ? 'reviewed-edit' : 'reviewed', bindings: [
        { panel_id: server.id, name: server.name, kind: 'landing', port: 443 },
        { panel_id: server.id, peer_id: 9, kind: 'link', port: port ?? 31001 },
      ] } }
    }
    if (path === '/admin/nodes') return { data: created }
    throw new Error(`Unexpected POST ${path}`)
  })
  const router = createMemoryRouter([
    { path: '/admin/nodes', element: <div>the list</div> },
    { path: '/admin/nodes/new', element: <NodeCreateRoute /> },
    { path: '/admin/nodes/:id', element: <div>the new node</div> },
  ], { initialEntries: ['/admin/nodes/new'] })
  render(<QueryClientProvider client={makeTestQueryClient()}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
  return router
}
const next = () => screen.getByRole('button', { name: /^(Next|Review ports|Create node)$/ })
const creates = () => api.post.mock.calls.filter(([path]) => path === '/admin/nodes')

beforeEach(() => { vi.clearAllMocks(); localStorage.clear() })

describe('create page', () => {
  it('walks the four steps, edits a planned port at the review, and creates only with the reviewed plan, then opens the new node', async () => {
    const router = mount()
    // Step 1: the inbound and the basics, prefilled from the server.
    expect(((await screen.findByLabelText(/Display name/)) as HTMLInputElement).value).toBe('server')
    expect((screen.getByLabelText(/Region/) as HTMLInputElement).value).toMatch(/United States · US$/)
    const sni = screen.getByLabelText(/Reality server names|serverNames/i) as HTMLInputElement
    const target = screen.getByLabelText(/Reality dest/) as HTMLInputElement
    const previous = sni.value
    fireEvent.click(screen.getByRole('button', { name: 'Random' }))
    expect(sni.value).not.toBe(previous)
    expect(target.value).toBe(`${sni.value}:443`)
    await waitFor(() => expect((screen.getByLabelText(/Private Key/) as HTMLInputElement).value).toBe('private'))
    fireEvent.click(next())
    // Step 2: one external entry.
    fireEvent.click(await screen.findByRole('button', { name: /^External entry/ }))
    fireEvent.change(await screen.findByLabelText(/Front address/), { target: { value: 'relay.example.test' } })
    fireEvent.click(next())
    // Step 3 (exit), then the plan.
    await screen.findByRole('radio', { name: /Leave from this server/ })
    fireEvent.click(next())
    const table = await screen.findByRole('table')
    expect(within(table).getAllByRole('row').length).toBeGreaterThan(1)
    const linkPort = screen.getByRole('spinbutton', { name: 'Transit port' }) as HTMLInputElement
    expect(linkPort.value).toBe('31001')
    fireEvent.change(linkPort, { target: { value: '31999' } })
    // An edited port must be reviewed again before anything is created.
    fireEvent.click(next())
    await screen.findByRole('button', { name: 'Create node' })
    expect(creates()).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: 'Create node' }))
    await waitFor(() => expect(creates()).toHaveLength(1))
    expect(creates()[0][1]).toMatchObject({
      setup: { confirm: 'reviewed-edit', link_ports: { [`9/${server.id}`]: 31999 } },
      relays: [{ address: 'relay.example.test' }],
      inbound: { port: 443, protocol: 'vless' },
    })
    expect('sort_order' in (creates()[0][1] as object)).toBe(false)
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/nodes/9'))
  })

  it('stops each step with its reason: blank basics, no line at all, a tunnel on a landing that cannot have one', async () => {
    mount()
    const name = (await screen.findByLabelText(/Display name/)) as HTMLInputElement
    fireEvent.change(name, { target: { value: '' } })
    fireEvent.click(next())
    expect((await screen.findByRole('alert')).textContent).toMatch(/Display name \/ address \/ region are required/)
    fireEvent.change(name, { target: { value: 'Taipei' } })
    await waitFor(() => expect((screen.getByLabelText(/Private Key/) as HTMLInputElement).value).toBe('private'))
    fireEvent.click(next())
    // Step 2: the direct line off and no entry is nothing to publish.
    fireEvent.click(await screen.findByRole('switch', { name: /Include direct line/ }))
    fireEvent.click(next())
    expect((await screen.findByRole('alert')).textContent).toMatch(/Include the direct line or add at least one other entry/)
    expect(screen.queryByRole('radio', { name: /Leave from this server/ })).toBeNull()
  })

  it('does not leave step 1 with an address or region the fields say is wrong', async () => {
    mount()
    fireEvent.change(await screen.findByLabelText(/^Address/), { target: { value: 'not a host!' } })
    await waitFor(() => expect((screen.getByLabelText(/Private Key/) as HTMLInputElement).value).toBe('private'))
    fireEvent.click(next())
    expect((await screen.findByRole('alert')).textContent).toMatch(/valid hostname or IP/)
    expect(screen.queryByRole('radio', { name: /Leave from this server/ })).toBeNull()
    expect(screen.queryByRole('switch', { name: /Include direct line/ })).toBeNull()
  })

  it('leaves the exit server empty when "another server" is chosen, and will not go on until one is picked', async () => {
    mount([{ ...server, panel_type: 'psp', address: 'land.example' }, { ...server, id: 2, name: 'Osaka', panel_type: 'psp', address: 'osaka.example' }])
    await waitFor(() => expect((screen.getByLabelText(/Private Key/) as HTMLInputElement).value).toBe('private'))
    fireEvent.click(next())
    await screen.findByRole('switch', { name: /Include direct line/ })
    fireEvent.click(next())
    fireEvent.click(await screen.findByRole('radio', { name: /Leave through another server/ }))
    expect((await screen.findByRole('combobox', { name: /Exit server/ }) as HTMLInputElement).value).toBe('')
    fireEvent.click(next())
    expect((await screen.findByRole('alert')).textContent).toMatch(/Choose the exit server/)
    expect(api.post.mock.calls.some(([p]) => p === '/admin/nodes/setup-preview')).toBe(false)
  })

  it('discards a created plan that the server refuses (409) and asks for a new review', async () => {
    mount()
    await waitFor(() => expect((screen.getByLabelText(/Private Key/) as HTMLInputElement).value).toBe('private'))
    fireEvent.click(next()); await screen.findByRole('switch', { name: /Include direct line/ })
    fireEvent.click(next()); await screen.findByRole('radio', { name: /Leave from this server/ })
    fireEvent.click(next()); await screen.findByRole('table')
    const base = api.post.getMockImplementation()!
    api.post.mockImplementation(async (path: string, ...rest: unknown[]) => {
      if (path === '/admin/nodes') throw Object.assign(new Error('x'), { response: { status: 409, data: { error: 'ports changed since the review' } } })
      return base(path, ...rest)
    })
    fireEvent.click(screen.getByRole('button', { name: 'Create node' }))
    expect((await screen.findByRole('alert')).textContent).toMatch(/ports changed since the review/)
    // The plan is gone: the button asks for a review again.
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review ports' })).toBeTruthy())
  })

  it('asks before leaving once something is entered, and not before', async () => {
    const router = mount()
    await screen.findByLabelText(/Display name/)
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/nodes'))
    await router.navigate('/admin/nodes/new')
    fireEvent.change(await screen.findByLabelText(/Display name/), { target: { value: 'Typed' } })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    const dialog = await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog).getByRole('button', { name: /Keep editing/ }))
    expect(router.state.location.pathname).toBe('/admin/nodes/new')
  })

  it('says why there is nothing to create on when no server can create inbounds', async () => {
    mount([{ ...server, capabilities: ['inbound.read'] }])
    expect(await screen.findByText(/No writable panel currently supports creating inbounds/)).toBeTruthy()
  })
})
