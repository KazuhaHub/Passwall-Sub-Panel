// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, installReads, mountPage } from '../../testHarness'

const confirm = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', async importOriginal => ({ ...(await importOriginal<typeof import('@/components/ConfirmHost')>()), confirm }))
const toast = vi.hoisted(() => vi.fn())
vi.mock('@/components/feedback/toasts', () => ({ pushToast: toast, pushSnack: toast }))

import { RoutesSection } from './RoutesSection'

const servers = [{ id: 2, name: 'tokyo' }, { id: 3, name: 'london' }]

const route = (over: Record<string, unknown> = {}) => ({
  id: 'r1', name: 'streaming', target: { kind: 'server', panel_id: 2 }, list_ids: [10], domains: [], cidrs: [], ports: '', network: '',
  entries: 120, sent: true, notes: [],
  link: { panel_id: 2, server_name: 'tokyo', exists: true, ready: true, in_applied: true, out_applied: false, quality: { state: 'ok', median_rtt_ms: 40, loss_percent: 0, withheld: false } },
  ...over,
})
const view = (over: Record<string, unknown> = {}) => ({
  supported: true, routes: [route(), route({ id: 'r2', name: '', target: { kind: 'local' }, list_ids: [], domains: ['full:a.example.com'], entries: 1, link: undefined })],
  default_exit: { kind: 'server', panel_id: 3 },
  default_link: { panel_id: 3, server_name: 'london', exists: true, ready: false, in_applied: true, out_applied: false },
  sent: true, running: false, warnings: [],
  budget: { entries: 121, rules: 4, bytes: 900, max_entries: 1000, max_rules: 50, max_bytes: 65536, engine: 'xray' },
  ...over,
})
const lists = { items: [
  { id: 10, name: 'streaming-sites', kind: 'custom', side: 'dest', entry_count: 120, regexp_count: 0, owner_group_id: 0, used_by: [] },
  { id: 11, name: 'geosite-cn', kind: 'geosite', side: 'dest', entry_count: 900, regexp_count: 0, owner_group_id: 0, used_by: [] },
  { id: 12, name: 'my-sources', kind: 'custom', side: 'source', entry_count: 3, regexp_count: 0, owner_group_id: 0, used_by: [] },
], refresh_hours: 24, budget: {} }

const tunnel = { exists: true, protocol: 'ss2022', exit_address: '', reality_server_name: '', reality_target: '', port: 40000, server_address: '203.0.113.2', up_bytes: 0, down_bytes: 0 }

function setup(routes: unknown, extra: Record<string, unknown> = {}) {
  installReads({ '/admin/nodes/1/routes': routes, '/admin/dest/lists': lists, '/admin/transit/probes': { items: [] }, '/admin/transit/links/1/2': tunnel, ...extra })
}
const ui = <RoutesSection nodeId={1} entryPanelId={1} servers={servers} />
const rows = () => screen.findAllByTestId('route-row')

const mount = (el: React.ReactElement) => mountPage(el)
const checkboxesOf = (row: HTMLElement) => within(row).getAllByRole('checkbox')

beforeEach(() => {
  confirm.mockReset()
  toast.mockReset()
  api.post.mockResolvedValue({ data: view() })
})

