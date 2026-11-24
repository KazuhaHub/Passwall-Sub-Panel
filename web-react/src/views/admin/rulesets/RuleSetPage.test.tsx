// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { defaultRulesContent } from '@/utils/ruleset/defaultRules.fixture'
import { api, installReads, list, mountRuleSets, toast, where } from './testHarness'

const content = '- DOMAIN,a.example,Proxy\n- DOMAIN-SUFFIX,b.example,Proxy\n# ads\n- DOMAIN-KEYWORD,adx,Block\n- IP-CIDR,10.0.0.0/8,Block,no-resolve\n'
const base = { slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: [] as string[], content }
const seeded = { ...base, slug: 'default_rules', name: 'Seeded rules', sort: 1 }
const template = { slug: 't1', name: 'Mihomo main', client_type: 'mihomo', is_default: false, rule_sets: ['custom'], content: '' }
const inspection = (extra: Record<string, unknown> = {}) => ({ groups: [], builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [], ...extra })
const PAGE = '/admin/rules/custom'

function reads(rows: unknown[] = [base, seeded]) {
  installReads({ '/admin/rules': list(rows), '/admin/templates': list([template]), '/admin/groups': list([]) })
  api.post.mockResolvedValue({ data: inspection() })
}
const label = (re: RegExp | string) => screen.findByLabelText(re)
const tab = (name: RegExp | string) => screen.findByRole('tab', { name })
const group = (name: RegExp | string) => screen.findByRole('row', { name })
async function openGroup(name: RegExp | string) {
  fireEvent.click(await group(name))
}
const saved = () => api.put.mock.calls[0][1] as { content: string }

