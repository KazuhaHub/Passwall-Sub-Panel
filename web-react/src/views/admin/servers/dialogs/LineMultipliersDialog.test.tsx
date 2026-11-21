// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, node, server, snack } from '../testHarness'
import { LineMultipliersDialog } from './LineMultipliersDialog'

const hk = { ...node, id: 1, panel_id: 1, display_name: 'hk', traffic_factor_milli: 1000, relays: [{ id: 'r1', name: 'GZ', address: 'gz.example', port: 0, enabled: true, traffic_factor_milli: 500 }] }
const elsewhere = { ...node, id: 2, panel_id: 9, display_name: 'elsewhere', relays: [] }
const open = () => mount(<LineMultipliersDialog server={server as never} onClose={() => {}} />)
const apply = () => screen.getByRole('button', { name: 'admin:servers.line_factors.apply' }) as HTMLButtonElement
const factor = () => screen.getByLabelText('admin:servers.line_factors.field') as HTMLInputElement

describe('LineMultipliersDialog', () => {
  it('lists only this server\'s lines and sends the selected ones with one factor', async () => {
    installReads({ '/admin/nodes': list([hk, elsewhere]) })
    api.put.mockResolvedValue({ data: { changed: 2 } })
    open()
    await screen.findByText('hk')
    expect(screen.queryByText('elsewhere')).toBeNull()
    expect(screen.getByText('×0.5')).toBeTruthy()
    fireEvent.click(screen.getByRole('checkbox', { name: 'admin:servers.line_factors.select_all' }))
    fireEvent.change(factor(), { target: { value: '1.5' } })
    fireEvent.click(apply())
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][0]).toBe('/admin/servers/1/line-factors')
    expect(api.put.mock.calls[0][1]).toEqual({ lines: [
      { node_id: 1, line_id: 'direct', traffic_factor_milli: 1500 },
      { node_id: 1, line_id: 'r1', traffic_factor_milli: 1500 },
    ] })
    await waitFor(() => expect(snack).toHaveBeenCalledWith('admin:servers.line_factors.updated', 'success'))
  })

  it('keeps Apply off until a line is ticked and the multiplier is valid, and says why', async () => {
    installReads({ '/admin/nodes': list([{ ...hk, relays: [] }]) })
    open()
    await screen.findByText('hk')
    expect(apply().disabled).toBe(true)
    fireEvent.change(factor(), { target: { value: '0.0004' } })
    // Not yet said: the box was not left and nothing is ticked.
    expect(screen.queryByText('admin:servers.line_factors.invalid')).toBeNull()
    fireEvent.click(screen.getByRole('checkbox', { name: /hk/ }))
    expect(screen.getByText('admin:servers.line_factors.invalid')).toBeTruthy()
    expect(apply().disabled).toBe(true)
    fireEvent.change(factor(), { target: { value: '0.5' } })
    expect(apply().disabled).toBe(false)
  })

  it('says when the lines could not be loaded, with a retry, instead of the empty message', async () => {
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/nodes') throw new Error('boom')
      return { data: list([]) }
    })
    open()
    await screen.findByText('admin:servers.line_factors.load_failed')
    expect(screen.queryByText('admin:servers.line_factors.empty')).toBeNull()
  })

  it('says an apply failure in the dialog', async () => {
    installReads({ '/admin/nodes': list([{ ...hk, relays: [] }]) })
    api.put.mockRejectedValue(new Error('nope'))
    open()
    await screen.findByText('hk')
    fireEvent.click(screen.getByRole('checkbox', { name: /hk/ }))
    fireEvent.click(apply())
    await screen.findByText('admin:servers.line_factors.apply_failed')
  })
})
