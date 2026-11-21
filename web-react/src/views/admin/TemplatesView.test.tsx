// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { api, installReads, list, mountPage, toast } from './subscription/testHarness'
import TemplatesView from './TemplatesView'

const PATH = '/admin/templates'
const yaml = 'proxies: []\nproxy-groups:\n  - name: Proxy\n    type: select\nrules: []\n'
const custom = { slug: 'custom-tpl', name: 'Custom template', client_type: 'mihomo', is_default: false, rule_sets: ['ads', 'cn'], content: yaml }
const seeded = { slug: 'default-mihomo', name: 'Seeded template', client_type: 'mihomo', is_default: true, rule_sets: [], content: '' }
const rule = (slug: string, content: string, enabled = true) => ({ slug, name: `${slug} rules`, sort: 1, enabled, direct_subscription_domain: false, proxy_group_order: [], content })
const ruleSets = [rule('ads', '- DOMAIN,a.example,Proxy'), rule('cn', '- DOMAIN,b.example,Other'), rule('off', '- MATCH,DIRECT', false)]

function reads(rows: unknown[] = [custom, seeded]) {
  installReads({ '/admin/templates': list(rows), '/admin/rules': list(ruleSets) })
}
const rowOf = async (name: string) => (await screen.findByText(name)).closest('tr')!
async function openMenu(name: string) {
  fireEvent.click(within(await rowOf(name)).getByRole('button', { name: new RegExp(`More actions for ${name}`) }))
}
const menuItem = (name: RegExp | string) => screen.findByRole('menuitem', { name })
const sheet = () => screen.findByRole('dialog')

describe('template list', () => {
  it('shows the bound rule sets and, in words, which group the template does not define', async () => {
    reads()
    mountPage(<TemplatesView />, PATH)
    const row = await rowOf('Custom template')
    expect(within(row).getByText('ads rules、cn rules')).toBeTruthy()
    expect(within(row).getByText('Missing 1')).toBeTruthy()
    expect(within(await rowOf('Seeded template')).getByText('Default')).toBeTruthy()
  })

  it('reports a failed read instead of an empty table', async () => {
    installReads({ '/admin/templates': new Error('offline'), '/admin/rules': list([]) })
    mountPage(<TemplatesView />, PATH)
    expect(await screen.findByText('Templates are temporarily unavailable')).toBeTruthy()
  })

  it('keeps Delete off, and says why, for the seeded default; it can be restored', async () => {
    reads()
    mountPage(<TemplatesView />, PATH)
    await openMenu('Seeded template')
    expect(await menuItem('Restore seeded default')).toBeTruthy()
    expect((await menuItem(/Seeded templates can't be deleted/)).getAttribute('aria-disabled')).toBe('true')
  })

  it('has no create button and no row actions without config write access', async () => {
    useAuthStore.setState({ role: 'user', userId: 1, hasToken: true })
    reads()
    mountPage(<TemplatesView />, PATH)
    await rowOf('Custom template')
    expect(screen.queryByRole('button', { name: 'New template' })).toBeNull()
    expect(screen.queryByRole('button', { name: /More actions/ })).toBeNull()
  })
})

describe('deleting and restoring', () => {
  it('names the template, focuses Cancel and deletes only after the answer', async () => {
    reads()
    api.delete.mockResolvedValue({})
    mountPage(<TemplatesView />, PATH)
    await openMenu('Custom template')
    fireEvent.click(await menuItem('Delete'))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText('Delete template custom-tpl?')).toBeTruthy()
    expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' }))
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/templates/custom-tpl'))
    expect(toast).toHaveBeenCalledWith({ message: 'Deleted', severity: 'success' })
  })

  it('restores a seeded template with one request, and the row waits meanwhile', async () => {
    reads()
    let finish!: () => void
    api.post.mockImplementation(async () => { await new Promise<void>(r => { finish = r }); return { data: {} } })
    mountPage(<TemplatesView />, PATH)
    await openMenu('Seeded template')
    fireEvent.click(await menuItem('Restore seeded default'))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Restore seeded default' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/templates/default-mihomo/reset'))
    expect((within(await rowOf('Seeded template')).getByRole('button', { name: /^Edit template/ }) as HTMLButtonElement).disabled).toBe(true)
    finish()
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'Restored to the seeded default', severity: 'success' }))
  })

  it('leaves the default and seeded templates out of a bulk delete', async () => {
    reads([custom, seeded, { ...custom, slug: 'two', name: 'Two' }])
    api.delete.mockResolvedValue({})
    mountPage(<TemplatesView />, PATH)
    expect(within(await rowOf('Seeded template')).queryByRole('checkbox')).toBeNull()
    fireEvent.click(within(await rowOf('Custom template')).getByRole('checkbox'))
    fireEvent.click(within(await rowOf('Two')).getByRole('checkbox'))
    fireEvent.click(await screen.findByRole('button', { name: /Bulk delete/ }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText('Delete 2 templates?')).toBeTruthy()
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(2))
  })
})

