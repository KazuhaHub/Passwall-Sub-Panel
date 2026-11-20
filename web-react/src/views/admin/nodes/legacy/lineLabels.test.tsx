// @vitest-environment jsdom
import { within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { installReads, list, mount, node } from '@/test/adminSaveHarness'
import { LegacyProbe, openMeta } from './legacyHarness'

vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn() }))

// The lines are edited in lines/ now; this dialog only reminds that its region and tags do not reach lines that exist.
it('says a change of the node\'s region or tags leaves existing lines alone, only when it has lines', async () => {
  installReads({ '/admin/nodes': list([{ ...node, relays: [{ id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true }] }]) })
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  expect(within(dialog).getByText('admin:nodes.line_labels.node_change_hint')).toBeTruthy()
})

it('shows no node hint for a node without lines', async () => {
  installReads({ '/admin/nodes': list([node]) })
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  expect(within(dialog).queryByText('admin:nodes.line_labels.node_change_hint')).toBeNull()
})
