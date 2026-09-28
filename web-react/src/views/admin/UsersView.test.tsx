// @vitest-environment jsdom
import { StrictMode } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, editRow, installReads, list, mount, snack, user } from '@/test/adminSaveHarness'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
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
  expect(api.put.mock.calls[0][1]).not.toHaveProperty('upn')
  const reopened = await editRow('new-name')
  expect(within(reopened).getByDisplayValue('new-name')).toBeTruthy()
})

it('saves an edited UPN and shows the returned canonical value', async () => {
  const saved = { ...user, upn: 'renamed@example.test' }
  installReads({ '/admin/users': list([user]) })
  api.put.mockResolvedValueOnce({ data: saved })
  mount(<UsersView />)
  const dialog = await editRow()
  fireEvent.change(within(dialog).getByRole('textbox', { name: 'admin:users.field.upn' }), {
    target: { value: ' Renamed@Example.Test ' },
  })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledWith(`/admin/users/${user.id}`, expect.objectContaining({
    upn: ' Renamed@Example.Test ',
  })))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const reopened = await editRow()
  expect(within(reopened).getByRole('textbox', { name: 'admin:users.field.upn' })).toHaveProperty('value', saved.upn)
})

it('updates the open edit dialog status after resuming the service', async () => {
  const suspended = { ...user, service_status: 'manual_suspended' }
  installReads({ '/admin/users': list([suspended]) })
  mount(<UsersView />)
  const dialog = await editRow()

  // The dialog renders the same read-only status the table row does.
  expect(within(dialog).getByDisplayValue('admin:users.status.service_suspended')).toBeTruthy()

  // Resuming succeeds and the reloaded row comes back active.
  installReads({ '/admin/users': list([user]) })
  fireEvent.click(within(dialog).getByRole('button', { name: 'admin:users.more_menu.resume_service' }))

  await waitFor(() =>
    expect(within(dialog).getByDisplayValue('admin:users.status.service_active')).toBeTruthy(),
  )
})

it('marks usage unknown instead of zero for a user outside the usage leaderboard', async () => {
  // The usage column is fed by the Top-N leaderboard, so a user who is not in
  // it has UNKNOWN usage. Rendering "0 / 100 GB" understates what they used.
  const limited = { ...user, display_name: 'limited-user', traffic_limit_bytes: 100 * 1024 ** 3 }
  installReads({ '/admin/users': list([limited]), '/admin/traffic/top': { items: [] } })
  mount(<UsersView />)

  const row = (await screen.findByText('limited-user')).closest('tr')!
  await waitFor(() => expect(row.textContent).toContain('100 GB'))
  expect(row.textContent).not.toMatch(/0 \/ 100 GB/)
})

it('still shows the real usage for a user the leaderboard covers', async () => {
  const limited = { ...user, display_name: 'limited-user', traffic_limit_bytes: 100 * 1024 ** 3 }
  installReads({
    '/admin/users': list([limited]),
    '/admin/traffic/top': { items: [{ user_id: user.id, period_used_bytes: 5 * 1024 ** 3 }] },
  })
  mount(<UsersView />)

  const row = (await screen.findByText('limited-user')).closest('tr')!
  await waitFor(() => expect(row.textContent).toContain('5 / 100 GB'))
})

it('keeps the batch selection across a same-scope refresh', async () => {
  // A refresh returns the same rows as a new array. Clearing the selection on
  // data identity would silently drop a batch the admin is still assembling —
  // the selection may only reset when the QUERY (page/sort/filter) changes.
  installReads({ '/admin/users': list([user]) })
  // The harness hands back the same array instance every call, which React's
  // state bail-out hides. A real server returns fresh objects, so clone the
  // list response to reproduce what a refresh actually delivers.
  const base = api.get.getMockImplementation()!
  api.get.mockImplementation(async (url: string) => {
    const res = await base(url)
    if (url !== '/admin/users') return res
    return { data: { ...res.data, items: res.data.items.map((u: object) => ({ ...u })) } }
  })
  mount(<UsersView />)

  const row = (await screen.findByText('old-name')).closest('tr')!
  const box = within(row).getByRole('checkbox') as HTMLInputElement
  fireEvent.click(box)
  await waitFor(() => expect(box.checked).toBe(true))

  const before = api.get.mock.calls.length
  fireEvent.click(screen.getByRole('button', { name: 'admin:users.refresh' }))
  await waitFor(() => expect(api.get.mock.calls.length).toBeGreaterThan(before))

  // Re-query the row: asserting on the pre-refresh node would pass even if the
  // row were unmounted, because a detached input keeps its `checked` value.
  const refreshed = (await screen.findByText('old-name')).closest('tr')!
  await waitFor(() =>
    expect((within(refreshed).getByRole('checkbox') as HTMLInputElement).checked).toBe(true),
  )
})

