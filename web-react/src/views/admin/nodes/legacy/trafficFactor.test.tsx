// @vitest-environment jsdom
import { fireEvent, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, installReads, list, mount, node } from '@/test/adminSaveHarness'
import { LegacyProbe, openMeta } from './legacyHarness'

vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn() }))

const writes = () => api.put.mock.calls.filter(([url]) => String(url) === '/admin/nodes/1/metadata').map(([, body]) => body)
const configured = {
  ...node, traffic_factor_milli: 1500,
  relays: [{ id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true, traffic_factor_milli: 250 }],
}

it('shows factors as decimals and omits untouched direct and relay factors so a concurrent change is not reverted', async () => {
  installReads({ '/admin/nodes': list([configured]) })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...configured, ...body } }))
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  const inputs = within(dialog).getAllByLabelText(/admin:nodes.field.traffic_factor/) as HTMLInputElement[]
  expect(inputs.map(i => i.value).sort()).toEqual(['0.25', '1.5'])
  fireEvent.change(within(dialog).getByLabelText(/admin:nodes.field.display_name/), { target: { value: 'renamed' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(writes()).toHaveLength(1))
  expect(writes()[0]).not.toHaveProperty('traffic_factor_milli')
  expect(writes()[0].relays[0].id).toBe('gz')
  expect(writes()[0].relays[0]).not.toHaveProperty('traffic_factor_milli')
})

it('sends a relay factor only after that relay was edited', async () => {
  installReads({ '/admin/nodes': list([configured]) })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...configured, ...body } }))
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  const relayInput = (within(dialog).getAllByLabelText(/admin:nodes.field.traffic_factor/) as HTMLInputElement[]).find(i => i.value === '0.25')!
  fireEvent.change(relayInput, { target: { value: '0.5' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(writes()).toHaveLength(1))
  expect(writes()[0].relays[0].traffic_factor_milli).toBe(500)
  expect(writes()[0]).not.toHaveProperty('traffic_factor_milli')
})

it('blocks saving while a multiplier is invalid or has too many decimals', async () => {
  installReads({ '/admin/nodes': list([configured]) })
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  const direct = within(dialog).getByLabelText('admin:nodes.field.traffic_factor_direct')
  for (const bad of ['', '0.0004', '101']) {
    fireEvent.change(direct, { target: { value: bad } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  }
  expect(api.put).not.toHaveBeenCalled()
})

it('sends the direct factor as milli when it changes', async () => {
  installReads({ '/admin/nodes': list([configured]) })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...configured, ...body } }))
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  fireEvent.change(within(dialog).getByLabelText('admin:nodes.field.traffic_factor_direct'), { target: { value: '0' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(writes()).toHaveLength(1))
  expect(writes()[0].traffic_factor_milli).toBe(0)
  expect(writes()[0].relays[0]).not.toHaveProperty('traffic_factor_milli')
})
