// @vitest-environment jsdom
import { configure, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { api, installReads, list, mountRuleSets } from './testHarness'

// The routing test's server mode (design 4.2 B4): the whole subscription of a Mihomo template, with the draft in place of the
// stored rule set. Asked for by the Test button or Enter; never by typing.
configure({ asyncUtilTimeout: 4000 })

const content = '- DOMAIN-SUFFIX,example.com,Proxy\n- DOMAIN,www.example.com,Block\n- MATCH,Final\n'
const base = { slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: [] as string[], content, revision: 'r1' }
const PAGE = '/admin/rules/custom'
const inspection = { groups: [], builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [] }
const mihomoA = { slug: 'mihomo-main', name: 'Main Mihomo', client_type: 'mihomo', is_default: true, rule_sets: ['custom'], content: '' }
const mihomoB = { slug: 'mihomo-alt', name: 'Alt Mihomo', client_type: 'mihomo', is_default: false, rule_sets: ['custom', 'other'], content: '' }
const singbox = { slug: 'sb-main', name: 'Main sing-box', client_type: 'sing-box', is_default: false, rule_sets: ['custom'], content: '' }
const unbound = { slug: 'mihomo-unbound', name: 'Unbound Mihomo', client_type: 'mihomo', is_default: false, rule_sets: ['other'], content: '' }
const alice = { id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 1 }

const own = (over: Record<string, unknown> = {}) => ({ source: 'rule_set', rule_set: 'custom', line: 2, rule: 'DOMAIN,www.example.com,Block', target: 'Block', ...over })
function answer(over: Record<string, unknown> = {}) {
  return {
    client_type: 'mihomo', template_slug: 'mihomo-main', group_id: 1, personal_rules: 'no_user', draft_applied: true, total_rules: 3, default_outbound: 'DIRECT',
    result: { state: 'matched', certain: true, ...own() },
    branches: [], branches_total: 0, also_matching: [], also_matching_total: 0,
    ...over,
  }
}

function reads(templates: unknown[] = [mihomoA, mihomoB, singbox, unbound]) {
  installReads({
    '/admin/rules': list([base]), '/admin/templates': list(templates), '/admin/groups': list([]),
    '/admin/users': list([alice]), '/admin/users/7': alice,
  })
}
/** The explain endpoint answers with `explain` (a value, or a function of the body); the groups inspection answers as it does for the other tester tests. */
function server(explain: unknown | ((body: unknown) => unknown) = answer()) {
  api.post.mockImplementation(async (url: string, body: unknown) => {
    if (url === '/admin/rules/explain') {
      const out = typeof explain === 'function' ? (explain as (b: unknown) => unknown)(body) : explain
      if (out instanceof Error) throw out
      return { data: out }
    }
    return { data: inspection }
  })
}
const explains = () => api.post.mock.calls.filter(([u]) => u === '/admin/rules/explain')
const failure = (status: number, data: Record<string, unknown> = {}) => Object.assign(new Error('failed'), { response: { status, data } })

afterEach(() => { vi.unstubAllGlobals(); useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true }) })

const openTester = async () => {
  fireEvent.click(await screen.findByRole('button', { name: 'Test routing' }))
  return await screen.findByRole('dialog')
}
const type = (dialog: HTMLElement, address: string) => fireEvent.change(within(dialog).getByLabelText('Address'), { target: { value: address } })
const byTemplate = (dialog: HTMLElement) => fireEvent.click(within(dialog).getByRole('radio', { name: 'By template' }))
const test = (dialog: HTMLElement) => fireEvent.click(within(dialog).getByRole('button', { name: 'Test' }))
/** Opens the tester in server mode with a connection typed, and runs it. */
async function run(address = 'www.example.com') {
  const dialog = await openTester()
  byTemplate(dialog)
  type(dialog, address)
  test(dialog)
  return dialog
}

