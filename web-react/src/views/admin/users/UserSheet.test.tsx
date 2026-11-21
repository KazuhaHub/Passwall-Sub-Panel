// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { account, api, group, list, mountPage, searchOf, serve, toast, user } from './testHarness'
import UsersPage from './UsersPage'

vi.mock('../risk/drawer/RiskUserDrawer', () => ({ default: () => null }))

const GB = 1024 ** 3
/** Opens the row's edit form, as an admin does. */
async function openSettings(name = 'old-name') {
  const row = (await screen.findByText(name)).closest('tr')!
  fireEvent.click(within(row).getByRole('button', { name: 'Edit' }))
  return screen.findByRole('textbox', { name: 'Display name' })
}
const put = () => api.put.mock.calls.find(c => String(c[0]).startsWith('/admin/users/'))
const saveBar = () => screen.getByRole('region', { name: 'You have unsaved changes' })

describe('the settings form', () => {
  it('opens on the settings tab from the row, with the user and the tab in the address', async () => {
    serve([user])
    mountPage(<UsersPage />)
    await openSettings()
    expect(searchOf().get('user')).toBe('2')
    expect(searchOf().get('tab')).toBe('settings')
    expect(screen.getByRole('textbox', { name: /UPN/ })).toHaveProperty('value', user.upn)
  })

  it('saves the profile with the same request it always sent, and keeps the saved row', async () => {
    serve([user])
    api.put.mockResolvedValueOnce({ data: { ...user, display_name: 'new-name' } })
    mountPage(<UsersPage />)
    const name = await openSettings()
    fireEvent.change(name, { target: { value: 'new-name' } })
    fireEvent.click(within(saveBar()).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(put()).toBeTruthy())
    expect(put()![0]).toBe('/admin/users/2')
    expect(put()![1]).toEqual({
      group_id: 1, email: user.email, traffic_reset_period: 'monthly', remark: '', display_name: 'new-name', role: 'user',
      traffic_limit_gb: 0, ip_limit: 0, device_limit: 0, clear_expire: true,
    })
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Saved', 'success'))
    // The form follows the saved row: nothing left to save.
    await waitFor(() => expect(screen.queryByRole('region', { name: 'You have unsaved changes' })).toBeNull())
    expect(screen.getByRole('textbox', { name: 'Display name' })).toHaveProperty('value', 'new-name')
  })

  it('sends the UPN only when it changed', async () => {
    serve([user])
    api.put.mockResolvedValue({ data: { ...user, upn: 'renamed@example.test' } })
    mountPage(<UsersPage />)
    await openSettings()
    fireEvent.change(screen.getByRole('textbox', { name: /UPN/ }), { target: { value: ' Renamed@Example.Test ' } })
    fireEvent.click(within(saveBar()).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(put()).toBeTruthy())
    expect(put()![1]).toMatchObject({ upn: ' Renamed@Example.Test ' })
  })

  it('sends the inherit flag, never the group\'s number, for a limit that follows the group', async () => {
    const follows = { ...user, inherits_traffic_limit: true, inherits_ip_limit: true, inherits_device_limit: true, traffic_limit_bytes: 50 * GB, ip_limit: 3, device_limit: 2 }
    serve([follows], { '/admin/groups': list([{ ...group, traffic_limit_gb: 50, ip_limit: 3, device_limit: 2 }]) })
    api.put.mockResolvedValue({ data: follows })
    mountPage(<UsersPage />)
    fireEvent.change(await openSettings(), { target: { value: 'x' } })
    fireEvent.click(within(saveBar()).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(put()).toBeTruthy())
    expect(put()![1]).toMatchObject({ inherit_traffic_limit: true, inherit_ip_limit: true, inherit_device_limit: true })
    expect(put()![1]).not.toHaveProperty('traffic_limit_gb')
    expect(put()![1]).not.toHaveProperty('ip_limit')
    expect(put()![1]).not.toHaveProperty('device_limit')
  })

  it('pushes a moved period usage as its own request, and re-reads the leaderboard', async () => {
    serve([user])
    api.put.mockResolvedValue({ data: user })
    mountPage(<UsersPage />)
    await openSettings()
    const topCalls = () => api.get.mock.calls.filter(c => c[0] === '/admin/traffic/top').length
    await waitFor(() => expect(topCalls()).toBeGreaterThan(0))
    const before = topCalls()
    fireEvent.change(screen.getByRole('textbox', { name: 'Period used (GB)' }), { target: { value: '5' } })
    fireEvent.blur(screen.getByRole('textbox', { name: 'Period used (GB)' }))
    fireEvent.click(within(saveBar()).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put.mock.calls.some(c => c[0] === '/admin/traffic/user/2')).toBe(true))
    expect(api.put.mock.calls.find(c => c[0] === '/admin/traffic/user/2')![1]).toEqual({ period_used_gb: 5 })
    await waitFor(() => expect(topCalls()).toBeGreaterThan(before))
  })

  it('writes the account switch through its own endpoint, only when it changed', async () => {
    serve([user])
    api.put.mockResolvedValue({ data: user })
    mountPage(<UsersPage />)
    await openSettings()
    fireEvent.change(screen.getByRole('combobox', { name: 'Account status' }), { target: { value: 'disabled' } })
    fireEvent.click(within(saveBar()).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.post.mock.calls.some(c => c[0] === '/admin/users/2/set-enabled')).toBe(true))
    expect(api.post.mock.calls.find(c => c[0] === '/admin/users/2/set-enabled')![1]).toEqual({ enabled: false, reason: undefined })
  })

  it('keeps the draft but says so when the stored row moved underneath it', async () => {
    let name = 'old-name'
    serve([user])
    const base = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string) => {
      const res = await base(url)
      return url === '/admin/users' ? { data: { ...res.data, items: [{ ...res.data.items[0], display_name: name }] } } : res
    })
    mountPage(<UsersPage />)
    fireEvent.change(await openSettings(), { target: { value: 'my-draft' } })
    name = 'changed-elsewhere'
    fireEvent.click(screen.getAllByRole('button', { name: 'Refresh', hidden: true })[0])

    expect(await screen.findByText('This user was changed elsewhere. Your edits are not updated automatically.')).toBeTruthy()
    expect(screen.getByRole('textbox', { name: 'Display name' })).toHaveProperty('value', 'my-draft')
    fireEvent.click(screen.getByRole('button', { name: 'Reload saved values' }))
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Display name' })).toHaveProperty('value', 'changed-elsewhere'))
  })

  it('previews the node change once the drafted group differs', async () => {
    const other = { ...group, id: 2, slug: 'other', name: 'Other' }
    serve([user], { '/admin/groups': list([group, other]) })
    api.post.mockResolvedValue({ data: { added: [{ id: 9, name: 'JP-1', region: 'JP', visible_lines: 0, total_lines: 0 }], removed: [], kept: 1 } })
    mountPage(<UsersPage />)
    await openSettings()
    expect(screen.queryByTestId('group-change-preview')).toBeNull()
    const input = screen.getByRole('combobox', { name: /Group/ })
    fireEvent.mouseDown(input)
    fireEvent.change(input, { target: { value: 'Oth' } })
    fireEvent.click(await screen.findByRole('option', { name: 'Other', hidden: true }))
    await waitFor(() => expect(screen.getByTestId('group-change-preview').textContent).toContain('JP-1'))
    expect(api.post.mock.calls.at(-1)).toEqual(['/admin/users/2/group-preview', { group_id: 2 }, expect.anything()])
    expect(api.put).not.toHaveBeenCalled()
  })

  it('says "unchanged" neutrally, "unknown" when the preview fails, and nothing once back on the current group', async () => {
    const other = { ...group, id: 2, slug: 'other', name: 'Other' }, third = { ...group, id: 3, slug: 'third', name: 'Third' }
    serve([user], { '/admin/groups': list([group, other, third]) })
    api.post.mockResolvedValue({ data: { added: [], removed: [], kept: 3 } })
    mountPage(<UsersPage />)
    await openSettings()
    const pick = async (name: string) => {
      const input = screen.getByRole('combobox', { name: /Group/ })
      fireEvent.mouseDown(input)
      fireEvent.change(input, { target: { value: name } })
      fireEvent.click(await screen.findByRole('option', { name, hidden: true }))
    }
    await pick('Other')
    await waitFor(() => expect(screen.getByTestId('group-change-preview').textContent).toBe('Moving does not change the available nodes'))
    api.post.mockRejectedValue(new Error('down'))
    await pick('Third')
    await waitFor(() => expect(screen.getByTestId('group-change-preview').textContent).toBe('The effect of moving cannot be determined right now'))
    await pick('group')
    await waitFor(() => expect(screen.queryByTestId('group-change-preview')).toBeNull())
  })

  it('asks before the sheet closes over an unsaved draft, and keeps it on "keep editing"', async () => {
    serve([user])
    mountPage(<UsersPage />)
    fireEvent.change(await openSettings(), { target: { value: 'my-draft' } })
    fireEvent.click(screen.getByRole('button', { name: 'Close details' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Discard unsaved changes?')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(searchOf().get('user')).toBe('2')
    expect(screen.getByRole('textbox', { name: 'Display name' })).toHaveProperty('value', 'my-draft')

    fireEvent.click(screen.getByRole('button', { name: 'Close details' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(searchOf().get('user')).toBeNull())
  })

  it('does not let an operator change roles, and shows no form for an admin account', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    serve([user, account(9, 'boss', { role: 'admin' })])
    mountPage(<UsersPage />)
    await openSettings()
    expect((screen.getByRole('combobox', { name: 'Role' }) as HTMLSelectElement).disabled).toBe(true)
    expect(screen.getByText('Operators cannot change roles')).toBeTruthy()

    // An admin account opened by link: read-only, with the reason.
    mountPage(<UsersPage />, '/admin/users?user=9&tab=settings')
    expect((await screen.findAllByText('Operators cannot modify admin or operator accounts.')).length).toBeGreaterThan(0)
  })
})

describe('creating a user', () => {
  it('sends the create request and then shows the initial password once', async () => {
    serve([])
    api.post.mockResolvedValue({ data: { user: { ...user, id: 5 }, initial_password: 'Secret-pw-123', synced_inbounds: 1 } })
    mountPage(<UsersPage />)
    fireEvent.click(await screen.findByRole('button', { name: 'New user' }))
    await waitFor(() => expect(searchOf().get('user')).toBe('new'))
    fireEvent.change(await screen.findByRole('textbox', { name: /UPN/ }), { target: { value: 'new@example.test' } })
    fireEvent.click(within(saveBar()).getByRole('button', { name: 'New user' }))

    await waitFor(() => expect(api.post.mock.calls.some(c => c[0] === '/admin/users')).toBe(true))
    const body = api.post.mock.calls.find(c => c[0] === '/admin/users')![1]
    expect(body).toMatchObject({ upn: 'new@example.test', group_id: 1, traffic_limit_gb: 0, ip_limit: 0, device_limit: 0, traffic_reset_period: 'monthly' })
    expect(body.expire_date).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Secret-pw-123')).toBeTruthy()
    // The sheet closed without asking: the draft was the thing just sent.
    await waitFor(() => expect(searchOf().get('user')).toBeNull())
  })

  it('does not send a form without a valid UPN', async () => {
    serve([])
    mountPage(<UsersPage />)
    fireEvent.click(await screen.findByRole('button', { name: 'New user' }))
    fireEvent.change(await screen.findByRole('textbox', { name: /UPN/ }), { target: { value: 'not-an-email' } })
    fireEvent.click(within(saveBar()).getByRole('button', { name: 'New user' }))
    expect(api.post).not.toHaveBeenCalled()
  })
})
