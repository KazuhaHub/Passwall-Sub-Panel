// @vitest-environment jsdom
// The group page: what a save sends, the leave guard, and what each tab does (these were the side sheet's tests).
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { api, empty, everyKey, mountWithLinks, PATH, previewOf, reads, scopeReply, team, toast } from './testHarness'
import GroupsPage from './GroupsPage'

// A group is a page now: `sheet` is kept as the name of "where the page's controls are" so the bodies below read as before.
const open = async (entry = `${PATH}/5`) => {
  const mounted = mountWithLinks(<GroupsPage />, entry)
  await screen.findByRole('heading', { level: 1 })
  return { ...mounted, sheet: document.body }
}
const tab = (sheet: HTMLElement, name: string) => fireEvent.click(within(sheet).getByRole('tab', { name }))
const save = (sheet: HTMLElement) => fireEvent.click(within(sheet).getByRole('button', { name: 'Save' }))
const putsTo = (prefix: string) => api.put.mock.calls.filter(([url]) => String(url).startsWith(prefix))
const rowByLabel = (sheet: HTMLElement, label: string) => {
  const el = Array.from(sheet.querySelectorAll('[data-slot="inheritance-field"]')).find(e => e.textContent?.includes(label))
  if (!el) throw new Error(`no inheritance row for ${label}`)
  return el as HTMLElement
}
async function openSection(sheet: HTMLElement, name: RegExp) {
  fireEvent.click(await within(sheet).findByRole('button', { name }))
}

