import type { ReactElement } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { cleanup, render, type RenderResult } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

import zhAdmin from '@/locales/zh-CN/admin.json'
import zhNav from '@/locales/zh-CN/nav.json'
import zhCommon from '@/locales/zh-CN/common.json'
import enAdmin from '@/locales/en-US/admin.json'
import enNav from '@/locales/en-US/nav.json'
import enCommon from '@/locales/en-US/common.json'
import { flatten, type Nested } from '@/i18n/options'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
import { makeTestQueryClient } from './queryTestUtils'

// THE HARNESS FOR THE ACCESS-CONTROL PAGE'S TESTS (plan §7.7). The same shape as
// the risk center's: a DATA router (createMemoryRouter, because useBlocker needs
// one), the REAL zh-CN or en-US bundles behind t (so a key the screen asks for
// and the bundle lacks shows as its raw key and fails the assertion), and the
// pieces that cannot run in jsdom replaced: the confirm host, the snackbar and
// CodeMirror. Import it for its side effects (the vi.mock calls) and its helpers.
//
//   import { renderAccess, confirmMock, snack, setTestLang } from '@/test/accessControlHarness'
//
// API modules are the test's to mock (`vi.mock('@/api/accessControl')`).

type Dict = Record<string, string>
const bundles: Record<string, Record<string, Dict>> = {
  'zh-CN': {
    admin: flatten(zhAdmin as Nested) as Dict, nav: flatten(zhNav as Nested) as Dict, common: flatten(zhCommon as Nested) as Dict,
  },
  'en-US': {
    admin: flatten(enAdmin as Nested) as Dict, nav: flatten(enNav as Nested) as Dict, common: flatten(enCommon as Nested) as Dict,
  },
}

const lang = vi.hoisted(() => ({ current: 'zh-CN' }))
/** Which bundle t reads. Reset to zh-CN after every test. */
export function setTestLang(next: 'zh-CN' | 'en-US') { lang.current = next }

export function translate(key: string, opts?: Record<string, unknown>, defaultNs = 'admin'): string {
  const colon = key.indexOf(':')
  const ns = colon > 0 ? key.slice(0, colon) : defaultNs
  const flat = colon > 0 ? key.slice(colon + 1) : key
  const dict = bundles[lang.current]?.[ns] ?? {}
  let raw: string | undefined
  if (opts && typeof opts.count === 'number') {
    // i18next: English asks for _one and _other, Chinese for _other only.
    const form = lang.current.startsWith('en') && opts.count === 1 ? 'one' : 'other'
    raw = dict[`${flat}_${form}`] ?? dict[`${flat}_other`] ?? dict[flat]
  } else {
    raw = dict[flat]
  }
  if (raw === undefined) return typeof opts?.defaultValue === 'string' ? opts.defaultValue : key
  return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (opts && name in opts ? String(opts[name]) : m))
}

vi.mock('react-i18next', () => ({
  useTranslation: (namespace: string | string[] = 'admin') => ({
    t: (key: string, opts?: Record<string, unknown>) => translate(key, opts, Array.isArray(namespace) ? namespace[0] : namespace),
    i18n: { get language() { return lang.current } },
  }),
  Trans: ({ children }: { children: ReactElement }) => children,
}))
vi.mock('@/i18n', () => ({ default: { t: (key: string, opts?: Record<string, unknown>) => translate(key, opts), get language() { return lang.current } } }))

const hoistedConfirm = vi.hoisted(() => vi.fn(() => Promise.resolve(true)))
vi.mock('@/components/ConfirmHost', () => ({ confirm: hoistedConfirm, default: () => null }))
/** `confirm()` of the confirm host: resolves true unless a test says otherwise. */
export const confirmMock = hoistedConfirm

const hoistedSnack = vi.hoisted(() => vi.fn())
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: hoistedSnack, default: () => null }))
/** `pushSnack`. */
export const snack = hoistedSnack

vi.mock('@/components/CodeEditor', async () => {
  const { forwardRef, useImperativeHandle } = await import('react')
  return {
    default: forwardRef(function CodeEditorStub({ value, onChange, readOnly }: {
      value: string; onChange: (value: string) => void; readOnly?: boolean
    }, ref) {
      useImperativeHandle(ref, () => ({ revealLine: vi.fn() }))
      return <textarea aria-label="code" value={value} readOnly={readOnly} onChange={e => onChange(e.target.value)} />
    }),
  }
})

afterEach(() => {
  cleanup()
  lang.current = 'zh-CN'
  confirmMock.mockClear()
  confirmMock.mockImplementation(() => Promise.resolve(true))
  snack.mockClear()
})

export interface RenderAccessOptions {
  /** The route to start on, with its query string. */
  route?: string
  role?: 'admin' | 'operator' | 'user'
  mode?: 'light' | 'dark'
  client?: QueryClient
}

export function renderAccess(
  ui: ReactElement,
  { route = '/admin/access-control', role = 'admin', mode = 'light', client = makeTestQueryClient() }: RenderAccessOptions = {},
): RenderResult & { client: QueryClient; router: ReturnType<typeof createMemoryRouter> } {
  useAuthStore.setState({ role, userId: 1, authEpoch: 1 })
  const theme = createAppTheme({ mode, sourceColor: '#6750a4', language: lang.current })
  const router = createMemoryRouter(
    [
      { path: '/admin/access-control', element: ui },
      { path: '/admin/dashboard', element: <p data-testid="dashboard">dashboard</p> },
      { path: '*', element: ui },
    ],
    { initialEntries: [route] },
  )
  const result = render(
    <QueryClientProvider client={client}>
      <ThemeProvider theme={theme}><RouterProvider router={router} /></ThemeProvider>
    </QueryClientProvider>,
  )
  return Object.assign(result, { client, router })
}

/** The location the router is at, as path + query. */
export function whereIs(router: { state: { location: { pathname: string; search: string } } }): string {
  return router.state.location.pathname + router.state.location.search
}
