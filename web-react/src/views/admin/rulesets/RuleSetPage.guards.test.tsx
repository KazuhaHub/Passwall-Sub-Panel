// @vitest-environment jsdom
import { configure, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mountRuleSets, toast } from './testHarness'

configure({ asyncUtilTimeout: 4000 })

const content = '- DOMAIN,a.example,Proxy\n- DOMAIN-SUFFIX,b.example,Proxy\n- MATCH,Proxy\n'
const base = { slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: [] as string[], content, revision: 'r1' }
const template = { slug: 't1', name: 'Mihomo main', client_type: 'mihomo', is_default: false, rule_sets: ['custom'], content: '' }
const inspection = () => ({ groups: [], builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [] })
const PAGE = '/admin/rules/custom'
const MIB = 1 << 20

function reads(rows: unknown[] = [base]) {
  installReads({ '/admin/rules': list(rows), '/admin/templates': list([template]), '/admin/groups': list([]) })
  api.post.mockResolvedValue({ data: inspection() })
}
const http = (status: number, data: unknown) => Object.assign(new Error(`status ${status}`), { response: { status, data } })
const body = (call = 0) => api.put.mock.calls[call][1] as Record<string, unknown>
async function rename(to: string) {
  fireEvent.click(await screen.findByRole('tab', { name: 'General' }))
  fireEvent.change(await screen.findByLabelText(/^Name/), { target: { value: to } })
}
const save = async () => fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
const messages = () => toast.mock.calls.map(c => String(c[0]?.message ?? c[0]))

describe('the size of the body', () => {
  it('allows what the old 1 MiB check refused and refuses a body past the server limit before any request', async () => {
    reads()
    api.put.mockResolvedValue({ data: { ...base, revision: 'r2' } })
    mountRuleSets(PAGE)
    await rename('x'.repeat(2 * MIB))
    await save()
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    const posts = api.post.mock.calls.length
    await rename('y'.repeat(9 * MIB))
    await save()
    await waitFor(() => expect(messages().some(m => m.includes('8.00 MB'))).toBe(true))
    expect(api.put).toHaveBeenCalledTimes(1)
    expect(api.post.mock.calls.length).toBe(posts)
  })

  it('a 413 from the server names the limit it reports, and no generic error', async () => {
    reads()
    api.put.mockRejectedValue(http(413, { error: 'Request body too large', code: 'body_too_large', limit_bytes: 8 * MIB }))
    mountRuleSets(PAGE)
    await rename('Mine')
    await save()
    await waitFor(() => expect(messages().some(m => /too large.*limit is 8\.00 MB/.test(m))).toBe(true))
    // The draft is still there, still unsaved.
    expect(screen.getByRole('button', { name: 'Save' })).toBeTruthy()
  })

  it('a 413 that is not the panel\'s own (an HTML page from a reverse proxy) says the limit is unknown and names nginx', async () => {
    reads()
    api.put.mockRejectedValue(http(413, '<html><body>413 Request Entity Too Large</body></html>'))
    mountRuleSets(PAGE)
    await rename('Mine')
    await save()
    await waitFor(() => expect(messages().some(m => m.includes('client_max_body_size'))).toBe(true))
  })

  it('a 413 on the inspection stops the save with the same message and sends no PUT', async () => {
    reads()
    api.post.mockRejectedValue(http(413, { code: 'body_too_large', limit_bytes: 8 * MIB }))
    mountRuleSets(PAGE)
    await rename('Mine')
    await save()
    await waitFor(() => expect(messages().some(m => /too large.*8\.00 MB/.test(m))).toBe(true))
    expect(api.put).not.toHaveBeenCalled()
  })

  it('a 413 on the draft inspection puts the problems panel in its failed state with that message', async () => {
    reads()
    api.post.mockRejectedValue(http(413, '<html>413</html>'))
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('button', { name: 'Issues and findings' }))
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText(/client_max_body_size/)).toBeTruthy()
    expect(within(dialog).queryByText(/couldn't be read/)).toBeNull()
  })
})

