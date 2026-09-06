// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, deferred, editRow, installReads, list, mount, user } from '@/test/adminSaveHarness'
import UsersView from './UsersView'

it('reopens with the saved user while the paged refresh is pending', async () => {
  const saved = { ...user, display_name: 'new-name' }
  installReads({ '/admin/users': list([user]), '/admin/users/2': saved })
  mount(<UsersView />)
  const dialog = await editRow()
  fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'new-name' } })

  const pending = deferred<{ data: unknown }>()
  const normalGet = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, ...args: unknown[]) =>
    url === '/admin/users' ? pending.promise : normalGet(url, ...args),
  )
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const reopened = await editRow('new-name')
  expect(within(reopened).getByDisplayValue('new-name')).toBeTruthy()

  await act(async () => pending.resolve({ data: list([saved]) }))
})
