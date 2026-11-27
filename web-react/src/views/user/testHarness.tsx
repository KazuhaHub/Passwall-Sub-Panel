import { type ReactElement } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, vi } from 'vitest'
import ConfirmHost from '@/components/ConfirmHost'
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enUser from '@/locales/en-US/user.json'
import { useAuthStore } from '@/stores/auth'
import { makeTestQueryClient } from '@/test/queryTestUtils'

// Shared set-up of the user portal tests (not part of the app): the request client is mocked, the REAL English strings
// are loaded (common from initI18n, user added here) so a test reads what a person reads and a missing key shows up as a
// raw key, and the toast and clipboard doors are replaced by spies.
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn() }))
const snack = vi.hoisted(() => vi.fn())
const copy = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/feedback/toasts', async importOriginal => ({ ...(await importOriginal<object>()), pushSnack: snack }))
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: copy }))

i18n.addResourceBundle('en-US', 'user', flatten(enUser as Nested), true, true)

export { api, snack, copy }

export const profile = {
  id: 1,
  upn: 'user@example.test',
  display_name: 'User',
  role: 'user',
  enabled: true,
  can_change_password: true,
  can_edit_personal_rules: true,
  sub_url: 'https://sub.example.test/secret-token',
  uuid: 'uuid-1',
  traffic_limit_bytes: 0,
  traffic_reset_period: 'monthly',
  expire_at: null,
}

export const zeroUsage = { user_id: 1, permanent_total_bytes: 0, period_used_bytes: 0, today_used_bytes: 0 }

/** Serves the reads the page makes; a test overrides only what it is about. A URL it does not list fails loudly. */
export function serve(over: Record<string, unknown> = {}, prof: Record<string, unknown> = {}) {
  const values: Record<string, unknown> = {
    '/user/me': { ...profile, ...prof },
    '/user/me/traffic': zeroUsage,
    '/user/me/traffic/history': { points: [], items: [] },
    '/user/me/server-status': { nodes: [] },
    '/user/me/rules': { personal_rules: '' },
    ...over,
  }
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
  useAuthStore.setState({ role: 'user', userId: 1, hasToken: true })
  api.post.mockResolvedValue({ data: {} })
  api.put.mockResolvedValue({ data: {} })
  serve()
})
afterEach(cleanup)

export function mountMe(page: ReactElement, entry = '/') {
  const client = makeTestQueryClient()
  const view = render(
    <QueryClientProvider client={client}><ConfirmHost /><MemoryRouter initialEntries={[entry]}>{page}</MemoryRouter></QueryClientProvider>,
  )
  return { client, ...view }
}

/** Opens the ⋮ account menu beside the name and chooses the entry that starts with `name`; the dialog it opens is the caller's to find. */
export async function chooseSecurity(name: string) {
  const trigger = await screen.findByRole('button', { name: 'Account security' })
  fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
  fireEvent.mouseDown(trigger, { button: 0 })
  fireEvent.click(trigger)
  fireEvent.click(await screen.findByRole('menuitem', { name: new RegExp(`^${name}`) }))
}
