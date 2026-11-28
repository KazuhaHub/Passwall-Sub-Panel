// The mocked request client has to be in place before anything that imports the real one.
export { api, list, toast } from '../subscription/testHarness'
import { api, installReads as installRawReads, list } from '../subscription/testHarness'
import { QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider, useLocation } from 'react-router'
import ConfirmHost from '@/components/ConfirmHost'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import RuleSetsView from '../RuleSetsView'
import RuleSetRoute, { RuleSetCreateRoute } from './RuleSetRoute'

// Shared by the rule-set page tests (not part of the app): the list, a rule set's page and the create page at the
// addresses the app gives them, on the same mocked request client as `subscription/testHarness`.
function Where() {
  const loc = useLocation()
  return <p data-testid="where">{loc.pathname + loc.search}</p>
}

export function mountRuleSets(entry: string | string[] = '/admin/rules') {
  const entries = Array.isArray(entry) ? entry : [entry]
  const router = createMemoryRouter([
    { path: '/admin/rules', element: <><RuleSetsView /><Where /></> },
    { path: '/admin/rules/new', element: <><RuleSetCreateRoute /><Where /></> },
    { path: '/admin/rules/:slug', element: <><RuleSetRoute /><Where /></> },
  ], { initialEntries: entries, initialIndex: entries.length - 1 })
  const client = makeTestQueryClient()
  const view = render(<QueryClientProvider client={client}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
  return { router, client, ...view }
}

export const where = () => document.querySelector('[data-testid="where"]')?.textContent ?? ''

type Row = { slug: string; content?: string; enabled?: boolean; [k: string]: unknown }

/** What the server derives for a list row, as far as these tests look: the line count and the size of the text. */
export const summaryOf = (row: Row) => {
  const { content = '', mihomo_sub_rules: _s, mihomo_rematch_outbounds: _r, proxy_group_members: _m, proxy_group_options: _o, ...rest } = row
  return { ...rest, line_count: content.split('\n').filter(l => l.trim()).length, size_bytes: content.length, targets: [] as string[] }
}

/**
 * `installReads` for the rule-set pages: give `/admin/rules` the whole rows, and the list is served as summaries (no text, as
 * the server does) while every row is also served whole at `/admin/rules/<slug>`, which is where the editor reads its document.
 * A row that is an Error fails both.
 */
export function installReads(values: Record<string, unknown>) {
  const rows = values['/admin/rules']
  if (rows instanceof Error || rows === undefined) { installRawReads(values); return }
  const items = (rows as { items: Row[] }).items
  const detail = Object.fromEntries(items.map(r => [`/admin/rules/${r.slug}`, r]))
  installRawReads({ ...detail, ...values, '/admin/rules': list(items.map(summaryOf)) })
  // A slug the server does not know is a 404, not a failure of the test double.
  const known = api.get.getMockImplementation()!
  api.get.mockImplementation(async (url: string, ...rest: unknown[]) => {
    if (/^\/admin\/rules\/[^/]+$/.test(url) && !(url in detail) && !(url in values)) throw Object.assign(new Error('Not found'), { response: { status: 404 } })
    return known(url, ...rest)
  })
}
