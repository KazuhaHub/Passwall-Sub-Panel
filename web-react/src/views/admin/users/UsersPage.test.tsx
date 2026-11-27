// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { account, api, lastParams, mountPage, searchOf, serve, user, where } from './testHarness'
import UsersPage from './UsersPage'

// The risk drawer has its own tests; it is the page's neighbour, not what this tests.
vi.mock('../risk/dialog/RiskUserDialog', () => ({ default: ({ userId }: { userId: number | null }) => (userId ? <div data-testid="risk-drawer">{userId}</div> : null) }))

const GB = 1024 ** 3
const rowOf = async (name: string) => (await screen.findByRole('row', { name: new RegExp(name) }))
async function openRowMenu(name: string) {
  const trigger = within(await rowOf(name)).getByRole('button', { name: /^Actions for/ })
  fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
  fireEvent.mouseDown(trigger, { button: 0 })
  fireEvent.click(trigger)
}

describe('the list', () => {
  it('asks the server for the page, the order and the filter it was given in the address', async () => {
    serve([user])
    mountPage(<UsersPage />, '/admin/users?page=2&q=alice&sort=upn-asc&group=1')
    await screen.findByText('old-name')
    expect(lastParams('/admin/users')).toMatchObject({ page: 2, keyword: 'alice', sort_by: 'upn', sort_dir: 'asc', group_id: 1 })
    expect(screen.getByRole('searchbox')).toHaveProperty('value', 'alice')
  })

  it('starts from the newest first, and a click on a column header writes its order to the address', async () => {
    serve([user])
    mountPage(<UsersPage />)
    await screen.findByText('old-name')
    expect(lastParams('/admin/users')).toMatchObject({ page: 1, sort_by: 'id', sort_dir: 'desc' })

    fireEvent.click(screen.getByRole('button', { name: /Status/ }))
    await waitFor(() => expect(searchOf().get('sort')).toBe('enabled-asc'))
    await waitFor(() => expect(lastParams('/admin/users')).toMatchObject({ sort_by: 'enabled', sort_dir: 'asc' }))
  })

  it('tells the account and the service apart, in words', async () => {
    const overQuota = account(3, 'over', { service_status: 'traffic_exceeded' })
    const disabled = account(4, 'off', { enabled: false, account_status: 'disabled', service_status: 'account_disabled', auto_disabled_reason: 'abuse' })
    const geo = account(5, 'geo', { service_status: 'manual_suspended', service_disabled_reason: 'geo_auto' })
    serve([user, overQuota, disabled, geo])
    mountPage(<UsersPage />)

    expect(within(await rowOf('over')).getByText('Account active')).toBeTruthy()
    expect(within(await rowOf('over')).getByText('Traffic exhausted')).toBeTruthy()
    expect(within(await rowOf('off')).getByText('Account disabled')).toBeTruthy()
    expect(within(await rowOf('off')).getByText('Proxy service unavailable')).toBeTruthy()
    expect(within(await rowOf('geo')).getByText('Auto-suspended (locations)')).toBeTruthy()
    expect(within(await rowOf('old-name')).getByText('Proxy service active')).toBeTruthy()
  })

  it('marks usage unknown instead of zero for a user outside the usage leaderboard', async () => {
    // The usage is fed by the Top-N leaderboard, so a user who is not in it has UNKNOWN usage: "0 / 100 GB" would understate it.
    serve([{ ...user, traffic_limit_bytes: 100 * GB }], { '/admin/traffic/top': { items: [] } })
    mountPage(<UsersPage />)
    const row = await rowOf('old-name')
    await waitFor(() => expect(row.textContent).toContain('100 GB'))
    expect(row.textContent).not.toMatch(/0 \/ 100 GB/)
  })

  it('shows the real usage for a user the leaderboard covers', async () => {
    serve([{ ...user, traffic_limit_bytes: 100 * GB }], { '/admin/traffic/top': { items: [{ user_id: user.id, period_used_bytes: 5 * GB }] } })
    mountPage(<UsersPage />)
    const row = await rowOf('old-name')
    await waitFor(() => expect(row.textContent).toContain('5 / 100 GB'))
  })

  it('does not re-scan the usage leaderboard on a list refresh', async () => {
    // /admin/traffic/top walks every user server-side: it loads once per page size, not per refresh.
    serve([user])
    const calls = (url: string) => api.get.mock.calls.filter(c => c[0] === url).length
    mountPage(<UsersPage />)
    await screen.findByText('old-name')
    await waitFor(() => expect(calls('/admin/traffic/top')).toBeGreaterThan(0))
    const topBefore = calls('/admin/traffic/top')
    const listBefore = calls('/admin/users')
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls('/admin/users')).toBeGreaterThan(listBefore))
    expect(calls('/admin/traffic/top')).toBe(topBefore)
  })

  it('keeps the batch selection across a refresh of the same page', async () => {
    serve([user])
    // A real server returns fresh objects on every read; the harness would hand back the same one.
    const base = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string) => {
      const res = await base(url)
      return url === '/admin/users' ? { data: { ...res.data, items: res.data.items.map((u: object) => ({ ...u })) } } : res
    })
    mountPage(<UsersPage />)
    const box = within(await rowOf('old-name')).getByRole('checkbox') as HTMLInputElement
    fireEvent.click(box)
    await waitFor(() => expect(within(screen.getByRole('region', { name: /Bulk/ })).getByText(/1 selected/)).toBeTruthy())

    const before = api.get.mock.calls.length
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(api.get.mock.calls.length).toBeGreaterThan(before))
    expect(await screen.findByText(/1 selected/)).toBeTruthy()
  })

  it('shows a way forward when nothing matches, and when there is nobody yet', async () => {
    serve([], { '/admin/users': { items: [], total: 0, page: 1, page_size: 25 } })
    mountPage(<UsersPage />, '/admin/users?q=nobody')
    expect(await screen.findByText('No users match the current filters')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }))
    await waitFor(() => expect(searchOf().get('q')).toBeNull())
    expect(await screen.findByText('No users yet')).toBeTruthy()
  })
})