describe('what a save sends', () => {
  it('PUTs the group the way the old dialog did (legacy conditions rewritten, an empty limit cleared), then the overrides that changed', async () => {
    reads()
    const { sheet, router } = await open()
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'Team 2' } })
    tab(sheet, 'Overrides')
    await openSection(sheet, /Notification thresholds/)
    const expiry = await within(sheet).findByRole('textbox', { name: /Expiry reminder/ }).catch(() => within(sheet).getByRole('spinbutton', { name: /Expiry reminder/ }))
    fireEvent.change(expiry, { target: { value: '9' } })
    save(sheet)
    await waitFor(() => expect(putsTo('/admin/groups/5/scope-settings')).toHaveLength(1))
    // One request per concern, the group first: region / tag / node / custom, in that order, `server:` drained into `node:`.
    expect(putsTo('/admin/groups/5')[0]).toEqual(['/admin/groups/5', {
      name: 'Team 2',
      tag_filter: { all: false, mode: 'any', tags: ['region:HK', 'tag:Premium', 'node:1.1.1.1', 'vendor:gcp'] },
      remark: 'Core', require_2fa: false,
      traffic_limit_gb: 100, ip_limit: 0, clear_device_limit: true,
    }])
    expect(putsTo('/admin/groups/5/scope-settings')[0]).toEqual(['/admin/groups/5/scope-settings', { type: 'notify', name: 'expire_before_days', value: '9' }])
    const order = api.put.mock.calls.map(([url]) => String(url))
    expect(order.indexOf('/admin/groups/5')).toBeLessThan(order.indexOf('/admin/groups/5/scope-settings'))
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'Updated', severity: 'success' }))
    // The page stays where it was (the sheet used to close): the person may carry on with another tab.
    expect(router.state.location.pathname).toBe(`${PATH}/5`)
  })

  it('is clean after a save: the overrides are read again, so a second save sends nothing twice', async () => {
    reads()
    let held: Record<string, string> = { 'notify.expire_before_days': '7' }
    const base = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string, cfg?: unknown) => (url === '/admin/groups/5/scope-settings' ? { data: scopeReply(held) } : base(url, cfg)))
    api.put.mockImplementation(async (url: string, body: { value?: string }) => {
      if (url === '/admin/groups/5/scope-settings') held = { ...held, 'notify.expire_before_days': String(body.value) }
      return { data: url === '/admin/groups/5' ? { group: { ...team, members: 0 } } : {} }
    })
    const { sheet } = await open()
    tab(sheet, 'Overrides')
    await openSection(sheet, /Notification thresholds/)
    fireEvent.change(await within(sheet).findByRole('spinbutton', { name: /Expiry reminder/ }), { target: { value: '9' } })
    save(sheet)
    await waitFor(() => expect(putsTo('/admin/groups/5/scope-settings')).toHaveLength(1))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Save' })).toBeNull())
    expect(putsTo('/admin/groups/5/scope-settings')).toHaveLength(1)
  })

  it('keeps the member count the list read when the save answers without one (the 2FA question after a save still names them)', async () => {
    reads()
    const { sheet } = await open()
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'Team 2' } })
    save(sheet)
    await waitFor(() => expect(putsTo('/admin/groups/5')).toHaveLength(1))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Save' })).toBeNull())
    fireEvent.click(within(sheet).getByRole('switch', { name: /Require this group's members/ }))
    save(sheet)
    expect(within(await screen.findByRole('alertdialog')).getByText(/This group has 3 members/)).toBeTruthy()
  })

  it('tells an emptied limit (the group states nothing) from a typed 0 (unlimited)', async () => {
    reads()
    const { sheet } = await open()
    tab(sheet, 'Limits')
    const traffic = within(sheet).getByLabelText(/Traffic limit/)
    fireEvent.change(traffic, { target: { value: '' } })
    fireEvent.blur(traffic)
    const devices = within(sheet).getByLabelText(/Device limit/)
    fireEvent.change(devices, { target: { value: '0' } })
    fireEvent.blur(devices)
    save(sheet)
    await waitFor(() => expect(putsTo('/admin/groups/5')).toHaveLength(1))
    expect(putsTo('/admin/groups/5')[0][1]).toMatchObject({ clear_traffic_limit: true, ip_limit: 0, device_limit: 0 })
    expect(putsTo('/admin/groups/5')[0][1]).not.toHaveProperty('traffic_limit_gb')
    expect(putsTo('/admin/groups/5')[0][1]).not.toHaveProperty('clear_device_limit')
  })

  it('warns, next to the box, that a device limit is stored but not enforced', async () => {
    reads()
    const { sheet } = await open()
    tab(sheet, 'Limits')
    const devices = within(sheet).getByLabelText(/Device limit/)
    fireEvent.change(devices, { target: { value: '3' } })
    expect(await within(sheet).findByText(/Not enforced yet/)).toBeTruthy()
  })

  it('creates with the blank defaults, leaving unstated limits out, and asks for a slug and a name first', async () => {
    reads([], { '/admin/groups/9': { ...empty, id: 9, slug: 'fresh', name: 'Fresh' }, '/admin/groups/9/scope-settings': scopeReply() })
    api.post.mockImplementation(async (url: string) => url.endsWith('node-preview') ? previewOf() : { data: { ...empty, id: 9, slug: 'fresh', name: 'Fresh' } })
    const { sheet, router } = await open(`${PATH}/new`)
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'Fresh' } })
    save(sheet)
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'Slug is required', severity: 'warning' }))
    expect(api.post.mock.calls.some(([url]) => url === '/admin/groups')).toBe(false)
    fireEvent.change(within(sheet).getByLabelText(/^Slug/), { target: { value: 'fresh' } })
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'Fresh' } })
    save(sheet)
    await waitFor(() => expect(api.post.mock.calls.some(([url]) => url === '/admin/groups')).toBe(true))
    expect(api.post).toHaveBeenCalledWith('/admin/groups', {
      slug: 'fresh', name: 'Fresh', tag_filter: { all: false, mode: 'all', tags: [] }, remark: '', require_2fa: false,
    })
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'Created', severity: 'success' }))
    // The new group's own page opens, with no "discard your changes?" for the form that was just saved.
    await waitFor(() => expect(router.state.location.pathname).toBe(`${PATH}/9`))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Fresh'))
  })

  it('stays on the state the server holds when an override write fails', async () => {
    reads()
    api.put.mockImplementation(async (url: string) => {
      if (url.endsWith('/scope-settings')) throw new Error('refused')
      return { data: { group: team } }
    })
    const { sheet } = await open()
    tab(sheet, 'Overrides')
    await openSection(sheet, /Notification thresholds/)
    fireEvent.change(await within(sheet).findByRole('spinbutton', { name: /Expiry reminder/ }), { target: { value: '9' } })
    save(sheet)
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: expect.stringContaining('failed to save'), severity: 'error' }))
    expect(toast).not.toHaveBeenCalledWith({ message: 'Updated', severity: 'success' })
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Team')
  })
})

