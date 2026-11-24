// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { AxiosError } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import type { Server } from '@/api/servers'
import { api, mountAdmin, toast } from '../adminTestHarness'
import SharedPortsDialog from './SharedPortsDialog'

vi.mock('@/i18n', () => ({ default: { t: (k: string) => k } }))

const server = { id: 1, name: 'server', panel_type: 'psp' } as unknown as Server

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
const saved = { id: 7, port: 443, listeners: [], routes: [{ server_names: ['a.example.com'], target: { kind: 'forward', node_id: 5, line_id: 'x' } }], default: { kind: 'external', host: 'www.example.com', port: 443 } }
const refusal = (status: number, data: Record<string, unknown>) => new AxiosError('x', String(status), undefined, undefined, { status, statusText: '', headers: {}, config: {} as never, data })

function install(fronts: unknown, ports: unknown[] = [], opts: unknown = options) {
  api.get.mockImplementation(async (url: string, cfg?: { params?: { what?: string } }) => {
    if (url === '/admin/servers/1/fronts') return { data: fronts }
    if (url === '/admin/servers/1/fronts/options') return { data: opts }
    if (url === '/admin/servers/1/ports') return { data: { ports } }
    if (url === '/admin/servers/1/fronts/suggest') return { data: cfg?.params?.what === 'camouflage' ? { host: 'www.asus.com', port: 443 } : { port: 30012 } }
    throw new Error(`Unexpected GET ${url}`)
  })
}
const open = async () => { mountAdmin(<SharedPortsDialog server={server} onClose={vi.fn()} />); return screen.findByRole('dialog') }
const addButton = (d: HTMLElement) => within(d).findByRole('button', { name: 'Add shared port' })
async function openEditorForNew() {
  const dialog = await open()
  const add = await addButton(dialog)
  await waitFor(() => expect(add.getAttribute('aria-disabled')).toBeNull())
  fireEvent.click(add)
  await screen.findByRole('heading', { name: 'Add shared port' }).catch(() => {})
  return dialog
}

