// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mountPage, toast } from './subscription/testHarness'
import RuleSetsView from './RuleSetsView'

const PATH = '/admin/rules'
const base = { slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: [] as string[], content: '- DOMAIN,a.example,DIRECT' }
const seeded = { ...base, slug: 'default_rules', name: 'Seeded rules', sort: 1 }
const template = { slug: 't1', name: 'Mihomo main', client_type: 'mihomo', is_default: false, rule_sets: ['custom'], content: '' }
const inspection = (extra: Record<string, unknown> = {}) => ({ groups: [], builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [], ...extra })

function reads(rows: unknown[] = [base, seeded]) {
  installReads({ '/admin/rules': list(rows), '/admin/templates': list([template]), '/admin/groups': list([]) })
  api.post.mockResolvedValue({ data: inspection() })
}

const rowOf = async (name: string) => (await screen.findByText(name)).closest('tr')!
async function openMenu(name: string) {
  const row = await rowOf(name)
  fireEvent.click(within(row).getByRole('button', { name: new RegExp(`More actions for ${name}`) }))
}
const menuItem = (name: RegExp | string) => screen.findByRole('menuitem', { name })
const sheet = () => screen.findByRole('dialog')

describe('rule-set list', () => {
  it('shows the rule sets with their usage and says why a failed read failed', async () => {
    reads()
    mountPage(<RuleSetsView />, PATH)
    const row = await rowOf('Custom rules')
    expect(within(row).getByText('Mihomo main')).toBeTruthy()
    expect(within(row).getByText('Enabled')).toBeTruthy()
    expect(within(await rowOf('Seeded rules')).getByText('Unused')).toBeTruthy()
  })

  it('reports a failed read instead of an empty table, with a retry', async () => {
    installReads({ '/admin/rules': new Error('offline'), '/admin/templates': list([]), '/admin/groups': list([]) })
    mountPage(<RuleSetsView />, PATH)
    expect(await screen.findByText('Rule sets are temporarily unavailable')).toBeTruthy()
    expect(screen.getByRole('button', { name: /Retry/ })).toBeTruthy()
  })

  it('gives a row one visible action, and a seeded rule set a restore item and a delete that says why it is off', async () => {
    reads()
    mountPage(<RuleSetsView />, PATH)
    await openMenu('Seeded rules')
    expect(await menuItem('Restore seeded default')).toBeTruthy()
    expect((await menuItem(/Seeded rule sets can't be deleted/)).getAttribute('aria-disabled')).toBe('true')
  })

  it('has no actions and no create button without config write access', async () => {
    const { useAuthStore } = await import('@/stores/auth')
    useAuthStore.setState({ role: 'user', userId: 1, hasToken: true })
    reads()
    mountPage(<RuleSetsView />, PATH)
    await rowOf('Custom rules')
    expect(screen.queryByRole('button', { name: 'New rule set' })).toBeNull()
    expect(screen.queryByRole('button', { name: /More actions/ })).toBeNull()
  })
})

describe('deleting and restoring', () => {
  it('names the rule set and the templates that use it, focuses Cancel, and deletes only after the answer', async () => {
    reads()
    api.delete.mockResolvedValue({})
    mountPage(<RuleSetsView />, PATH)
    await openMenu('Custom rules')
    fireEvent.click(await menuItem('Delete'))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/Delete rule set custom\?/)).toBeTruthy()
    expect(within(dialog).getByText(/referenced by Mihomo main/)).toBeTruthy()
    expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/rules/custom'))
    expect(toast).toHaveBeenCalledWith({ message: 'Deleted', severity: 'success' })
  })

  it('restores a seeded default through a confirmation and one request, and keeps the row quiet meanwhile', async () => {
    reads()
    let finish!: () => void
    api.post.mockImplementation(async (url: string) => {
      if (url === '/admin/rules/default_rules/reset') await new Promise<void>(r => { finish = r })
      return { data: inspection() }
    })
    mountPage(<RuleSetsView />, PATH)
    await openMenu('Seeded rules')
    fireEvent.click(await menuItem('Restore seeded default'))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Restore seeded default' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/rules/default_rules/reset'))
    // While it runs the row's actions are off: a second restore cannot be started.
    const row = await rowOf('Seeded rules')
    expect((within(row).getByRole('button', { name: /^Edit rule set/ }) as HTMLButtonElement).disabled).toBe(true)
    finish()
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'Restored to the seeded default', severity: 'success' }))
  })
})

