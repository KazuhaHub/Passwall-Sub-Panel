// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, editRow, installReads, list, mount, user } from '@/test/adminSaveHarness'
import UsersView from './UsersView'

it('reopens with the user returned by the update without reloading the stale list', async () => {
  const saved = { ...user, display_name: 'new-name' }
  installReads({ '/admin/users': list([user]) })
  api.put.mockResolvedValueOnce({ data: saved })
  mount(<UsersView />)
  const dialog = await editRow()
  fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'new-name' } })

  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const reopened = await editRow('new-name')
  expect(within(reopened).getByDisplayValue('new-name')).toBeTruthy()
})