it('does not re-scan the usage leaderboard on every list refresh', async () => {
  // /admin/traffic/top walks every user server-side. Tying it to the list's data
  // identity makes each refresh pay a full scan — and once the list polls, that
  // becomes a per-poll cost. It should load once per page size, not per refresh.
  installReads({ '/admin/users': list([user]) })
  const topCalls = () => api.get.mock.calls.filter(c => c[0] === '/admin/traffic/top').length
  const listCalls = () => api.get.mock.calls.filter(c => c[0] === '/admin/users').length

  mount(<UsersView />)
  await screen.findByText('old-name')
  await waitFor(() => expect(topCalls()).toBeGreaterThan(0))

  // Compare against what mount itself cost rather than a literal: StrictMode
  // double-invokes effects, so the absolute count is not the thing under test.
  const topBefore = topCalls()
  const listBefore = listCalls()
  fireEvent.click(screen.getByRole('button', { name: 'admin:users.refresh' }))
  await waitFor(() => expect(listCalls()).toBeGreaterThan(listBefore))

  expect(topCalls()).toBe(topBefore)
})

it('keeps the draft but flags a row that moved underneath an open edit dialog', async () => {
  const original = { ...user, display_name: 'old-name' }
  installReads({ '/admin/users': list([original]) })
  // Serve a mutable row so a refresh can deliver a change made elsewhere.
  let savedName = 'old-name'
  const base = api.get.getMockImplementation()!
  api.get.mockImplementation(async (url: string) => {
    const res = await base(url)
    if (url !== '/admin/users') return res
    return { data: { ...res.data, items: [{ ...res.data.items[0], display_name: savedName }] } }
  })
  mount(<UsersView />)
  const dialog = await editRow()

  // Admin starts editing...
  fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'my-draft' } })

  // ...then the saved row changes underneath them.
  savedName = 'changed-elsewhere'
  // Queried from the DOM, not by role: MUI marks everything outside an open
  // Dialog aria-hidden, so role queries cannot reach the toolbar behind it.
  const refreshBtn = Array.from(document.querySelectorAll('button'))
    .find(b => b.textContent?.includes('admin:users.refresh'))!
  fireEvent.click(refreshBtn)

  // The unsaved draft must survive the refresh...
  await waitFor(() => expect(within(dialog).getByDisplayValue('my-draft')).toBeTruthy())
  // ...but the admin has to be told the stored row moved on.
  await waitFor(() => expect(within(dialog).getByText('admin:users.edit.row_changed')).toBeTruthy())

  fireEvent.click(within(dialog).getByRole('button', { name: 'admin:users.edit.reload_saved' }))
  await waitFor(() => expect(within(dialog).getByDisplayValue('changed-elsewhere')).toBeTruthy())
})

it('re-reads the usage leaderboard after a write that changes usage', async () => {
  // The leaderboard is a full-table scan server-side, so it is deliberately not
  // tied to the list refresh — which means nothing would update it after a
  // usage edit unless the write invalidates it explicitly.
  installReads({ '/admin/users': list([user]) })
  const topCalls = () => api.get.mock.calls.filter(c => c[0] === '/admin/traffic/top').length
  api.put.mockResolvedValueOnce({ data: { ...user } })

  mount(<UsersView />)
  const dialog = await editRow()
  await waitFor(() => expect(topCalls()).toBeGreaterThan(0))
  const before = topCalls()

  fireEvent.change(within(dialog).getByLabelText('admin:users.field.period_used_gb'), {
    target: { value: '5' },
  })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(topCalls()).toBeGreaterThan(before))
})


// ---- The risk column and the in-place risk drawer ----------------------------
//
// The Users page is where an admin already is when they look at one account,
// so the risk center comes to it: a chip per account the risk center has an
// opinion on, and the same drawer the risk center opens, driven by `?risk=`
// so a copied link reopens it and the phone's Back closes it.

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