describe('the scope of the routing test', () => {
  it('starts on this draft alone, in the browser, and offers the Mihomo templates this rule set is in, the first chosen', async () => {
    reads(); server()
    mountRuleSets(PAGE)
    const dialog = await openTester()
    expect(within(dialog).getByRole('radio', { name: 'This rule set only' }).getAttribute('aria-checked')).toBe('true')
    expect(within(dialog).queryByRole('button', { name: 'Test' })).toBeNull()
    // Typing answers locally and asks the server nothing.
    type(dialog, 'www.example.com')
    expect(await within(dialog).findByText(/Matches rule DOMAIN-SUFFIX,example.com,Proxy/)).toBeTruthy()
    expect(within(dialog).getByText('Answer from this rule set\'s draft only, worked out in your browser.')).toBeTruthy()
    byTemplate(dialog)
    const select = within(dialog).getByLabelText('Template') as HTMLSelectElement
    expect([...select.options].map(o => o.textContent)).toEqual(['Main Mihomo', 'Alt Mihomo'])
    expect(select.value).toBe('mihomo-main')
    expect(within(dialog).getByText(/sing-box templates aren't listed/)).toBeTruthy()
    expect(explains()).toHaveLength(0)
  })

  it('in both modes the address field says the client is assumed to have connected by name', async () => {
    reads(); server()
    mountRuleSets(PAGE)
    const dialog = await openTester()
    expect(within(dialog).getByText(/Assumes the client connected by name; if it connected to an IP address, enter it\./)).toBeTruthy()
    byTemplate(dialog)
    expect(within(dialog).getByText(/Assumes the client connected by name;/)).toBeTruthy()
  })

  it('in the browser, a no-resolve IP rule above the match does not make the answer uncertain when no address was entered', async () => {
    installReads({ '/admin/rules': list([{ ...base, content: '- IP-CIDR,10.0.0.0/8,Direct,no-resolve\n- MATCH,Final\n' }]), '/admin/templates': list([]), '/admin/groups': list([]) })
    server()
    mountRuleSets(PAGE)
    const dialog = await openTester()
    type(dialog, 'host.example')
    expect(await within(dialog).findByText('Traffic goes to proxy group Final.')).toBeTruthy()
    expect(within(dialog).queryByText('This result is not certain')).toBeNull()
  })

  it('says why there is nothing to test against when the rule set is in no Mihomo template, and when it is only in sing-box ones', async () => {
    reads([unbound]); server()
    const first = mountRuleSets(PAGE)
    let dialog = await openTester()
    expect(within(dialog).getByText(/isn't in a Mihomo template/)).toBeTruthy()
    expect(within(dialog).queryByRole('radio', { name: 'By template' })).toBeNull()
    first.unmount()
    reads([singbox, unbound]); server()
    mountRuleSets(PAGE)
    dialog = await openTester()
    expect(within(dialog).getByText(/only in sing-box templates/)).toBeTruthy()
  })

  it('is not offered to an operator, who is told why in one line', async () => {
    useAuthStore.setState({ role: 'operator', userId: 1, hasToken: true })
    reads(); server()
    mountRuleSets(PAGE)
    const dialog = await openTester()
    expect(within(dialog).getByText('Testing against a template needs an administrator.')).toBeTruthy()
    expect(within(dialog).queryByRole('radio', { name: 'By template' })).toBeNull()
    // The browser test still works.
    type(dialog, 'www.example.com')
    expect(await within(dialog).findByText(/Matches rule DOMAIN-SUFFIX,example.com,Proxy/)).toBeTruthy()
    expect(explains()).toHaveLength(0)
  })
})

describe('asking the server', () => {
  it('sends nothing while the form is filled in; the Test button sends the template, the whole draft and the connection', async () => {
    reads(); server()
    mountRuleSets(PAGE)
    const dialog = await openTester()
    byTemplate(dialog)
    type(dialog, 'Claude.AI')
    fireEvent.click(within(dialog).getByRole('button', { name: 'More conditions' }))
    fireEvent.change(within(dialog).getByLabelText('Resolved IP'), { target: { value: '1.2.3.4' } })
    fireEvent.change(within(dialog).getByLabelText('Process name'), { target: { value: 'curl' } })
    fireEvent.click(within(dialog).getByRole('radio', { name: 'UDP' }))
    // Long enough for any debounced request to have gone out.
    await new Promise(r => setTimeout(r, 1300))
    expect(explains()).toHaveLength(0)
    test(dialog)
    await within(dialog).findByText(/Answer from the server/)
    expect(explains()).toHaveLength(1)
    const [url, body, config] = explains()[0]
    expect(url).toBe('/admin/rules/explain')
    expect(body).toEqual({
      template_slug: 'mihomo-main',
      // What the page holds as the draft: the same body a save sends, less the read-only revision.
      draft: { slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: [], proxy_group_members: {}, proxy_group_options: {}, mihomo_sub_rules: [], mihomo_rematch_outbounds: [], content },
      probe: { host: 'claude.ai', network: 'udp', port: 443, ip: '1.2.3.4', process: 'curl' },
    })
    // The caller words every failure itself.
    expect(config).toMatchObject({ _skipErrorToast: true })
  })

  it('sends the edited draft, the chosen template and the chosen user', async () => {
    reads(); server()
    mountRuleSets(PAGE)
    // Edit the draft first (Basics tab: the name), then test.
    fireEvent.click(await screen.findByRole('tab', { name: 'General' }))
    fireEvent.change(await screen.findByLabelText(/^Name/), { target: { value: 'Renamed' } })
    fireEvent.click(await screen.findByRole('tab', { name: 'Rules' }))
    const dialog = await openTester()
    byTemplate(dialog)
    fireEvent.change(within(dialog).getByLabelText('Template'), { target: { value: 'mihomo-alt' } })
    const picker = within(dialog).getByRole('combobox', { name: "Include a user's personal rules" })
    fireEvent.mouseDown(picker)
    fireEvent.click(await screen.findByRole('option', { name: 'Alice (alice)' }))
    type(dialog, 'example.com')
    test(dialog)
    await within(dialog).findByText(/Answer from the server/)
    expect(explains()[0][1]).toMatchObject({ template_slug: 'mihomo-alt', user_id: 7, draft: { slug: 'custom', name: 'Renamed' } })
  })

  it('Enter in a field asks, once, and never saves the page', async () => {
    reads(); server()
    mountRuleSets(PAGE)
    const dialog = await openTester()
    byTemplate(dialog)
    type(dialog, 'www.example.com')
    fireEvent.keyDown(within(dialog).getByLabelText('Address'), { key: 'Enter' })
    await within(dialog).findByText(/Answer from the server/)
    expect(explains()).toHaveLength(1)
    expect(api.put).not.toHaveBeenCalled()
  })

  it('Enter that commits an input-method candidate does not ask', async () => {
    reads(); server()
    mountRuleSets(PAGE)
    const dialog = await openTester()
    byTemplate(dialog)
    type(dialog, 'www.example.com')
    fireEvent.keyDown(within(dialog).getByLabelText('Address'), { key: 'Enter', isComposing: true })
    fireEvent.keyDown(within(dialog).getByLabelText('Address'), { key: 'Enter', keyCode: 229 })
    await new Promise(r => setTimeout(r, 50))
    expect(explains()).toHaveLength(0)
    expect(api.put).not.toHaveBeenCalled()
  })

  it('does not send a second request while one is running', async () => {
    reads()
    let release: (v: unknown) => void = () => {}
    api.post.mockImplementation(async (url: string) => {
      if (url !== '/admin/rules/explain') return { data: inspection }
      return await new Promise(r => { release = () => r({ data: answer() }) })
    })
    mountRuleSets(PAGE)
    const dialog = await openTester()
    byTemplate(dialog)
    type(dialog, 'www.example.com')
    test(dialog)
    fireEvent.keyDown(within(dialog).getByLabelText('Address'), { key: 'Enter' })
    expect((within(dialog).getByRole('button', { name: 'Testing' }) as HTMLButtonElement).disabled).toBe(true)
    expect(explains()).toHaveLength(1)
    release(null)
    await within(dialog).findByText(/Answer from the server/)
  })

  it('refuses an empty or malformed connection before sending, next to the field', async () => {
    reads(); server()
    mountRuleSets(PAGE)
    const dialog = await openTester()
    byTemplate(dialog)
    test(dialog)
    expect(await within(dialog).findByText('Enter a domain or an IP address')).toBeTruthy()
    type(dialog, 'https://example.com/x')
    test(dialog)
    expect(await within(dialog).findByText(/without a URL, port or spaces/)).toBeTruthy()
    expect(explains()).toHaveLength(0)
  })
})

describe('the answer', () => {
  it('names the first match, where it is and the group it goes to, and opens a line of this rule set in the editor', async () => {
    reads()
    server(answer({ result: { state: 'matched', certain: true, ...own({ line: 1, rule: 'DOMAIN-SUFFIX,example.com,Proxy', target: 'Proxy' }), group_type: 'select', group_default: 'Fast' } }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText('Answer from the server, using template Main Mihomo and your draft.')).toBeTruthy()
    expect(within(dialog).getByText('Rule set Custom rules')).toBeTruthy()
    expect(within(dialog).getByText('DOMAIN-SUFFIX,example.com,Proxy')).toBeTruthy()
    expect(within(dialog).getByText('Traffic goes to proxy group Proxy. Its default choice is Fast; subscribers can change it in their client.')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Go to line 1' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await screen.findByRole('heading', { name: /1 · Proxy/ })).toBeTruthy()
  })

  it.each([
    ['personal', { source: 'personal', line: 3 }, 'Personal rules'],
    ['subscription domain', { source: 'subscription_domain', rule_set: undefined, line: 1 }, 'Direct subscription domain rule'],
    ['another rule set', { source: 'rule_set', rule_set: 'other', line: 9 }, 'Rule set other'],
    ['the template', { source: 'template', rule_set: undefined, line: 12 }, 'The template'],
  ])('names a match in %s and does not offer to open it in this editor', async (_name, ref, label) => {
    reads()
    server(answer({ result: { state: 'matched', certain: true, ...own(ref) } }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText(label)).toBeTruthy()
    expect(within(dialog).queryByRole('button', { name: /^Go to line/ })).toBeNull()
  })

  it('says when matching may go on past the match, and why', async () => {
    reads()
    server(answer({ result: { state: 'continues', certain: true, continues_via: 'pass', ...own({ target: 'Proxy' }) } }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText('This rule may not be the last word')).toBeTruthy()
    expect(within(dialog).getByText(/Proxy can send matching on to the rules below/)).toBeTruthy()
  })

  it('lists the lines that could not be decided with the reason, the match below them and the rules that also match', async () => {
    reads()
    server(answer({
      result: { state: 'undecided', certain: false, ...own({ line: 3, rule: 'MATCH,Final', target: 'Final' }) },
      branches: [
        { source: 'rule_set', rule_set: 'custom', line: 1, rule: 'GEOSITE,cn,CN', target: 'CN', reason: 'client_data' },
        { source: 'personal', line: 1, rule: 'SRC-PORT,5000,Direct', target: 'Direct', reason: 'input' },
      ], branches_total: 2,
      also_matching: [{ source: 'template', line: 40, rule: 'MATCH,Last', target: 'Last', status: 'possible' }], also_matching_total: 1,
    }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText('This result is not certain')).toBeTruthy()
    expect(within(dialog).getByText(/Needs data from the client/)).toBeTruthy()
    expect(within(dialog).getByText(/Needs a condition that wasn't entered/)).toBeTruthy()
    expect(within(dialog).getByText('If it matches → CN')).toBeTruthy()
    expect(within(dialog).getByText('If none of them match, matching continues:')).toBeTruthy()
    expect(within(dialog).getByText('1 more rules also match and may apply:')).toBeTruthy()
    expect(within(dialog).getByText('MATCH,Last')).toBeTruthy()
  })

  it('says that nothing matches, and which rules below are out of reach', async () => {
    reads()
    server(answer({ result: { state: 'no_match', certain: true }, also_matching: [], also_matching_total: 0 }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText(/No rule matches/)).toBeTruthy()
    server(answer({ also_matching: [{ source: 'template', line: 40, rule: 'MATCH,Last', target: 'Last', status: 'unreachable' }], also_matching_total: 1 }))
    test(dialog)
    expect(await within(dialog).findByText('1 more rules also match but come later, so they don\'t apply:')).toBeTruthy()
  })

  it('reports how the answer was put together: the draft left out, unconfirmed lines and the state of the personal rules', async () => {
    reads()
    server(answer({ warnings: ['draft_not_in_template', 'draft_disabled', 'unconfirmed_lines'], personal_rules: 'not_in_template', user_id: 7 }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText(/isn't in the chosen template/)).toBeTruthy()
    expect(within(dialog).getByText(/The draft is turned off/)).toBeTruthy()
    expect(within(dialog).getByText(/lines on Passwall Node servers aren't confirmed yet/)).toBeTruthy()
    expect(within(dialog).getByText(/has no place for personal rules/)).toBeTruthy()
    server(answer({ personal_rules: 'included', user_id: 7 }))
    test(dialog)
    expect(await within(dialog).findByText('This user\'s personal rules are included.')).toBeTruthy()
  })

  it('marks an answer out of date once the connection or the draft changes', async () => {
    reads(); server()
    mountRuleSets(PAGE)
    const dialog = await run()
    await within(dialog).findByText(/Answer from the server/)
    expect(within(dialog).queryByText(/have changed since this answer/)).toBeNull()
    type(dialog, 'example.org')
    expect(within(dialog).getByText(/have changed since this answer/)).toBeTruthy()
    expect(explains()).toHaveLength(1)
  })

  it('does not offer to open a line from an answer that is out of date', async () => {
    reads()
    server(answer({ result: { state: 'matched', certain: true, ...own({ line: 1, rule: 'DOMAIN-SUFFIX,example.com,Proxy', target: 'Proxy' }) } }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByRole('button', { name: 'Go to line 1' })).toBeTruthy()
    type(dialog, 'example.org')
    expect(within(dialog).queryByRole('button', { name: 'Go to line 1' })).toBeNull()
  })

  it('names the mode that produced the answer when it goes back to this draft alone', async () => {
    reads(); server()
    mountRuleSets(PAGE)
    const dialog = await run()
    await within(dialog).findByText(/Answer from the server/)
    fireEvent.click(within(dialog).getByRole('radio', { name: 'This rule set only' }))
    expect(await within(dialog).findByText('Answer from this rule set\'s draft only, worked out in your browser.')).toBeTruthy()
    expect(within(dialog).queryByText(/Answer from the server/)).toBeNull()
  })
})

describe('when the server refuses', () => {
  it.each([
    ['explain_port_invalid', /The port must be a number/],
    ['explain_resolved_invalid', /The resolved IP isn't a valid address/],
    ['explain_address_invalid', /without a URL, port or spaces/],
  ])('a 400 %s is shown by the field it concerns', async (code, text) => {
    reads(); server(failure(400, { code, error: 'x', params: { field: 'x' } }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText(text)).toBeTruthy()
    expect(within(dialog).queryByText(/Answer from the server/)).toBeNull()
  })

  it('a 404 says the template or the user is gone, under the template', async () => {
    reads(); server(failure(404, { error: 'Not found' }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText(/The template or the user was not found/)).toBeTruthy()
  })

  it('a 422 says the template cannot be tested', async () => {
    reads(); server(failure(422, { code: 'explain_unsupported_client', error: 'x' }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText(/isn't a Mihomo template/)).toBeTruthy()
  })

  it('a 413 says the draft is too large, with the limit', async () => {
    reads(); server(failure(413, { limit_bytes: 8 << 20 }))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText(/8\.00 MB/)).toBeTruthy()
  })

  it('anything else says the test could not be run, and a second try is possible', async () => {
    reads(); server(failure(500, {}))
    mountRuleSets(PAGE)
    const dialog = await run()
    expect(await within(dialog).findByText('The test could not be run. Try again.')).toBeTruthy()
    server(answer())
    test(dialog)
    expect(await within(dialog).findByText(/Answer from the server/)).toBeTruthy()
    expect(within(dialog).queryByText('The test could not be run. Try again.')).toBeNull()
  })
})
