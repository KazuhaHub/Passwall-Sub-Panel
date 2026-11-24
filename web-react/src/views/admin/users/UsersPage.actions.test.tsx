// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { account, api, mountPage, serve, toast, user } from './testHarness'
import UsersPage from './UsersPage'

vi.mock('../risk/dialog/RiskUserDialog', () => ({ default: () => null }))

// A deferred promise the test settles on its own schedule: a slow (3-10 s) upstream answer.
function deferred<T = unknown>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const rowOf = async (name = 'old-name') => (await screen.findByRole('row', { name: new RegExp(name) }))
async function openMenu(name = 'old-name') {
  const trigger = within(await rowOf(name)).getByRole('button', { name: /^Actions for/ })
  fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
  fireEvent.mouseDown(trigger, { button: 0 })
  fireEvent.click(trigger)
}
const choose = async (label: string) => fireEvent.click(await screen.findByRole('menuitem', { name: label }))
const confirmButton = async (name: string) => within(await screen.findByRole('alertdialog')).getByRole('button', { name })

it('renews by 30 days with the same request, and the row shows the wait', async () => {
  const target = { ...user, expire_at: '2026-01-01T00:00:00Z' }
  serve([target])
  const pending = deferred<{ data: unknown }>()
  api.put.mockImplementation(async () => pending.promise)
  mountPage(<UsersPage />)
  await openMenu()
  await choose('Renew 30 days')

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put.mock.calls[0][0]).toBe('/admin/users/2')
  expect(Object.keys(api.put.mock.calls[0][1])).toEqual(['expire_at'])
  // The busy mark is state set just before the request: it renders a tick after the request is seen, so wait for it.
  await waitFor(() => expect(document.querySelector('tr[data-id="2"]')?.getAttribute('data-row-state')).toBe('pending'))

  pending.resolve({ data: {} })
  await waitFor(() => expect((document.querySelector('tr[data-id="2"]') as HTMLElement).getAttribute('data-row-state')).toBeNull())
  expect(toast).toHaveBeenCalledWith('Renewed by 30 days', 'success')
})

it('deletes only after a confirmation that says what goes with the user, with focus on Cancel', async () => {
  serve([user])
  mountPage(<UsersPage />)
  await openMenu()
  await choose('Delete')

  const dialog = await screen.findByRole('alertdialog')
  expect(dialog.textContent).toContain('This cannot be undone.')
  expect(dialog.textContent).toContain(user.upn)
  await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' })))
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
  expect(api.delete).not.toHaveBeenCalled()

  await openMenu()
  await choose('Delete')
  fireEvent.click(await confirmButton('Delete'))
  await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(1))
  expect(api.delete.mock.calls[0][0]).toBe('/admin/users/2')
  await waitFor(() => expect(toast).toHaveBeenCalledWith('Deleted', 'success'))
})

it('resumes a suspended service with the same request, once', async () => {
  const suspended = { ...user, service_status: 'manual_suspended', service_disabled_reason: 'service_manual' }
  serve([suspended])
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async () => pending.promise)
  mountPage(<UsersPage />)
  await openMenu()
  await choose('Resume proxy service')

  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  expect(api.post.mock.calls[0]).toEqual(['/admin/users/2/set-service-status', { enabled: true, reason: undefined, detail: undefined }])
  // The busy mark is state set just before the request: it renders a tick after the request is seen, so wait for it.
  await waitFor(() => expect(document.querySelector('tr[data-id="2"]')?.getAttribute('data-row-state')).toBe('pending'))
  pending.resolve({ data: {} })
  await waitFor(() => expect(toast).toHaveBeenCalledWith('Proxy service resumed', 'success'))
})

it('suspends the service as a manual hold', async () => {
  serve([user])
  mountPage(<UsersPage />)
  await openMenu()
  await choose('Suspend proxy service')
  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  expect(api.post.mock.calls[0]).toEqual(['/admin/users/2/set-service-status', { enabled: false, reason: 'service_manual', detail: undefined }])
})