/** The address bar, and a Back button, beside the page. */
function Where() {
  const loc = useLocation()
  const navigate = useNavigate()
  return (
    <>
      <p data-testid="location">{loc.pathname + loc.search}</p>
      <button type="button" onClick={() => void navigate(-1)}>history-back</button>
    </>
  )
}

/** The page at `path`, one history entry after another page, so Back is observable. */
function mountAt(path: string) {
  return render(
    <StrictMode>
      <MemoryRouter initialEntries={['/admin/dashboard', path]} initialIndex={1}>
        <ThemeProvider theme={theme}>
          <QueryClientProvider client={makeTestQueryClient()}>
            <Routes>
              <Route path="/admin/users" element={<><UsersView /><Where /></>} />
              <Route path="/admin/dashboard" element={<p data-testid="location">/admin/dashboard</p>} />
            </Routes>
          </QueryClientProvider>
        </ThemeProvider>
      </MemoryRouter>
    </StrictMode>,
  )
}

const where = () => screen.getByTestId('location').textContent ?? ''
/** The drawer's param; the list's own params (its default sort) ride along. */
const riskParam = () => new URL(where(), 'http://x').searchParams.get('risk')
const CHIP = 'admin:users.risk_chip_hint'
const levelsCalls = () => api.get.mock.calls.filter(c => c[0] === '/admin/risk-center/levels')
const headers = () => screen.getAllByRole('columnheader').map(th => th.textContent)

const account = (id: number, name: string, extra: Record<string, unknown> = {}) =>
  ({ ...user, id, upn: `${name}@example.test`, email: `${name}@example.test`, display_name: name, ...extra })
const flagged = account(2, 'flagged')
const suspect = account(3, 'suspect')
const held = account(4, 'held')
const dismissed = account(5, 'dismissed')
const trusted = account(6, 'trusted')
const clean = account(7, 'clean')

const LEVELS = {
  '2': { level: 'flagged', auto_suspended: false, open: true, dismissed: false, trusted: false },
  '3': { level: 'suspect', auto_suspended: false, open: true, dismissed: false, trusted: false },
  '4': { level: '', auto_suspended: true, open: true, dismissed: false, trusted: false },
  '5': { level: 'flagged', auto_suspended: false, open: false, dismissed: true, trusted: false },
  '6': { level: '', auto_suspended: false, open: false, dismissed: false, trusted: true },
}

const NO_REVIEW = {
  dismissed: false, dismissed_at_ms: 0, dismissed_by: 0, dismissed_by_upn: '', note: '', levels: {},
  reopened: false, lapsed: false, escalated: [], trusted: false, trusted_at_ms: 0, trusted_by: 0, trusted_by_upn: '',
}

/** The drawer's read for one account: enough to render its header and 概览. */
function summaryOf(u: { id: number; upn: string }) {
  return {
    user: { id: u.id, upn: u.upn, display_name: '', role: 'user', group_id: 1, group_name: 'group', enabled: true,
      traffic_limit_bytes: 0 },
    attention: [], review: NO_REVIEW, geo: null, signals: [],
    live: { items: [], total: 0, page: 1, page_size: 1, panels: [], device_window_hours: 24,
      devices_unavailable: false, snapshot: null, refresh: { cooldown_seconds: 30, available_in_seconds: 0 } },
    devices: [], device_window_hours: 24, devices_unavailable: false,
  }
}

/** Reads for the page with the risk column and the drawer of each account. */
function serveRisk(users: { id: number; upn: string }[], levels: unknown = LEVELS) {
  const reads: Record<string, unknown> = { '/admin/users': list(users), '/admin/risk-center/levels': levels }
  for (const u of users) {
    reads[`/admin/risk-center/users/${u.id}`] = summaryOf(u)
    reads[`/admin/traffic/user/${u.id}`] = { user_id: u.id, permanent_total_bytes: 0, period_used_bytes: 0, today_used_bytes: 0 }
  }
  installReads(reads)
}

async function rowOf(name: string) {
  return (await screen.findByText(name)).closest('tr')!
}

