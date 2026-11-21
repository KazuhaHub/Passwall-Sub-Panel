import { type ReactElement } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { afterEach, beforeEach, vi } from 'vitest'
import ConfirmHost from '@/components/ConfirmHost'
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import enAuth from '@/locales/en-US/auth.json'
import enLanguage from '@/locales/en-US/language.json'
import { useAuthStore } from '@/stores/auth'
import { makeTestQueryClient } from '@/test/queryTestUtils'

// Shared set-up of the tests of the language-pack, shared-port and legal-document pages: the request client is
// mocked, the REAL English strings are loaded behind t, toasts are recorded, and the page runs in a data router
// (useBlocker needs one) with the real confirm host mounted. Not part of the app.
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
const toast = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', () => ({ client: api }))
// Both doors record (message, severity); a test reads `toast.mock.calls`.
vi.mock('@/components/feedback/toasts', () => ({
  pushSnack: (message: string, severity?: string) => toast(message, severity),
  pushToast: (input: { message: string; severity?: string }) => toast(input.message, input.severity),
}))

for (const [ns, bundle] of [['admin', enAdmin], ['auth', enAuth], ['language', enLanguage]] as const) {
  i18n.addResourceBundle('en-US', ns, flatten(bundle as Nested), true, true)
}

export { api, toast }

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(cleanup)

/** Renders `page` at `route` in a data router with the confirm host, and returns the router for location assertions. */
export function mountAdmin(page: ReactElement, route = '/') {
  const router = createMemoryRouter([{ path: '*', element: page }], { initialEntries: [route] })
  const client = makeTestQueryClient()
  const view = render(<QueryClientProvider client={client}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
  return { router, client, ...view }
}

/** GET answers by URL; an unlisted URL fails the test loudly. A value that is an Error rejects. */
export function installReads(values: Record<string, unknown>) {
  api.get.mockImplementation(async (url: string) => {
    if (!(url in values)) throw new Error(`Unexpected GET ${url}`)
    const v = values[url]
    if (v instanceof Error) throw v
    return { data: v }
  })
}
