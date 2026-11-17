// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, editRow, installReads, list, mount, node } from '@/test/adminSaveHarness'
import NodesView from './NodesView'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const entry = { id: 2, name: 'entry', url: '', panel_type: 'psp', capabilities: ['inbound.read', 'inbound.update', 'inbound.create', 'inbound.enable'], enabled: true }
const landingServer = { ...entry, id: 1, name: 'server' }

const listener = {
  node_id: 77, name: 'tunnel-77', enabled: true, port: 40123, protocol: 'vless', flow: 'xtls-rprx-vision', security: 'reality',
  server_names: ['www.asus.com'], target: 'www.asus.com:443', short_ids: ['ab12'], public_key: 'PUB', private_key: 'PRIV',
  config_sync_state: 'synced', applied: true, egress_panel_id: 1, egress_ready: true, routes: 0,
}
const tunnelLine = { id: 'T1', name: 'Mobile', address: 'entry.example', port: 40123, enabled: true, entry_panel_id: 2, mode: 'tunnel', running: true, tunnel: listener }
const forwardLine = { id: 'F1', name: 'Plain', address: 'fwd.example', port: 8443, enabled: true, entry_panel_id: 2, managed_ready: true }

const refusal = (data: Record<string, unknown>, status = 409) => new AxiosError('x', String(status), undefined, undefined, {
  status, statusText: '', headers: {}, config: {} as never, data,
})
const tunnelRefusal = (action: string, removes: unknown[] = []) => refusal({ code: 'confirm_required', params: { kind: 'tunnel', action, creates: [], removes, refresh_needed: true } })

const metadataPuts = () => api.put.mock.calls.filter(([url]) => String(url) === '/admin/nodes/1/metadata')

function setup(relays: unknown[]) {
  const landing = { ...node, panel_name: 'server', relays }
  installReads({ '/admin/nodes': list([landing]), '/admin/servers': list([landingServer, entry]) })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...landing, ...body } }))
  return landing
}

beforeEach(() => confirmation.mockReset())

