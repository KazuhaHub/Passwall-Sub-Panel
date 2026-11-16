// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, editRow, group, installReads, list, mount } from '@/test/adminSaveHarness'
import GroupsView from './GroupsView'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const previewCalls = () => api.post.mock.calls.filter(([url]) => String(url).includes('node-preview'))

const answer = (over: Record<string, unknown> = {}) => ({
  data: { nodes: [{ id: 1, name: 'HK-1', region: 'HK', visible_lines: 1, total_lines: 2 }], excluded: [], added: [], removed: [], members: 0, ...over },
})

it('previews the saved group\'s draft filter and warns members will be resynced', async () => {
  const saved = { ...group, name: 'old-name', members: 5, tag_filter: { all: false, tags: ['region:HK'], mode: 'all' } }
  installReads({ '/admin/groups': list([saved]) })
  api.post.mockImplementation(async () => answer({ excluded: [{ id: 3, name: 'US-1', reason: 'allowlist_unenforceable' }], added: [2], members: 5 }))
  mount(<GroupsView />)
  const dialog = await editRow()
  await waitFor(() => expect(within(dialog).getByTestId('group-node-preview').textContent).toBe('admin:groups.preview.nodes'))
  expect(within(dialog).getByTestId('group-node-excluded').textContent).toBe('admin:groups.preview.excluded_allowlist')
  expect(within(dialog).getByTestId('group-node-change').textContent).toBe('admin:groups.preview.change')
  const [url, body] = previewCalls().at(-1)!
  expect(url).toBe('/admin/groups/1/node-preview')
  expect(body).toEqual({ tag_filter: { all: false, mode: 'all', tags: ['region:HK'] } })
  // Toggling "match all" re-asks with the draft, without saving anything.
  const before = previewCalls().length
  fireEvent.click(within(dialog).getByRole('switch', { name: 'admin:groups.field.match_all' }))
  await waitFor(() => expect(previewCalls().length).toBeGreaterThan(before))
  expect(previewCalls().at(-1)![1]).toEqual({ tag_filter: { all: true, mode: 'all', tags: [] } })
  expect(api.put).not.toHaveBeenCalled()
})

it('shows "no node" in the error colour and "unknown" when the preview fails', async () => {
  const saved = { ...group, name: 'old-name', tag_filter: { all: false, tags: ['region:ZZ'], mode: 'all' } }
  installReads({ '/admin/groups': list([saved]) })
  api.post.mockImplementation(async () => answer({ nodes: [] }))
  mount(<GroupsView />)
  const dialog = await editRow()
  await waitFor(() => expect(within(dialog).getByTestId('group-node-preview').textContent).toBe('admin:groups.preview.none'))
  expect(within(dialog).queryByTestId('group-node-change')).toBeNull()
  api.post.mockRejectedValue(new Error('down'))
  fireEvent.click(within(dialog).getByRole('switch', { name: 'admin:groups.field.match_all' }))
  await waitFor(() => expect(within(dialog).getByTestId('group-node-preview').textContent).toBe('admin:groups.preview.unknown'))
})

it('previews a group being created against no saved group', async () => {
  installReads({ '/admin/groups': list([]) })
  api.post.mockImplementation(async () => answer())
  mount(<GroupsView />)
  fireEvent.click(await screen.findByRole('button', { name: /admin:groups.create|admin:groups.add|admin:groups.new/ }))
  await waitFor(() => expect(previewCalls().length).toBeGreaterThan(0))
  expect(previewCalls().at(-1)![0]).toBe('/admin/groups/node-preview')
})
