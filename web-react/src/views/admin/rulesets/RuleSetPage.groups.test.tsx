// @vitest-environment jsdom
import { configure, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, installReads, list, mountRuleSets } from './testHarness'

// The proxy-group rename, the "view by target" list and the phone tab menu (phase 1 leftovers of the rule-set editor).
// The draft's inspection is asked for a second after the draft settles (useDraftInspection), so waits are longer than the default.
configure({ asyncUtilTimeout: 4000 })

const content = '- DOMAIN,a.example,Proxy\n- DOMAIN-SUFFIX,b.example,Proxy\n# ads\n- DOMAIN-KEYWORD,adx,Block\n- IP-CIDR,10.0.0.0/8,Block,no-resolve\n# again\n- DOMAIN,c.example,Proxy\n'
const base = { slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: ['Proxy', 'Block'] as string[], content }
const grp = (name: string) => ({ name, configured: false, options: { type: 'select' }, default_members: [{ kind: 'builtin', value: 'DIRECT' }], members: [{ kind: 'builtin', value: 'DIRECT' }] })
const inspection = (extra: Record<string, unknown> = {}) => ({ groups: [grp('Proxy'), grp('Block')], builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [], ...extra })
const PAGE = '/admin/rules/custom'
const saved = () => api.put.mock.calls[0][1] as { content: string; proxy_group_order: string[]; proxy_group_members: Record<string, unknown[]> }

function reads(row: Record<string, unknown> = base) {
  installReads({ '/admin/rules': list([row]), '/admin/templates': list([]), '/admin/groups': list([]) })
  // The server lists the groups the draft's rules name, as it does for real: a renamed group is a different group.
  api.post.mockImplementation(async (_url: string, body: { content: string; proxy_group_members?: Record<string, unknown[]> }) => {
    const names = [...new Set(body.content.split('\n').filter(l => l.startsWith('- ')).map(l => l.split(',')[2]))]
    return { data: inspection({ groups: names.map(n => ({ ...grp(n), members: body.proxy_group_members?.[n] ?? grp(n).members })) }) }
  })
}
afterEach(() => { vi.unstubAllGlobals() })

