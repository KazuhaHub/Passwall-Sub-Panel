// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, editRow, installReads, list, mount, user } from '@/test/adminSaveHarness'
import UsersView from './UsersView'

// This file's harness (adminSaveHarness) does not mount ConfirmHost, so an
// un-mocked confirm() would resolve false and short-circuit every guarded
// action before it reaches the network. Stub it to resolve true so the
// underlying request still fires and its busy state is observable.
const confirmMock = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))

// A deferred promise the test resolves on its own schedule, standing in for a
// slow (3-10s) upstream response.
function deferred<T = unknown>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

it('keeps the renew icon busy until the request settles, and ignores a second click', async () => {
  const target = { ...user, expire_at: '2026-01-01T00:00:00Z' }
  installReads({ '/admin/users': list([target]) })
  const pending = deferred<{ data: unknown }>()
  api.put.mockImplementation(async (url: string) => {
    if (url === `/admin/users/${target.id}`) return pending.promise
    return { data: {} }
  })
  mount(<UsersView />)

  const row = (await screen.findByText('old-name')).closest('tr')!
  const renewButton = within(row).getByTestId('RestartAltIcon').closest('button') as HTMLButtonElement
  await waitFor(() => expect(renewButton.disabled).toBe(false))

  fireEvent.click(renewButton)
  // Busy the instant the click fires — updateUser hasn't answered yet.
  expect(renewButton.disabled).toBe(true)
  expect(renewButton.getAttribute('aria-busy')).toBe('true')
  expect(within(renewButton).getByRole('progressbar')).toBeTruthy()

  fireEvent.click(renewButton)
  expect(api.put).toHaveBeenCalledTimes(1)

  pending.resolve({ data: {} })
  await waitFor(() => expect(renewButton.disabled).toBe(false))
  expect(renewButton.getAttribute('aria-busy')).toBeNull()
})

it('keeps the delete icon busy until the request settles, and ignores a second click', async () => {
  installReads({ '/admin/users': list([user]) })
  const pending = deferred<{ data: unknown }>()
  api.delete.mockReturnValue(pending.promise)
  mount(<UsersView />)

  const row = (await screen.findByText('old-name')).closest('tr')!
  const deleteButton = within(row).getByTestId('DeleteOutlinedIcon').closest('button') as HTMLButtonElement
  await waitFor(() => expect(deleteButton.disabled).toBe(false))

  fireEvent.click(deleteButton)
  // Busy immediately — before the confirm dialog's own promise, let alone
  // the delete request, has had a chance to settle.
  expect(deleteButton.disabled).toBe(true)
  expect(deleteButton.getAttribute('aria-busy')).toBe('true')

  await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(1))
  // A second click while the row is still busy must not re-open the confirm
  // dialog or fire a second delete.
  fireEvent.click(deleteButton)
  expect(confirmMock).toHaveBeenCalledTimes(1)
  expect(api.delete).toHaveBeenCalledTimes(1)
  expect(within(deleteButton).getByRole('progressbar')).toBeTruthy()

  pending.resolve({ data: {} })
  await waitFor(() => expect(deleteButton.disabled).toBe(false))
  expect(deleteButton.getAttribute('aria-busy')).toBeNull()
})

it('keeps the row More button busy while Resume Service (from the more-menu) is in flight, and refuses to reopen the menu', async () => {
  // The MenuItem itself is gone the instant it is clicked (closeMore() runs
  // synchronously), so the only element left on screen to carry the "still
  // working" signal is the row's own More button.
  const suspended = { ...user, service_status: 'manual_suspended' }
  installReads({ '/admin/users': list([suspended]) })
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async (url: string) => {
    if (url === `/admin/users/${suspended.id}/set-service-status`) return pending.promise
    return { data: {} }
  })
  mount(<UsersView />)

  const row = (await screen.findByText('old-name')).closest('tr')!
  const moreButton = within(row).getByTestId('MoreVertIcon').closest('button') as HTMLButtonElement
  fireEvent.click(moreButton)
  fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:users.more_menu.resume_service' }))

  expect(moreButton.disabled).toBe(true)
  expect(moreButton.getAttribute('aria-busy')).toBe('true')
  expect(within(moreButton).getByRole('progressbar')).toBeTruthy()

  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  // Disabled, so a second click must not reopen the menu or resend the call.
  fireEvent.click(moreButton)
  expect(screen.queryByRole('menuitem', { name: 'admin:users.more_menu.resume_service' })).toBeNull()
  expect(api.post).toHaveBeenCalledTimes(1)

  pending.resolve({ data: {} })
  await waitFor(() => expect(moreButton.disabled).toBe(false))
  expect(moreButton.getAttribute('aria-busy')).toBeNull()
})