describe('the risk column', () => {
  const flagged = account(2, 'flagged')
  const LEVELS = { '2': { level: 'flagged', auto_suspended: false, open: true, dismissed: false, trusted: false } }
  const CHIP = 'Open risk details'
  const levelsCalls = () => api.get.mock.calls.filter(c => c[0] === '/admin/risk-center/levels')

  it('shows an admin the chip from one /levels read', async () => {
    serve([flagged, account(7, 'clean')], { '/admin/risk-center/levels': LEVELS })
    mountPage(<UsersPage />)
    expect((await within(await rowOf('flagged')).findByRole('button', { name: CHIP })).textContent).toBe('Flagged')
    expect(within(await rowOf('clean')).queryByRole('button', { name: CHIP })).toBeNull()
    expect(levelsCalls().length).toBeGreaterThan(0)
  })

  it('is neither shown to an operator nor read for one', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    serve([flagged], { '/admin/risk-center/levels': LEVELS })
    mountPage(<UsersPage />)
    const row = await rowOf('flagged')
    expect(screen.queryByRole('columnheader', { name: 'Risk' })).toBeNull()
    expect(within(row).queryByRole('button', { name: CHIP })).toBeNull()
    await waitFor(() => expect(api.get.mock.calls.some(c => c[0] === '/admin/traffic/top')).toBe(true))
    expect(levelsCalls()).toHaveLength(0)
    // The admin-only reads an operator's token cannot make are not made either (they would answer 403).
    expect(api.get.mock.calls.some(c => c[0] === '/admin/servers')).toBe(false)
  })

  it('stays blank, with no toast, when /levels fails', async () => {
    serve([flagged], { '/admin/risk-center/levels': new Error('down') })
    mountPage(<UsersPage />)
    const row = await rowOf('flagged')
    await waitFor(() => expect(levelsCalls().length).toBeGreaterThan(0))
    expect(within(row).queryByRole('button', { name: CHIP })).toBeNull()
  })

  it('says dismissed, trusted and held in words, each its own chip', async () => {
    const held = account(4, 'held'), dismissed = account(5, 'dismissed'), trusted = account(6, 'trusted')
    serve([held, dismissed, trusted], { '/admin/risk-center/levels': {
      '4': { level: '', auto_suspended: true, open: true, dismissed: false, trusted: false },
      '5': { level: 'flagged', auto_suspended: false, open: false, dismissed: true, trusted: false },
      '6': { level: '', auto_suspended: false, open: false, dismissed: false, trusted: true },
    } })
    mountPage(<UsersPage />)
    const chip = async (name: string) => (await within(await rowOf(name)).findByRole('button', { name: CHIP })).textContent
    expect(await chip('held')).toBe('Suspended')
    expect(await chip('dismissed')).toBe('Dismissed')
    expect(await chip('trusted')).toBe('Trusted')
  })

  it('opens the drawer for a link to ?risk=<id>', async () => {
    serve([flagged], { '/admin/risk-center/levels': LEVELS })
    mountPage(<UsersPage />, '/admin/users?risk=2')
    expect((await screen.findByTestId('risk-drawer')).textContent).toBe('2')
  })

  it('pushes the drawer from a chip, so Back closes it', async () => {
    serve([flagged], { '/admin/risk-center/levels': LEVELS })
    const { router } = mountPage(<UsersPage />)
    fireEvent.click(await within(await rowOf('flagged')).findByRole('button', { name: CHIP }))
    expect((await screen.findByTestId('risk-drawer')).textContent).toBe('2')
    await router.navigate(-1)
    await waitFor(() => expect(screen.queryByTestId('risk-drawer')).toBeNull())
  })

  it('keeps the row menu to what makes sense without opening the user', async () => {
    serve([account(7, 'clean')])
    mountPage(<UsersPage />)
    await openRowMenu('clean')
    expect((await screen.findAllByRole('menuitem')).map(i => i.textContent)).toEqual(['Copy subscription URL', 'Renew 30 days', 'Disable account', 'Suspend proxy service', 'Delete'])
  })

  it('offers 风控 details for any account in the page menu to an admin, and not to an operator', async () => {
    serve([account(7, 'clean')])
    const { unmount } = mountPage(<UsersPage />, '/admin/users/7')
    fireEvent.click(await screen.findByRole('button', { name: 'More' }))
    expect(await screen.findByRole('menuitem', { name: 'Risk details' })).toBeTruthy()
    unmount()
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    mountPage(<UsersPage />, '/admin/users/7')
    fireEvent.click(await screen.findByRole('button', { name: 'More' }))
    expect(await screen.findByRole('menuitem', { name: 'Reset password' })).toBeTruthy()
    expect(screen.queryByRole('menuitem', { name: 'Risk details' })).toBeNull()
  })

  it("opens the drawer over the user's page from its risk lookup, and Back returns to the page", async () => {
    serve([flagged], { '/admin/risk-center/levels': LEVELS })
    const { router } = mountPage(<UsersPage />, '/admin/users/2')
    fireEvent.click(await screen.findByRole('button', { name: 'Risk lookup' }))
    await waitFor(() => expect(searchOf().get('risk')).toBe('2'))
    expect(where().startsWith('/admin/users/2?')).toBe(true)
    await router.navigate(-1)
    await waitFor(() => expect(where()).toBe('/admin/users/2'))
    expect(screen.queryByTestId('risk-drawer')).toBeNull()
  })

  it('opens the drawer over a user page for a link to ?risk=<id>', async () => {
    serve([flagged], { '/admin/risk-center/levels': LEVELS })
    mountPage(<UsersPage />, '/admin/users/2?tab=security&risk=2')
    expect((await screen.findByTestId('risk-drawer')).textContent).toBe('2')
    expect(await screen.findByRole('tab', { name: /Security/, selected: true })).toBeTruthy()
  })

  it('opens the risk drawer from a chip with risk=<id> in the address', async () => {
    serve([flagged], { '/admin/risk-center/levels': LEVELS, '/admin/risk-center/users/2': {} })
    mountPage(<UsersPage />)
    fireEvent.click(await within(await rowOf('flagged')).findByRole('button', { name: CHIP }))
    await waitFor(() => expect(searchOf().get('risk')).toBe('2'))
  })
})

