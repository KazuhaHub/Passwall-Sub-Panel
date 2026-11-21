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

// Shared set-up of the rule-set and template page tests (not part of the app): the request client is mocked, the REAL
// English strings are loaded, the CodeMirror editor is a textarea (CodeMirror is irrelevant here), toasts are
// recorded, and the page runs in a data router like in the app.
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
const toast = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/feedback/toasts', () => ({ pushToast: toast, pushSnack: toast }))
vi.mock('@/components/CodeEditor', () => ({
  default: ({ value, onChange, ariaLabel }: { value: string; onChange: (v: string) => void; ariaLabel?: string }) =>
    <textarea aria-label={ariaLabel ?? 'code'} value={value} onChange={e => onChange(e.target.value)} />,
}))

i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)

export { api, toast }
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
  api.put.mockImplementation(async (_url: string, body: unknown) => ({ data: body }))
})
afterEach(cleanup)

export function mountPage(page: ReactElement, path: string, entry = path) {
  const router = createMemoryRouter([{ path, element: page }, { path: '/elsewhere', element: <div>elsewhere</div> }], { initialEntries: [entry] })
  const client = makeTestQueryClient()
  const view = render(<QueryClientProvider client={client}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
  return { router, client, ...view }
}
