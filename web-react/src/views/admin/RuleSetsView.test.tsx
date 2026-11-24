// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mountPage, toast } from './subscription/testHarness'
import { mountRuleSets, where } from './rulesets/testHarness'
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
    const del = await menuItem(/^Delete/)
    expect(del.getAttribute('aria-disabled')).toBe('true')
    expect(document.getElementById(del.getAttribute('aria-describedby')!)?.textContent).toMatch(/Seeded rule sets can't be deleted/)
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

describe('opening a rule set', () => {
  it('sends the row, its edit button, New rule set and Duplicate to the rule set pages, carrying the list\'s place', async () => {
    reads()
    mountRuleSets('/admin/rules?q=cus')
    fireEvent.click(within(await rowOf('Custom rules')).getByRole('button', { name: /^Edit rule set/ }))
    await waitFor(() => expect(where()).toBe('/admin/rules/custom'))
    fireEvent.click(await screen.findByRole('link', { name: /Rule sets/ }))
    await waitFor(() => expect(where()).toBe('/admin/rules?q=cus'))
    fireEvent.click(await screen.findByRole('button', { name: 'New rule set' }))
    await waitFor(() => expect(where()).toBe('/admin/rules/new'))
  })

  it('sends Duplicate to the create page with the source named', async () => {
    reads()
    mountRuleSets('/admin/rules')
    await openMenu('Custom rules')
    fireEvent.click(await menuItem('Duplicate as a new rule set'))
    await waitFor(() => expect(where()).toBe('/admin/rules/new?copy=custom'))
  })

  it.each([
    ['?edit=custom', '/admin/rules/custom'],
    ['?edit=new', '/admin/rules/new'],
    ['?edit=new&copy=custom', '/admin/rules/new?copy=custom'],
  ])('sends an old sheet link %s on to %s', async (search, to) => {
    reads()
    mountRuleSets(`/admin/rules${search}`)
    await waitFor(() => expect(where()).toBe(to))
  })

  it('lets an operator open a rule set, which the list could not before', async () => {
    const { useAuthStore } = await import('@/stores/auth')
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    reads()
    mountRuleSets('/admin/rules')
    fireEvent.click(await rowOf('Custom rules'))
    await waitFor(() => expect(where()).toBe('/admin/rules/custom'))
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