describe('an operator', () => {
  it('cannot edit an admin account: the button is off with its reason, and no mutating action is offered', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    const boss = account(9, 'boss', { role: 'admin' })
    serve([boss, user])
    mountPage(<UsersPage />)

    const bossRow = await rowOf('boss')
    expect(within(bossRow).getByRole('button', { name: 'Edit' }).getAttribute('aria-disabled')).toBe('true')
    // Not selectable either (no checkbox): every bulk action writes.
    expect(within(bossRow).queryByRole('checkbox')).toBeNull()
    // An ordinary account is open to them.
    const own = await rowOf('old-name')
    expect(within(own).getByRole('button', { name: 'Edit' }).getAttribute('aria-disabled')).toBeNull()
    expect(within(own).getByRole('checkbox')).toBeTruthy()
  })

  it('is not offered the reconcile run', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    serve([user])
    mountPage(<UsersPage />)
    await screen.findByText('old-name')
    expect(screen.queryByRole('button', { name: 'More' })).toBeNull()
  })
})

it('opens the user named in the address, on the tab named there, and keeps the token out of it', async () => {
  serve([user])
  mountPage(<UsersPage />, '/admin/users/2?tab=security')
  expect(await screen.findByRole('tab', { name: 'Security', selected: true })).toBeTruthy()
  expect(where()).not.toContain('token-2')
})

