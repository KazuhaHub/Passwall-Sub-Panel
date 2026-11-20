// @vitest-environment jsdom
import { fireEvent, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, installReads, list, mount, node } from '@/test/adminSaveHarness'
import { LegacyProbe, openMeta } from './legacyHarness'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const visibilityCalls = () => api.post.mock.calls.filter(([url]) => String(url) === '/admin/nodes/1/line-visibility')

it('previews each line\'s visible groups from the unsaved draft and flags a line no group sees', async () => {
  const configured = { ...node, region: 'HK', tags: ['edge'], relays: [
    { id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true, region: 'CN', tags: ['cmcc'] },
    { id: 'sh', name: 'SH', address: 'sh.example', port: 0, enabled: true, region: 'HK', tags: ['edge'] },
  ] }
  installReads({ '/admin/nodes': list([configured]) })
  api.post.mockImplementation(async (url: string) => String(url).endsWith('/line-visibility')
    ? { data: { groups: [{ id: 1, name: 'VIP' }, { id: 2, name: 'Staff' }], node_groups: [1], saved_node_groups: [1, 2], relays: [[], [1]] } }
    : { data: {} })
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  await waitFor(() => expect(within(dialog).getAllByTestId('line-visibility')).toHaveLength(2))
  const captions = within(dialog).getAllByTestId('line-visibility').map(el => el.textContent)
  expect(captions).toEqual(['admin:nodes.line_labels.visible_none', 'admin:nodes.line_labels.visible_groups'])
  // ADR 0042 §2: the node's own groups, and the one this draft would leave.
  expect(within(dialog).getByTestId('node-groups').textContent).toBe('admin:nodes.node_groups.visible')
  expect(within(dialog).getByTestId('node-groups-removed').textContent).toBe('admin:nodes.node_groups.removed')
  const body = visibilityCalls().at(-1)![1]
  expect(body).toEqual({ server_address: configured.server_address, region: 'HK', tags: ['edge'], relays: [{ id: 'gz', region: 'CN', tags: ['cmcc'] }, { id: 'sh', region: 'HK', tags: ['edge'] }] })

  // Editing a label re-asks with the draft value, without saving anything.
  const before = visibilityCalls().length
  fireEvent.change(within(dialog).getAllByLabelText('admin:nodes.line_labels.region')[0], { target: { value: 'HK' } })
  await waitFor(() => expect(visibilityCalls().length).toBeGreaterThan(before))
  expect(visibilityCalls().at(-1)![1].relays[0]).toEqual({ id: 'gz', region: 'HK', tags: ['cmcc'] })
  fireEvent.change(within(dialog).getByDisplayValue(configured.server_address), { target: { value: 'new.example' } })
  await waitFor(() => expect(visibilityCalls().at(-1)![1].server_address).toBe('new.example'))
  expect(api.put).not.toHaveBeenCalled()
})

it('shows "unknown" rather than "no group" when the preview cannot be read', async () => {
  const configured = { ...node, relays: [{ id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true, region: 'US', tags: [] }] }
  installReads({ '/admin/nodes': list([configured]) })
  api.post.mockRejectedValue(new Error('down'))
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  await waitFor(() => expect(within(dialog).getByTestId('line-visibility').textContent).toBe('admin:nodes.line_labels.visibility_unknown'))
  expect(within(dialog).getByTestId('node-groups').textContent).toBe('admin:nodes.line_labels.visibility_unknown')
  expect(within(dialog).queryByTestId('node-groups-removed')).toBeNull()
})