describe('the risk column', () => {
  it('shows an admin each account the risk center lists, from one /levels read', async () => {
    serveRisk([flagged, suspect, held, dismissed, trusted, clean])
    mountAt('/admin/users')

    const chipOf = async (name: string) => within(await rowOf(name)).findByRole('button', { name: CHIP })
    const f = await chipOf('flagged')
    expect(f.textContent).toBe('admin:risk_center.state.flagged')
    expect(f.className).toMatch(/MuiChip-filled/)
    expect(f.className).toMatch(/MuiChip-colorError/)

    const s = await chipOf('suspect')
    expect(s.textContent).toBe('admin:risk_center.state.suspect')
    expect(s.className).toMatch(/MuiChip-filled/)
    expect(s.className).toMatch(/MuiChip-colorWarning/)

    // Held with no fresh verdict: the hold is the reason it is listed. The
    // chip says it in the risk center's short level word — the status column
    // beside it already names the hold in full, and a six-character chip
    // widened the list past the screen — and names it in full on hover.
    const h = await chipOf('held')
    expect(h.textContent).toBe('admin:risk_center.flags.level.suspended')
    expect(h.className).toMatch(/MuiChip-filled/)
    expect(h.className).toMatch(/MuiChip-colorError/)
    fireEvent.mouseOver(h)
    expect((await screen.findByRole('tooltip')).textContent).toBe('admin:users.status.geo_auto')
    fireEvent.mouseLeave(h)
    await waitFor(() => expect(screen.queryByRole('tooltip')).toBeNull())

    // Dismissed: still there, drawn outlined, and SAYS it is dismissed — a
    // label reading 已标记 read as open to anyone who did not hover. The
    // level it was dismissed at is on hover.
    const d = await chipOf('dismissed')
    expect(d.textContent).toBe('admin:risk_center.review.badge_dismissed')
    expect(d.className).toMatch(/MuiChip-outlined/)
    expect(d.className).toMatch(/MuiChip-colorError/)
    fireEvent.mouseOver(d)
    expect((await screen.findByRole('tooltip')).textContent).toBe('admin:risk_center.state.flagged')

    // Trusted with nothing at attention: an outlined 已信任.
    const tr = await chipOf('trusted')
    expect(tr.textContent).toBe('admin:risk_center.review.badge_trusted')
    expect(tr.className).toMatch(/MuiChip-outlined/)
    expect(tr.className).toMatch(/MuiChip-colorDefault/)

    // An account the risk center has nothing on: a blank cell.
    expect(within(await rowOf('clean')).queryByRole('button', { name: CHIP })).toBeNull()

    // One column, between 状态 and 最近活跃, and one read for the whole page.
    const cols = headers()
    expect(cols.indexOf('admin:users.table.risk')).toBe(cols.indexOf('admin:users.table.status') + 1)
    expect(cols[cols.indexOf('admin:users.table.risk') + 1]).toBe('admin:users.table.last_online')
    expect(new Set(levelsCalls().map(c => c[0])).size).toBe(1)
  })

  it("re-reads the column after this page's own resume, so a lifted hold's chip goes", async () => {
    // /levels has no interval: nothing but an invalidation re-reads it while
    // the admin stays on the page, and the row's status would say active
    // beside a chip still naming the hold.
    const onHold = { ...held, service_status: 'manual_suspended', service_disabled_reason: 'geo_auto' }
    serveRisk([onHold])
    mountAt('/admin/users')
    const row = await rowOf('held')
    expect((await within(row).findByRole('button', { name: CHIP })).textContent)
      .toBe('admin:risk_center.flags.level.suspended')

    serveRisk([held], {})
    fireEvent.click(within(row).getByTestId('MoreVertIcon').closest('button')!)
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:users.more_menu.resume_service' }))

    await waitFor(() => expect(within(row).queryByRole('button', { name: CHIP })).toBeNull())
  })

  it("re-reads the column after this page's own suspend", async () => {
    serveRisk([suspect])
    mountAt('/admin/users')
    const row = await rowOf('suspect')
    await within(row).findByRole('button', { name: CHIP })
    await waitFor(() => expect(levelsCalls().length).toBeGreaterThan(0))
    const before = levelsCalls().length

    fireEvent.click(within(row).getByTestId('MoreVertIcon').closest('button')!)
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:users.more_menu.suspend_service' }))

    await waitFor(() => expect(levelsCalls().length).toBeGreaterThan(before))
  })

  it('is only as wide as its chip, so the list still fits a laptop screen', async () => {
    // At 1440×900 the list had about 57px to spare; a column at the table's
    // default 16px side padding around a default small chip took 90 and cut
    // off 操作. The neighbours' padding spaces this one, and the chip is dense.
    serveRisk([flagged])
    mountAt('/admin/users')
    const chip = await within(await rowOf('flagged')).findByRole('button', { name: CHIP })
    const th = screen.getAllByRole('columnheader').find(el => el.textContent === 'admin:users.table.risk')!
    for (const cell of [th, chip.closest('td')!]) {
      expect(getComputedStyle(cell).paddingLeft).toBe('0px')
      expect(getComputedStyle(cell).paddingRight).toBe('0px')
    }
    expect(getComputedStyle(chip).height).toBe('20px')
    expect(getComputedStyle(chip).fontSize).toBe('12px')
  })

  it('spans the empty row across the extra column for an admin', async () => {
    serveRisk([])
    mountAt('/admin/users')
    await waitFor(() => expect(document.querySelector('tbody td[colspan]')?.textContent).toBe('—'))
    expect(headers()).toHaveLength(11)
    expect(document.querySelector('tbody td[colspan]')?.getAttribute('colspan')).toBe('11')
  })

  it('is neither shown to an operator nor read for one', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    serveRisk([flagged])
    mountAt('/admin/users')
    const row = await rowOf('flagged')
    expect(headers()).not.toContain('admin:users.table.risk')
    expect(headers()).toHaveLength(10)
    expect(within(row).queryByRole('button', { name: CHIP })).toBeNull()
    // Let every mount-time read go out before asserting one never did.
    await waitFor(() => expect(api.get.mock.calls.some(c => c[0] === '/admin/traffic/top')).toBe(true))
    expect(levelsCalls()).toHaveLength(0)
  })

  it('an operator\'s empty row spans the ten columns it has', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    serveRisk([])
    mountAt('/admin/users')
    await waitFor(() => expect(document.querySelector('tbody td[colspan]')?.textContent).toBe('—'))
    expect(document.querySelector('tbody td[colspan]')?.getAttribute('colspan')).toBe('10')
  })

  it('stays blank, with no toast, when /levels fails', async () => {
    // No /levels in the reads: the harness throws for it.
    installReads({ '/admin/users': list([flagged]) })
    mountAt('/admin/users')
    const row = await rowOf('flagged')
    await waitFor(() => expect(levelsCalls().length).toBeGreaterThan(0))
    // The read opts out of the global error toast: it only decorates the list.
    expect(levelsCalls()[0][1]).toMatchObject({ _skipErrorToast: true })
    expect(headers()).toContain('admin:users.table.risk')
    expect(within(row).queryByRole('button', { name: CHIP })).toBeNull()
    expect(snack).not.toHaveBeenCalled()
  })
})

