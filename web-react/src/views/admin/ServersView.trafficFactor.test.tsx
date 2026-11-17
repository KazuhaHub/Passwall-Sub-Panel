// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, server } from '@/test/adminSaveHarness'
import { nativeServer } from '@/test/installationHarness'
import ServersView from './ServersView'

async function openEdit(name: string) {
  const row = (await screen.findByText(name)).closest('tr')!
  fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.edit' }))
  return screen.findByRole('dialog')
}

describe('ServersView traffic multiplier', () => {
  it('shows a badge only when the factor differs from 1000', async () => {
    installReads({ '/admin/servers': list([
      { ...server, id: 1, name: 'plain', traffic_factor_milli: 1000 },
      { ...server, id: 2, name: 'pricey', traffic_factor_milli: 1500 },
      { ...server, id: 3, name: 'gratis', traffic_factor_milli: 0 },
    ]) })
    mount(<ServersView />)
    const cellOf = async (name: string) => (await screen.findByText(name, { exact: false })).closest('td')!
    expect(within(await cellOf('plain')).queryByTestId('traffic-factor-badge')).toBeNull()
    expect(within(await cellOf('pricey')).getByTestId('traffic-factor-badge').textContent).toBe('×1.5')
    expect(within(await cellOf('gratis')).getByTestId('traffic-factor-badge').textContent).toBe('common:traffic_factor.free')
  })

  it('shows the multiplier as a decimal and sends milli on a Passwall Node edit only when changed', async () => {
    const saved = { ...nativeServer, traffic_factor_milli: 1500 }
    installReads({ '/admin/servers': list([saved]) })
    api.put.mockResolvedValue({ data: saved })
    mount(<ServersView />)

    let dialog = await openEdit(nativeServer.name)
    const input = within(dialog).getByLabelText('admin:servers.field.traffic_factor') as HTMLInputElement
    expect(input.value).toBe('1.5')
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][1]).not.toHaveProperty('traffic_factor_milli')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    dialog = await openEdit(nativeServer.name)
    fireEvent.change(within(dialog).getByLabelText('admin:servers.field.traffic_factor'), { target: { value: '0.5' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
    expect(api.put.mock.calls[1][0]).toBe('/admin/servers/7')
    expect(api.put.mock.calls[1][1]).toMatchObject({ traffic_factor_milli: 500 })
  })

  it('rejects an out-of-range multiplier without sending', async () => {
    installReads({ '/admin/servers': list([{ ...server, traffic_factor_milli: 1000 }]) })
    mount(<ServersView />)
    const dialog = await openEdit('server')
    fireEvent.change(within(dialog).getByLabelText('admin:servers.field.traffic_factor'), { target: { value: '101' } })
    expect(within(dialog).getByText('admin:servers.validate.traffic_factor')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    expect(api.put).not.toHaveBeenCalled()
  })
})
