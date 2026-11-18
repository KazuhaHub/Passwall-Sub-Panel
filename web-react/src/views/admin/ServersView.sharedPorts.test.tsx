// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, server } from '@/test/adminSaveHarness'
import ServersView from './ServersView'

const psp = { ...server, panel_type: 'psp' }

const options = {
  listeners: [
    { node_id: 1, name: 'hk', protocol: 'vless', port: 443, enabled: true, frontable: true, security: 'reality', server_names: ['a.example.com'] },
    { node_id: 2, name: 'ss', protocol: 'shadowsocks', port: 443, enabled: true, frontable: false, reason: 'no_sni', server_names: [] },
  ],
  lines: [],
}

const view = (over: Record<string, unknown> = {}) => ({
  support: { supported: true }, withheld: [], fronts: [], max: 16,
  defaults: { port: 443, loopback_low: 30000, loopback_high: 30999, probe_low: 31000, probe_high: 31999 }, ...over,
})

function install(fronts: unknown, ports: unknown[] = []) {
  installReads({ '/admin/servers': list([psp]) })
  const base = api.get.getMockImplementation()!
  api.get.mockImplementation(async (url: string, cfg?: { params?: { what?: string } }) => {
    if (url === '/admin/servers/1/fronts') return { data: fronts }
    if (url === '/admin/servers/1/fronts/options') return { data: options }
    if (url === '/admin/servers/1/ports') return { data: { ports } }
    if (url === '/admin/servers/1/fronts/suggest') return { data: cfg?.params?.what === 'camouflage' ? { host: 'www.asus.com', port: 443 } : { port: 30012 } }
    return base(url)
  })
}

async function openDialog() {
  const row = (await screen.findByText('server')).closest('tr')!
  fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.shared_ports' }))
  return screen.findByRole('dialog')
}