it('keeps the reason dialog open and pending until the request settles, refuses Escape meanwhile, and closes on success', async () => {
  serve([user])
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async () => pending.promise)
  mountPage(<UsersPage />)
  await openMenu()
  await choose('Disable account')
  const dialog = await screen.findByRole('dialog')
  fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'abuse' } })
  const button = within(dialog).getByRole('button', { name: 'Disable account' }) as HTMLButtonElement
  fireEvent.click(button)

  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  expect(api.post.mock.calls[0]).toEqual(['/admin/users/2/set-enabled', { enabled: false, reason: 'abuse' }])
  expect(button.disabled).toBe(true)
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect((within(dialog).getByRole('button', { name: 'Cancel' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.keyDown(dialog, { key: 'Escape' })
  await new Promise(resolve => setTimeout(resolve, 300))
  expect(screen.queryByRole('dialog')).toBeTruthy()

  pending.resolve({ data: {} })
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

it('keeps the reason dialog open, with the text, when the request fails', async () => {
  serve([user])
  api.post.mockRejectedValue(new Error('network error'))
  mountPage(<UsersPage />)
  await openMenu()
  await choose('Disable account')
  const dialog = await screen.findByRole('dialog')
  fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'abuse' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Disable account' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
  await waitFor(() => expect((within(dialog).getByRole('button', { name: 'Disable account' }) as HTMLButtonElement).disabled).toBe(false))
  expect(screen.getByRole('dialog')).toBeTruthy()
  expect((within(dialog).getByRole('textbox') as HTMLTextAreaElement).value).toBe('abuse')
})

it('cannot disable your own account from the list', async () => {
  serve([account(1, 'me')])
  mountPage(<UsersPage />)
  await openMenu('me')
  const item = await screen.findByRole('menuitem', { name: 'Disable account' })
  expect(item.getAttribute('aria-disabled') === 'true' || item.hasAttribute('data-disabled')).toBe(true)
})

it('shows the Refresh button as busy while a refetch is in flight, and ignores a second click', async () => {
  serve([user])
  mountPage(<UsersPage />)
  await screen.findByText('old-name')
  const pending = deferred<{ data: unknown }>()
  const base = api.get.getMockImplementation()!
  let calls = 0
  api.get.mockImplementation(async (url: string) => {
    if (url !== '/admin/users') return base(url)
    calls += 1
    return calls === 1 ? pending.promise : base(url)
  })
  const button = screen.getByRole('button', { name: 'Refresh' }) as HTMLButtonElement
  fireEvent.click(button)
  await waitFor(() => expect(button.getAttribute('aria-busy')).toBe('true'))
  expect(button.disabled).toBe(true)
  fireEvent.click(button)
  expect(calls).toBe(1)
  pending.resolve({ data: { items: [user], total: 1, page: 1, page_size: 25 } })
  await waitFor(() => expect(button.disabled).toBe(false))
})

it('puts the bulk actions in the bar: enable, disable, renew, delete, and more', async () => {
  serve([user, account(3, 'second', { expire_at: '2026-01-01T00:00:00Z' })])
  api.put.mockResolvedValue({ data: {} })
  mountPage(<UsersPage />)
  fireEvent.click(within(await rowOf()).getByRole('checkbox'))
  fireEvent.click(within(await rowOf('second')).getByRole('checkbox'))
  const bar = await screen.findByRole('region', { name: /Bulk/ })
  expect(bar.textContent).toContain('2 selected')
  for (const name of ['Bulk enable accounts', 'Bulk disable accounts', 'Bulk renew 30d', 'Bulk delete', 'More']) {
    expect(within(bar).getByRole('button', { name })).toBeTruthy()
  }
  // Renew skips the permanent one, as before, and says so.
  fireEvent.click(within(bar).getByRole('button', { name: 'Bulk renew 30d' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put.mock.calls[0][0]).toBe('/admin/users/3')
  await waitFor(() => expect(toast).toHaveBeenCalledWith('Renewed 1 users, skipped 1 permanent ones', 'success'))
})

it('names the people in the bulk delete confirmation and sends one delete per user', async () => {
  serve([user, account(3, 'second')])
  mountPage(<UsersPage />)
  fireEvent.click(within(await rowOf()).getByRole('checkbox'))
  fireEvent.click(within(await rowOf('second')).getByRole('checkbox'))
  fireEvent.click(within(await screen.findByRole('region', { name: /Bulk/ })).getByRole('button', { name: 'Bulk delete' }))
  const dialog = await screen.findByRole('alertdialog')
  expect(dialog.textContent).toContain('old-name')
  expect(dialog.textContent).toContain('second')
  expect(dialog.textContent).toContain('This cannot be undone.')
  fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
  await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(2))
  expect(api.delete.mock.calls.map(c => c[0]).sort()).toEqual(['/admin/users/2', '/admin/users/3'])
})

// ---- the security tab ----


it.each([
  ['Reset authenticator app (TOTP)', 'reset-2fa', { totp_enabled: true }, true],
  ['Reset credentials', 'reset-credentials', {}, true],
  ['Unlink SSO binding', 'unlink-sso', { sso_provider: 'google' }, true],
  ['Reset emergency-access count', 'reset-emergency-usage', {}, false],
])('keeps "%s" busy in the security tab until the request settles, and ignores a second click', async (label, path, extra, asks) => {
  serve([{ ...user, ...extra }])
  const pending = deferred<{ data: unknown }>()
  api.post.mockImplementation(async (url: string) => (url === `/admin/users/2/${path}` ? pending.promise : { data: {} }))
  mountPage(<UsersPage />, '/admin/users?user=2&tab=security')
  const button = await screen.findByRole('button', { name: label }) as HTMLButtonElement
  fireEvent.click(button)
  if (asks) {
    const dialog = await screen.findByRole('alertdialog')
    // Each names the account and puts focus on Cancel.
    expect(dialog.textContent).toContain(user.upn)
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' })))
    fireEvent.click(within(dialog).getAllByRole('button').find(b => b.textContent !== 'Cancel')!)
  }
  await waitFor(() => expect(api.post.mock.calls.filter(c => c[0] === `/admin/users/2/${path}`)).toHaveLength(1))
  expect(button.disabled).toBe(true)
  expect(button.getAttribute('aria-busy')).toBe('true')
  fireEvent.click(button)
  expect(api.post.mock.calls.filter(c => c[0] === `/admin/users/2/${path}`)).toHaveLength(1)
  pending.resolve({ data: { sub_token: 't', sub_url: 'u', uuid: 'x' } })
  await waitFor(() => expect(button.disabled).toBe(false))
})

it('re-reads the risk column and the opened user after this page resumes the service, so a lifted hold goes', async () => {
  const held = { ...user, service_status: 'manual_suspended', service_disabled_reason: 'service_manual' }
  const hold = { '2': { level: '', auto_suspended: true, open: true, dismissed: false, trusted: false } }
  serve([held], { '/admin/risk-center/levels': hold })
  mountPage(<UsersPage />, '/admin/users?user=2')
  expect(await screen.findAllByText('Proxy service suspended')).toBeTruthy()
  const levelsCalls = () => api.get.mock.calls.filter(c => c[0] === '/admin/risk-center/levels').length
  await waitFor(() => expect(levelsCalls()).toBeGreaterThan(0))
  const before = levelsCalls()

  // The reload answers an active user, as the server would after a resume.
  serve([user], { '/admin/risk-center/levels': {} })
  fireEvent.click(await screen.findByRole('button', { name: 'Resume proxy service' }))
  await waitFor(() => expect(levelsCalls()).toBeGreaterThan(before))
  // The page shows the fresh row's state, not the snapshot it opened with.
  await waitFor(() => expect(screen.queryAllByText('Proxy service suspended')).toHaveLength(0))
  expect(screen.getAllByText('Proxy service active').length).toBeGreaterThan(0)
})
