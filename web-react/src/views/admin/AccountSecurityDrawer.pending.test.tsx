// @vitest-environment jsdom
//
// AccountSecurityDrawer has no route of its own — it is only ever reached
// through UsersView's per-row "More" menu, so these tests drive it the same
// way an admin does: open UsersView, open the row menu, open the drawer.
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, installReads, list, mount, user } from '@/test/adminSaveHarness'
import UsersView from './UsersView'

// This harness does not mount ConfirmHost, so an un-mocked confirm() would
// resolve false and short-circuit reset-2FA / reset-credentials / unlink-sso
// before they ever reach the network. Stub it to resolve true.
const confirmMock = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))

// A deferred promise the test resolves on its own schedule, standing in for a
// slow (3-10s) upstream response.
function deferred<T = unknown>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

async function openDrawer() {
  const row = (await screen.findByText('old-name')).closest('tr')!
  fireEvent.click(within(row).getByTestId('MoreVertIcon').closest('button')!)
  fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:users.more_menu.account_security' }))
}

it('keeps Reset 2FA busy in the drawer until the request settles, and ignores a second click', async () => {
  const target = { ...user, totp_enabled: true }
  installReads({ '/admin/users': list([target]) })
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async (url: string) => {
    if (url === `/admin/users/${target.id}/reset-2fa`) return pending.promise
    return { data: {} }
  })
  mount(<UsersView />)
  await openDrawer()

  const button = await screen.findByRole('button', { name: 'admin:users.more_menu.reset_2fa' }) as HTMLButtonElement
  fireEvent.click(button)
  // Busy immediately, ahead of the confirm() dialog's own promise.
  expect(button.disabled).toBe(true)
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect(within(button).getByRole('progressbar')).toBeTruthy()

  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  fireEvent.click(button)
  expect(confirmMock).toHaveBeenCalledTimes(1)
  expect(api.post).toHaveBeenCalledTimes(1)

  pending.resolve({ data: {} })
  await waitFor(() => expect(button.disabled).toBe(false))
  expect(button.getAttribute('aria-busy')).toBeNull()
})

it('keeps Reset credentials busy in the drawer until the request settles, and ignores a second click', async () => {
  installReads({ '/admin/users': list([user]) })
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async (url: string) => {
    if (url === `/admin/users/${user.id}/reset-credentials`) return pending.promise
    return { data: {} }
  })
  mount(<UsersView />)
  await openDrawer()

  const button = await screen.findByRole('button', { name: 'admin:users.more_menu.reset_credentials' }) as HTMLButtonElement
  fireEvent.click(button)
  expect(button.disabled).toBe(true)
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect(within(button).getByRole('progressbar')).toBeTruthy()

  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  fireEvent.click(button)
  expect(api.post).toHaveBeenCalledTimes(1)

  pending.resolve({ data: { sub_token: 't', sub_url: 'u', uuid: 'x' } })
  await waitFor(() => expect(button.disabled).toBe(false))
})

it('keeps Unlink SSO busy in the drawer until the request settles, and ignores a second click', async () => {
  const target = { ...user, sso_provider: 'google' }
  installReads({ '/admin/users': list([target]) })
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async (url: string) => {
    if (url === `/admin/users/${target.id}/unlink-sso`) return pending.promise
    return { data: {} }
  })
  mount(<UsersView />)
  await openDrawer()

  const button = await screen.findByRole('button', { name: 'admin:users.more_menu.unlink_sso' }) as HTMLButtonElement
  await waitFor(() => expect(button.disabled).toBe(false))
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

it('keeps Reset emergency access busy in the drawer until the request settles, and ignores a second click', async () => {
  installReads({ '/admin/users': list([user]) })
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async (url: string) => {
    if (url === `/admin/users/${user.id}/reset-emergency-usage`) return pending.promise
    return { data: {} }
  })
  mount(<UsersView />)
  await openDrawer()

  // No confirm() gate on this one — it fires straight away.
  const button = await screen.findByRole('button', { name: 'admin:users.more_menu.reset_emergency' }) as HTMLButtonElement
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