describe('the in-place risk drawer', () => {
  it('opens from a chip with risk=<id> pushed, over the list, and Back closes it', async () => {
    serveRisk([flagged, clean])
    mountAt('/admin/users')
    const chip = await within(await rowOf('flagged')).findByRole('button', { name: CHIP })

    fireEvent.click(chip)

    expect(riskParam()).toBe('2')
    const header = await screen.findByTestId('risk-drawer-header')
    expect(within(header).getByText('flagged@example.test')).toBeTruthy()
    // In place: the list is still there beneath it.
    expect(screen.getByText('clean')).toBeTruthy()

    act(() => { fireEvent.click(screen.getByText('history-back')) })
    expect(where()).toMatch(/^\/admin\/users/)
    expect(riskParam()).toBeNull()
    await waitFor(() => expect(screen.queryByTestId('risk-drawer-header')).toBeNull())
  })

  it('closed with its X, leaves no entry behind: ONE Back then leaves the page', async () => {
    // The list's own URL sync runs on every URL change; had it replaced the
    // pushed entry, the drawer's mark would be gone, close() would replace
    // the param away, and the pushed entry would stay behind as a dead one.
    serveRisk([flagged, clean])
    mountAt('/admin/users')
    fireEvent.click(await within(await rowOf('flagged')).findByRole('button', { name: CHIP }))
    const header = await screen.findByTestId('risk-drawer-header')

    fireEvent.click(within(header).getByRole('button', { name: 'admin:risk_center.drawer.close' }))
    await waitFor(() => expect(riskParam()).toBeNull())
    expect(where()).toMatch(/^\/admin\/users/)

    act(() => { fireEvent.click(screen.getByText('history-back')) })
    await waitFor(() => expect(where()).toBe('/admin/dashboard'))
  })

  it('opens for a deep link to ?risk=<id>', async () => {
    serveRisk([flagged])
    mountAt('/admin/users?risk=2')
    const header = await screen.findByTestId('risk-drawer-header')
    expect(within(header).getByText('flagged@example.test')).toBeTruthy()
  })

  it('the row menu offers 风控详情 for any account, to an admin', async () => {
    serveRisk([clean], {})
    mountAt('/admin/users')
    const row = await rowOf('clean')
    fireEvent.click(within(row).getByTestId('MoreVertIcon').closest('button')!)
    // Beside 账号安全.
    const items = (await screen.findAllByRole('menuitem')).map(el => el.textContent)
    expect(items.indexOf('admin:users.more_menu.risk_details'))
      .toBe(items.indexOf('admin:users.more_menu.account_security') + 1)

    fireEvent.click(screen.getByRole('menuitem', { name: 'admin:users.more_menu.risk_details' }))

    expect(riskParam()).toBe('7')
    const header = await screen.findByTestId('risk-drawer-header')
    expect(within(header).getByText('clean@example.test')).toBeTruthy()
  })

  it('the row menu offers an operator no 风控详情', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    serveRisk([clean], {})
    mountAt('/admin/users')
    const row = await rowOf('clean')
    fireEvent.click(within(row).getByTestId('MoreVertIcon').closest('button')!)
    expect(await screen.findByRole('menuitem', { name: 'admin:users.more_menu.account_security' })).toBeTruthy()
    expect(screen.queryByRole('menuitem', { name: 'admin:users.more_menu.risk_details' })).toBeNull()
  })

  it("the edit dialog's 风控查询 opens the drawer above the dialog, and closing it returns there", async () => {
    serveRisk([user], {})
    mountAt('/admin/users')
    const dialog = await editRow()

    fireEvent.click(within(dialog).getByRole('button', { name: /admin:users\.risk_lookup/ }))

    expect(riskParam()).toBe(String(user.id))
    const header = await screen.findByTestId('risk-drawer-header')
    expect(within(header).getByText(user.upn)).toBeTruthy()
    // The dialog is still mounted beneath the drawer, draft and all.
    expect(dialog.isConnected).toBe(true)
    expect(within(dialog).getByDisplayValue('old-name')).toBeTruthy()

    fireEvent.click(within(header).getByRole('button', { name: 'admin:risk_center.drawer.close' }))

    // Closed the way it opened: Back, onto the list's own entry.
    await waitFor(() => expect(riskParam()).toBeNull())
    expect(where()).toMatch(/^\/admin\/users/)
    await waitFor(() => expect(screen.queryByTestId('risk-drawer-header')).toBeNull())
    expect(within(dialog).getByDisplayValue('old-name')).toBeTruthy()
  })

  it('offers an operator no risk lookup in the edit dialog', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    installReads({ '/admin/users': list([user]) })
    mount(<UsersView />)
    const dialog = await editRow()
    expect(within(dialog).queryByRole('button', { name: /admin:users\.risk_lookup/ })).toBeNull()
    expect(within(dialog).queryByRole('link', { name: /admin:users\.risk_lookup/ })).toBeNull()
    // The usage link beside it stays: the traffic page is staff-visible.
    expect(within(dialog).getByRole('link', { name: /admin:users\.detail\.view_usage/ })).toBeTruthy()
  })
})

describe('the service state', () => {
  it.each([
    ['geo_auto', 'admin:users.status.geo_auto'],
    ['geo_anomaly', 'admin:users.status.geo_manual'],
    ['service_manual', 'admin:users.status.service_suspended'],
  ])('names a %s hold as the risk center does, in the row and in the edit dialog', async (reason, label) => {
    const suspended = { ...user, service_status: 'manual_suspended', service_disabled_reason: reason }
    serveRisk([suspended], {})
    mountAt('/admin/users')
    const row = await rowOf('old-name')
    expect(within(row).getByText(label)).toBeTruthy()

    const dialog = await editRow()
    expect(within(dialog).getByDisplayValue(label)).toBeTruthy()
  })
})

describe('the search box', () => {
  it('is seeded from ?q= — the risk drawer links here by UPN', async () => {
    serveRisk([user], {})
    mountAt('/admin/users?q=alice')
    expect(await screen.findByDisplayValue('alice')).toBeTruthy()
    const call = api.get.mock.calls.find(c => c[0] === '/admin/users')
    expect(call?.[1]?.params).toMatchObject({ keyword: 'alice' })
  })
})
