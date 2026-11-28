// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { api, installReads, list, mountPage, toast } from './subscription/testHarness'
import { mountTemplates, where } from './templates/testHarness'
import TemplatesView from './TemplatesView'

const PATH = '/admin/templates'
const yaml = 'proxies: []\nproxy-groups:\n  - name: Proxy\n    type: select\nrules: []\n'
const custom = { slug: 'custom-tpl', name: 'Custom template', client_type: 'mihomo', is_default: false, rule_sets: ['ads', 'cn'], content: yaml }
const seeded = { slug: 'default-mihomo', name: 'Seeded template', client_type: 'mihomo', is_default: true, rule_sets: [], content: '' }
// A list row as the server sends it: `targets` comes from the server's reading of the rules (stated here for each row), not from any text.
const rule = (slug: string, targets: string[], enabled = true) => ({ slug, name: `${slug} rules`, sort: 1, enabled, direct_subscription_domain: false, proxy_group_order: [], line_count: targets.length, size_bytes: 0, targets })
const ruleSets = [rule('ads', ['Proxy']), rule('cn', ['Other']), rule('off', [], false)]

function reads(rows: unknown[] = [custom, seeded]) {
  installReads({ '/admin/templates': list(rows), '/admin/rules': list(ruleSets) })
}
const rowOf = async (name: string) => (await screen.findByText(name)).closest('tr')!
async function openMenu(name: string) {
  fireEvent.click(within(await rowOf(name)).getByRole('button', { name: new RegExp(`More actions for ${name}`) }))
}
const menuItem = (name: RegExp | string) => screen.findByRole('menuitem', { name })

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
    const del = await menuItem(/^Delete/)
    expect(del.getAttribute('aria-disabled')).toBe('true')
    expect(document.getElementById(del.getAttribute('aria-describedby')!)?.textContent).toMatch(/Seeded templates can't be deleted/)
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
    await waitFor(() => expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' })))
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

describe('opening a template', () => {
  it('goes to its own page from the row, and "New template" goes to the create page', async () => {
    reads()
    mountTemplates()
    fireEvent.click(within(await rowOf('Custom template')).getByRole('button', { name: /^Edit template/ }))
    await waitFor(() => expect(where()).toBe('/admin/templates/custom-tpl'))
    expect(await screen.findByRole('heading', { name: 'Custom template' })).toBeTruthy()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('opens the create page and the duplicate page from the list', async () => {
    reads()
    const { router } = mountTemplates()
    fireEvent.click(await screen.findByRole('button', { name: 'New template' }))
    await waitFor(() => expect(where()).toBe('/admin/templates/new'))
    await router.navigate(-1)
    await openMenu('Seeded template')
    fireEvent.click(await menuItem('Duplicate as a new template'))
    await waitFor(() => expect(where()).toBe('/admin/templates/new?copy=default-mihomo'))
  })

  it.each([
    ['?edit=custom-tpl', '/admin/templates/custom-tpl'],
    ['?edit=new', '/admin/templates/new'],
    ['?edit=new&copy=custom-tpl', '/admin/templates/new?copy=custom-tpl'],
  ])('sends the old side-sheet address %s to %s, replacing it', async (search, to) => {
    reads()
    const { router } = mountTemplates(`/admin/templates${search}`)
    await waitFor(() => expect(where()).toBe(to))
    // Replaced, so Back does not return to the redirect.
    expect(router.state.historyAction).toBe('REPLACE')
  })
})
