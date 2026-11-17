// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, node, server } from '@/test/adminSaveHarness'
import ServersView from './ServersView'

describe('ServersView line multipliers', () => {
  it('sends the selected lines of this server with one factor and reports the count', async () => {
    const nodes = [
      { ...node, id: 1, panel_id: 1, display_name: 'hk', traffic_factor_milli: 1000, relays: [{ id: 'r1', name: 'GZ', address: 'gz.example', port: 0, enabled: true, traffic_factor_milli: 500 }] },
      { ...node, id: 2, panel_id: 9, display_name: 'elsewhere', relays: [] },
    ]
    installReads({ '/admin/nodes': list(nodes), '/admin/servers': list([server]) })
    api.put.mockResolvedValue({ data: { changed: 2 } })
    mount(<ServersView />)
    const row = (await screen.findByText('server')).closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.line_factors' }))
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByText('hk')
    expect(within(dialog).queryByText('elsewhere')).toBeNull()
    fireEvent.click(within(dialog).getByLabelText('admin:servers.line_factors.select_all'))
    fireEvent.change(within(dialog).getByLabelText('admin:servers.line_factors.field'), { target: { value: '1.5' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:servers.line_factors.apply' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][0]).toBe('/admin/servers/1/line-factors')
    expect(api.put.mock.calls[0][1]).toEqual({ lines: [
      { node_id: 1, line_id: 'direct', traffic_factor_milli: 1500 },
      { node_id: 1, line_id: 'r1', traffic_factor_milli: 1500 },
    ] })
  })
})

describe('ServersView line multipliers dialog states', () => {
  async function open() {
    const row = (await screen.findByText('server')).closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.line_factors' }))
    return screen.findByRole('dialog')
  }
  const nodes = [{ ...node, id: 1, panel_id: 1, display_name: 'hk', relays: [] }]

  it('does not fetch nodes until opened, and keeps Apply disabled until a line is selected', async () => {
    installReads({ '/admin/nodes': list(nodes) })
    mount(<ServersView />)
    await screen.findByText('server')
    expect(api.get.mock.calls.some(c => c[0] === '/admin/nodes')).toBe(false)
    const dialog = await open()
    await within(dialog).findByText('hk')
    const apply = within(dialog).getByRole('button', { name: 'admin:servers.line_factors.apply' }) as HTMLButtonElement
    expect(apply.disabled).toBe(true)
    fireEvent.change(within(dialog).getByLabelText('admin:servers.line_factors.field'), { target: { value: '0.0004' } })
    fireEvent.click(within(dialog).getByLabelText(/hk/))
    expect(apply.disabled).toBe(true)
    fireEvent.change(within(dialog).getByLabelText('admin:servers.line_factors.field'), { target: { value: '0.5' } })
    expect(apply.disabled).toBe(false)
  })

  it('shows a load error instead of the empty message', async () => {
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/nodes') throw new Error('boom')
      return { data: url === '/admin/servers' ? list([server]) : list([]) }
    })
    mount(<ServersView />)
    const dialog = await open()
    await within(dialog).findByText('admin:servers.line_factors.load_failed')
    expect(within(dialog).queryByText('admin:servers.line_factors.empty')).toBeNull()
  })

  it('shows an apply failure without an unhandled rejection', async () => {
    installReads({ '/admin/nodes': list(nodes) })
    api.put.mockRejectedValue(new Error('nope'))
    mount(<ServersView />)
    const dialog = await open()
    await within(dialog).findByText('hk')
    fireEvent.click(within(dialog).getByLabelText(/hk/))
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:servers.line_factors.apply' }))
    await within(dialog).findByText('admin:servers.line_factors.apply_failed')
  })
})

describe('ServersView client identities', () => {
  it('shows the count and a warning chip only when flagged', async () => {
    installReads({ '/admin/servers': list([
      { ...server, id: 1, name: 'calm', client_identities: 12 },
      { ...server, id: 2, name: 'busy', client_identities: 21000, client_identities_warning: true },
    ]) })
    mount(<ServersView />)
    const busy = (await screen.findByText('busy', { exact: false })).closest('td')!
    const calm = (await screen.findByText('calm', { exact: false })).closest('td')!
    expect(within(busy).getByTestId('client-identities-warning')).toBeTruthy()
    expect(within(calm).queryByTestId('client-identities-warning')).toBeNull()
    expect(within(calm).getByTestId('client-identities')).toBeTruthy()
  })
})