describe('NodesView tunnel lines', () => {
  it('round-trips a saved tunnel line without sending the listener or the running flag', async () => {
    setup([tunnelLine, forwardLine])
    mount(<NodesView />)
    const dialog = await editRow()
    expect(within(dialog).getByTestId('tunnel-card')).toBeTruthy()
    // The tunnel line has no entry port field: its port is the listener's.
    expect(within(dialog).getAllByLabelText(/admin:nodes.relay.port/)).toHaveLength(1)
    fireEvent.change(within(dialog).getByLabelText(/admin:nodes.field.display_name/), { target: { value: 'renamed' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(1))
    const [t, f] = (metadataPuts()[0][1] as { relays: Record<string, unknown>[] }).relays
    expect(t).toMatchObject({ id: 'T1', mode: 'tunnel', entry_panel_id: 2 })
    expect(t).not.toHaveProperty('tunnel')
    expect(t).not.toHaveProperty('running')
    expect(f).not.toHaveProperty('mode')
  })

  it('sends the mode of a new tunnel line, with no port', async () => {
    setup([])
    mount(<NodesView />)
    const dialog = await editRow()
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:nodes.relay.add' }))
    fireEvent.change(within(dialog).getByLabelText(/admin:nodes.relay.address/), { target: { value: 'entry.example' } })
    fireEvent.mouseDown(within(dialog).getByLabelText('admin:nodes.relay.entry'))
    fireEvent.click(await screen.findByRole('option', { name: 'entry' }))
    fireEvent.click(await within(dialog).findByRole('button', { name: 'admin:nodes.tunnel.mode_tunnel' }))
    expect(within(dialog).getByTestId('tunnel-new-hint')).toBeTruthy()
    expect(within(dialog).queryByLabelText(/admin:nodes.relay.port/)).toBeNull()
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(1))
    expect((metadataPuts()[0][1] as { relays: unknown[] }).relays[0]).toMatchObject({ mode: 'tunnel', entry_panel_id: 2, port: 0 })
  })

  it('asks before a save that removes a tunnel line, and sends nothing more when declined', async () => {
    setup([tunnelLine])
    api.put.mockRejectedValue(tunnelRefusal('remove_line', [{ node_id: 77, name: 'tunnel-77', line_name: 'Mobile', port: 40123 }]))
    confirmation.mockResolvedValue(false)
    mount(<NodesView />)
    const dialog = await editRow()
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:nodes.relay.remove' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(confirmation).toHaveBeenCalledTimes(1))
    expect(confirmation.mock.calls[0][0].title).toBe('admin:nodes.tunnel.confirm.remove_line_title')
    await waitFor(() => expect((within(dialog).getByRole('button', { name: 'common:actions.ok' }) as HTMLButtonElement).disabled).toBe(false))
    expect(metadataPuts()).toHaveLength(1)
  })

  it('repeats the save with confirm_tunnels once the admin agrees', async () => {
    const landing = setup([tunnelLine])
    api.put.mockRejectedValueOnce(tunnelRefusal('remove_line', [{ node_id: 77, name: 'tunnel-77', line_name: 'Mobile', port: 40123 }]))
      .mockResolvedValueOnce({ data: { ...landing, relays: [] } })
    confirmation.mockResolvedValue(true)
    mount(<NodesView />)
    const dialog = await editRow()
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:nodes.relay.remove' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(2))
    expect(metadataPuts()[1][2]).toEqual({ params: { confirm_tunnels: 'true' } })
  })

  it('keeps unsaved edits on other rows when a saved line switches mode', async () => {
    setup([forwardLine, tunnelLine])
    const switched = { ...node, relays: [forwardLine, { ...tunnelLine, mode: undefined, tunnel: undefined, running: undefined, port: 8444 }] }
    api.post.mockResolvedValue({ data: switched })
    mount(<NodesView />)
    const dialog = await editRow()
    fireEvent.change(within(dialog).getAllByLabelText(/admin:nodes.relay.name/)[0], { target: { value: 'Plain edited' } })
    const card = within(dialog).getByTestId('tunnel-card').closest('[data-testid="tunnel-line"]') as HTMLElement
    fireEvent.click(within(card).getByRole('button', { name: 'admin:nodes.tunnel.mode_forward' }))
    await waitFor(() => expect(within(dialog).queryByTestId('tunnel-card')).toBeNull())
    expect(api.post).toHaveBeenCalledWith('/admin/nodes/1/lines/T1/mode', { mode: 'forward' }, expect.anything())
    expect((within(dialog).getAllByLabelText(/admin:nodes.relay.name/)[0] as HTMLInputElement).value).toBe('Plain edited')
  })

  it('opens the listener\'s settings with the line banner and without the fields the server ignores', async () => {
    setup([tunnelLine])
    const childNode = { ...node, id: 77, display_name: 'tunnel-77', parent_node_id: 1, parent_line_id: 'T1', parent_node_name: 'old-name', relays: [] }
    const reads = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string, ...rest: unknown[]) =>
      url === '/admin/nodes/77' ? { data: { node: childNode, clients: [] } } : reads(url, ...rest))
    mount(<NodesView />)
    const dialog = await editRow()
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:nodes.tunnel.open_listener' }))
    const banner = await screen.findByTestId('listener-banner')
    expect(banner.textContent).toContain('admin:nodes.tunnel.listener_banner')
    expect(screen.queryByLabelText(/admin:nodes.field.display_name/)).toBeNull()
    expect(screen.queryByLabelText(/admin:nodes.field.region/)).toBeNull()
    expect(screen.queryByText('admin:nodes.relay.title')).toBeNull()
    expect(screen.queryByTestId('egress-section')).toBeNull()
  })

  it('asks before opening the listener when the editor has unsaved changes', async () => {
    setup([tunnelLine])
    confirmation.mockResolvedValue(false)
    mount(<NodesView />)
    const dialog = await editRow()
    fireEvent.change(within(dialog).getByLabelText(/admin:nodes.field.display_name/), { target: { value: 'renamed' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:nodes.tunnel.open_listener' }))
    await waitFor(() => expect(confirmation).toHaveBeenCalledTimes(1))
    expect(confirmation.mock.calls[0][0].title).toBe('admin:nodes.tunnel.open_discard_title')
    expect(api.get.mock.calls.some(([url]) => url === '/admin/nodes/77')).toBe(false)
    expect((within(dialog).getByLabelText(/admin:nodes.field.display_name/) as HTMLInputElement).value).toBe('renamed')
  })
})
