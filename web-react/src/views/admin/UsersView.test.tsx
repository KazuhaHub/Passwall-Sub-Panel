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
