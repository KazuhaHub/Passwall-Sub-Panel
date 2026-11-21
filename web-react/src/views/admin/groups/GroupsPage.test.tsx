// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { api, empty, installReads, mountPage, mountWithLinks, PATH, previewOf, reads, scopeReply, team } from './testHarness'
import GroupsPage from './GroupsPage'

// The row is looked up again until it is in the table: the list redraws as the per-group answers arrive.
const rowOf = (name: string) => waitFor(() => {
  const row = screen.getByText(name).closest('tr')
  if (!row) throw new Error(`no row for ${name}`)
  return row
})
async function openMenu(name: string) {
  fireEvent.click(within(await rowOf(name)).getByRole('button', { name: new RegExp(`More actions for ${name}`) }))
}
const menuItem = (name: RegExp | string) => screen.findByRole('menuitem', { name })

describe('the group list', () => {
  it('shows what an admin scans for: members, matched nodes and lines, default limits, two-step and overrides', async () => {
    reads([{ ...team, require_2fa: true }, empty])
    api.post.mockImplementation(async (url: string) => url === '/admin/groups/5/node-preview'
      ? previewOf({ nodes: [{ id: 1, name: 'HK-1', region: 'HK', visible_lines: 2, total_lines: 3 }, { id: 2, name: 'HK-2', region: 'HK', visible_lines: 1, total_lines: 1 }] })
      : previewOf({ nodes: [] }))
    mountPage(<GroupsPage />, PATH)
    const row = await rowOf('Team')
    expect(within(row).getByText('Core')).toBeTruthy()
    expect(within(row).getByText('team')).toBeTruthy()
    // Conditions: the conjunction first, then the first two tags and how many more.
    expect(within(row).getByText('OR')).toBeTruthy()
    expect(within(row).getByText('region:HK')).toBeTruthy()
    expect(within(row).getByText('+2')).toBeTruthy()
    expect(await within(row).findByText('2 nodes · 3 lines')).toBeTruthy()
    expect(within(row).getByText('Traffic 100 GB · IP unlimited')).toBeTruthy()
    expect(within(row).getByText('Required')).toBeTruthy()
    expect(await within(row).findByText('1')).toBeTruthy() // one override (notify.expire_before_days)
    // A filter that reaches nothing is the one count worth words: those members get an empty subscription.
    const quiet = await rowOf('Spare')
    expect(await within(quiet).findByText('No nodes')).toBeTruthy()
    expect(within(quiet).getByText('Not set')).toBeTruthy()
    expect(within(quiet).getByText('All nodes')).toBeTruthy()
  })

  it('asks the saved filter of each group, with the read-only preview endpoint', async () => {
    reads()
    mountPage(<GroupsPage />, PATH)
    await rowOf('Team')
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/groups/5/node-preview',
      { tag_filter: team.tag_filter }, expect.anything()))
  })

  it('reports a failed read instead of an empty table, with a retry', async () => {
    installReads({ '/admin/groups': new Error('offline') })
    mountPage(<GroupsPage />, PATH)
    expect(await screen.findByText('Groups are temporarily unavailable')).toBeTruthy()
    expect(screen.getByRole('button', { name: /Retry/ })).toBeTruthy()
  })

  it('says there are no groups, and filters to nothing with a way back', async () => {
    reads([])
    const { unmount } = mountPage(<GroupsPage />, PATH)
    expect(await screen.findByText('No groups yet')).toBeTruthy()
    unmount()
    reads()
    mountPage(<GroupsPage />, PATH, `${PATH}?q=zzz`)
    expect(await screen.findByText('No groups match the search')).toBeTruthy()
    fireEvent.click(screen.getAllByRole('button', { name: 'Clear search' }).at(-1)!)
    expect(await screen.findByText('Team')).toBeTruthy()
  })

  it('gives a row one visible action and the rest in its menu', async () => {
    reads()
    mountPage(<GroupsPage />, PATH)
    const row = await rowOf('Team')
    expect(within(row).getAllByRole('button').filter(b => /Edit group|More actions/.test(b.getAttribute('aria-label') ?? ''))).toHaveLength(2)
    await openMenu('Team')
    expect(await menuItem('View members')).toBeTruthy()
    expect(await menuItem('Delete')).toBeTruthy()
  })
})

describe('the per-group counts', () => {
  it('never has more than 4 of the list\'s background reads in flight, however many groups there are', async () => {
    const many = Array.from({ length: 30 }, (_, i) => ({ ...empty, id: 100 + i, slug: `g${i}`, name: `Group ${i}` }))
    const extra: Record<string, unknown> = {}
    for (const g of many) extra[`/admin/groups/${g.id}/scope-settings`] = scopeReply()
    reads(many, extra)
    let now = 0; let peak = 0
    const slow = async <T,>(v: T) => { now++; peak = Math.max(peak, now); await new Promise(r => setTimeout(r, 5)); now--; return v }
    const get = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string, cfg?: unknown) => (String(url).endsWith('/scope-settings') ? slow(await get(url, cfg)) : get(url, cfg)))
    api.post.mockImplementation(async () => slow(previewOf()))
    mountPage(<GroupsPage />, PATH)
    await rowOf('Group 0')
    await waitFor(() => expect(api.post.mock.calls.length).toBeGreaterThanOrEqual(25), { timeout: 5000 })
    await waitFor(() => expect(now).toBe(0))
    expect(peak).toBeLessThanOrEqual(4)
    expect(peak).toBeGreaterThan(1)
    // Only the rows on the page are asked about, not all 30.
    expect(api.post.mock.calls.length).toBe(25)
  })
})