describe('the revision', () => {
  it('sends the loaded revision as base_revision, never as revision, and moves on to the one the save returns', async () => {
    reads()
    api.put.mockResolvedValueOnce({ data: { ...base, name: 'One', revision: 'r2' } }).mockResolvedValueOnce({ data: { ...base, name: 'Two', revision: 'r3' } })
    mountRuleSets(PAGE)
    await rename('One')
    await save()
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(body(0)).toMatchObject({ name: 'One', base_revision: 'r1' })
    expect(body(0)).not.toHaveProperty('revision')
    await screen.findByRole('heading', { name: 'One' })
    await rename('Two')
    await save()
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
    expect(body(1)).toMatchObject({ name: 'Two', base_revision: 'r2' })
  })

  it('a new rule set, and a copy of one, is created without a base_revision', async () => {
    reads()
    api.put.mockResolvedValue({ data: { ...base, slug: 'copy', revision: 'n1' } })
    mountRuleSets('/admin/rules/new?copy=custom')
    fireEvent.change(await screen.findByLabelText(/^Slug/), { target: { value: 'copy' } })
    fireEvent.change(await screen.findByLabelText(/^Name/), { target: { value: 'Copy' } })
    await save()
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(body(0)).not.toHaveProperty('base_revision')
    expect(body(0)).not.toHaveProperty('revision')
  })
})

describe('a rule set read without a revision', () => {
  it('is not saved: no unconditional overwrite, the person is asked to reload', async () => {
    const { revision: _r, ...bare } = base
    reads([bare])
    mountRuleSets(PAGE)
    await rename('Mine')
    await save()
    await waitFor(() => expect(messages().some(m => /without a revision/.test(m))).toBe(true))
    expect(api.put).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
  })
})

describe('a stale save (409 ruleset_stale)', () => {
  const theirs = { ...base, name: 'Theirs', content: '- DOMAIN,a.example,Proxy\n- DOMAIN,c.example,Proxy\n- MATCH,Proxy\n', revision: 'r9' }
  const stale = () => http(409, { error: 'Rule set changed since it was loaded', code: 'ruleset_stale', current: theirs })
  async function conflict() {
    reads()
    api.put.mockRejectedValueOnce(stale())
    mountRuleSets(PAGE)
    await rename('Mine')
    await save()
    await screen.findByText('The rule set has changed')
  }

  it('keeps the draft untouched and offers the three ways forward, with no generic error', async () => {
    await conflict()
    expect((screen.getByLabelText(/^Name/) as HTMLInputElement).value).toBe('Mine')
    expect(screen.getByRole('button', { name: 'Save' })).toBeTruthy()
    for (const name of ['Review their changes', 'Overwrite with my version', 'Discard mine and load theirs']) expect(screen.getByRole('button', { name })).toBeTruthy()
    expect(api.put).toHaveBeenCalledTimes(1)
  })

  it('"Review their changes" shows what they changed and what I changed against the same base', async () => {
    await conflict()
    fireEvent.click(screen.getByRole('button', { name: 'Review their changes' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('+DOMAIN,c.example,Proxy'.slice(1), { exact: false })).toBeTruthy()
    expect(within(dialog).getByText('Their changes')).toBeTruthy()
    expect(within(dialog).getByText('Your changes')).toBeTruthy()
    // Both sides renamed it, so the field list says so.
    expect(within(dialog).getByText(/Changed by both/)).toBeTruthy()
  })

  it('"Overwrite" asks first with Cancel focused, then sends my draft against THEIR revision', async () => {
    await conflict()
    api.put.mockResolvedValueOnce({ data: { ...base, name: 'Mine', revision: 'r10' } })
    fireEvent.click(screen.getByRole('button', { name: 'Overwrite with my version' }))
    const ask = await screen.findByRole('alertdialog')
    await waitFor(() => expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' })))
    expect(api.put).toHaveBeenCalledTimes(1)
    fireEvent.click(within(ask).getByRole('button', { name: 'Overwrite' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
    expect(body(1)).toMatchObject({ name: 'Mine', base_revision: 'r9', content })
    await waitFor(() => expect(screen.queryByText('The rule set has changed')).toBeNull())
  })

  it('cancelling the overwrite sends nothing', async () => {
    await conflict()
    fireEvent.click(screen.getByRole('button', { name: 'Overwrite with my version' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.put).toHaveBeenCalledTimes(1)
  })

  it('"Discard mine and load theirs" asks first, then shows their version as the clean baseline, with no request', async () => {
    await conflict()
    fireEvent.click(screen.getByRole('button', { name: 'Discard mine and load theirs' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Discard mine' }))
    await screen.findByRole('heading', { name: 'Theirs' })
    expect(screen.queryByText('The rule set has changed')).toBeNull()
    expect(api.put).toHaveBeenCalledTimes(1)
    // The next save is against their revision.
    api.put.mockResolvedValueOnce({ data: { ...theirs, name: 'Later', revision: 'r11' } })
    await rename('Later')
    await save()
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
    expect(body(1)).toMatchObject({ base_revision: 'r9' })
  })
})