describe('requiring two-step verification', () => {
  it('asks with the number of members it reaches, focuses Cancel, and sends nothing on Cancel', async () => {
    reads()
    const { sheet } = await open()
    fireEvent.click(within(sheet).getByRole('switch', { name: /Require this group's members/ }))
    save(sheet)
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText(/This group has 3 members/)).toBeTruthy()
    await waitFor(() => expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' })))
    fireEvent.click(within(ask).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.put).not.toHaveBeenCalled()
    save(sheet)
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Require' }))
    await waitFor(() => expect(putsTo('/admin/groups/5')).toHaveLength(1))
    expect(putsTo('/admin/groups/5')[0][1]).toMatchObject({ require_2fa: true })
  })

  it('does not ask for a group with nobody in it', async () => {
    reads()
    api.put.mockResolvedValue({ data: { group: empty } })
    const { sheet } = await open(`${PATH}/6`)
    fireEvent.click(within(sheet).getByRole('switch', { name: /Require this group's members/ }))
    save(sheet)
    await waitFor(() => expect(putsTo('/admin/groups/6')).toHaveLength(1))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })
})

describe('the unsaved-changes guard', () => {
  const back = () => screen.getByRole('link', { name: 'Groups' })

  it('asks before the page is left with a draft, keeps it on Keep, and drops it on Discard', async () => {
    reads()
    const { sheet, router } = await open()
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'Draft' } })
    fireEvent.click(back())
    const ask = await screen.findByRole('alertdialog')
    fireEvent.click(within(ask).getByRole('button', { name: /Keep|Cancel/ }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(router.state.location.pathname).toBe(`${PATH}/5`)
    expect((within(sheet).getByLabelText(/^Name/) as HTMLInputElement).value).toBe('Draft')
    fireEvent.click(back())
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: /Discard/ }))
    await waitFor(() => expect(router.state.location.pathname).toBe(PATH))
    expect(api.put).not.toHaveBeenCalled()
  })

  it('names the tabs that hold the draft', async () => {
    reads()
    const { sheet } = await open()
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'Draft' } })
    tab(sheet, 'Limits')
    const traffic = within(sheet).getByLabelText(/Traffic limit/)
    fireEvent.change(traffic, { target: { value: '5' } })
    fireEvent.blur(traffic)
    fireEvent.click(back())
    expect(within(await screen.findByRole('alertdialog')).getByText('Unsaved changes in: General, Limits')).toBeTruthy()
  })

  it('moves between tabs without asking and keeps the draft, and marks the tab that holds it', async () => {
    reads()
    const { sheet, router } = await open()
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'Draft' } })
    expect(within(within(sheet).getByRole('tab', { name: /General/ })).getByRole('img', { name: 'Unsaved changes' })).toBeTruthy()
    tab(sheet, 'Layout')
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(router.state.location.search).toBe('?tab=layout')
    fireEvent.click(within(sheet).getByRole('tab', { name: /General/ }))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect((within(sheet).getByLabelText(/^Name/) as HTMLInputElement).value).toBe('Draft')
  })

  it('leaves a clean page without asking', async () => {
    reads()
    const { router } = await open()
    fireEvent.click(back())
    await waitFor(() => expect(router.state.location.pathname).toBe(PATH))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('asks before a link takes a draft to another page, and before it is carried to another group', async () => {
    reads()
    const { sheet, router } = await open()
    tab(sheet, 'Layout')
    tab(sheet, 'General')
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'Draft' } })
    tab(sheet, 'Layout')
    fireEvent.click(await within(sheet).findByRole('link', { name: 'Open the Nodes page' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: /Keep|Cancel/ }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(router.state.location.pathname).toBe(`${PATH}/5`)
    void router.navigate(`${PATH}/6`)
    const ask = await screen.findByRole('alertdialog')
    expect(router.state.location.pathname).toBe(`${PATH}/5`)
    fireEvent.click(within(ask).getByRole('button', { name: /Discard/ }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${PATH}/6`))
    // The other group starts from its own saved state, not from the draft.
    expect((await screen.findByRole('heading', { level: 1 })).textContent).toBe('Spare')
    expect((within(document.body).getByLabelText(/^Name/) as HTMLInputElement).value).toBe('Spare')
  })

  it('asks before a new group with a typed name is abandoned', async () => {
    reads([])
    const { sheet } = await open(`${PATH}/new`)
    fireEvent.change(within(sheet).getByLabelText(/^Slug/), { target: { value: 'x' } })
    fireEvent.click(back())
    expect(await screen.findByRole('alertdialog')).toBeTruthy()
  })
})

describe('a group that cannot be shown', () => {
  const gone = (status: number) => Object.assign(new Error('x'), { response: { status } })

  it.each(['abc', '0', '-2', '1.5'])('says there is no such group for the address /%s, without asking the server', async id => {
    reads()
    mountWithLinks(<GroupsPage />, `${PATH}/${id}`)
    expect(await screen.findByText('This group no longer exists')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Groups' }).getAttribute('href')).toBe(PATH)
    expect(api.get).not.toHaveBeenCalled()
  })

  it('says so for a group the server does not have, and when it is not ours to open, in the same frame', async () => {
    reads([team, empty], { '/admin/groups/5': gone(404), '/admin/groups/6': gone(403) })
    const { unmount } = mountWithLinks(<GroupsPage />, `${PATH}/5`)
    expect(await screen.findByText('This group no longer exists')).toBeTruthy()
    unmount()
    mountWithLinks(<GroupsPage />, `${PATH}/6`)
    expect(await screen.findByText('You cannot open this group')).toBeTruthy()
  })

  it('offers a retry when the read fails some other way', async () => {
    reads([team, empty], { '/admin/groups/5': gone(500) })
    mountWithLinks(<GroupsPage />, `${PATH}/5`)
    expect(await screen.findByRole('button', { name: /Retry/ })).toBeTruthy()
  })

  it('keeps an operator out of the page and the create form (they only read the list), with no read of the group', async () => {
    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    reads()
    const { unmount } = mountWithLinks(<GroupsPage />, `${PATH}/5`)
    expect(await screen.findByText('Only administrators can edit groups')).toBeTruthy()
    unmount()
    mountWithLinks(<GroupsPage />, `${PATH}/new`)
    expect(await screen.findByText('Only administrators can edit groups')).toBeTruthy()
    expect(api.get).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
  })
})

describe('the header', () => {
  it('names the group, its slug, its members and its saved state, and offers View members and Delete in More', async () => {
    reads([{ ...team, require_2fa: true, dest_state: 'report' }, empty])
    await open()
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Team')
    expect(screen.getByText('team')).toBeTruthy()
    expect(screen.getAllByText('Members in this group: 3').length).toBeGreaterThan(0)
    expect(screen.getByText('Two-step verification: Required')).toBeTruthy()
    expect(screen.getByText('Allowlist · report only')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /More/ }))
    expect(await screen.findByRole('menuitem', { name: 'View members' })).toBeTruthy()
    expect(await screen.findByRole('menuitem', { name: 'Delete' })).toBeTruthy()
  })

  it('titles the create page "New group" and has no More menu', async () => {
    reads([])
    await open(`${PATH}/new`)
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('New group')
    expect(screen.queryByRole('button', { name: /More/ })).toBeNull()
  })

  it('deletes an empty group from its page after the usual question, and leaves for the list without asking about a draft', async () => {
    reads()
    api.delete.mockResolvedValue({})
    const { sheet, router } = await open(`${PATH}/6`)
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'Draft' } })
    fireEvent.click(screen.getByRole('button', { name: /More/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText('Delete group Spare?')).toBeTruthy()
    expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' }))
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/groups/6'))
    await waitFor(() => expect(router.state.location.pathname).toBe(PATH))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('does not ask to delete a group that has members: it says how many and offers Users', async () => {
    reads()
    const { router } = await open()
    fireEvent.click(screen.getByRole('button', { name: /More/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText(/3 users are in this group/)).toBeTruthy()
    fireEvent.click(within(ask).getByRole('button', { name: 'Go to Users' }))
    expect(api.delete).not.toHaveBeenCalled()
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/users'))
  })
})

describe('the nodes and lines tab', () => {
  const previewPosts = () => api.post.mock.calls.filter(([url]) => String(url).includes('node-preview') && !String(url).includes('/5/node-preview') ? true : String(url) === '/admin/groups/5/node-preview')

  it('previews the draft filter live, links each matched node, and warns what a save changes for members', async () => {
    reads()
    api.post.mockImplementation(async () => previewOf({ nodes: [{ id: 1, name: 'HK-1', region: 'HK', visible_lines: 1, total_lines: 2 }], excluded: [{ id: 3, name: 'US-1', reason: 'allowlist_unenforceable' }], added: [2], members: 3 }))
    const { sheet } = await open()
    tab(sheet, 'Nodes and lines')
    await waitFor(() => expect(within(sheet).getByTestId('group-node-preview').textContent).toContain('Matches 1 nodes; 1 of their 2 lines are visible to this group'))
    expect(within(sheet).getByRole('link', { name: /HK-1/ }).getAttribute('href')).toBe('/admin/nodes/1')
    expect(within(sheet).getByTestId('group-node-excluded').textContent).toContain('US-1')
    expect(within(sheet).getByTestId('group-node-change').textContent).toContain('3 members gain 1 and lose 0 nodes')
    const posts = previewPosts()
    expect(posts.at(-1)).toEqual(['/admin/groups/5/node-preview', { tag_filter: { all: false, mode: 'any', tags: ['region:HK', 'tag:Premium', 'node:1.1.1.1', 'vendor:gcp'] } }, expect.anything()])
    // Toggling "match all" re-asks with the draft, without saving anything.
    const before = previewPosts().length
    fireEvent.click(within(sheet).getByRole('switch', { name: 'Match all nodes' }))
    await waitFor(() => expect(previewPosts().length).toBeGreaterThan(before))
    expect(previewPosts().at(-1)![1]).toEqual({ tag_filter: { all: true, mode: 'any', tags: [] } })
    expect(api.put).not.toHaveBeenCalled()
  })

  it('says "no node" in warning words, and "cannot be determined" when the read fails', async () => {
    reads()
    api.post.mockImplementation(async () => previewOf({ nodes: [] }))
    const { sheet } = await open()
    tab(sheet, 'Nodes and lines')
    await waitFor(() => expect(within(sheet).getByTestId('group-node-preview').textContent).toContain('No node matches'))
    expect(within(sheet).queryByTestId('group-node-change')).toBeNull()
    api.post.mockRejectedValue(new Error('down'))
    fireEvent.click(within(sheet).getByRole('switch', { name: 'Match all nodes' }))
    await waitFor(() => expect(within(sheet).getByTestId('group-node-preview').textContent).toContain('cannot be determined'))
  })

  it('previews a group being created against no saved group', async () => {
    reads([])
    const { sheet } = await open(`${PATH}/new`)
    tab(sheet, 'Nodes and lines')
    await waitFor(() => expect(api.post.mock.calls.some(([url]) => url === '/admin/groups/node-preview')).toBe(true))
  })

  it('suggests the regions, tags and nodes that exist, and still takes one that does not', async () => {
    reads()
    const { sheet } = await open(`${PATH}/6`)
    tab(sheet, 'Nodes and lines')
    fireEvent.click(await within(sheet).findByRole('switch', { name: 'Match all nodes' }))
    const region = await within(sheet).findByRole('combobox', { name: 'Region' })
    fireEvent.focus(region)
    fireEvent.change(region, { target: { value: 'JP' } })
    fireEvent.keyDown(region, { key: 'Enter' })
    expect(within(sheet).getByText('JP')).toBeTruthy()
  })
})

describe('the overrides tab', () => {
  it('shows the global value the group inherits, that it overrides it, and the way back to following global', async () => {
    reads()
    const { sheet } = await open()
    tab(sheet, 'Overrides')
    const heading = await within(sheet).findByRole('button', { name: /Notification thresholds/ })
    // A collapsed section still says how many of its rows are overridden.
    expect(heading.textContent).toContain('1 overridden')
    fireEvent.click(heading)
    const row = rowByLabel(sheet, 'Expiry reminder (days)')
    expect(row.textContent).toMatch(/Global.*3/)
    expect(row.textContent).toMatch(/This group.*7/)
    expect(row.textContent).toContain('In effect')
    // The other row follows global and says so.
    expect(rowByLabel(sheet, 'Traffic-low reminder (%)').textContent).toContain('Follows above')
    fireEvent.click(within(row).getByRole('radio', { name: 'Follow the level above' }))
    expect(row.textContent).toMatch(/Following the level above, currently 3/)
    save(sheet)
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/groups/5/scope-settings/notify/expire_before_days'))
    expect(putsTo('/admin/groups/5/scope-settings')).toHaveLength(0)
  })

  it('offers only the settings the backend allows per group', async () => {
    reads([team, empty], { '/admin/groups/5/scope-settings': scopeReply({}, ['notify.expire_before_days']) })
    const { sheet } = await open()
    tab(sheet, 'Overrides')
    await openSection(sheet, /Notification thresholds/)
    expect(rowByLabel(sheet, 'Expiry reminder (days)')).toBeTruthy()
    expect(within(sheet).queryByText('Traffic-low reminder (%)')).toBeNull()
    // A category with nothing allowed is not shown at all.
    expect(within(sheet).queryByRole('button', { name: /Emergency access/ })).toBeNull()
    expect(within(sheet).queryByRole('button', { name: /Two-factor/ })).toBeNull()
  })

  it('leaves the detector exceptions to the risk center and says how many are set there', async () => {
    reads([team, empty], { '/admin/groups/5/scope-settings': scopeReply({ 'geo_anomaly.max_cities': '4' }) })
    const { sheet } = await open()
    tab(sheet, 'Overrides')
    expect(await within(sheet).findByText(/Overridden in the risk center: 1/)).toBeTruthy()
    expect(within(sheet).queryByText('Country tolerance')).toBeNull()
    expect((await within(sheet).findByRole('link', { name: 'Open the risk center' })).getAttribute('href')).toBe('/admin/risk?tab=policy&group=5')
  })

  it('says when the overrides cannot be read, offers a retry, and still saves the group itself', async () => {
    reads([team, empty], { '/admin/groups/5/scope-settings': new Error('offline') })
    const { sheet } = await open()
    tab(sheet, 'Overrides')
    expect((await within(sheet).findAllByText("Could not load this group's overrides")).length).toBeGreaterThan(0)
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'Team 2' } })
    save(sheet)
    await waitFor(() => expect(putsTo('/admin/groups/5')).toHaveLength(1))
    expect(putsTo('/admin/groups/5/scope-settings')).toHaveLength(0)
  })

  it('asks to save a new group first', async () => {
    reads([])
    const { sheet } = await open(`${PATH}/new`)
    tab(sheet, 'Overrides')
    expect(within(sheet).getByText('Save the group first, then set per-group overrides here.')).toBeTruthy()
  })
})

describe('the layout tab', () => {
  it('says plainly that the Nodes page decides the order, links to it, and shows the saved layout without editing it', async () => {
    reads()
    const { sheet } = await open()
    tab(sheet, 'Layout')
    expect(await within(sheet).findByText(/only order lines that tie, that is, lines that were never positioned/)).toBeTruthy()
    expect((await within(sheet).findByRole('link', { name: 'Open the Nodes page' })).getAttribute('href')).toBe('/admin/nodes')
    expect(within(sheet).getByText('by_region_then_id')).toBeTruthy()
    // The region flag is a per-group override of a global setting, with the global value in view.
    const flag = rowByLabel(sheet, 'Region flag prefix on node names')
    expect(flag.textContent).toMatch(/Global.*On/)
    expect(flag.textContent).toContain('Follows above')
  })
})

void everyKey