describe('the rule-set page', () => {
  it('saves an edit with exactly the body the old sheet sent (everything else as loaded)', async () => {
    const rich = {
      ...base,
      proxy_group_order: ['B', 'A'],
      proxy_group_members: { A: [{ kind: 'builtin', value: 'DIRECT' }] },
      proxy_group_options: { A: { type: 'select' } },
      mihomo_sub_rules: [{ name: 'ai', content: '- MATCH,DIRECT' }],
      mihomo_rematch_outbounds: [{ name: 'R', target_rematch_name: 'x', target_sub_rule: 'ai' }],
    }
    reads([rich])
    api.post.mockResolvedValue({ data: inspection({ groups: [
      { name: 'A', configured: true, options: { type: 'select' }, default_members: [], members: [] },
      { name: 'B', configured: false, options: { type: 'select' }, default_members: [], members: [] },
    ] }) })
    mountRuleSets(PAGE)
    fireEvent.click(await tab('General'))
    fireEvent.change(await label(/^Name/), { target: { value: 'Renamed' } })
    fireEvent.change(await label('Sort'), { target: { value: '7' } })
    fireEvent.click(await tab('Rules'))
    // Text view, then the same text with one line changed.
    fireEvent.click(await screen.findByRole('radio', { name: 'Text' }))
    fireEvent.change(await screen.findByRole('textbox', { name: 'Main rules' }), { target: { value: '- DOMAIN,b.example,A\n- MATCH,B' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put).toHaveBeenCalledWith('/admin/rules/custom', {
      slug: 'custom', name: 'Renamed', sort: 7, enabled: true, direct_subscription_domain: false,
      content: '- DOMAIN,b.example,A\n- MATCH,B',
      proxy_group_order: ['B', 'A'],
      proxy_group_members: { A: [{ kind: 'builtin', value: 'DIRECT' }] },
      proxy_group_options: { A: { type: 'select' } },
      mihomo_sub_rules: [{ name: 'ai', content: '- MATCH,DIRECT' }],
      mihomo_rematch_outbounds: [{ name: 'R', target_rematch_name: 'x', target_sub_rule: 'ai' }],
    })
    // The server was asked to inspect the same draft first.
    expect(api.post).toHaveBeenCalledWith('/admin/rules/inspect-proxy-groups', expect.objectContaining({ content: '- DOMAIN,b.example,A\n- MATCH,B', mihomo_sub_rules: rich.mihomo_sub_rules }), expect.anything())
    // Stays on the page, now clean.
    expect(where()).toBe(PAGE)
    expect(await screen.findByRole('heading', { name: 'Renamed' })).toBeTruthy()
    await waitFor(() => expect(screen.queryByRole('region', { name: /unsaved/i })).toBeNull())
  })

  it('shows the groups of the rules and edits through the form: add a rule, change a target, delete a rule', async () => {
    reads()
    mountRuleSets(PAGE)
    expect((await group(/Proxy/)).textContent).toContain('2')
    await openGroup(/Block/)
    expect(await screen.findByRole('heading', { name: /2 · Block/ })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Add' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Match'), { target: { value: 'ads.example' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    // Delete the keyword rule.
    fireEvent.click(await screen.findByRole('button', { name: /More actions for adx/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: /Delete adx/ }))
    // Aim the group at REJECT: the button says how many lines change.
    fireEvent.change(screen.getByLabelText('Target'), { target: { value: 'REJECT' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Change to REJECT (2 lines)' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    // Only the lines those edits name changed; the note, the order and the first group are as they were.
    expect(saved().content).toBe('- DOMAIN,a.example,Proxy\n- DOMAIN-SUFFIX,b.example,Proxy\n# ads\n- IP-CIDR,10.0.0.0/8,REJECT,no-resolve\n- DOMAIN-SUFFIX,ads.example,REJECT\n')
  })

  it('switching to the text view and back changes nothing', async () => {
    reads()
    mountRuleSets(PAGE)
    await group(/Proxy/)
    fireEvent.click(screen.getByRole('radio', { name: 'Text' }))
    expect((await screen.findByRole('textbox', { name: 'Main rules' }) as HTMLTextAreaElement).value).toBe(content)
    fireEvent.click(screen.getByRole('radio', { name: 'Groups' }))
    await group(/Proxy/)
    expect(screen.queryByRole('region', { name: /unsaved/i })).toBeNull()
  })

  it('says how to leave the code editor with the keyboard, in the text view', async () => {
    reads()
    mountRuleSets(PAGE)
    await group(/Proxy/)
    fireEvent.click(screen.getByRole('radio', { name: 'Text' }))
    expect(await screen.findByText(/press Escape and then Tab/)).toBeTruthy()
  })

  it('pastes a list into a group after a check that names the duplicates', async () => {
    reads()
    mountRuleSets(PAGE)
    await openGroup(/Proxy/)
    fireEvent.click(await screen.findByRole('button', { name: 'Paste list' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'DOMAIN,a.example,Other\nnew.example\n+.other.example\n# note\n' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Check' }))
    expect(await within(dialog).findByText('2 can be added')).toBeTruthy()
    expect(within(dialog).getByText('Skipped 1 duplicates')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 2' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(saved().content).toBe('- DOMAIN,a.example,Proxy\n- DOMAIN-SUFFIX,b.example,Proxy\n- DOMAIN-SUFFIX,new.example,Proxy\n- DOMAIN-SUFFIX,other.example,Proxy\n# ads\n- DOMAIN-KEYWORD,adx,Block\n- IP-CIDR,10.0.0.0/8,Block,no-resolve\n')
  })

  it('reorders groups with "Move to…" and can take it back', async () => {
    reads()
    mountRuleSets(PAGE)
    fireEvent.click(await within(await group(/Block/)).findByRole('button', { name: /Actions for group 2/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Move to…' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Place after this group'), { target: { value: 'top' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(saved().content).toBe('# ads\n- DOMAIN-KEYWORD,adx,Block\n- IP-CIDR,10.0.0.0/8,Block,no-resolve\n- DOMAIN,a.example,Proxy\n- DOMAIN-SUFFIX,b.example,Proxy\n')
  })

  it('undo puts back the content before the last edit', async () => {
    reads()
    mountRuleSets(PAGE)
    await openGroup(/Block/)
    fireEvent.click(await screen.findByRole('button', { name: /More actions for adx/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: /Delete adx/ }))
    await screen.findByRole('region', { name: /unsaved/i })
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }))
    await waitFor(() => expect(screen.queryByRole('region', { name: /unsaved/i })).toBeNull())
  })

  it('finds a domain across groups and says where the hits are', async () => {
    reads()
    mountRuleSets(PAGE)
    await group(/Proxy/)
    fireEvent.change(screen.getByRole('textbox', { name: 'Find a domain, IP or rule…' }), { target: { value: 'b.example' } })
    expect(await screen.findByText('1 results in 1 groups')).toBeTruthy()
    await waitFor(() => expect(screen.queryByRole('row', { name: /Block/ })).toBeNull())
  })

  it('shows the whole seeded rule set as 29 groups and draws only the rows near the screen of the biggest', async () => {
    reads([{ ...base, content: defaultRulesContent() }])
    mountRuleSets(PAGE)
    expect((await screen.findAllByRole('row', { name: /^\d+ ·|🎯 全球直连|🚀 节点选择/ })).length).toBeGreaterThan(0)
    expect(await screen.findByText('9,474 rules · 29 groups')).toBeTruthy()
    fireEvent.click((await screen.findAllByRole('row')).find(r => r.textContent?.includes('6,904'))!)
    expect(await screen.findByRole('heading', { name: /🚀 节点选择/ })).toBeTruthy()
    const table = await screen.findByRole('table', { name: 'Rules in 🚀 节点选择' })
    // 6,904 rules in the group, a screenful (plus margin) drawn.
    const drawn = within(table).getAllByRole('row').length
    expect(drawn).toBeGreaterThan(5)
    expect(drawn).toBeLessThan(120)
  })
})

describe('creating', () => {
  it('creates with the blank defaults the old editor used, and refuses an existing or reserved slug', async () => {
    reads()
    mountRuleSets('/admin/rules/new')
    fireEvent.change(await label(/^Slug/), { target: { value: 'custom' } })
    fireEvent.change(await label(/^Name/), { target: { value: 'Dup' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: expect.stringContaining('already exists'), severity: 'warning' }))
    expect(api.put).not.toHaveBeenCalled()
    // The router matches paths without regard to case, so NEW would be the create address too.
    fireEvent.change(await label(/^Slug/), { target: { value: 'NEW' } })
    expect((await screen.findAllByText(/"NEW" is reserved/)).length).toBeGreaterThan(0)
    expect(api.put).not.toHaveBeenCalled()
    fireEvent.change(await label(/^Slug/), { target: { value: 'fresh' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put).toHaveBeenCalledWith('/admin/rules/fresh', {
      slug: 'fresh', name: 'Dup', sort: 100, enabled: true, direct_subscription_domain: false,
      proxy_group_order: [], proxy_group_members: {}, proxy_group_options: {}, mihomo_sub_rules: [], mihomo_rematch_outbounds: [], content: '',
    })
    await waitFor(() => expect(where()).toBe('/admin/rules/fresh'))
  })

  it('duplicates into a new rule set named after the original, carrying its content and metadata', async () => {
    reads([{ ...base, proxy_group_members: { A: [{ kind: 'builtin', value: 'DIRECT' }] } }])
    mountRuleSets('/admin/rules/new?copy=custom')
    expect((await label(/^Slug/) as HTMLInputElement).value).toBe('custom-copy')
    expect((await label(/^Name/) as HTMLInputElement).value).toBe('Custom rules (Copy)')
    expect((await label(/^Slug/) as HTMLInputElement).disabled).toBe(false)
    fireEvent.click(await tab('Rules'))
    fireEvent.click(screen.getByRole('radio', { name: 'Text' }))
    expect((await screen.findByRole('textbox', { name: 'Main rules' }) as HTMLTextAreaElement).value).toBe(content)
  })

  it('is closed to a user who cannot write configuration', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    reads()
    mountRuleSets('/admin/rules/new')
    expect(await screen.findByText('You cannot create rule sets')).toBeTruthy()
  })
})

describe('saving', () => {
  it('confirms before removing the settings of a group the rules no longer contain, and saves nothing on Keep', async () => {
    reads([{ ...base, content: '- MATCH,Keep', proxy_group_order: ['Gone', 'Keep'], proxy_group_members: { Gone: [{ kind: 'builtin', value: 'DIRECT' }] } }])
    api.post.mockResolvedValue({ data: inspection({ groups: [{ name: 'Keep', configured: false, options: { type: 'select' }, default_members: [], members: [] }] }) })
    mountRuleSets(PAGE)
    fireEvent.click(await tab('General'))
    fireEvent.change(await label(/^Name/), { target: { value: 'N' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText(/Gone/)).toBeTruthy()
    await waitFor(() => expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' })))
    fireEvent.click(within(ask).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.put).not.toHaveBeenCalled()
    expect((await label(/^Name/) as HTMLInputElement).value).toBe('N')
  })

  it('does not save while the server reports an error in a Rematch outbound, and takes the person to that tab', async () => {
    reads([{ ...base, mihomo_rematch_outbounds: [{ name: '', target_rematch_name: '', target_sub_rule: '' }] }])
    api.post.mockResolvedValue({ data: inspection({ issues: [{ level: 'error', section: 'rematch_outbound', name: '', code: 'invalid_name', params: { index: 0 }, message: 'invalid' }] }) })
    mountRuleSets(PAGE)
    fireEvent.click(await tab('General'))
    fireEvent.change(await label(/^Name/), { target: { value: 'N' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.getByRole('tab', { name: /Rematch outbounds/ }).getAttribute('aria-selected')).toBe('true'))
    expect(toast).toHaveBeenCalledWith({ message: expect.stringContaining('Rematch'), severity: 'warning' })
    expect(api.put).not.toHaveBeenCalled()
    expect(await screen.findByText(/cannot be empty or contain commas/)).toBeTruthy()
  })

  it('keeps a problem in the main rules on the Rules tab, in plain sight', async () => {
    reads()
    api.post.mockResolvedValue({ data: inspection({ issues: [{ level: 'error', section: 'rules', code: 'invalid_rule_yaml', message: 'The rule is not a valid YAML rule list' }] }) })
    mountRuleSets(PAGE)
    fireEvent.click(await tab('General'))
    fireEvent.change(await label(/^Name/), { target: { value: 'N' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Rules' }).getAttribute('aria-selected')).toBe('true'))
    expect(await screen.findByText('The rule is not a valid YAML rule list')).toBeTruthy()
    expect(api.put).not.toHaveBeenCalled()
  })

  it('refuses to send a body the server would refuse, and says so before any request', async () => {
    reads([{ ...base, content: '- DOMAIN-SUFFIX,x.example,P\n'.repeat(40_000) }])
    mountRuleSets(PAGE)
    fireEvent.click(await tab('General'))
    fireEvent.change(await label(/^Name/), { target: { value: 'N' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: expect.stringContaining('too large'), severity: 'warning' }))
    expect(api.post).not.toHaveBeenCalled()
    expect(api.put).not.toHaveBeenCalled()
  })

  it('edits the sub-rules and the Rematch outbounds on their own tabs and saves them with the draft', async () => {
    reads([{ ...base, content: '- DOMAIN,ai.example,AI', mihomo_sub_rules: [{ name: 'ai-rules', content: '- MATCH,DIRECT' }], mihomo_rematch_outbounds: [{ name: 'AI', target_rematch_name: 'ai', target_sub_rule: 'ai-rules' }] }])
    mountRuleSets(PAGE)
    fireEvent.click(await tab('Sub-rules (1)'))
    expect((await screen.findByLabelText('Sub-rule name') as HTMLInputElement).value).toBe('ai-rules')
    fireEvent.change(await screen.findByRole('textbox', { name: 'ai-rules' }), { target: { value: '- MATCH,REJECT' } })
    fireEvent.click(await tab('Rematch outbounds (1)'))
    fireEvent.change(await screen.findByLabelText('Rematch name'), { target: { value: 'ai-v2' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][1]).toMatchObject({
      mihomo_sub_rules: [{ name: 'ai-rules', content: '- MATCH,REJECT' }],
      mihomo_rematch_outbounds: [{ name: 'AI', target_rematch_name: 'ai-v2', target_sub_rule: 'ai-rules' }],
    })
  })

  it('keeps precise line identities when a rule set with line members is edited and saved', async () => {
    const line = { kind: 'line' as const, node_id: 17, line_id: 'stable-relay-id' }
    reads([{ ...base, content: '- MATCH,Pick', proxy_group_members: { Pick: [line] } }])
    api.post.mockResolvedValue({ data: inspection({
      groups: [{ name: 'Pick', configured: true, options: { type: 'select' }, default_members: [], members: [line] }],
      lines: [{ node_id: 17, line_id: line.line_id, display_name: 'Node B', kind: 'relay', available: true }],
    }) })
    mountRuleSets(PAGE)
    fireEvent.click(await tab('Proxy groups'))
    expect((await screen.findByTitle('line:17:stable-relay-id')).textContent).toBeTruthy()
    fireEvent.click(await tab('General'))
    fireEvent.change(await label(/^Name/), { target: { value: 'new-name' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][1].proxy_group_members).toEqual({ Pick: [line] })
  })

  it('does not start a second save while one is running', async () => {
    reads()
    let finish!: () => void
    api.put.mockImplementation(async (_u: string, body: unknown) => { await new Promise<void>(r => { finish = r }); return { data: body } })
    mountRuleSets(PAGE)
    fireEvent.click(await tab('General'))
    fireEvent.change(await label(/^Name/), { target: { value: 'N' } })
    const save = screen.getByRole('button', { name: 'Save' })
    fireEvent.click(save)
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByRole('button', { name: /Saving/ }))
    fireEvent.submit(document.querySelector('form')!)
    expect(api.put).toHaveBeenCalledTimes(1)
    finish()
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'Saved', severity: 'success' }))
  })
})

describe('leaving', () => {
  it('asks once before leaving with unsaved edits, not on a tab switch, and not when nothing changed', async () => {
    reads()
    mountRuleSets(['/admin/rules', PAGE])
    fireEvent.click(await tab('General'))
    fireEvent.change(await label(/^Name/), { target: { value: 'Changed' } })
    fireEvent.click(await tab('Rules'))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    fireEvent.click(screen.getByRole('link', { name: /Rule sets/ }))
    const ask = await screen.findByRole('alertdialog')
    fireEvent.click(within(ask).getByRole('button', { name: /Keep editing/ }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(where()).toBe(PAGE)
    fireEvent.click(screen.getByRole('link', { name: /Rule sets/ }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: /Discard/ }))
    await waitFor(() => expect(where()).toBe('/admin/rules'))
  })

  it('leaves without asking when nothing was changed', async () => {
    reads()
    mountRuleSets(['/admin/rules?q=cus', PAGE])
    await screen.findByRole('heading', { name: 'Custom rules' })
    fireEvent.click(screen.getByRole('link', { name: /Rule sets/ }))
    await waitFor(() => expect(where()).toBe('/admin/rules'))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })
})

describe('the address and the header', () => {
  it('shows "not found" in the frame for an unknown slug, with the way back', async () => {
    reads()
    mountRuleSets('/admin/rules/nope')
    expect(await screen.findByText('This rule set was not found')).toBeTruthy()
    expect(screen.getByRole('link', { name: /Rule sets/ })).toBeTruthy()
  })

  it('names the templates that use the rule set, and deletes through the same confirmation as the list', async () => {
    reads()
    api.delete.mockResolvedValue({})
    mountRuleSets(['/admin/rules', PAGE])
    expect(await screen.findByText(/custom · Enabled · Used by 1 templates/)).toBeTruthy()
    fireEvent.click(within(document.querySelector<HTMLElement>('[data-slot=object-page-actions]')!).getByRole('button', { name: /More/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText(/referenced by Mihomo main/)).toBeTruthy()
    await waitFor(() => expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' })))
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/rules/custom'))
    await waitFor(() => expect(where()).toBe('/admin/rules'))
  })

  it('keeps Delete off for a seeded rule set and restores it through a confirmation', async () => {
    reads()
    mountRuleSets('/admin/rules/default_rules')
    await screen.findByRole('heading', { name: 'Seeded rules' })
    fireEvent.click(within(document.querySelector<HTMLElement>('[data-slot=object-page-actions]')!).getByRole('button', { name: /More/ }))
    expect((await screen.findByRole('menuitem', { name: /^Delete/ })).getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(screen.getByRole('menuitem', { name: 'Restore seeded default' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Restore seeded default' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/rules/default_rules/reset'))
  })
})

describe('an operator', () => {
  it('reads the rule set and has nothing to change it with', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    reads()
    mountRuleSets(PAGE)
    expect(await screen.findByText('Only administrators can change rule sets')).toBeTruthy()
    await openGroup(/Proxy/)
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Add' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Paste list' })).toBeNull()
    expect(screen.queryByRole('tab', { name: 'Proxy groups' })).toBeNull()
    expect(document.querySelector('[data-slot=object-page-actions]')).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
  })
})
