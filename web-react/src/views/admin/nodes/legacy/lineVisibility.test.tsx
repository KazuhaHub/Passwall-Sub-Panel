// @vitest-environment jsdom
import { fireEvent, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, installReads, list, mount, node } from '@/test/adminSaveHarness'
import { LegacyProbe, openMeta } from './legacyHarness'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const visibilityCalls = () => api.post.mock.calls.filter(([url]) => String(url) === '/admin/nodes/1/line-visibility')

it('previews which groups would see the node with the unsaved address, region and tags, and the one it would leave', async () => {
  const configured = { ...node, region: 'HK', tags: ['edge'], relays: [
    { id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true, region: 'CN', tags: ['cmcc'] },
  ] }
  installReads({ '/admin/nodes': list([configured]) })
  api.post.mockImplementation(async (url: string) => String(url).endsWith('/line-visibility')
    ? { data: { groups: [{ id: 1, name: 'VIP' }, { id: 2, name: 'Staff' }], node_groups: [1], saved_node_groups: [1, 2], relays: [[1]] } }
    : { data: {} })
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  // ADR 0042 §2: the node's own groups, and the one this draft would leave.
  await waitFor(() => expect(within(dialog).getByTestId('node-groups').textContent).toBe('admin:nodes.node_groups.visible'))
  expect(within(dialog).getByTestId('node-groups-removed').textContent).toBe('admin:nodes.node_groups.removed')
  expect(visibilityCalls().at(-1)![1]).toEqual({ server_address: configured.server_address, region: 'HK', tags: ['edge'], relays: [{ id: 'gz', region: 'CN', tags: ['cmcc'] }] })
  fireEvent.change(within(dialog).getByDisplayValue(configured.server_address), { target: { value: 'new.example' } })
  await waitFor(() => expect(visibilityCalls().at(-1)![1].server_address).toBe('new.example'))
  expect(api.put).not.toHaveBeenCalled()
})

it('shows "unknown" rather than "no group" when the node\'s preview cannot be read', async () => {
  const configured = { ...node, relays: [{ id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true, region: 'US', tags: [] }] }
  installReads({ '/admin/nodes': list([configured]) })
  api.post.mockRejectedValue(new Error('down'))
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  await waitFor(() => expect(within(dialog).getByTestId('node-groups').textContent).toBe('admin:nodes.line_labels.visibility_unknown'))
  expect(within(dialog).queryByTestId('node-groups-removed')).toBeNull()
})