it('opens a user who is not on any list page by reading that user', async () => {
  serve([user], { '/admin/users/12': account(12, 'elsewhere') })
  mountPage(<UsersPage />, '/admin/users/12')
  expect((await screen.findAllByText('elsewhere@example.test')).length).toBeGreaterThan(0)
  expect(api.get.mock.calls.some(c => c[0] === '/admin/users/12')).toBe(true)
})

describe('the old ?user= addresses', () => {
  it.each([
    ['/admin/users?user=2', '/admin/users/2'],
    ['/admin/users?user=2&tab=security', '/admin/users/2?tab=security'],
    ['/admin/users?user=2&tab=nonsense', '/admin/users/2'],
    ['/admin/users?user=new', '/admin/users/new'],
  ])('%s is sent on to %s', async (from, to) => {
    serve([user])
    mountPage(<UsersPage />, from)
    await waitFor(() => expect(where()).toBe(to))
  })

  it('replaces the entry, so Back does not return to the redirect', async () => {
    serve([user])
    const { router } = mountPage(<UsersPage />, ['/admin/users?q=a', '/admin/users?user=2'])
    await waitFor(() => expect(where()).toBe('/admin/users/2'))
    await router.navigate(-1)
    await waitFor(() => expect(where()).toBe('/admin/users?q=a'))
  })

  it('leaves a malformed id alone: the list opens', async () => {
    serve([user])
    mountPage(<UsersPage />, '/admin/users?user=abc')
    expect(await screen.findByText('old-name')).toBeTruthy()
    expect(where()).toBe('/admin/users?user=abc')
  })
})

