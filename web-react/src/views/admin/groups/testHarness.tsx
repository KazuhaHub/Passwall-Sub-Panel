// Shared set-up of the groups page tests (not part of the app): the subscription pages' harness (request client mocked,
// real English strings, toasts recorded, a data router, the confirm host) plus the fixtures and reads every test here needs.
// The harness first: its vi.mock calls must be registered before anything loads the real request client.
import type { ReactElement } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import ConfirmHost from '@/components/ConfirmHost'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { api, installReads, list, mountPage, toast } from '../subscription/testHarness'
import { SCOPE_KEYS } from '@/components/scope/scopeOverrides'

export { api, installReads, list, mountPage, toast }

/** mountPage with the places this page links to (Users, the Nodes page, access control) as routes, so a click on a link can be followed. */
export function mountWithLinks(page: ReactElement, entry: string) {
  const router = createMemoryRouter([
    { path: PATH, element: page },
    { path: '/admin/users', element: <div>users page</div> },
    { path: '/admin/nodes', element: <div>nodes page</div> },
    { path: '/admin/risk', element: <div>risk page</div> },
    { path: '/admin/access-control', element: <div>access page</div> },
  ], { initialEntries: [entry] })
  const client = makeTestQueryClient()
  const view = render(<QueryClientProvider client={client}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
  return { router, client, ...view }
}


export const PATH = '/admin/groups'

/** A group with a mixed filter (the legacy `server:` spelling included), limits in all three states and a saved layout. */
export const team = {
  id: 5, slug: 'team', name: 'Team',
  tag_filter: { all: false, mode: 'any', tags: ['region:HK', 'tag:Premium', 'vendor:gcp', 'server:1.1.1.1'] },
  layout: { separators: [{ position: 0, name: 'Premium' }], sort: [{ node_id: 1, weight: 5 }], default_sort_strategy: 'by_region_then_id' },
  remark: 'Core', require_2fa: false,
  traffic_limit_gb: 100, ip_limit: 0, device_limit: null,
  members: 3, dest_state: 'off',
}
export const empty = { ...team, id: 6, slug: 'spare', name: 'Spare', tag_filter: { all: true, tags: [], mode: 'all' }, members: 0, remark: '', traffic_limit_gb: null, ip_limit: null }

export const everyKey = SCOPE_KEYS.map(k => k.key)
export const scopeReply = (overrides: Record<string, string> = {}, overridable: string[] = everyKey) => ({ scope_type: 'group', scope_id: 0, overridable, overrides })
export const nodeRow = { id: 1, display_name: 'HK 01', region: 'HK', tags: ['Premium'], server_address: 'hk.example.test' }

export const previewOf = (over: Record<string, unknown> = {}) => ({
  data: { nodes: [{ id: 1, name: 'HK-1', region: 'HK', visible_lines: 1, total_lines: 2 }], excluded: [], added: [], removed: [], members: 0, ...over },
})

/** Reads for a page of these groups; `extra` adds or replaces any other URL. */
export function reads(groups: unknown[] = [team, empty], extra: Record<string, unknown> = {}) {
  installReads({
    '/admin/groups': list(groups),
    '/admin/nodes': list([nodeRow]),
    '/admin/groups/5/scope-settings': scopeReply({ 'notify.expire_before_days': '7' }),
    '/admin/groups/6/scope-settings': scopeReply(),
    '/admin/settings/ui': { expire_before_days: 3, sub_region_flag_prefix: true },
    ...extra,
  })
  api.post.mockImplementation(async () => previewOf())
  api.put.mockImplementation(async (url: string) => ({ data: url === '/admin/groups/5' ? { group: team } : {} }))
}
