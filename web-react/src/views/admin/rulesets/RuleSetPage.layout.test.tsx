// @vitest-environment jsdom
import { configure, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// jsdom has no layout, so the width of the Rules tab's box is supplied here.
const box = vi.hoisted(() => ({ width: null as number | null }))
vi.mock('./useContainerWidth', () => ({ useContainerWidth: () => box.width }))

import { api, installReads, list, mountRuleSets } from './testHarness'

configure({ asyncUtilTimeout: 4000 })

const content = '- DOMAIN,a.example,Proxy\n- DOMAIN-SUFFIX,b.example,Block\n'
const base = { slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: [] as string[], content, revision: 'r1' }
const inspection = { groups: [], builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [] }
const PAGE = '/admin/rules/custom'
const STATS = '2 rules · 2 groups'

beforeEach(() => {
  box.width = null
  installReads({ '/admin/rules': list([base]), '/admin/templates': list([]), '/admin/groups': list([]) })
  api.post.mockResolvedValue({ data: inspection })
})
const toolbar = () => screen.findByRole('toolbar', { name: 'Rule tools' })
const openTester = async () => fireEvent.click(await screen.findByRole('button', { name: 'Test routing' }))

describe('where the routing test is drawn', () => {
  it('over the page, as a dialog that leaves the page usable, below the width a third column needs; Escape puts it away', async () => {
    box.width = 976
    mountRuleSets(PAGE)
    await openTester()
    const panel = await screen.findByRole('dialog', { name: 'Test routing' })
    expect(panel.getAttribute('aria-modal')).toBe('false')
    expect(screen.queryByRole('complementary', { name: 'Test routing' })).toBeNull()
    // The rules are still there to use while it is open.
    expect(screen.getByRole('row', { name: /Proxy/ })).toBeTruthy()
    fireEvent.keyDown(within(panel).getByLabelText('Address'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Test routing' })).toBeNull())
  })

  it('as a column of its own once the directory, the group and the test all keep their room', async () => {
    box.width = 1500
    mountRuleSets(PAGE)
    await openTester()
    expect(await screen.findByRole('complementary', { name: 'Test routing' })).toBeTruthy()
    expect(screen.queryByRole('dialog', { name: 'Test routing' })).toBeNull()
  })

  it('does not stop Ctrl+S from saving while the over-the-page panel is open (it is not the thing being worked on)', async () => {
    box.width = 976
    mountRuleSets(PAGE)
    fireEvent.click(await screen.findByRole('radio', { name: 'Text' }))
    fireEvent.change(await screen.findByRole('textbox', { name: 'Main rules' }), { target: { value: content + '- DOMAIN,c.example,Proxy\n' } })
    await openTester()
    await screen.findByRole('dialog', { name: 'Test routing' })
    api.put.mockResolvedValue({ data: { ...base, revision: 'r2' } })
    fireEvent.keyDown(window, { key: 's', ctrlKey: true })
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  })
})

describe('what the toolbar keeps in view', () => {
  it('says the counts itself when it has the room, and groups find, view and filter on the left', async () => {
    box.width = 1000
    mountRuleSets(PAGE)
    const bar = await toolbar()
    expect(within(bar).getByText(STATS)).toBeTruthy()
    expect(within(bar).getByRole('textbox', { name: /^Find/ })).toBeTruthy()
    for (const name of ['Undo', 'Redo', 'Issues and findings', 'Test routing']) expect(within(bar).getByRole('button', { name })).toBeTruthy()
    expect(within(bar).getByRole('radiogroup', { name: 'View' })).toBeTruthy()
  })

  it('leaves the counts to the directory under 960px and keeps every control', async () => {
    box.width = 800
    mountRuleSets(PAGE)
    const bar = await toolbar()
    expect(within(bar).queryByText(STATS)).toBeNull()
    expect(within(bar).getByRole('button', { name: 'Issues and findings' })).toBeTruthy()
    expect(await screen.findByText(STATS)).toBeTruthy()
  })

  it('under 760px moves Issues and the target filter into the More menu, which opens the filter row', async () => {
    box.width = 600
    mountRuleSets(PAGE)
    const bar = await toolbar()
    expect(within(bar).queryByRole('button', { name: 'Issues and findings' })).toBeNull()
    expect(screen.queryByLabelText('Filter by target')).toBeNull()
    fireEvent.click(within(bar).getByRole('button', { name: 'More' }))
    expect(await screen.findByRole('menuitem', { name: 'Issues and findings' })).toBeTruthy()
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Filter by target' }))
    expect(await screen.findByLabelText('Filter by target')).toBeTruthy()
  })

  it('is drawn in the strip under the tabs, and only while the Rules tab is the one shown', async () => {
    mountRuleSets(PAGE)
    const bar = await toolbar()
    expect(bar.closest('[data-slot="rules-strip"]')).toBeTruthy()
    fireEvent.click(await screen.findByRole('tab', { name: 'General' }))
    await waitFor(() => expect(screen.queryByRole('toolbar', { name: 'Rule tools' })).toBeNull())
    fireEvent.click(await screen.findByRole('tab', { name: 'Rules' }))
    expect(await toolbar()).toBeTruthy()
  })
})