it('keeps the Suspend Service button in the edit dialog busy until the request settles, and ignores a second click', async () => {
  // The default fixture is account-active / service-active, so
  // canSuspendService(user) is true and this button renders.
  installReads({ '/admin/users': list([user]) })
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async (url: string) => {
    if (url === `/admin/users/${user.id}/set-service-status`) return pending.promise
    return { data: {} }
  })
  mount(<UsersView />)
  const dialog = await editRow()

  const button = within(dialog).getByRole('button', { name: 'admin:users.more_menu.suspend_service' }) as HTMLButtonElement
  fireEvent.click(button)
  expect(button.disabled).toBe(true)
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect(within(button).getByRole('progressbar')).toBeTruthy()

  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  fireEvent.click(button)
  expect(api.post).toHaveBeenCalledTimes(1)

  pending.resolve({ data: {} })
  await waitFor(() => expect(button.disabled).toBe(false))
})

it('keeps the reason dialog open with its confirm button pending until the request settles, then closes on success', async () => {
  installReads({ '/admin/users': list([user]) })
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async (url: string) => {
    if (url === `/admin/users/${user.id}/set-enabled`) return pending.promise
    return { data: {} }
  })
  mount(<UsersView />)

  const row = (await screen.findByText('old-name')).closest('tr')!
  // user.enabled is true, so this is the disable toggle and opens the
  // reason dialog with the "disable" wording.
  fireEvent.click(within(row).getByTestId('ToggleOnIcon').closest('button')!)

  const button = await screen.findByRole('button', { name: 'admin:users.action.disable' }) as HTMLButtonElement
  fireEvent.click(button)

  // Still open and busy — the dialog used to close the instant submit was
  // clicked, before setEnabled had even been awaited.
  expect(screen.getByRole('dialog')).toBeTruthy()
  expect(button.disabled).toBe(true)
  expect(button.getAttribute('aria-busy')).toBe('true')

  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  fireEvent.click(button)
  expect(api.post).toHaveBeenCalledTimes(1)

  pending.resolve({ data: {} })
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

// Dismissing it mid-request would hide the only sign the change is still in
// flight, with the row still showing the old status.
it('refuses Cancel and Escape while the reason request is in flight', async () => {
  installReads({ '/admin/users': list([user]) })
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async (url: string) => {
    if (url === `/admin/users/${user.id}/set-enabled`) return pending.promise
    return { data: {} }
  })
  mount(<UsersView />)
  const row = (await screen.findByText('old-name')).closest('tr')!
  fireEvent.click(within(row).getByTestId('ToggleOnIcon').closest('button')!)
  fireEvent.click(await screen.findByRole('button', { name: 'admin:users.action.disable' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))

  const cancel = screen.getByRole('button', { name: 'common:actions.cancel' }) as HTMLButtonElement
  expect(cancel.disabled).toBe(true)
  fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
  await new Promise(resolve => setTimeout(resolve, 400))
  expect(screen.getByRole('dialog')).toBeTruthy()

  pending.resolve({ data: {} })
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

it('keeps the reason dialog open when the request fails, so the admin can retry', async () => {
  installReads({ '/admin/users': list([user]) })
  api.post.mockImplementation(async (url: string) => {
    if (url === `/admin/users/${user.id}/set-enabled`) throw new Error('network error')
    return { data: {} }
  })
  mount(<UsersView />)

  const row = (await screen.findByText('old-name')).closest('tr')!
  fireEvent.click(within(row).getByTestId('ToggleOnIcon').closest('button')!)

  const button = await screen.findByRole('button', { name: 'admin:users.action.disable' }) as HTMLButtonElement
  fireEvent.click(button)

  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  // The button itself recovers (a rejection only ends the wait), but the
  // dialog must stay open on failure instead of silently losing the draft.
  await waitFor(() => expect(button.disabled).toBe(false))
  // A dialog that closes on click (the bug) is still present here too — MUI's
  // ~225ms exit transition keeps it in the DOM briefly after `open` flips to
  // false. Outlast that transition so this only passes when the dialog is
  // genuinely still open, not just mid-close.
  await new Promise(resolve => setTimeout(resolve, 400))
  expect(screen.getByRole('dialog')).toBeTruthy()
  expect(screen.getByRole('button', { name: 'admin:users.action.disable' })).toBeTruthy()
})

it('shows the Refresh button as busy while a refetch is in flight, and ignores a second click', async () => {
  installReads({ '/admin/users': list([user]) })
  mount(<UsersView />)
  await screen.findByText('old-name')

  const pending = deferred<{ data: unknown }>()
  const base = api.get.getMockImplementation()!
  let usersCalls = 0
  api.get.mockImplementation(async (url: string) => {
    if (url !== '/admin/users') return base(url)
    usersCalls += 1
    return usersCalls === 1 ? pending.promise : base(url)
  })

  const button = screen.getByRole('button', { name: 'admin:users.refresh' }) as HTMLButtonElement
  fireEvent.click(button)

  await waitFor(() => expect(button.getAttribute('aria-busy')).toBe('true'))
  expect(button.disabled).toBe(true)
  expect(within(button).getByRole('progressbar')).toBeTruthy()

  // Disabled, so a second click must not start a second refetch.
  fireEvent.click(button)
  expect(usersCalls).toBe(1)

  pending.resolve({ data: list([user]) })
  await waitFor(() => expect(button.disabled).toBe(false))
  expect(button.getAttribute('aria-busy')).toBeNull()
})
