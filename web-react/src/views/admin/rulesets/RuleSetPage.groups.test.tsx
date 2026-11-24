// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, installReads, list, mountRuleSets } from './testHarness'

// The proxy-group rename, the "view by target" list and the phone tab menu (phase 1 leftovers of the rule-set editor).
const content = '- DOMAIN,a.example,Proxy\n- DOMAIN-SUFFIX,b.example,Proxy\n# ads\n- DOMAIN-KEYWORD,adx,Block\n- IP-CIDR,10.0.0.0/8,Block,no-resolve\n# again\n- DOMAIN,c.example,Proxy\n'
const base = { slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: ['Proxy', 'Block'] as string[], content }
const grp = (name: string) => ({ name, configured: false, options: { type: 'select' }, default_members: [{ kind: 'builtin', value: 'DIRECT' }], members: [{ kind: 'builtin', value: 'DIRECT' }] })
const inspection = (extra: Record<string, unknown> = {}) => ({ groups: [grp('Proxy'), grp('Block')], builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [], ...extra })
const PAGE = '/admin/rules/custom'
const saved = () => api.put.mock.calls[0][1] as { content: string; proxy_group_order: string[]; proxy_group_members: Record<string, unknown[]> }

function reads(row: Record<string, unknown> = base) {
  installReads({ '/admin/rules': list([row]), '/admin/templates': list([]), '/admin/groups': list([]) })
  // The server lists the groups the draft's rules name, as it does for real: a renamed group is a different group.
  api.post.mockImplementation(async (_url: string, body: { content: string }) => {
    const names = [...new Set(body.content.split('\n').filter(l => l.startsWith('- ')).map(l => l.split(',')[2]))]
    return { data: inspection({ groups: names.map(grp) }) }
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
    expect(await within(dialog).findByText('This changes 3 rule lines and 1 member references')).toBeTruthy()
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