describe('Shared ports dialog', () => {
  it('says Xray is needed and turns adding off, with the reason, when the server cannot run fronts', async () => {
    install(view({ support: { supported: false, reason: 'engine_not_xray' } }))
    const dialog = await open()
    await within(dialog).findByText('SNI shared ports need Xray')
    const add = await addButton(dialog)
    expect(add.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(add)
    expect(within(dialog).queryByLabelText('Shared port')).toBeNull()
  })

  it('builds a front from the admin\'s own values, pre-fills only on request, and sends the confirmation', async () => {
    install(view())
    api.post.mockResolvedValue({ data: { front: { id: 3 }, warnings: [] } })
    const dialog = await openEditorForNew()

    // A listener that cannot share says why and cannot be ticked.
    const ss = await within(dialog).findByRole('checkbox', { name: /ss \(shadowsocks/ })
    expect(ss.getAttribute('aria-disabled') ?? ss.getAttribute('disabled')).not.toBeNull()
    expect(within(dialog).getByText(/No readable SNI/)).toBeTruthy()

    // Ticking the reality listener pre-fills a loopback port and its names, both editable.
    fireEvent.click(within(dialog).getByRole('checkbox', { name: /hk \(vless\+reality/ }))
    const loopback = await within(dialog).findByLabelText('Loopback port of hk') as HTMLInputElement
    await waitFor(() => expect(loopback.value).toBe('30012'))
    fireEvent.change(loopback, { target: { value: '30500' } })
    expect((within(dialog).getByLabelText('SNI names of route 1') as HTMLInputElement).value).toBe('a.example.com')

    // The default is empty until typed or randomly filled, and Save says what is missing.
    expect((within(dialog).getByLabelText('Camouflage site') as HTMLInputElement).value).toBe('')
    const save = within(dialog).getByRole('button', { name: 'Save' })
    expect(save.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(within(dialog).getAllByRole('button', { name: 'Random' }).at(-1)!)
    await waitFor(() => expect((within(dialog).getByLabelText('Camouflage site') as HTMLInputElement).value).toBe('www.asus.com'))

    // Moving a listener that holds the port needs the explicit confirmation.
    expect(save.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(within(dialog).getByRole('checkbox', { name: 'I confirm the endpoint changes and reconnection' }))
    await waitFor(() => expect(save.getAttribute('aria-disabled')).toBeNull())
    fireEvent.click(save)
    const posts = () => api.post.mock.calls.filter(c => c[0] === '/admin/servers/1/fronts')
    await waitFor(() => expect(posts()).toHaveLength(1))
    expect(posts()[0][1]).toEqual({
      port: 443,
      listeners: [{ node_id: 1, loopback_port: 30500 }],
      routes: [{ server_names: ['a.example.com'], target: { kind: 'listener', node_id: 1 } }],
      default: { host: 'www.asus.com', port: 443 },
      confirm_move_listeners: true,
    })
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Shared port saved', 'success'))
  })

  it('shows what is wrong beside the field once it is left, and all of it in the bar', async () => {
    install(view())
    const dialog = await openEditorForNew()
    fireEvent.click(await within(dialog).findByRole('checkbox', { name: /hk \(vless\+reality/ }))
    const loopback = await within(dialog).findByLabelText('Loopback port of hk') as HTMLInputElement
    await waitFor(() => expect(loopback.value).toBe('30012'))
    fireEvent.change(loopback, { target: { value: '443' } })
    expect(within(dialog).queryByText(/must be 1 to 65535 and differ/)).toBeNull()
    fireEvent.blur(loopback)
    expect(await within(dialog).findByText(/must be 1 to 65535 and differ from the shared port/)).toBeTruthy()
    expect(loopback.getAttribute('aria-invalid')).toBe('true')
  })

  it('shows the server\'s refusal under the form, worded by its code, and keeps the draft', async () => {
    install(view())
    api.post.mockRejectedValue(refusal(400, { error: 'english', code: 'default_is_self', params: { host: '1.2.3.4', port: 443 } }))
    const dialog = await openEditorForNew()
    fireEvent.click(await within(dialog).findByRole('checkbox', { name: /hk \(vless\+reality/ }))
    await waitFor(() => expect((within(dialog).getByLabelText('Loopback port of hk') as HTMLInputElement).value).toBe('30012'))
    fireEvent.change(within(dialog).getByLabelText('Camouflage site'), { target: { value: '1.2.3.4' } })
    fireEvent.click(within(dialog).getByRole('checkbox', { name: 'I confirm the endpoint changes and reconnection' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await within(dialog).findByText(/is this server's own public address/)).toBeTruthy()
    expect((within(dialog).getByLabelText('Camouflage site') as HTMLInputElement).value).toBe('1.2.3.4')
  })

  it('draws the routing of saved ports and lists them with one action and a menu per row', async () => {
    install(view({ fronts: [saved] }))
    const dialog = await open()
    const graph = await within(dialog).findByRole('region', { name: 'Routing of shared ports' })
    expect(within(graph).getAllByText('Port 443').length).toBeGreaterThan(0)
    expect(within(graph).getByText('a.example.com')).toBeTruthy()
    expect(within(graph).getByText('Other or no SNI')).toBeTruthy()
    const row = within(dialog).getByText('Port 443', { selector: 'td' }).closest('tr')!
    expect(within(row).getByRole('button', { name: 'Edit port 443' })).toBeTruthy()
    expect(within(row).getByRole('button', { name: 'More' })).toBeTruthy()
    expect(within(row).getByText('In effect')).toBeTruthy()
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
    const dialog = await open()
    fireEvent.click(await within(dialog).findByRole('tab', { name: 'Port usage' }))
    await within(dialog).findByText('31000–31999')
    expect(within(dialog).getByText('31123')).toBeTruthy()
    expect(within(dialog).getByText('30001')).toBeTruthy()
    expect(within(dialog).getByText('a.example.com → listener on this server (loopback port 30001)')).toBeTruthy()
    expect(within(dialog).getByText('Other / no SNI → external site www.example.com:443')).toBeTruthy()
  })

  it('names a tunnel line\'s listener by its line in the usage tab', async () => {
    install(view(), [
      { port: 40123, bind: 'public', kind: 'listener', node_id: 77, node_name: 'tunnel-77', parent_node_id: 5, parent_name: 'HK-1', line_name: 'Mobile', protocol: 'vless', label: 'x', enabled: true },
      { port: 30001, bind: 'loopback', kind: 'front_loopback', node_id: 77, node_name: 'tunnel-77', parent_node_id: 5, parent_name: 'HK-1', line_name: 'Mobile', label: 'y', enabled: true },
    ])
    const dialog = await open()
    fireEvent.click(await within(dialog).findByRole('tab', { name: 'Port usage' }))
    await within(dialog).findByText('Tunnel line Mobile of HK-1 (vless)')
    expect(within(dialog).getByText('Tunnel line Mobile of HK-1 (behind the shared port)')).toBeTruthy()
    expect(within(dialog).queryByText(/Inbound of node/)).toBeNull()
  })

  it('marks saved fronts as not in effect, with the reason, when the server is not on Xray; editing is off, deleting stays', async () => {
    install(view({ support: { supported: false, reason: 'engine_not_xray' }, fronts: [saved] }))
    const dialog = await open()
    await within(dialog).findByText('Not in effect')
    expect(within(dialog).getByText('SNI shared ports need Xray')).toBeTruthy()
    const edit = within(dialog).getByRole('button', { name: 'Edit port 443' })
    expect(edit.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(edit)
    expect(within(dialog).queryByLabelText('Shared port')).toBeNull()
    expect(within(dialog).getByRole('button', { name: 'More' })).toBeTruthy()
  })

  it('shows a saved front that is withheld, with the reason', async () => {
    install(view({ fronts: [saved], withheld: [{ front_id: 7, port: 443, reason: 'listener_not_behind', detail: 'stray' }] }))
    const dialog = await open()
    await within(dialog).findByText(/Port 443 is saved but not being sent: an enabled listener on this port is not behind it/)
    expect(within(dialog).getByText('Not in effect')).toBeTruthy()
  })
})

describe('Deleting a shared port', () => {
  async function toDelete() {
    install(view({ fronts: [saved] }))
    const dialog = await open()
    const row = (await within(dialog).findByText('Port 443', { selector: 'td' })).closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: 'More' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete port 443' }))
    return screen.findByRole('alertdialog')
  }
  const plan = refusal(409, { error: 'e', code: 'confirm_required', params: { action: 'delete', port: 443, leaving_listeners: ['hk'], leaving_lines: ['5/x'], tunnels: [] } })

  it('asks first, naming the port, focuses Cancel, and sends nothing until yes', async () => {
    const ask = await toDelete()
    expect(within(ask).getByText('Delete shared port 443')).toBeTruthy()
    await waitFor(() => expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' })))
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(within(ask).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('deletes a port with nothing behind it after the first yes', async () => {
    const ask = await toDelete()
    api.delete.mockResolvedValue({ data: {} })
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/servers/1/fronts/7', expect.objectContaining({ params: undefined })))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Shared port deleted', 'success'))
  })

  it('shows the server\'s plan as its own dialog with what is affected, and resends with the confirmation only after yes', async () => {
    const ask = await toDelete()
    api.delete.mockRejectedValueOnce(plan).mockResolvedValue({ data: {} })
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    const planDialog = await screen.findByRole('alertdialog', { name: 'Delete shared port 443' })
    await waitFor(() => expect(within(planDialog).getByTestId('front-plan-text').textContent).toContain('Deleting the shared port 443'))
    expect(within(planDialog).getByText('hk')).toBeTruthy()
    expect(within(planDialog).getByText('5/x')).toBeTruthy()
    expect(api.delete).toHaveBeenCalledTimes(1)
    fireEvent.click(within(planDialog).getByRole('button', { name: 'Delete port 443' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(2))
    expect(api.delete.mock.calls[1][1]).toEqual(expect.objectContaining({ params: { confirm: 'true' } }))
  })

  it('does nothing more when the plan is declined', async () => {
    const ask = await toDelete()
    api.delete.mockRejectedValue(plan)
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    const planDialog = await screen.findByRole('alertdialog', { name: 'Delete shared port 443' })
    fireEvent.click(within(planDialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.delete).toHaveBeenCalledTimes(1)
  })
})

describe('Editing a shared port', () => {
  it('closing with unsaved edits asks first; going back does too', async () => {
    install(view({ fronts: [saved] }), [], { listeners: [], lines: [{ node_id: 5, node_name: 'n', line_id: 'x', line_name: 'GZ', port: 443, enabled: true, server_names: ['a.example.com'] }] })
    const dialog = await open()
    const edit = await within(dialog).findByRole('button', { name: 'Edit port 443' })
    await waitFor(() => expect(edit.getAttribute('aria-disabled')).toBeNull())
    fireEvent.click(edit)
    const names = await within(dialog).findByLabelText('SNI names of route 1')
    fireEvent.change(names, { target: { value: 'b.example.com' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Shared ports' }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText('Discard unsaved changes?')).toBeTruthy()
    fireEvent.click(within(ask).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect((within(dialog).getByLabelText('SNI names of route 1') as HTMLInputElement).value).toBe('b.example.com')
    // The PUT is the same request as before.
    api.put.mockResolvedValue({ data: { front: saved, warnings: [] } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][0]).toBe('/admin/servers/1/fronts/7')
    expect(api.put.mock.calls[0][1]).toMatchObject({ port: 443, routes: [{ server_names: ['b.example.com'], target: { kind: 'forward', node_id: 5, line_id: 'x' } }], confirm_move_listeners: false })
  })
})
