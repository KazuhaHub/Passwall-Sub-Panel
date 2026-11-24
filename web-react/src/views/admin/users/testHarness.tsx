import { type ReactElement } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, configure, render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider, useLocation } from 'react-router'
import { afterEach, beforeEach, vi } from 'vitest'
import ConfirmHost from '@/components/ConfirmHost'
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import { useAuthStore } from '@/stores/auth'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import UserCreatePage from './UserCreatePage'
import UserDetailRoute from './UserDetailRoute'

// Shared set-up of the users page tests: the request client is mocked, the REAL English strings are loaded
// (common from initI18n, admin added here), and the page runs in a data router like in the app. Not part of the app.
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const toast = vi.hoisted(() => vi.fn())
vi.mock('@/components/feedback/toasts', () => ({ pushSnack: toast, pushToast: toast }))

// A loaded CI runner can take longer than the default one second to settle a sheet or a confirmation.
configure({ asyncUtilTimeout: 5000 })
vi.setConfig({ testTimeout: 15000 })

i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)

export { api, toast }
export const list = (items: unknown[], total = items.length) => ({ items, total, page: 1, page_size: 25 })

export const group = { id: 1, slug: 'test', name: 'group', tag_filter: { all: true, tags: [], mode: 'all' }, members: 0, remark: '', require_2fa: false }
/** A regular account in good standing, with nothing limited. */
export const user = {
  id: 2, upn: 'user@example.test', display_name: 'old-name', email: 'user@example.test', group_id: 1, role: 'user', enabled: true,
  sso_provider: 'local', uuid: 'uuid-2', sub_url: 'https://sub.example.test/sub/token-2', created_at: '2026-01-01T00:00:00Z',
  traffic_limit_bytes: 0, traffic_reset_period: 'monthly', ip_limit: 0, device_limit: 0, emergency_used_count: 0,
}
export const account = (id: number, name: string, extra: Record<string, unknown> = {}) =>
  ({ ...user, id, upn: `${name}@example.test`, email: `${name}@example.test`, display_name: name, ...extra })

const syncStatus = (id: number) => ({
  target_type: 'user', target_id: id, target_exists: true, observed_at: '2026-01-01T00:00:00Z', covered_task_types: [], state: 'no_active_tasks',
  active_tasks: [], active_tasks_truncated: false, recent_terminal_tasks: [], history_truncated: false, history_scope: 'retained_only',
})

/** What the page reads when it mounts; a test overrides what it cares about. */
type Row = { id: number; [k: string]: unknown }
export function defaults(users: Row[]): Record<string, unknown> {
  const reads: Record<string, unknown> = {
    '/admin/users': list(users),
    '/admin/groups': list([group]),
    '/admin/servers': list([]),
    '/admin/traffic/top': { items: [] },
    '/admin/risk-center/levels': {},
    '/admin/auth-events': list([]),
  }
  for (const u of users) {
    reads[`/admin/users/${u.id}`] = u
    reads[`/admin/users/${u.id}/sync-status`] = syncStatus(u.id)
    reads[`/admin/users/${u.id}/limit-enforcement`] = { ip_limit: 0, device_limit: 0, device_limit_enforced_anywhere: false, panels: [] }
    reads[`/admin/traffic/user/${u.id}/servers`] = { items: [] }
    reads[`/admin/traffic/user/${u.id}/nodes`] = { items: [] }
  }
  return reads
}

/** Answers each URL from `values`; an Error value rejects. A URL nobody listed fails the test loudly. */
export function installReads(values: Record<string, unknown>) {
  api.get.mockImplementation(async (url: string) => {
    if (!(url in values)) throw new Error(`Unexpected GET ${url}`)
    const v = values[url]
    if (v instanceof Error) throw v
    return { data: v }
  })
}

/** Reads for a page of `users`, with `overrides` on top. */
export function serve(users: Row[], overrides: Record<string, unknown> = {}) {
  installReads({ ...defaults(users), ...overrides })
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  api.put.mockResolvedValue({ data: {} })
  api.post.mockResolvedValue({ data: {} })
  api.delete.mockResolvedValue({ data: {} })
})
afterEach(cleanup)

/** The address bar, for assertions. */
function Where() {
  const loc = useLocation()
  return <p data-testid="where">{loc.pathname + loc.search}</p>
}

/**
 * The list (the `page` given), a user's page and the create page, at the addresses the app gives them, so a click
 * from the list lands where it does in the app. `initialEntries` lets a test start from a history.
 */
export function mountPage(page: ReactElement, entry: string | string[] = '/admin/users') {
  const entries = Array.isArray(entry) ? entry : [entry]
  const router = createMemoryRouter([
    { path: '/admin/users', element: <>{page}<Where /></> },
    { path: '/admin/users/new', element: <><UserCreatePage /><Where /></> },
    { path: '/admin/users/:id', element: <><UserDetailRoute /><Where /></> },
    { path: '/admin/traffic', element: <p>traffic</p> },
  ], { initialEntries: entries, initialIndex: entries.length - 1 })
  const client = makeTestQueryClient()
  const view = render(<QueryClientProvider client={client}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
  return { router, client, ...view }
}

export const where = () => document.querySelector('[data-testid="where"]')?.textContent ?? ''
export const searchOf = () => new URLSearchParams(where().split('?')[1] ?? '')

/** The params of the last GET to `url`. */
export function lastParams(url: string): Record<string, unknown> | undefined {
  return [...api.get.mock.calls].reverse().find(([u]) => u === url)?.[1]?.params
}
