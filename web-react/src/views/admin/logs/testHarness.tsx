import { type ReactElement } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { afterEach, beforeEach, vi } from 'vitest'
import ConfirmHost from '@/components/ConfirmHost'
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import { useAuthStore } from '@/stores/auth'
import { makeTestQueryClient } from '@/test/queryTestUtils'

// Shared set-up of the logs, sync tasks and node issues tests: the request client is mocked, the REAL
// English strings are loaded (common from initI18n, admin added here), and the page runs in a data
// router like in the app. Not part of the app.
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const toast = vi.hoisted(() => vi.fn())
vi.mock('@/components/feedback/toasts', () => ({ pushSnack: toast, pushToast: toast }))

i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)

export { api, toast }
export const list = (items: unknown[], total = items.length) => ({ items, total, page: 1, page_size: 25 })

/** Answers each URL from `values`; an Error value rejects. A URL nobody listed fails the test loudly. */
export function installReads(values: Record<string, unknown>) {
  api.get.mockImplementation(async (url: string) => {
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
})
afterEach(cleanup)

export function mountPage(page: ReactElement, entry: string, path: string) {
  const router = createMemoryRouter([{ path, element: page }], { initialEntries: [entry] })
  const client = makeTestQueryClient()
  const view = render(<QueryClientProvider client={client}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
  return { router, client, ...view }
}

/** The params of the last GET to `url`. */
export function lastParams(url: string): Record<string, unknown> | undefined {
  return [...api.get.mock.calls].reverse().find(([u]) => u === url)?.[1]?.params
}
