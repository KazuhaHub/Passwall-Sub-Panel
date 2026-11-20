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

// Shared set-up of the nodes page tests: the request client is mocked, the REAL English strings are loaded
// (common from initI18n, admin added here), and the page runs in a data router like in the app. Not part of the app.
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)

export { api }
export const list = (items: unknown[]) => ({ items, total: items.length, page: 1, page_size: 200 })

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

export function mountPage(page: ReactElement, entry = '/admin/nodes') {
  const router = createMemoryRouter([{ path: '/admin/nodes', element: page }, { path: '/admin/nodes/:id', element: <div>node page</div> }], { initialEntries: [entry] })
  const client = makeTestQueryClient()
  const view = render(<QueryClientProvider client={client}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
  return { router, client, ...view }
}
