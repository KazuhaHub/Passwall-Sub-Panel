// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, editRow, group, installReads, list, mount, user } from '@/test/adminSaveHarness'
import UsersView from './UsersView'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const previewCalls = () => api.post.mock.calls.filter(([url]) => String(url).endsWith('/group-preview'))

it('previews the node change only once the drafted group differs', async () => {
  const other = { ...group, id: 2, slug: 'other', name: 'Other' }
  installReads({ '/admin/users': list([user]), '/admin/groups': list([group, other]) })
  api.post.mockImplementation(async () => ({ data: { added: [{ id: 9, name: 'JP-1', region: 'JP', visible_lines: 0, total_lines: 0 }], removed: [], kept: 1 } }))
  mount(<UsersView />)
  const dialog = await editRow()
  expect(within(dialog).queryByTestId('group-change-preview')).toBeNull()
  expect(previewCalls()).toHaveLength(0)
  fireEvent.mouseDown(within(dialog).getByLabelText(/admin:users.field.group/))
  fireEvent.click(await screen.findByRole('option', { name: 'Other' }))
  await waitFor(() => expect(within(dialog).getByTestId('group-change-preview').textContent).toBe('admin:users.group_preview.change'))
  expect(previewCalls().at(-1)).toEqual(['/admin/users/2/group-preview', { group_id: 2 }, expect.anything()])
  expect(api.put).not.toHaveBeenCalled()
})

it('says "unchanged" neutrally and "unknown" when the preview fails', async () => {
  const other = { ...group, id: 2, slug: 'other', name: 'Other' }
  const third = { ...group, id: 3, slug: 'third', name: 'Third' }
  installReads({ '/admin/users': list([user]), '/admin/groups': list([group, other, third]) })
  api.post.mockImplementation(async () => ({ data: { added: [], removed: [], kept: 3 } }))
  mount(<UsersView />)
  const dialog = await editRow()
  fireEvent.mouseDown(within(dialog).getByLabelText(/admin:users.field.group/))
  fireEvent.click(await screen.findByRole('option', { name: 'Other' }))
  await waitFor(() => expect(within(dialog).getByTestId('group-change-preview').textContent).toBe('admin:users.group_preview.same'))
  api.post.mockRejectedValue(new Error('down'))
  fireEvent.mouseDown(within(dialog).getByLabelText(/admin:users.field.group/))
  fireEvent.click(await screen.findByRole('option', { name: 'Third' }))
  await waitFor(() => expect(within(dialog).getByTestId('group-change-preview').textContent).toBe('admin:users.group_preview.unknown'))
  // Back to the current group: nothing to preview.
  fireEvent.mouseDown(within(dialog).getByLabelText(/admin:users.field.group/))
  fireEvent.click(await screen.findByRole('option', { name: 'group' }))
  await waitFor(() => expect(within(dialog).queryByTestId('group-change-preview')).toBeNull())
})
