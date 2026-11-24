// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, installReads, list, mountRuleSets } from './testHarness'

// The routing test and the problems panel on the Rules tab (phase 2 of the rule-set editor).
const content = '- GEOSITE,cn,CN\n- DOMAIN-SUFFIX,example.com,Proxy\n# ads\n- DOMAIN,www.example.com,Block\n- DOMAIN-KEYWORD,example,Proxy\n- DOMAIN-SUFFIX,www.example.com,Proxy\n- IP-CIDR,10.0.0.0/8,Direct,no-resolve\n- MATCH,Final\n'
const base = { slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: [] as string[], content }
const PAGE = '/admin/rules/custom'
const inspection = { groups: [], builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [] }

function reads(row: Record<string, unknown> = base) {
  installReads({ '/admin/rules': list([row]), '/admin/templates': list([]), '/admin/groups': list([]) })
  api.post.mockResolvedValue({ data: inspection })
}
afterEach(() => { vi.unstubAllGlobals() })

const openTester = async () => {
  fireEvent.click(await screen.findByRole('button', { name: 'Test routing' }))
  return await screen.findByRole('dialog')
}
const type = (dialog: HTMLElement, address: string) => fireEvent.change(within(dialog).getByLabelText('Address'), { target: { value: address } })

describe('the routing test', () => {
  it('names the first rule that matches, its group and target, and the later rules that would have matched', async () => {
    reads({ ...base, content: content.replace('- GEOSITE,cn,CN\n', '') })
    mountRuleSets(PAGE)
    const dialog = await openTester()
    expect(within(dialog).getByText(/Enter a domain or IP to see which rule it matches/)).toBeTruthy()
    type(dialog, 'www.example.com')
    expect(await within(dialog).findByText('Matches rule DOMAIN-SUFFIX,example.com,Proxy in group 1 (Proxy), line 1.')).toBeTruthy()
    expect(within(dialog).getByText('Traffic goes to proxy group Proxy.')).toBeTruthy()
    // The DOMAIN, the keyword, the narrower suffix and the fallback all match too and never apply.
    expect(within(dialog).getByText('4 more rules also match but come later, so they don\'t apply:')).toBeTruthy()
    expect(within(dialog).queryByText('This result is not certain')).toBeNull()
  })

  it('says a built-in outbound in words', async () => {
    reads({ ...base, content: '- DOMAIN-SUFFIX,a.example,REJECT\n- DOMAIN-SUFFIX,b.example,DIRECT\n' })
    mountRuleSets(PAGE)
    const dialog = await openTester()
    type(dialog, 'x.a.example')
    expect(await within(dialog).findByText('Traffic is rejected.')).toBeTruthy()
    type(dialog, 'b.example')
    expect(await within(dialog).findByText('Traffic is sent directly.')).toBeTruthy()
  })

  it('does not present an answer as certain when a rule above it cannot be decided here', async () => {
    reads()
    mountRuleSets(PAGE)
    const dialog = await openTester()
    type(dialog, 'www.example.com')
    expect(await within(dialog).findByText('This result is not certain')).toBeTruthy()
    expect(within(dialog).getByText('Line 1 GEOSITE,cn,CN needs data from the client and can\'t be checked here.')).toBeTruthy()
    expect(within(dialog).getByText('If it matches → CN')).toBeTruthy()
    expect(within(dialog).getByText(/If none of them match/)).toBeTruthy()
  })

  it('a domain against an IP rule without no-resolve asks for the resolved address, and a resolved address settles it', async () => {
    reads({ ...base, content: '- IP-CIDR,10.0.0.0/8,Direct\n- MATCH,Final\n' })
    mountRuleSets(PAGE)
    const dialog = await openTester()
    type(dialog, 'host.example')
    expect(await within(dialog).findByText(/Mihomo resolves the domain here/)).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'More conditions' }))
    fireEvent.change(within(dialog).getByLabelText('Resolved IP'), { target: { value: '10.1.2.3' } })
    expect(await within(dialog).findByText('Traffic goes to proxy group Direct.')).toBeTruthy()
    expect(within(dialog).queryByText('This result is not certain')).toBeNull()
  })

  it('refuses what is not an address, naming the problem', async () => {
    reads()
    mountRuleSets(PAGE)
    const dialog = await openTester()
    type(dialog, 'https://example.com/x')
    expect(await within(dialog).findByText(/Enter a domain or an IP address, without a URL/)).toBeTruthy()
  })

  it('does not test a document it cannot read as a list', async () => {
    reads({ ...base, content: '- DOMAIN,a.example,P\n  ,DIRECT\n' })
    mountRuleSets(PAGE)
    const dialog = await openTester()
    type(dialog, 'a.example')
    expect(await within(dialog).findByText(/routing can't be tested until they are fixed/)).toBeTruthy()
  })

  it('opens the matching line in its group, and the page keeps what was typed when the test is closed and opened again', async () => {
    reads({ ...base, content: content.replace('- GEOSITE,cn,CN\n', '') })
    mountRuleSets(PAGE)
    let dialog = await openTester()
    type(dialog, 'www.example.com')
    fireEvent.click(await within(dialog).findByRole('button', { name: 'Go to line 1' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await screen.findByRole('heading', { name: /1 · Proxy/ })).toBeTruthy()
    dialog = await openTester()
    expect((within(dialog).getByLabelText('Address') as HTMLInputElement).value).toBe('www.example.com')
  })

  it('is its own screen on a phone, with the way back', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({ matches: /max-width/.test(query), media: query, addEventListener: () => {}, removeEventListener: () => {} }))
    reads({ ...base, content: content.replace('- GEOSITE,cn,CN\n', '') })
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('button', { name: 'Test routing' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    fireEvent.change(await screen.findByLabelText('Address'), { target: { value: 'www.example.com' } })
    expect(await screen.findByText('Traffic goes to proxy group Proxy.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Back to the rules' }))
    expect(await screen.findByRole('button', { name: 'Test routing' })).toBeTruthy()
  })
})

describe('the problems panel', () => {
  it('lists the rules an earlier rule covers in three groups, names the rule that wins, and opens the line', async () => {
    reads({ ...base, content: content.replace('- GEOSITE,cn,CN\n', '') + '- DOMAIN,after.example,X\n' })
    mountRuleSets(PAGE)
    // The analysis runs when the page is idle, a moment after the text is in.
    const button = await screen.findByRole('button', { name: /^\d+ findings$/ }, { timeout: 3000 })
    fireEvent.click(button)
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Never applies: matched first by an earlier rule with a different target')).toBeTruthy()
    expect(within(dialog).getByText('Line 1 matches first (Proxy); this line never applies')).toBeTruthy()
    // Folded groups open on click; the harmless ones are named as such.
    fireEvent.click(within(dialog).getByRole('button', { name: /Redundant/ }))
    expect(await within(dialog).findByText('Line 1 matches first with the same target; this line is redundant')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: /Never matches/ }))
    expect(await within(dialog).findByText('Comes after the fallback rule (line 7) and never matches')).toBeTruthy()
    // Nothing in it edits the rules.
    expect(within(dialog).queryByRole('button', { name: /remove|delete/i })).toBeNull()
    fireEvent.click(within(dialog).getAllByRole('button', { name: /^Go to line/ })[0])
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await screen.findByRole('heading', { name: /2 · Block/ })).toBeTruthy()
  })

  it('says so when there is nothing to report', async () => {
    reads({ ...base, content: '- DOMAIN,a.example,P\n- DOMAIN,b.example,P\n' })
    mountRuleSets(PAGE)
    await screen.findByRole('row', { name: /P/ })
    fireEvent.click(screen.getByRole('button', { name: 'Issues and findings' }))
    expect(await screen.findByText('No issues found', undefined, { timeout: 3000 })).toBeTruthy()
  })
})