describe('role gating', () => {
  it('lets an operator read the list: no create, no row actions, no checkboxes, and none of the admin-only reads', async () => {
    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    reads()
    mountPage(<GroupsPage />, PATH, `${PATH}?edit=5`)
    await rowOf('Team')
    expect(screen.queryByRole('button', { name: 'New group' })).toBeNull()
    expect(screen.queryByRole('button', { name: /More actions/ })).toBeNull()
    expect(screen.queryByRole('checkbox')).toBeNull()
    // Not even a link to the sheet opens it.
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.queryByRole('columnheader', { name: /Nodes and lines/ })).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
    expect(api.get.mock.calls.some(([url]) => String(url).includes('scope-settings'))).toBe(false)
  })

  it('keeps the destinations tab to administrators', async () => {
    reads()
    mountPage(<GroupsPage />, PATH, `${PATH}?edit=5`)
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).getByRole('tab', { name: 'Destinations' })).toBeTruthy()
  })
})

describe('deleting a group', () => {
  it('names the group and what goes with it, focuses Cancel, and deletes only after the answer', async () => {
    reads()
    api.delete.mockResolvedValue({})
    mountPage(<GroupsPage />, PATH)
    await openMenu('Spare')
    fireEvent.click(await menuItem('Delete'))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Delete group Spare?')).toBeTruthy()
    expect(within(dialog).getByText(/setting overrides and its destination allowlist/)).toBeTruthy()
    expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/groups/6'))
  })

  it('does not ask for a group that has members: it says how many, and offers the way forward', async () => {
    reads()
    const { router } = mountWithLinks(<GroupsPage />, PATH)
    await openMenu('Team')
    fireEvent.click(await menuItem('Delete'))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Group Team still has members')).toBeTruthy()
    expect(within(dialog).getByText(/3 users are in this group/)).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Go to Users' }))
    expect(api.delete).not.toHaveBeenCalled()
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/users'))
  })

  it('offers a checkbox only on empty groups, and a bulk delete that confirms and reports each outcome', async () => {
    reads([team, empty, { ...empty, id: 7, slug: 'two', name: 'Two' }])
    api.delete.mockImplementation(async (url: string) => { if (url.endsWith('/7')) throw new Error('nope'); return {} })
    mountPage(<GroupsPage />, PATH)
    await rowOf('Team')
    expect(within(await rowOf('Team')).queryByRole('checkbox')).toBeNull()
    fireEvent.click(within(await rowOf('Spare')).getByRole('checkbox'))
    fireEvent.click(within(await rowOf('Two')).getByRole('checkbox'))
    fireEvent.click(await screen.findByRole('button', { name: /Delete selected/ }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText(/Spare、Two/)).toBeTruthy()
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.queryByText('Spare')).toBeNull())
    expect(screen.getByText('Two')).toBeTruthy()
  })

  it('keeps the row quiet while its delete is in flight, so a second delete cannot start', async () => {
    reads()
    let finish!: () => void
    api.delete.mockImplementation(() => new Promise<void>(r => { finish = r }))
    mountPage(<GroupsPage />, PATH)
    await openMenu('Spare')
    fireEvent.click(await menuItem('Delete'))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(1))
    const row = await rowOf('Spare')
    expect((within(row).getByRole('button', { name: /^Edit group/ }) as HTMLButtonElement).disabled).toBe(true)
    finish()
    await waitFor(() => expect((within(row).getByRole('button', { name: /^Edit group/ }) as HTMLButtonElement).disabled).toBe(false))
  })
})

describe('the destinations summary on the list', () => {
  const destGroup = { group_id: 5, group_name: 'Team', members: 3, state: 'on', state_changed_at: 1, updated_at: 1, list_ids: [], lists: [], base_list_id: 0, extra_list_id: 0, base_count: 0, eligible_nodes: 1, total_nodes: 1, ineligible: [], applied_nodes: 1, pending_nodes: [], not_ready_lists: [], would_deny_recent: null, denied_24h: 0 }

  it('badges a group whose allowlist is recording or enforcing, and only that', async () => {
    reads([{ ...team, dest_state: 'report' }, { ...empty, dest_state: 'off' }])
    mountPage(<GroupsPage />, PATH)
    expect(await screen.findByText('Allowlist · report only')).toBeTruthy()
    expect(screen.getAllByTestId('allowlist-state')).toHaveLength(1)
  })

  it('opens a group on the destinations tab from a link with ?edit and &dtab, and keeps the link', async () => {
    reads([{ ...team, dest_state: 'on' }], { '/admin/dest/groups': { items: [destGroup] } })
    const { router } = mountPage(<GroupsPage />, PATH, `${PATH}?edit=5&dtab=destination`)
    expect(await screen.findByText('Allowlist · on', {}, { timeout: 3000 })).toBeTruthy()
    const sheet = screen.getByRole('dialog')
    expect(within(sheet).getByRole('tab', { name: 'Destinations' }).getAttribute('aria-selected')).toBe('true')
    expect(within(sheet).getByRole('button', { name: /Edit in access control/ })).toBeTruthy()
    expect(router.state.location.search).toContain('edit=5')
  })

  it('a group that is not saved yet only says to save it first', async () => {
    reads([], { '/admin/dest/groups': { items: [] } })
    mountPage(<GroupsPage />, PATH, `${PATH}?edit=new&dtab=destination`)
    expect(await screen.findByText('Save the group first, then set its access mode')).toBeTruthy()
  })
})