describe('the template sheet', () => {
  it('saves an edit with exactly the draft: only what was changed differs from what was loaded', async () => {
    reads()
    mountPage(<TemplatesView />, PATH)
    fireEvent.click(within(await rowOf('Custom template')).getByRole('button', { name: /^Edit template/ }))
    const dialog = await sheet()
    expect((within(dialog).getByLabelText(/^Slug/) as HTMLInputElement).disabled).toBe(true)
    fireEvent.change(within(dialog).getByLabelText(/^Name/), { target: { value: 'Renamed' } })
    fireEvent.change(within(dialog).getByLabelText('Client type'), { target: { value: 'sing-box' } })
    fireEvent.change(within(dialog).getByLabelText('Template content'), { target: { value: '{"outbounds": {{ outbounds }}}' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put).toHaveBeenCalledWith('/admin/templates/custom-tpl', {
      slug: 'custom-tpl', name: 'Renamed', client_type: 'sing-box', is_default: false,
      rule_sets: ['ads', 'cn'], content: '{"outbounds": {{ outbounds }}}',
    })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await screen.findByText('Renamed')).toBeTruthy()
  })

  it('binds rule sets in the order they are picked, and a disabled one cannot be picked', async () => {
    reads([{ ...custom, rule_sets: [] }])
    mountPage(<TemplatesView />, PATH)
    fireEvent.click(within(await rowOf('Custom template')).getByRole('button', { name: /^Edit template/ }))
    const dialog = await sheet()
    const picker = within(dialog).getByRole('combobox', { name: 'Rule sets' })
    fireEvent.keyDown(picker, { key: 'ArrowDown' })
    expect((await screen.findByRole('option', { name: /off rules \(off\)/ })).getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(screen.getByRole('option', { name: /cn rules \(cn\)/ }))
    fireEvent.keyDown(picker, { key: 'ArrowDown' })
    fireEvent.click(await screen.findByRole('option', { name: /ads rules \(ads\)/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][1].rule_sets).toEqual(['cn', 'ads'])
  })

  it('creates with the blank defaults, and refuses a slug that already exists', async () => {
    reads()
    mountPage(<TemplatesView />, PATH)
    fireEvent.click(await screen.findByRole('button', { name: 'New template' }))
    const dialog = await sheet()
    fireEvent.change(within(dialog).getByLabelText(/^Slug/), { target: { value: 'custom-tpl' } })
    fireEvent.change(within(dialog).getByLabelText(/^Name/), { target: { value: 'Dup' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: expect.stringContaining('already exists'), severity: 'warning' }))
    expect(api.put).not.toHaveBeenCalled()
    fireEvent.change(within(dialog).getByLabelText(/^Slug/), { target: { value: 'fresh' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put).toHaveBeenCalledWith('/admin/templates/fresh', { slug: 'fresh', name: 'Dup', client_type: 'mihomo', is_default: false, rule_sets: [], content: '' })
  })

  it('requires a slug and a name before anything is sent', async () => {
    reads()
    mountPage(<TemplatesView />, PATH)
    fireEvent.click(await screen.findByRole('button', { name: 'New template' }))
    const dialog = await sheet()
    fireEvent.change(within(dialog).getByLabelText('Template content'), { target: { value: 'x' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'Slug and name are required', severity: 'warning' }))
    expect(api.put).not.toHaveBeenCalled()
  })

  it('duplicates with is_default off and its own copy of the rule-set list', async () => {
    reads([{ ...seeded, rule_sets: ['ads'] }])
    mountPage(<TemplatesView />, PATH)
    await openMenu('Seeded template')
    fireEvent.click(await menuItem('Duplicate as a new template'))
    const dialog = await sheet()
    expect((within(dialog).getByLabelText(/^Slug/) as HTMLInputElement).value).toBe('default-mihomo-copy')
    expect((within(dialog).getByLabelText(/^Name/) as HTMLInputElement).value).toBe('Seeded template (Copy)')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put).toHaveBeenCalledWith('/admin/templates/default-mihomo-copy', {
      slug: 'default-mihomo-copy', name: 'Seeded template (Copy)', client_type: 'mihomo', is_default: false, rule_sets: ['ads'], content: '',
    })
  })

  it('says what the unsaved draft would cover, and asks before closing with unsaved edits', async () => {
    reads()
    mountPage(<TemplatesView />, PATH, `${PATH}?edit=custom-tpl`)
    const dialog = await sheet()
    expect((await within(dialog).findByTestId('template-hint')).textContent).toMatch(/not found in the template's proxy groups: Other/)
    fireEvent.change(within(dialog).getByLabelText(/^Name/), { target: { value: 'Changed' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close details' }))
    const ask = await screen.findByRole('alertdialog')
    fireEvent.click(within(ask).getByRole('button', { name: /Keep editing/ }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(screen.getByRole('dialog')).toBeTruthy()
  })
})