describe('renaming a proxy group', () => {
  it('shows how many groups and rules use it, says what the rename changes before it does, and rewrites every line, the order and the member lists', async () => {
    reads({ ...base, proxy_group_members: { Block: [{ kind: 'proxy_group', value: 'Proxy' }, { kind: 'builtin', value: 'DIRECT' }], Proxy: [{ kind: 'builtin', value: 'DIRECT' }] } })
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    expect(await screen.findByText('Used by 2 groups, 3 rules')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Rename' }))
    const dialog = await screen.findByRole('dialog')
    const input = within(dialog).getByLabelText('New name')
    // Taken names are refused with the reason, and nothing can be confirmed.
    fireEvent.change(input, { target: { value: 'Block' } })
    expect(await within(dialog).findByText(/already has this name/)).toBeTruthy()
    expect((within(dialog).getByRole('button', { name: 'Rename' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(input, { target: { value: 'Main' } })
    expect(await within(dialog).findByText('This changes 3 rule lines and 1 member references, and writes out 0 member lists that were following the group\'s name.')).toBeTruthy()
    // What the rename does not reach is said before it is confirmed (here no template uses the rule set).
    expect(within(dialog).getByText(/Not changed by this rename: other rule sets and users' personal rules/)).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Rename' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(saved().content).toBe('- DOMAIN,a.example,Main\n- DOMAIN-SUFFIX,b.example,Main\n# ads\n- DOMAIN-KEYWORD,adx,Block\n- IP-CIDR,10.0.0.0/8,Block,no-resolve\n# again\n- DOMAIN,c.example,Main\n')
    expect(saved().proxy_group_order).toEqual(['Main', 'Block'])
    expect(Object.keys(saved().proxy_group_members)).toEqual(['Block', 'Main'])
    expect(saved().proxy_group_members.Block).toEqual([{ kind: 'proxy_group', value: 'Main' }, { kind: 'builtin', value: 'DIRECT' }])
  })

  it('is unavailable, with the reason, while the rules are text only', async () => {
    reads({ ...base, content: '- DOMAIN,a.example,Proxy\n  ,DIRECT\n' })
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    const button = await screen.findByRole('button', { name: 'Rename' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    expect(screen.getByText(/lines the editor can only show as text/)).toBeTruthy()
  })

  it('"Show its rules" lists the rules of that group together on the Rules tab', async () => {
    reads()
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Show its rules' }))
    expect(await screen.findByRole('table', { name: 'All rules that point at Proxy' })).toBeTruthy()
  })
})

describe('view by target', () => {
  it('lists every rule of one target from all its groups as a read-only table that says why, and a row opens its group', async () => {
    reads()
    mountRuleSets(PAGE)
    await screen.findByRole('row', { name: /Block/ })
    fireEvent.change(screen.getByLabelText('Filter by target'), { target: { value: 'Proxy' } })
    const table = await screen.findByRole('table', { name: 'All rules that point at Proxy' })
    // Two runs of Proxy, three rules, in the order of the document.
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows.map(r => r.textContent)).toEqual([expect.stringContaining('a.example'), expect.stringContaining('b.example'), expect.stringContaining('c.example')])
    expect(screen.getByText(/can't be edited here/)).toBeTruthy()
    // Nothing in it edits: no checkboxes, no menus.
    expect(within(table).queryByRole('checkbox')).toBeNull()
    expect(within(table).queryByRole('button', { name: /More actions/ })).toBeNull()
    fireEvent.click(rows[2])
    expect(await screen.findByRole('heading', { name: /3 · Proxy/ })).toBeTruthy()
  })
})

describe('on a phone', () => {
  it('the tabs are one menu, and choosing from it switches the tab', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({ matches: /max-width/.test(query), media: query, addEventListener: () => {}, removeEventListener: () => {} }))
    reads()
    mountRuleSets(PAGE)
    const menu = await screen.findByLabelText('Section') as HTMLSelectElement
    expect(screen.queryByRole('tab', { name: 'Rules' })).toBeNull()
    expect(Array.from(menu.options).map(o => o.textContent)).toEqual(['Rules', 'Proxy groups', 'Sub-rules', 'Rematch outbounds', 'General'])
    fireEvent.change(menu, { target: { value: 'basics' } })
    expect(await screen.findByLabelText(/^Name/)).toBeTruthy()
  })
})

describe('a sub-rule', () => {
  it('is edited in the grouped editor, with the rule set\'s proxy groups as targets and no routing test', async () => {
    reads({ ...base, mihomo_sub_rules: [{ name: 'ai', content: '- DOMAIN,x.example,Other\n- MATCH,DIRECT\n' }] })
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('tab', { name: 'Sub-rules (1)' }))
    const panel = within(await screen.findByRole('tabpanel'))
    const target = await panel.findByLabelText('Target') as HTMLSelectElement
    // Its own group, a group only the main rules name, and the built-in outbounds.
    expect(Array.from(target.options).map(o => o.value)).toEqual(expect.arrayContaining(['Other', 'Proxy', 'Block', 'DIRECT', 'REJECT']))
    expect(panel.queryByRole('button', { name: 'Test routing' })).toBeNull()
    // A name the main rules already use is not a new group.
    fireEvent.change(target, { target: { value: '\u0000new' } })
    fireEvent.change(await panel.findByLabelText('Proxy group name'), { target: { value: 'Proxy' } })
    expect(await panel.findByText(/already has this name/)).toBeTruthy()
  })
})

describe('the rename keeps what the group does (review A)', () => {
  const seededLike = { ...base, content: '- DOMAIN,ad.example,🛑 广告拦截\n- DOMAIN,a.example,Proxy\n', proxy_group_order: [] as string[] }

  // The backend derives the members of a group with no stored list from its NAME: a name with 广告拦截 in it blocks.
  const backend = (body: { content: string; proxy_group_members?: Record<string, unknown[]> }) => {
    const names = [...new Set(body.content.split('\n').filter(l => l.startsWith('- ')).map(l => l.split(',')[2]))]
    const byName = (n: string) => (n.includes('广告拦截') ? [{ kind: 'builtin', value: 'REJECT' }, { kind: 'builtin', value: 'DIRECT' }] : [{ kind: 'builtin', value: 'DIRECT' }, { kind: 'node_set', value: 'remaining' }])
    return { data: inspection({ groups: names.map(n => ({ ...grp(n), members: body.proxy_group_members?.[n] ?? byName(n) })) }) }
  }

  it('the repro: 🛑 广告拦截 renamed to Ads still blocks, because its members are written out under the new name', async () => {
    reads(seededLike)
    api.post.mockImplementation(async (_u: string, body: never) => backend(body))
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    fireEvent.click(await screen.findByRole('button', { name: /广告拦截/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('New name'), { target: { value: 'Ads' } })
    expect(await within(dialog).findByText('This changes 1 rule lines and 0 member references, and writes out 1 member lists that were following the group\'s name.')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Rename' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(saved().proxy_group_members.Ads).toEqual([{ kind: 'builtin', value: 'REJECT' }, { kind: 'builtin', value: 'DIRECT' }])
  })

  it('is not offered while the backend\'s members for this draft are not in', async () => {
    reads(seededLike)
    api.post.mockRejectedValue(new Error('network'))
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    // The inspection failed: there is no group to select and no rename to offer.
    await waitFor(() => expect(api.post).toHaveBeenCalled(), { timeout: 3000 })
    expect(screen.queryByRole('button', { name: 'Rename' })).toBeNull()
  })

  it('the confirmation says what the rename does not reach, naming the templates', async () => {
    installReads({ '/admin/rules': list([seededLike]), '/admin/templates': list([{ slug: 't1', name: 'Mihomo main', client_type: 'mihomo', is_default: false, rule_sets: ['custom'], content: '' }]), '/admin/groups': list([]) })
    api.post.mockImplementation(async (_u: string, body: never) => backend(body))
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('tab', { name: 'Proxy groups' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('New name'), { target: { value: 'Main' } })
    expect(await within(dialog).findByText(/the template's own proxy group order and users' personal rules.*\(Mihomo main\)/)).toBeTruthy()
  })
})
