// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, installReads, list, mount, node } from '@/test/adminSaveHarness'
import NodesView from './NodesView'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const separator = { id: 50, kind: 'separator', display_name: '---- HK ----', sort_order: 5, enabled: true, mode: 'node_bound', node_ids: [1], tags: [] }

it('previews which groups would show a node-bound separator', async () => {
  installReads({ '/admin/nodes': list([node]), '/admin/nodes/separator': list([separator]) })
  api.post.mockImplementation(async (url: string) => String(url).endsWith('/separator/group-preview')
    ? { data: { groups: [{ id: 1, name: 'VIP' }], visible: [] } }
    : { data: {} })
  mount(<NodesView />)
  const row = (await screen.findByText('---- HK ----')).closest('tr')!
  fireEvent.click(within(row).getByTestId('EditOutlinedIcon').closest('button')!)
  const dialog = await screen.findByRole('dialog')
  await waitFor(() => expect(within(dialog).getByTestId('separator-groups').textContent).toBe('admin:nodes.separator_preview.none'))
  const call = api.post.mock.calls.filter(([url]) => String(url).endsWith('/separator/group-preview')).at(-1)!
  expect(call[1]).toEqual({ mode: 'node_bound', node_ids: [1] })
  expect(api.put).not.toHaveBeenCalled()
})
