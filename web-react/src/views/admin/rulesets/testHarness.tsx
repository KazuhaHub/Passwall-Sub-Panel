// The mocked request client has to be in place before anything that imports the real one.
export { api, installReads, list, toast } from '../subscription/testHarness'
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