describe('RoutesSection', () => {
  it('shows the rows with their link status, entries, sent state, default exit and budget', async () => {
    setup(view({ routes: [route({ notes: [{ code: 'list_regexp_skipped', list: 'streaming-sites', count: 3 }] }), view().routes[1]], warnings: [{ code: 'exit_not_ready', message: 'x', params: { server: 'london', panel_id: 3 } }] }))
    mount(ui)
    const [first, second] = await rows()
    expect(within(first).getByTestId('route-link').dataset.state).toBe('ready')
    expect(within(first).getByTestId('route-link-ends').textContent).toContain('applied')
    expect(within(first).getByTestId('route-link-ends').textContent).toContain('waiting')
    expect(within(first).getByTestId('route-link-quality').dataset.state).toBe('ok')
    expect(within(first).getByTestId('route-entries').textContent).toBe('120 compiled entries')
    expect(within(first).getByTestId('route-sent').dataset.state).toBe('sent')
    expect(within(first).getByTestId('route-note').dataset.code).toBe('list_regexp_skipped')
    // A local target has no tunnel to show.
    expect(within(second).queryByTestId('route-link')).toBeNull()
    expect(screen.getByTestId('routes-default-link').dataset.state).toBe('not_ready')
    expect(screen.getByTestId('routes-default-exit').textContent).toBe('Default exit: london')
    expect(screen.getByTestId('routes-applied').dataset.state).toBe('waiting')
    expect(screen.getByTestId('routes-budget-entries').textContent).toContain('Destination entries: 121 / 1000')
    expect(screen.getByTestId('route-warning').dataset.code).toBe('exit_not_ready')
    expect(screen.getByTestId('routes-save').hasAttribute('disabled')).toBe(true)
  })

  it('draws the path of each route: what it matches, then where it leaves', async () => {
    setup(view())
    mount(ui)
    const [first, second] = await rows()
    // Names of lists come from the destination lists; a local route leaves from this server.
    expect(within(first).getByTestId('route-path').textContent).toBe('streaming-sites→tokyo')
    expect(within(second).getByTestId('route-path').textContent).toBe('full:a.example.com→this server')
  })

  it('says when routes are not sent yet and when the server runs them', async () => {
    setup(view({ sent: false, running: false }))
    const { unmount } = mount(ui)
    await waitFor(() => expect(screen.getByTestId('routes-applied').dataset.state).toBe('not_sent'))
    unmount()
    setup(view({ sent: true, running: true }))
    mount(ui)
    await waitFor(() => expect(screen.getByTestId('routes-applied').dataset.state).toBe('running'))
  })

  it('reorders with the buttons, and only then can be saved with the new order', async () => {
    setup(view())
    api.put.mockResolvedValue({ data: view() })
    mount(ui)
    await rows()
    fireEvent.click(screen.getByTestId('route-down-0'))
    expect(screen.getByTestId('routes-unsaved')).toBeTruthy()
    await waitFor(() => expect(screen.getByTestId('routes-save').hasAttribute('disabled')).toBe(false))
    fireEvent.click(screen.getByTestId('routes-save'))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    const body = api.put.mock.calls[0][1] as { routes: { id: string }[] }
    expect(body.routes.map(r => r.id)).toEqual(['r2', 'r1'])
    await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Routes saved', severity: 'success' })))
  })

  it('removes a route, and discards the changes with revert', async () => {
    setup(view())
    mount(ui)
    await rows()
    fireEvent.click(screen.getByTestId('route-remove-0'))
    expect(screen.getAllByTestId('route-row').length).toBe(1)
    fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }))
    await waitFor(() => expect(screen.getAllByTestId('route-row').length).toBe(2))
    expect(screen.queryByTestId('routes-unsaved')).toBeNull()
  })

  it('adds a route with a target and a list, runs the dry run, and saves the exact body', async () => {
    setup(view({ routes: [], sent: false }))
    const after = view({ routes: [route({ id: 'r1', name: '' })], warnings: [{ code: 'redundant_route', message: 'x', route_id: 'r1' }] })
    api.put.mockImplementation(async () => { setup(after); return { data: after } })
    mount(ui)
    fireEvent.click(await screen.findByRole('button', { name: 'Add route' }))
    const [row] = await rows()
    // A route with no target and nothing to match is named, and cannot be saved.
    expect(within(row).getAllByTestId('route-issue').length).toBe(2)
    expect(screen.getByTestId('routes-save').hasAttribute('disabled')).toBe(true)

    fireEvent.click(within(row).getByRole('checkbox', { name: /streaming-sites/ }))
    fireEvent.change(within(row).getByLabelText('Leaves through'), { target: { value: 'server:2' } })

    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1), { timeout: 3000 })
    expect(api.post.mock.calls[0][0]).toBe('/admin/nodes/1/routes/check')
    await waitFor(() => expect(screen.getByTestId('routes-save').hasAttribute('disabled')).toBe(false))
    fireEvent.click(screen.getByTestId('routes-save'))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][0]).toBe('/admin/nodes/1/routes')
    expect(api.put.mock.calls[0][1]).toEqual({ routes: [{ target: { kind: 'server', panel_id: 2 }, list_ids: [10], domains: [], cidrs: [], ports: '', network: '' }] })
    // The save's own warnings are shown after it.
    await waitFor(() => expect(screen.getByTestId('route-warning').dataset.code).toBe('redundant_route'))
  })

  it('offers local as a target and only destination lists', async () => {
    setup(view({ routes: [] }))
    mount(ui)
    fireEvent.click(await screen.findByRole('button', { name: 'Add route' }))
    const [row] = await rows()
    // The source-side list is not offered.
    expect(checkboxesOf(row).length).toBe(2)
    expect(within(row).queryByRole('checkbox', { name: /my-sources/ })).toBeNull()
    const target = within(row).getByLabelText('Leaves through')
    expect(within(target).getByRole('option', { name: 'Leave from this server (local)' })).toBeTruthy()
    expect(within(target).getByRole('option', { name: 'london' })).toBeTruthy()
  })

  it('shows a localized refusal naming the route and keeps the draft', async () => {
    setup(view())
    const err = new AxiosError('bad', '400', undefined, undefined, {
      status: 400, statusText: '', headers: {}, config: {} as never,
      data: { error: 'too big', code: 'over_budget', params: { listener: 'in-1', node: 'hk', limit: 'rules', used: 60, max: 50 } },
    })
    api.put.mockRejectedValue(err)
    mount(ui)
    await rows()
    fireEvent.click(screen.getByTestId('route-down-0'))
    await waitFor(() => expect(screen.getByTestId('routes-save').hasAttribute('disabled')).toBe(false))
    fireEvent.click(screen.getByTestId('routes-save'))
    expect((await screen.findByTestId('routes-save-error')).textContent).toBe('Listener in-1 of node hk would exceed the rules budget (60 of 50). Remove routes or use smaller lists.')
    expect(screen.getByTestId('routes-unsaved')).toBeTruthy()
  })

  it('is disabled with the reason when unsupported, still shows saved routes, and still clears them', async () => {
    setup(view({ supported: false, reason: 'no_capability' }))
    api.put.mockResolvedValue({ data: view({ supported: false, reason: 'no_capability', routes: [] }) })
    confirm.mockResolvedValue(true)
    mount(ui)
    expect((await screen.findByTestId('routes-unsupported')).textContent).toContain('too old for routing by destination')
    const [first] = await rows()
    expect((await rows()).length).toBe(2)
    expect((screen.getByTestId('route-down-0') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Add route' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByTestId('routes-save').hasAttribute('disabled')).toBe(true)
    const controls = [...first.querySelectorAll('select, input, textarea'), ...within(first).getAllByRole('checkbox')]
    expect(controls.length).toBeGreaterThan(5)
    expect(controls.every(c => (c as HTMLInputElement).disabled || c.hasAttribute('data-disabled') || c.getAttribute('aria-disabled') === 'true')).toBe(true)
    const clear = screen.getByTestId('routes-clear') as HTMLButtonElement
    expect(clear.disabled).toBe(false)
    fireEvent.click(clear)
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/nodes/1/routes', { routes: [] }, expect.anything()))
  })

  it('does not clear when the confirmation is declined', async () => {
    setup(view())
    confirm.mockResolvedValue(false)
    mount(ui)
    await rows()
    fireEvent.click(screen.getByTestId('routes-clear'))
    await waitFor(() => expect(confirm).toHaveBeenCalled())
    expect(api.put).not.toHaveBeenCalled()
  })

  it('says why a tunnel whose ends both applied is still not ready: they are on different ports', async () => {
    const differing = route({ link: { panel_id: 2, server_name: 'tokyo', exists: true, ready: false, in_applied: true, out_applied: true, ports_differ: true, in_port: 443, out_port: 23456 } })
    setup(view({ routes: [differing, view().routes[1]] }))
    mount(ui)
    await rows()
    const note = await screen.findByTestId('route-link-ports')
    expect(note.textContent).toContain('the exit listens on port 443 and the entry dials port 23456')
  })

  it('says the exit server above is changed but not saved', async () => {
    setup(view())
    mount(<RoutesSection nodeId={1} entryPanelId={1} servers={servers} pendingExit={2} />)
    expect(await screen.findByTestId('routes-exit-pending')).toBeTruthy()
  })

  it('reports unsaved changes to the page, and not unsaved when it goes away', async () => {
    setup(view())
    const onDirtyChange = vi.fn()
    const { unmount } = mount(<RoutesSection nodeId={1} entryPanelId={1} servers={servers} onDirtyChange={onDirtyChange} />)
    await rows()
    expect(onDirtyChange).toHaveBeenLastCalledWith(false)
    fireEvent.click(screen.getByTestId('route-down-0'))
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(true))
    unmount()
    expect(onDirtyChange).toHaveBeenLastCalledWith(false)
  })

  it('is never dirty in setup mode, holds the rows in the wizard, fetches nothing and has no Save', async () => {
    installReads({ '/admin/dest/lists': lists })
    const onDirtyChange = vi.fn()
    const setRows = vi.fn()
    mount(<RoutesSection nodeId={0} entryPanelId={1} servers={servers} setup={{ rows: [], setRows }} onDirtyChange={onDirtyChange} />)
    fireEvent.click(await screen.findByRole('button', { name: 'Add route' }))
    expect(setRows).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('routes-save')).toBeNull()
    expect(screen.queryByTestId('routes-default-exit')).toBeNull()
    expect(api.get.mock.calls.some(([u]) => String(u).includes('/routes'))).toBe(false)
    expect(onDirtyChange).not.toHaveBeenCalledWith(true)
  })

  it('offers one collapsed tunnel block per distinct exit server, and saves it on its own', async () => {
    const second = route({ id: 'r3', name: 'again' })
    const gone = route({ id: 'r4', target: { kind: 'server', panel_id: 9 }, link: { panel_id: 9, exists: false, ready: false, in_applied: false, out_applied: false, gone: true } })
    setup(view({ routes: [route(), second, gone, view().routes[1]] }))
    api.put.mockResolvedValue({ data: { ...tunnel, protocol: 'vless_reality', exit_address: '203.0.113.9', reality_server_name: 'www.asus.com', reality_target: 'www.asus.com:443' } })
    mount(ui)
    await rows()
    const blocks = await screen.findAllByTestId('route-tunnel')
    // Server 2 is used twice but listed once; the deleted server 9 and the local route have none.
    expect(blocks.length).toBe(1)
    expect(api.get.mock.calls.some(([u]) => String(u).startsWith('/admin/transit/links'))).toBe(false)
    fireEvent.click(screen.getByTestId('route-tunnel-toggle'))
    const address = await screen.findByLabelText('Exit address')
    expect((screen.getByTestId('route-tunnel-save') as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(address, { target: { value: ' 203.0.113.9 ' } })
    fireEvent.click(screen.getByTestId('route-tunnel-save'))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/transit/links/1/2', { protocol: 'ss2022', exit_address: '203.0.113.9', reality_server_name: undefined, reality_target: undefined }))
    // The tunnel save is not the routes' save.
    expect(api.put.mock.calls.every(([u]) => String(u).startsWith('/admin/transit'))).toBe(true)
  })

  describe('changing a tunnel that nodes use as their default exit', () => {
    async function open(users: string[]) {
      setup(view())
      api.put.mockResolvedValue({ data: tunnel })
      mount(<RoutesSection nodeId={1} entryPanelId={1} servers={servers} defaultExitNodesOf={id => (id === 2 ? users : [])} />)
      await rows()
      fireEvent.click(await screen.findByTestId('route-tunnel-toggle'))
      return screen.findByLabelText('Exit address')
    }
    const tunnelPuts = () => api.put.mock.calls.filter(([u]) => String(u).startsWith('/admin/transit'))

    it('names the nodes and asks before sending when a default-exit node is affected', async () => {
      const address = await open(['hk-1', 'hk-2'])
      expect(screen.getByTestId('route-tunnel-users').textContent).toBe('Default exit of: hk-1, hk-2')
      fireEvent.change(address, { target: { value: '203.0.113.9' } })
      confirm.mockResolvedValue(false)
      fireEvent.click(screen.getByTestId('route-tunnel-save'))
      await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))
      expect(confirm.mock.calls[0][0].message).toContain('hk-1, hk-2')
      expect(confirm.mock.calls[0][0].message).not.toContain('re-handshake')
      // Cancelled: nothing is sent.
      expect(tunnelPuts()).toEqual([])
      confirm.mockResolvedValue(true)
      fireEvent.click(screen.getByTestId('route-tunnel-save'))
      await waitFor(() => expect(tunnelPuts().length).toBe(1))
    })

    it('warns about the re-handshake and asks when the protocol changes, even with no default-exit node', async () => {
      await open([])
      fireEvent.change(screen.getByLabelText('Tunnel protocol'), { target: { value: 'vless_reality' } })
      confirm.mockResolvedValue(false)
      fireEvent.click(screen.getByTestId('route-tunnel-save'))
      await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))
      expect(confirm.mock.calls[0][0].message).toContain('re-handshake')
      expect(tunnelPuts()).toEqual([])
    })

    it('sends an address edit with no default-exit node without asking', async () => {
      const address = await open([])
      fireEvent.change(address, { target: { value: '203.0.113.9' } })
      fireEvent.click(screen.getByTestId('route-tunnel-save'))
      await waitFor(() => expect(tunnelPuts().length).toBe(1))
      expect(confirm).not.toHaveBeenCalled()
    })
  })
})