describe('ServersView shared ports', () => {
  it('offers the dialog for a Passwall Node server only', async () => {
    installReads({ '/admin/servers': list([server]) })
    mount(<ServersView />)
    const row = (await screen.findByText('server')).closest('tr')!
    expect(within(row).queryByRole('button', { name: 'admin:servers.action.shared_ports' })).toBeNull()
  })

  it('says Xray is needed and disables adding when the server cannot run fronts', async () => {
    install(view({ support: { supported: false, reason: 'engine_not_xray' } }))
    mount(<ServersView />)
    const dialog = await openDialog()
    await within(dialog).findByText('admin:servers.fronts.unsupported.engine_not_xray')
    const add = within(dialog).getByRole('button', { name: 'admin:servers.fronts.add' }) as HTMLButtonElement
    expect(add.disabled).toBe(true)
  })

  it('builds a front from the admin\'s own values, pre-fills only on request, and sends the confirmation', async () => {
    install(view())
    api.post.mockResolvedValue({ data: { front: { id: 3 }, warnings: [] } })
    mount(<ServersView />)
    const dialog = await openDialog()
    const add = await within(dialog).findByRole('button', { name: 'admin:servers.fronts.add' })
    await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(add)
    const editor = (await screen.findAllByRole('dialog')).at(-1)!

    // A listener that cannot share says why and cannot be ticked.
    const ss = within(editor).getByLabelText(/ss \(shadowsocks/) as HTMLInputElement
    expect(ss.disabled).toBe(true)
    expect(within(editor).getByText('admin:servers.fronts.refusal.no_sni')).toBeTruthy()

    // Ticking the reality listener pre-fills a loopback port and its names, both editable.
    fireEvent.click(within(editor).getByLabelText(/hk \(vless\+reality/))
    const loopback = await within(editor).findByLabelText('admin:servers.fronts.loopback_of') as HTMLInputElement
    await waitFor(() => expect(loopback.value).toBe('30012'))
    fireEvent.change(loopback, { target: { value: '30500' } })
    expect((within(editor).getByLabelText('admin:servers.fronts.names_of') as HTMLInputElement).value).toBe('a.example.com')

    // The default is empty until typed or randomly filled.
    const save = within(editor).getByRole('button', { name: 'common:actions.save' }) as HTMLButtonElement
    expect(save.disabled).toBe(true)
    fireEvent.click(within(editor).getAllByRole('button', { name: 'admin:servers.fronts.random' }).at(-1)!)
    await waitFor(() => expect((within(editor).getByLabelText('admin:servers.fronts.default_host') as HTMLInputElement).value).toBe('www.asus.com'))

    // Moving a listener that holds the port needs the explicit confirmation.
    expect(save.disabled).toBe(true)
    fireEvent.click(within(editor).getByLabelText('admin:servers.fronts.confirm_label'))
    await waitFor(() => expect(save.disabled).toBe(false))
    fireEvent.click(save)
    const saved = () => api.post.mock.calls.filter(c => c[0] === '/admin/servers/1/fronts')
    await waitFor(() => expect(saved()).toHaveLength(1))
    expect(saved()[0][1]).toEqual({
      port: 443,
      listeners: [{ node_id: 1, loopback_port: 30500 }],
      routes: [{ server_names: ['a.example.com'], target: { kind: 'listener', node_id: 1 } }],
      default: { host: 'www.asus.com', port: 443 },
      confirm_move_listeners: true,
    })
  })

  it('shows the server\'s refusal in the editor, worded by its code', async () => {
    install(view())
    const refusal = Object.assign(new Error('refused'), {
      isAxiosError: true, response: { status: 400, data: { error: 'english', code: 'default_is_self', params: { host: '1.2.3.4', port: 443 } } },
    })
    api.post.mockRejectedValue(refusal)
    mount(<ServersView />)
    const dialog = await openDialog()
    const add = await within(dialog).findByRole('button', { name: 'admin:servers.fronts.add' })
    await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(add)
    const editor = (await screen.findAllByRole('dialog')).at(-1)!
    fireEvent.click(within(editor).getByLabelText(/hk \(vless\+reality/))
    await waitFor(() => expect((within(editor).getByLabelText('admin:servers.fronts.loopback_of') as HTMLInputElement).value).toBe('30012'))
    fireEvent.change(within(editor).getByLabelText('admin:servers.fronts.default_host'), { target: { value: '1.2.3.4' } })
    fireEvent.click(within(editor).getByLabelText('admin:servers.fronts.confirm_label'))
    fireEvent.click(within(editor).getByRole('button', { name: 'common:actions.save' }))
    await within(editor).findByText('admin:servers.fronts.error.default_is_self')
  })

  it('lists every port in the usage tab, with what is behind a shared port', async () => {
    install(view(), [
      { port: 443, bind: 'public', kind: 'front', front_id: 1, label: 'x', enabled: true, behind: [
        { server_names: ['a.example.com'], target: { kind: 'listener', node_id: 1 }, label: 'l', loopback_port: 30001 },
        { server_names: [], target: { kind: 'external', host: 'www.example.com', port: 443 }, label: 'e', is_default: true },
      ] },
      { port: 30001, bind: 'loopback', kind: 'front_loopback', node_name: 'hk', label: 'y', enabled: true },
      { port: 31000, range_high: 31999, bind: 'loopback', kind: 'probe_range', label: 'z', enabled: true },
      { port: 31123, bind: 'loopback', kind: 'probe_loopback', label: 'lnk_1', enabled: true },
    ])
    mount(<ServersView />)
    const dialog = await openDialog()
    fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:servers.fronts.tab_ports' }))
    await within(dialog).findByText('31000–31999')
    await within(dialog).findByText('31123')
    expect(within(dialog).getByText('30001')).toBeTruthy()
    expect(within(dialog).getByText('admin:servers.fronts.ports.route_arrow')).toBeTruthy()
    expect(within(dialog).getByText('admin:servers.fronts.ports.default_arrow')).toBeTruthy()
  })

  it('names a tunnel line\'s listener by its line in the usage tab', async () => {
    install(view(), [
      { port: 40123, bind: 'public', kind: 'listener', node_id: 77, node_name: 'tunnel-77', parent_node_id: 5, parent_name: 'HK-1', line_name: 'Mobile', protocol: 'vless', label: 'x', enabled: true },
      { port: 30001, bind: 'loopback', kind: 'front_loopback', node_id: 77, node_name: 'tunnel-77', parent_node_id: 5, parent_name: 'HK-1', line_name: 'Mobile', label: 'y', enabled: true },
      { port: 443, bind: 'public', kind: 'front', front_id: 1, label: 'z', enabled: true, behind: [
        { server_names: ['a.example.com'], target: { kind: 'listener', node_id: 77 }, label: 'l', loopback_port: 30001, parent_node_id: 5, parent_name: 'HK-1', line_name: 'Mobile' },
      ] },
    ])
    mount(<ServersView />)
    const dialog = await openDialog()
    fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:servers.fronts.tab_ports' }))
    await within(dialog).findByText('admin:servers.fronts.ports.kind.tunnel_listener')
    expect(within(dialog).getByText('admin:servers.fronts.ports.kind.tunnel_front_loopback')).toBeTruthy()
    expect(within(dialog).queryByText('admin:servers.fronts.ports.kind.listener')).toBeNull()
    expect(within(dialog).getByText('admin:servers.fronts.ports.route_arrow')).toBeTruthy()
  })

  it('marks saved fronts as not in effect, with the reason, when the server is not on Xray', async () => {
    const saved = { id: 7, port: 443, listeners: [], routes: [{ server_names: ['a.example.com'], target: { kind: 'forward', node_id: 5, line_id: 'x' } }], default: { kind: 'external', host: 'www.example.com', port: 443 } }
    install(view({ support: { supported: false, reason: 'engine_not_xray' }, fronts: [saved] }))
    mount(<ServersView />)
    const dialog = await openDialog()
    await within(dialog).findByText('admin:servers.fronts.inactive')
    expect(within(dialog).getByText('admin:servers.fronts.unsupported.engine_not_xray')).toBeTruthy()
    // Editing is off; deleting stays possible.
    await waitFor(() => expect((within(dialog).getByRole('button', { name: 'admin:servers.fronts.edit_port' }) as HTMLButtonElement).disabled).toBe(true))
  })

  it('shows a saved front that is withheld, with the reason', async () => {
    const saved = { id: 7, port: 443, listeners: [], routes: [{ server_names: ['a.example.com'], target: { kind: 'forward', node_id: 5, line_id: 'x' } }], default: { kind: 'external', host: 'www.example.com', port: 443 } }
    install(view({ fronts: [saved], withheld: [{ front_id: 7, port: 443, reason: 'listener_not_behind', detail: 'stray' }] }))
    mount(<ServersView />)
    const dialog = await openDialog()
    await within(dialog).findByText('admin:servers.fronts.withheld.text')
    expect(within(dialog).getByText('admin:servers.fronts.inactive')).toBeTruthy()
  })
})
