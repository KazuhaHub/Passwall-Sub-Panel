// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { api, installReads, list, mountTemplates, toast, where } from './testHarness'

const yaml = 'proxies: []\nproxy-groups:\n  - name: Proxy\n    type: select\nrules: []\n'
const custom = { slug: 'custom-tpl', name: 'Custom template', client_type: 'mihomo', is_default: false, rule_sets: ['ads', 'cn'], content: yaml }
const seeded = { slug: 'default-mihomo', name: 'Seeded template', client_type: 'mihomo', is_default: true, rule_sets: [], content: '' }
const rule = (slug: string, content: string, enabled = true) => ({ slug, name: `${slug} rules`, sort: 1, enabled, direct_subscription_domain: false, proxy_group_order: [], content })
const ruleSets = [rule('ads', '- DOMAIN,a.example,Proxy'), rule('cn', '- DOMAIN,b.example,Other'), rule('off', '- MATCH,DIRECT', false)]

function reads(rows: unknown[] = [custom, seeded]) {
  installReads({ '/admin/templates': list(rows), '/admin/rules': list(ruleSets) })
}
const PAGE = '/admin/templates/custom-tpl'
const label = (re: RegExp | string) => screen.findByLabelText(re)

describe('a template page', () => {
  it('saves an edit with exactly the draft and stays on the page, now clean', async () => {
    reads()
    mountTemplates(PAGE)
    expect((await label(/^Slug/) as HTMLInputElement).disabled).toBe(true)
    fireEvent.change(await label(/^Name/), { target: { value: 'Renamed' } })
    fireEvent.change(await label('Client type'), { target: { value: 'sing-box' } })
    fireEvent.change(await label('Template content'), { target: { value: '{"outbounds": {{ outbounds }}}' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put).toHaveBeenCalledWith('/admin/templates/custom-tpl', {
      slug: 'custom-tpl', name: 'Renamed', client_type: 'sing-box', is_default: false,
      rule_sets: ['ads', 'cn'], content: '{"outbounds": {{ outbounds }}}',
    })
    expect(await screen.findByRole('heading', { name: 'Renamed' })).toBeTruthy()
    expect(where()).toBe(PAGE)
    await waitFor(() => expect(screen.queryByRole('region', { name: /unsaved/i })).toBeNull())
  })

  it('binds rule sets in the order they are picked, and a disabled one cannot be picked', async () => {
    reads([{ ...custom, rule_sets: [] }])
    mountTemplates(PAGE)
    const picker = await screen.findByRole('combobox', { name: 'Rule sets' })
    fireEvent.keyDown(picker, { key: 'ArrowDown' })
    expect((await screen.findByRole('option', { name: /off rules \(off\)/ })).getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(screen.getByRole('option', { name: /cn rules \(cn\)/ }))
    fireEvent.keyDown(picker, { key: 'ArrowDown' })
    fireEvent.click(await screen.findByRole('option', { name: /ads rules \(ads\)/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][1].rule_sets).toEqual(['cn', 'ads'])
  })

  it('says what the unsaved draft would cover, and asks before leaving with unsaved edits', async () => {
    reads()
    mountTemplates(['/admin/templates', PAGE])
    expect((await screen.findByTestId('template-hint')).textContent).toMatch(/not found in the template's proxy groups: Other/)
    fireEvent.change(await label(/^Name/), { target: { value: 'Changed' } })
    fireEvent.click(screen.getByRole('link', { name: /Templates/ }))
    const ask = await screen.findByRole('alertdialog')
    fireEvent.click(within(ask).getByRole('button', { name: /Keep editing/ }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(where()).toBe(PAGE)
    fireEvent.click(screen.getByRole('link', { name: /Templates/ }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: /Discard/ }))
    await waitFor(() => expect(where()).toBe('/admin/templates'))
  })

  it('leaves without asking when nothing was changed, to the list as it was left', async () => {
    reads()
    mountTemplates(['/admin/templates?q=cus', PAGE])
    await screen.findByRole('heading', { name: 'Custom template' })
    // No remembered address (opened cold): the plain list.
    fireEvent.click(screen.getByRole('link', { name: /Templates/ }))
    await waitFor(() => expect(where()).toBe('/admin/templates'))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('shows "not found" in the frame for an unknown slug, with the way back', async () => {
    reads()
    mountTemplates('/admin/templates/nope')
    expect(await screen.findByText('This template was not found')).toBeTruthy()
    expect(screen.getByRole('link', { name: /Templates/ })).toBeTruthy()
  })

  it('is read-only without config write access: no save bar, no actions, fields off', async () => {
    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    reads()
    mountTemplates(PAGE)
    expect((await label(/^Name/) as HTMLInputElement).disabled).toBe(true)
    expect(screen.getByText('You can view this template but not change it.')).toBeTruthy()
    expect(screen.queryByRole('button', { name: /Duplicate/ })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull()
  })

  it('deletes from the page after the confirmation, and goes back to the list', async () => {
    reads()
    api.delete.mockResolvedValue({})
    mountTemplates(PAGE)
    fireEvent.click(await screen.findByRole('button', { name: /^More/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const ask = await screen.findByRole('alertdialog')
    await waitFor(() => expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' })))
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/templates/custom-tpl'))
    await waitFor(() => expect(where()).toBe('/admin/templates'))
  })

  it('restores a seeded template with one request and starts again from it', async () => {
    reads()
    api.post.mockResolvedValue({ data: {} })
    mountTemplates('/admin/templates/default-mihomo')
    fireEvent.click(await screen.findByRole('button', { name: /^More/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Restore seeded default' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Restore seeded default' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/templates/default-mihomo/reset'))
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'Restored to the seeded default', severity: 'success' }))
  })
})

describe('the create page', () => {
  it('creates with the blank defaults, refuses an existing or reserved slug, then lands on the new page', async () => {
    reads()
    mountTemplates('/admin/templates/new')
    fireEvent.change(await label(/^Slug/), { target: { value: 'custom-tpl' } })
    fireEvent.change(await label(/^Name/), { target: { value: 'Dup' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: expect.stringContaining('already exists'), severity: 'warning' }))
    fireEvent.change(await label(/^Slug/), { target: { value: 'new' } })
    // The refusal is now said next to the field, and Save stays off until it is fixed.
    expect((await screen.findAllByText(/is reserved/)).length).toBeGreaterThan(0)
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(api.put).not.toHaveBeenCalled()
    fireEvent.change(await label(/^Slug/), { target: { value: 'fresh' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put).toHaveBeenCalledWith('/admin/templates/fresh', { slug: 'fresh', name: 'Dup', client_type: 'mihomo', is_default: false, rule_sets: [], content: '' })
    await waitFor(() => expect(where()).toBe('/admin/templates/fresh'))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('requires a slug and a name before anything is sent', async () => {
    reads()
    mountTemplates('/admin/templates/new')
    fireEvent.change(await label('Template content'), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith({ message: 'Slug and name are required', severity: 'warning' }))
    expect(api.put).not.toHaveBeenCalled()
  })

  it('duplicates with is_default off and its own copy of the rule-set list', async () => {
    reads([{ ...seeded, rule_sets: ['ads'] }])
    mountTemplates('/admin/templates/new?copy=default-mihomo')
    expect((await label(/^Slug/) as HTMLInputElement).value).toBe('default-mihomo-copy')
    expect((await label(/^Name/) as HTMLInputElement).value).toBe('Seeded template (Copy)')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put).toHaveBeenCalledWith('/admin/templates/default-mihomo-copy', {
      slug: 'default-mihomo-copy', name: 'Seeded template (Copy)', client_type: 'mihomo', is_default: false, rule_sets: ['ads'], content: '',
    })
  })

  it('is not offered without config write access', async () => {
    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    reads()
    mountTemplates('/admin/templates/new')
    expect(await screen.findByText('You cannot create templates')).toBeTruthy()
  })
})