describe("a user's page", () => {
  it("says so in the page's own frame when there is no such user, with a way back", async () => {
    serve([user], { '/admin/users/99': Object.assign(new Error('gone'), { response: { status: 404 } }) })
    mountPage(<UsersPage />, '/admin/users/99')
    expect(await screen.findByText('This user no longer exists')).toBeTruthy()
    expect(screen.getAllByRole('link', { name: 'Users' }).length).toBeGreaterThan(0)
  })

  it('treats an address that is not a number as no such user, without asking the server', async () => {
    serve([user])
    mountPage(<UsersPage />, '/admin/users/abc')
    expect(await screen.findByText('This user no longer exists')).toBeTruthy()
    expect(api.get.mock.calls.some(c => String(c[0]).startsWith('/admin/users/abc'))).toBe(false)
  })

  it('says it is not open to you for a 403, and offers a retry for any other failure', async () => {
    serve([user], { '/admin/users/98': Object.assign(new Error('no'), { response: { status: 403 } }), '/admin/users/97': Object.assign(new Error('boom'), { response: { status: 500 } }) })
    const { unmount } = mountPage(<UsersPage />, '/admin/users/98')
    expect(await screen.findByText('You cannot open this user')).toBeTruthy()
    unmount()
    mountPage(<UsersPage />, '/admin/users/97')
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(screen.getByRole('button', { name: /Retry|Try again/ })).toBeTruthy()
  })

  it("goes back to the list as it was left: the name opens the page, the back link restores page, search and order", async () => {
    serve([user])
    mountPage(<UsersPage />, '/admin/users?q=old&sort=upn-asc&page=1')
    fireEvent.click(await screen.findByRole('link', { name: user.upn }))
    await screen.findByRole('heading', { level: 1, name: user.upn })
    const back = screen.getByRole('link', { name: 'Users' })
    expect(back.getAttribute('href')).toBe('/admin/users?q=old&sort=upn-asc&page=1')
    fireEvent.click(back)
    await waitFor(() => expect(where()).toBe('/admin/users?q=old&sort=upn-asc&page=1'))
    await waitFor(() => expect(lastParams('/admin/users')).toMatchObject({ keyword: 'old', sort_by: 'upn', sort_dir: 'asc' }))
  })

  it('opens on a click of the row too, and its back link is the plain list when opened cold', async () => {
    serve([user])
    const { unmount } = mountPage(<UsersPage />, '/admin/users?q=old')
    fireEvent.click((await screen.findByText('old-name')).closest('tr')!)
    await waitFor(() => expect(where()).toBe('/admin/users/2'))
    expect((await screen.findByRole('link', { name: 'Users' })).getAttribute('href')).toBe('/admin/users?q=old')
    unmount()
    mountPage(<UsersPage />, '/admin/users/2')
    expect((await screen.findByRole('link', { name: 'Users' })).getAttribute('href')).toBe('/admin/users')
  })

  it('shows who the user is and both switches in words, with the explanations one tap away', async () => {
    serve([{ ...user, role: 'operator', expire_at: '2020-01-01T00:00:00Z' }])
    mountPage(<UsersPage />, '/admin/users/2')
    expect((await screen.findByRole('heading', { level: 1 })).textContent).toBe(user.upn)
    expect(screen.getByText('old-name · user@example.test')).toBeTruthy()
    const header = document.querySelector('[data-slot="object-page-header"]') as HTMLElement
    expect(within(header).getByText('Operator')).toBeTruthy()
    // Account and service are separate lines: an expired user can still sign in.
    expect(within(header).getByText('Account')).toBeTruthy()
    expect(within(header).getByText('Proxy service')).toBeTruthy()
    fireEvent.click(within(header).getAllByRole('button', { name: /sign in/ })[0])
  })

  it('disables the account from the header with the reason dialog, and asks nothing of your own account', async () => {
    serve([user, account(1, 'me')])
    mountPage(<UsersPage />, '/admin/users/2')
    fireEvent.click(await screen.findByRole('button', { name: 'Disable account' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: /^Disable/ }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/users/2/set-enabled', { enabled: false, reason: undefined }))
  })

  it('switches off Disable on your own account, with the reason', async () => {
    serve([account(1, 'me')])
    mountPage(<UsersPage />, '/admin/users/1')
    const off = await screen.findByRole('button', { name: 'Disable account' })
    expect(off.getAttribute('aria-disabled')).toBe('true')
    expect(off.getAttribute('title')).toBe('You cannot disable your own account')
    fireEvent.click(off)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('puts the rest in More: reset 2FA is off with its reason when no authenticator app is set up', async () => {
    serve([user])
    mountPage(<UsersPage />, '/admin/users/2')
    fireEvent.click(await screen.findByRole('button', { name: 'More' }))
    const item = await screen.findByRole('menuitem', { name: 'Reset authenticator app (TOTP)' })
    expect(item.hasAttribute('data-disabled') || item.getAttribute('aria-disabled') === 'true').toBe(true)
    expect(screen.getByText('No authenticator app is set up')).toBeTruthy()
    for (const name of ['Copy subscription URL', 'Renew 30 days', 'Reset password', 'Reset credentials', 'Delete']) {
      expect(screen.getByRole('menuitem', { name })).toBeTruthy()
    }
  })

  it('deletes from the page after a confirmation naming the user, then leaves for the list without asking', async () => {
    serve([user])
    mountPage(<UsersPage />, '/admin/users/2')
    fireEvent.click(await screen.findByRole('button', { name: 'More' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog.textContent).toContain(user.upn)
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' })))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/users/2'))
    await waitFor(() => expect(where()).toBe('/admin/users'))
  })
})
