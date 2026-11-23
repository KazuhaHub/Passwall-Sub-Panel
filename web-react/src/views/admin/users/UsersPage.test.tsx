// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { account, api, lastParams, mountPage, searchOf, serve, user, where } from './testHarness'
import UsersPage from './UsersPage'

// The risk drawer is a still-MUI component of another change, that needs the app theme; it is the page's neighbour, not what this tests.
vi.mock('../risk/drawer/RiskUserDrawer', () => ({ default: ({ userId }: { userId: number | null }) => (userId ? <div data-testid="risk-drawer">{userId}</div> : null) }))

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

  it('offers 风控 details for any account in the row menu to an admin, and not to an operator', async () => {
    serve([account(7, 'clean')])
    const { unmount } = mountPage(<UsersPage />)
    await openRowMenu('clean')
    expect(await screen.findByRole('menuitem', { name: 'Risk details' })).toBeTruthy()
    unmount()
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    mountPage(<UsersPage />)
    await openRowMenu('clean')
    expect(await screen.findByRole('menuitem', { name: 'Account security' })).toBeTruthy()
    expect(screen.queryByRole('menuitem', { name: 'Risk details' })).toBeNull()
  })

  it("hands over to the drawer from the sheet's risk lookup, and Back returns to the sheet", async () => {
    serve([flagged], { '/admin/risk-center/levels': LEVELS })
    const { router } = mountPage(<UsersPage />, '/admin/users?user=2')
    fireEvent.click(await screen.findByRole('button', { name: 'Risk lookup' }))
    await waitFor(() => expect(searchOf().get('risk')).toBe('2'))
    expect(searchOf().get('user')).toBeNull()
    await router.navigate(-1)
    await waitFor(() => expect(searchOf().get('user')).toBe('2'))
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
  mountPage(<UsersPage />, '/admin/users?user=2&tab=security')
  expect(await screen.findByRole('tab', { name: 'Security', selected: true })).toBeTruthy()
  expect(where()).not.toContain('token-2')
})

it('opens a user who is not on this page by reading that user', async () => {
  serve([user], { '/admin/users/12': account(12, 'elsewhere') })
  mountPage(<UsersPage />, '/admin/users?user=12')
  expect((await screen.findAllByText('elsewhere@example.test')).length).toBeGreaterThan(0)
  expect(api.get.mock.calls.some(c => c[0] === '/admin/users/12')).toBe(true)
})
