import { StrictMode, type ReactElement } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { afterEach, beforeEach, vi } from 'vitest'
import ConfirmHost from '@/components/ConfirmHost'
import { useAuthStore } from '@/stores/auth'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import ServerCreateRoute from './ServerCreateRoute'
import ServerInstallRoute from './ServerInstallRoute'
import ServerRoute from './ServerRoute'

// Shared set-up of the servers page tests (the page, its sheets and its dialogs). The request client is mocked,
// toasts are recorded as (message, severity), and a translation returns its own key prefixed with the namespace, so a
// test names a string by its key and a wording change does not break it. The page runs in a data router (the leave
// guard needs one) with the real confirm host mounted. Not part of the app.
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
const snack = vi.hoisted(() => vi.fn())

vi.mock('@/api/client', () => ({ client: api }))
// Both doors record (message, severity); a test reads `snack.mock.calls`.
vi.mock('@/components/feedback/toasts', () => ({
  pushSnack: (message: string, severity?: string) => snack(message, severity),
  pushToast: (input: { message: string; severity?: string }) => snack(input.message, input.severity),
}))
vi.mock('@/i18n', () => ({ default: { t: (key: string) => key, language: 'en-US' } }))
// `t` and `i18n` keep their identity between renders, like the real ones: a page that memoises its columns on `t` would
// otherwise rebuild them every render and remount the whole table.
vi.mock('react-i18next', () => {
  const cache = new Map<string, (key: string) => string>()
  const i18n = { language: 'en-US' }
  return {
    useTranslation: (namespace: string | string[] = 'admin') => {
      const first = Array.isArray(namespace) ? namespace[0] : namespace
      let t = cache.get(first)
      if (!t) { t = (key: string) => key.includes(':') ? key : `${first}:${key}`; cache.set(first, t) }
      return { t, i18n }
    },
    Trans: ({ children }: { children: unknown }) => children,
  }
})

export { api, snack }

export const list = (items: unknown[]) => ({ items, total: items.length, page: 1, page_size: 25 })
export const server = { id: 1, name: 'server', url: 'https://panel.example.test', panel_type: '3xui', auth_method: 'token', has_api_token: true, capabilities: ['inbound.read', 'inbound.update', 'inbound.create', 'inbound.enable'], enabled: true }
export const node = { id: 1, panel_id: 1, inbound_id: 1, panel_name: 'server', display_name: 'old-name', server_address: 'proxy.example.test', region: 'US', tags: [], sort_order: 0, enabled: true, protocol: 'vless', desired_protocol: 'vless', desired_port: 443, observed_protocol: 'vless', observed_port: 443, endpoint_in_sync: true, flow: '', relays: [], cert_source: 'manual', cert_id: 0 }

const defaults: Record<string, unknown> = {
  '/admin/servers': list([server]),
  '/admin/nodes': list([]),
  '/admin/settings/ui': {},
  '/admin/transit/probe-settings': { settings: {}, defaults: {}, effective: {} },
}

/** GET answers by URL (the defaults above plus `overrides`); an unlisted URL fails the test loudly. */
export function installReads(overrides: Record<string, unknown> = {}) {
  api.get.mockImplementation(async (url: string) => {
    const values = { ...defaults, ...overrides }
    if (!(url in values)) throw new Error(`Unexpected GET ${url}`)
    const v = values[url]
    if (v instanceof Error) throw v
    return { data: v }
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  installReads()
  api.put.mockResolvedValue({ data: {} })
  api.post.mockResolvedValue({ data: {} })
})
afterEach(cleanup)

/**
 * Renders `page` at `entry` in a data router with the confirm host, in StrictMode (the install-command tests rely on its
 * double mount), with a query client of its own so no cached rows leak between tests. The server pages the list leads to
 * (a server, a new one, an installation) are routed at their real addresses, so a test can follow the list into them.
 */
export function mount(page: ReactElement, entry = '/admin/servers') {
  const router = createMemoryRouter([
    { path: '/admin/servers/new', element: <ServerCreateRoute /> },
    { path: '/admin/servers/:id', element: <ServerRoute /> },
    { path: '/admin/servers/:id/install', element: <ServerInstallRoute /> },
    { path: '*', element: page },
  ], { initialEntries: [entry] })
  const client = makeTestQueryClient()
  const view = render(
    <StrictMode>
      <QueryClientProvider client={client}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>
    </StrictMode>,
  )
  return { router, client, ...view }
}
export const mountWithClient = mount
