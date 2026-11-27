// @vitest-environment jsdom
import { configure, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { api, installReads, list, mountRuleSets } from './testHarness'

// When the page asks the server to read the draft (inspect-proxy-groups), and what the problems panel says while it waits, fails
// or cannot ask (re-review). Each request is an admin audit row, so it is asked for only while something on screen uses the answer.
configure({ asyncUtilTimeout: 4000 })

const base = {
  revision: 'r1',
  slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: [] as string[],
  content: '- DOMAIN,a.example,Proxy\n- DOMAIN,a.example,Proxy\n- DOMAIN-SUFFIX,b.example,DIRECT\n- DOMAIN,www.b.example,REJECT\n- DOMAIN,c.example,Other\n',
  mihomo_sub_rules: [{ name: 'sub', content: '- DOMAIN,x.example,Proxy\n- DOMAIN,x.example,Proxy\n- MATCH,DIRECT\n' }],
}
const PAGE = '/admin/rules/custom'
const member = [{ kind: 'builtin', value: 'DIRECT' }]
const grp = (name: string) => ({ name, configured: false, options: { type: 'select' }, default_members: member, members: member })
const answer = () => ({ data: { groups: ['Proxy', 'Other'].map(grp), builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [] } })
const inspections = () => api.post.mock.calls.filter(c => c[0] === '/admin/rules/inspect-proxy-groups')
const pause = (ms: number) => new Promise(r => setTimeout(r, ms))

function reads() {
  installReads({ '/admin/rules': list([base]), '/admin/templates': list([]), '/admin/groups': list([]) })
  api.post.mockImplementation(async () => answer())
}

describe('when the draft is inspected', () => {
  it('not at all while nothing on screen uses the answer (the Rules tab with the test and the panel closed)', async () => {
    reads()
    mountRuleSets(PAGE)
    await screen.findByRole('row', { name: /Proxy/ })
    await pause(1500)
    expect(inspections()).toHaveLength(0)
  })

  it('once for the Proxy groups tab, and not again when the tabs are switched back and forth with no edit', async () => {
    reads()
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    await waitFor(() => expect(inspections()).toHaveLength(1))
    fireEvent.click(await screen.findByRole('tab', { name: 'Rules' }))
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    fireEvent.click(await screen.findByRole('tab', { name: 'General' }))
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    await pause(1600)
    expect(inspections()).toHaveLength(1)
  })

  it('ten edits inside the pause send one request, and the reset after a save of the same draft sends none', { timeout: 20000 }, async () => {
    reads()
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    await waitFor(() => expect(inspections()).toHaveLength(1))
    // The type of the group, then ten changes of the check URL, all inside the one-second pause.
    fireEvent.change(await screen.findByLabelText('Proxy group type'), { target: { value: 'url-test' } })
    const url = await screen.findByLabelText('Test URL')
    for (let i = 0; i < 10; i++) fireEvent.change(url, { target: { value: `https://t${i}.example/` } })
    await waitFor(() => expect(inspections()).toHaveLength(2))
    await pause(1600)
    expect(inspections()).toHaveLength(2)
    // The save asks once itself (as it always did); the page resets to what was saved, which is the draft already answered.
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    await pause(1600)
    expect(inspections()).toHaveLength(3)
  })

  it('a request in flight is dropped when a newer draft supersedes it', async () => {
    reads()
    const signals: AbortSignal[] = []
    api.post.mockImplementation((url: string, _b: unknown, cfg?: { signal?: AbortSignal }) => {
      if (url === '/admin/rules/inspect-proxy-groups' && cfg?.signal) signals.push(cfg.signal)
      return new Promise(() => {})
    })
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    await waitFor(() => expect(inspections()).toHaveLength(1))
    // The first request never answers; an edit makes a newer draft, whose request aborts it.
    fireEvent.click(await screen.findByRole('tab', { name: 'Rules' }))
    fireEvent.click(await screen.findByRole('radio', { name: 'Text' }))
    fireEvent.change(await screen.findByRole('textbox', { name: 'Main rules' }), { target: { value: '- MATCH,DIRECT\n' } })
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    await waitFor(() => expect(inspections()).toHaveLength(2))
    expect(signals[0].aborted).toBe(true)
    expect(signals[1].aborted).toBe(false)
  })
})

describe('the problems panel says where it stands', () => {
  const openSubPanel = async () => {
    fireEvent.click(await screen.findByRole('tab', { name: 'Sub-rules (1)' }))
    const panel = within(await screen.findByRole('tabpanel'))
    fireEvent.click(await panel.findByRole('button', { name: 'Issues and findings' }))
    return await screen.findByRole('dialog')
  }

  it('on a sub-rule it asks for the groups too, and shows results, not a spinner that never ends', async () => {
    reads()
    mountRuleSets(PAGE)
    const dialog = await openSubPanel()
    expect(within(dialog).getByText('Checking…')).toBeTruthy()
    expect(await within(dialog).findByText(/Redundant/)).toBeTruthy()
    expect(inspections().length).toBeGreaterThanOrEqual(1)
  })

  it('when the groups cannot be read it says so, offers Retry, and still lists what does not need them', async () => {
    reads()
    api.post.mockRejectedValue(new Error('network'))
    mountRuleSets(PAGE)
    const dialog = await openSubPanel()
    expect(await within(dialog).findByText(/couldn't be read, so only the findings that do not depend on them are shown/)).toBeTruthy()
    // An identical rule aimed at the same group is redundant whatever the group does; so is a rule under one aimed at DIRECT or REJECT.
    expect(await within(dialog).findByText(/Redundant/)).toBeTruthy()
    const before = inspections().length
    api.post.mockImplementation(async () => answer())
    fireEvent.click(within(dialog).getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(inspections().length).toBeGreaterThan(before))
    await waitFor(() => expect(within(dialog).queryByText(/couldn't be read/)).toBeNull())
  })

  it('for an operator, who cannot read the groups, it says so at once and lists the same partial findings, with no request', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    reads()
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('button', { name: 'Issues and findings' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/only administrators can read/)).toBeTruthy()
    expect(await within(dialog).findByText(/Redundant/)).toBeTruthy()
    expect(inspections()).toHaveLength(0)
    useAuthStore.setState({ role: 'admin' })
  })
})