describe('the rule-set sheet', () => {
  it('saves an edited rule set with exactly the body the old editor sent (everything else as loaded)', async () => {
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
    mountPage(<RuleSetsView />, PATH)
    fireEvent.click(within(await rowOf('Custom rules')).getByRole('button', { name: /^Edit rule set/ }))
    const dialog = await sheet()
    fireEvent.change(within(dialog).getByLabelText(/^Name/), { target: { value: 'Renamed' } })
    fireEvent.change(within(dialog).getByLabelText('Sort'), { target: { value: '7' } })
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Main rules' }), { target: { value: '- DOMAIN,b.example,A\n- MATCH,B' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
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
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    // The table shows the saved row without reloading the list.
    expect(await screen.findByText('Renamed')).toBeTruthy()
  })

  it('creates with the blank defaults the old editor used, and refuses an existing slug', async () => {
    reads()
    mountPage(<RuleSetsView />, PATH)
    fireEvent.click(await screen.findByRole('button', { name: 'New rule set' }))
    const dialog = await sheet()
    fireEvent.change(within(dialog).getByLabelText(/^Slug/), { target: { value: 'custom' } })
    fireEvent.change(within(dialog).getByLabelText(/^Name/), { target: { value: 'Dup' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: expect.stringContaining('already exists'), severity: 'warning' }))
    expect(api.put).not.toHaveBeenCalled()
    fireEvent.change(within(dialog).getByLabelText(/^Slug/), { target: { value: 'fresh' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put).toHaveBeenCalledWith('/admin/rules/fresh', {
      slug: 'fresh', name: 'Dup', sort: 100, enabled: true, direct_subscription_domain: false,
      proxy_group_order: [], proxy_group_members: {}, proxy_group_options: {}, mihomo_sub_rules: [], mihomo_rematch_outbounds: [], content: '',
    })
  })

  it('duplicates into a new rule set named after the original, carrying its content and metadata', async () => {
    reads([{ ...base, proxy_group_members: { A: [{ kind: 'builtin', value: 'DIRECT' }] } }])
    const { router } = mountPage(<RuleSetsView />, PATH)
    fireEvent.click(await (async () => { await openMenu('Custom rules'); return menuItem('Duplicate as a new rule set') })())
    const dialog = await sheet()
    expect(router.state.location.search).toContain('copy=custom')
    expect((within(dialog).getByLabelText(/^Slug/) as HTMLInputElement).value).toBe('custom-copy')
    expect((within(dialog).getByLabelText(/^Name/) as HTMLInputElement).value).toBe('Custom rules (Copy)')
    expect((within(dialog).getByRole('textbox', { name: 'Main rules' }) as HTMLTextAreaElement).value).toBe(base.content)
    expect((within(dialog).getByLabelText(/^Slug/) as HTMLInputElement).disabled).toBe(false)
  })

  it('confirms before removing the settings of a group the rules no longer contain, and saves nothing on Keep', async () => {
    reads([{ ...base, content: '- MATCH,Keep', proxy_group_order: ['Gone', 'Keep'], proxy_group_members: { Gone: [{ kind: 'builtin', value: 'DIRECT' }] } }])
    api.post.mockResolvedValue({ data: inspection({ groups: [{ name: 'Keep', configured: false, options: { type: 'select' }, default_members: [], members: [] }] }) })
    mountPage(<RuleSetsView />, PATH)
    fireEvent.click(within(await rowOf('Custom rules')).getByRole('button', { name: /^Edit rule set/ }))
    const dialog = await sheet()
    fireEvent.change(within(dialog).getByLabelText(/^Name/), { target: { value: 'N' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText(/Gone/)).toBeTruthy()
    expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' }))
    fireEvent.click(within(ask).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.put).not.toHaveBeenCalled()
    expect((within(dialog).getByLabelText(/^Name/) as HTMLInputElement).value).toBe('N')
  })

  it('does not save while the server reports an error in a Rematch outbound, and takes the person to that tab', async () => {
    reads([{ ...base, mihomo_rematch_outbounds: [{ name: '', target_rematch_name: '', target_sub_rule: '' }] }])
    api.post.mockResolvedValue({ data: inspection({ issues: [{ level: 'error', section: 'rematch_outbound', name: '', code: 'invalid_name', params: { index: 0 }, message: 'invalid' }] }) })
    mountPage(<RuleSetsView />, PATH)
    fireEvent.click(within(await rowOf('Custom rules')).getByRole('button', { name: /^Edit rule set/ }))
    const dialog = await sheet()
    fireEvent.change(within(dialog).getByLabelText(/^Name/), { target: { value: 'N' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(within(dialog).getByRole('tab', { name: /Rematch outbounds/ }).getAttribute('aria-selected')).toBe('true'))
    expect(toast).toHaveBeenCalledWith({ message: expect.stringContaining('Rematch'), severity: 'warning' })
    expect(api.put).not.toHaveBeenCalled()
    // Shown now that a save was tried.
    expect(await within(dialog).findByText(/cannot be empty or contain commas/)).toBeTruthy()
  })

  it('edits the sub-rules and the Rematch outbounds on their own tabs and saves them with the draft', async () => {
    reads([{ ...base, content: '- DOMAIN,ai.example,AI', mihomo_sub_rules: [{ name: 'ai-rules', content: '- MATCH,DIRECT' }], mihomo_rematch_outbounds: [{ name: 'AI', target_rematch_name: 'ai', target_sub_rule: 'ai-rules' }] }])
    mountPage(<RuleSetsView />, PATH)
    fireEvent.click(within(await rowOf('Custom rules')).getByRole('button', { name: /^Edit rule set/ }))
    const dialog = await sheet()
    // The counts are on the tabs.
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Sub-rules (1)' }))
    expect((await within(dialog).findByLabelText('Sub-rule name') as HTMLInputElement).value).toBe('ai-rules')
    fireEvent.change(await within(dialog).findByRole('textbox', { name: 'ai-rules' }), { target: { value: '- MATCH,REJECT' } })
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Rematch outbounds (1)' }))
    fireEvent.change(await within(dialog).findByLabelText('Rematch name'), { target: { value: 'ai-v2' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][1]).toMatchObject({
      mihomo_sub_rules: [{ name: 'ai-rules', content: '- MATCH,REJECT' }],
      mihomo_rematch_outbounds: [{ name: 'AI', target_rematch_name: 'ai-v2', target_sub_rule: 'ai-rules' }],
    })
  })

  it('shows Rematch errors only once the tab has been left or a save was tried', async () => {
    reads([{ ...base, mihomo_rematch_outbounds: [{ name: '', target_rematch_name: '', target_sub_rule: '' }] }])
    api.post.mockResolvedValue({ data: inspection({ issues: [{ level: 'error', section: 'rematch_outbound', name: '', code: 'invalid_name', params: { index: 0 }, message: 'invalid' }] }) })
    mountPage(<RuleSetsView />, PATH)
    fireEvent.click(within(await rowOf('Custom rules')).getByRole('button', { name: /^Edit rule set/ }))
    const dialog = await sheet()
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Rematch outbounds (1)' }))
    await waitFor(() => expect(api.post).toHaveBeenCalled())
    expect(within(dialog).queryByText(/cannot be empty or contain commas/)).toBeNull()
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Main rules' }))
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Rematch outbounds (1)' }))
    expect(await within(dialog).findByText(/cannot be empty or contain commas/)).toBeTruthy()
  })

  it('keeps precise line identities when a rule set with line members is edited and saved', async () => {
    const line = { kind: 'line' as const, node_id: 17, line_id: 'stable-relay-id' }
    reads([{ ...base, content: '- MATCH,Pick', proxy_group_members: { Pick: [line] } }])
    api.post.mockResolvedValue({ data: inspection({
      groups: [{ name: 'Pick', configured: true, options: { type: 'select' }, default_members: [], members: [line] }],
      lines: [{ node_id: 17, line_id: line.line_id, display_name: 'Node B', kind: 'relay', available: true }],
    }) })
    mountPage(<RuleSetsView />, PATH)
    fireEvent.click(within(await rowOf('Custom rules')).getByRole('button', { name: /^Edit rule set/ }))
    const dialog = await sheet()
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Proxy group members' }))
    expect((await within(dialog).findByTitle('line:17:stable-relay-id')).textContent).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Main rules' }))
    fireEvent.change(within(dialog).getByLabelText(/^Name/), { target: { value: 'new-name' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][1].proxy_group_members).toEqual({ Pick: [line] })
  })

  it('asks before the sheet is closed with unsaved edits, and not when nothing changed', async () => {
    reads()
    const { router } = mountPage(<RuleSetsView />, PATH, `${PATH}?edit=custom`)
    const dialog = await sheet()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close details' }))
    await waitFor(() => expect(router.state.location.search).toBe(''))
    expect(screen.queryByRole('alertdialog')).toBeNull()

    router.navigate(`${PATH}?edit=custom`)
    const again = await sheet()
    fireEvent.change(within(again).getByLabelText(/^Name/), { target: { value: 'Changed' } })
    fireEvent.click(within(again).getByRole('button', { name: 'Close details' }))
    const ask = await screen.findByRole('alertdialog')
    expect(router.state.location.search).toBe('?edit=custom')
    fireEvent.click(within(ask).getByRole('button', { name: /Keep editing/ }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect((within(again).getByLabelText(/^Name/) as HTMLInputElement).value).toBe('Changed')
  })

  it('says how to leave the code editor with the keyboard', async () => {
    reads()
    mountPage(<RuleSetsView />, PATH, `${PATH}?edit=custom`)
    expect(await screen.findByText(/press Escape and then Tab/)).toBeTruthy()
  })
})

describe('bulk actions', () => {
  it('leaves seeded rule sets out of the selection and confirms a bulk delete with the count', async () => {
    reads([base, { ...base, slug: 'other', name: 'Other' }, seeded])
    api.delete.mockResolvedValue({})
    mountPage(<RuleSetsView />, PATH)
    const seededRow = await rowOf('Seeded rules')
    expect(within(seededRow).queryByRole('checkbox')).toBeNull()
    fireEvent.click(within(await rowOf('Custom rules')).getByRole('checkbox'))
    fireEvent.click(within(await rowOf('Other')).getByRole('checkbox'))
    fireEvent.click(await screen.findByRole('button', { name: /Bulk delete/ }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText(/Delete 2 rule sets\?/)).toBeTruthy()
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(2))
    expect(api.delete).toHaveBeenCalledWith('/admin/rules/custom')
    expect(api.delete).toHaveBeenCalledWith('/admin/rules/other')
  })

  it('bulk disable saves each selected rule set with enabled false and nothing else changed', async () => {
    reads()
    mountPage(<RuleSetsView />, PATH)
    fireEvent.click(within(await rowOf('Custom rules')).getByRole('checkbox'))
    fireEvent.click(await screen.findByRole('button', { name: 'Bulk disable' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/rules/custom', { ...base, enabled: false }))
  })
})
