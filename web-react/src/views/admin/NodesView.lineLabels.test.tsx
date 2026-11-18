// @vitest-environment jsdom
import { fireEvent, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, editRow, installReads, list, mount, node } from '@/test/adminSaveHarness'
import NodesView from './NodesView'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const relayWrites = () => api.put.mock.calls.filter(([url]) => String(url) === '/admin/nodes/1/metadata').map(([, body]) => body)

it('round-trips saved relay labels and prefills a new relay from the node form', async () => {
  const configured = { ...node, region: 'HK', tags: ['edge'], relays: [{ id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true, region: 'CN', tags: ['cmcc'] }] }
  installReads({ '/admin/nodes': list([configured]) })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...configured, ...body } }))
  mount(<NodesView />)
  const dialog = await editRow()
  expect(within(dialog).getByText('admin:nodes.line_labels.node_change_hint')).toBeTruthy()
  const regions = within(dialog).getAllByLabelText('admin:nodes.line_labels.region') as HTMLInputElement[]
  expect(regions.map(input => input.value)).toEqual(['CN'])
  // The node's own region changes in the same edit; the new relay copies the
  // form's current value, the saved relay keeps its own.
  fireEvent.change(within(dialog).getByLabelText(/admin:nodes.field.region/), { target: { value: 'JP' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'admin:nodes.relay.add' }))
  const after = within(dialog).getAllByLabelText('admin:nodes.line_labels.region') as HTMLInputElement[]
  expect(after.map(input => input.value)).toEqual(['CN', 'JP'])
  fireEvent.change(within(dialog).getAllByLabelText(/admin:nodes.relay.address/)[1], { target: { value: 'sh.example' } })
  fireEvent.change(after[0], { target: { value: ' cn ' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(relayWrites()).toHaveLength(1))
  const relays = relayWrites()[0].relays
  expect(relays[0]).toMatchObject({ id: 'gz', region: 'cn', tags: ['cmcc'] })
  expect(relays[1]).toMatchObject({ address: 'sh.example', region: 'JP', tags: ['edge'] })
})

it('keeps a relay without loaded labels free of label fields so the server keeps its value', async () => {
  const legacy = { ...node, relays: [{ id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true }] }
  installReads({ '/admin/nodes': list([legacy]) })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...legacy, ...body } }))
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.change(within(dialog).getByLabelText(/admin:nodes.field.display_name/), { target: { value: 'renamed' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(relayWrites()).toHaveLength(1))
  const relay = relayWrites()[0].relays[0]
  expect(relay).not.toHaveProperty('region')
  expect(relay).not.toHaveProperty('tags')
})

it('shows no node hint for a node without lines', async () => {
  installReads({ '/admin/nodes': list([node]) })
  mount(<NodesView />)
  const dialog = await editRow()
  expect(within(dialog).queryByText('admin:nodes.line_labels.node_change_hint')).toBeNull()
})
