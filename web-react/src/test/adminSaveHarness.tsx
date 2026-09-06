import { StrictMode, type ReactElement } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
const snack = vi.hoisted(() => vi.fn())

vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack }))
vi.mock('@/i18n', () => ({ default: { t: (key: string) => key, language: 'en-US' } }))
vi.mock('react-i18next', () => ({
  useTranslation: (namespace: string | string[] = 'admin') => ({
    t: (key: string) => key.includes(':') ? key : `${Array.isArray(namespace) ? namespace[0] : namespace}:${key}`,
    i18n: { language: 'en-US' },
  }),
  Trans: ({ children }: { children: ReactElement }) => children,
}))
vi.mock('@/components/CodeEditor', () => ({
  default: ({ value, onChange }: { value: string; onChange: (value: string) => void }) =>
    <textarea aria-label="code" value={value} onChange={e => onChange(e.target.value)} />,
}))

export const list = (items: unknown[]) => ({ items, total: items.length, page: 1, page_size: 25 })
export const group = { id: 1, slug: 'test', name: 'group', tag_filter: { all: true, tags: [], mode: 'all' }, members: 0, remark: '', require_2fa: false }
export const server = { id: 1, name: 'server', url: 'https://panel.example.test', panel_type: '3xui', auth_method: 'token', has_api_token: true, capabilities: ['inbound.read', 'inbound.update', 'inbound.create', 'inbound.enable'], enabled: true }
export const node = { id: 1, panel_id: 1, inbound_id: 1, panel_name: 'server', display_name: 'old-name', server_address: 'proxy.example.test', region: 'US', tags: [], sort_order: 0, enabled: true, protocol: 'vless', flow: '', relays: [], cert_source: 'manual', cert_id: 0 }
export const user = { id: 2, upn: 'user@example.test', display_name: 'old-name', email: 'user@example.test', group_id: 1, role: 'user', enabled: true, traffic_limit_bytes: 0, traffic_reset_period: 'monthly', ip_limit: 0, device_limit: 0, emergency_used_count: 0 }

const defaults: Record<string, unknown> = {
  '/admin/rules': list([]),
  '/admin/templates': list([]),
  '/admin/groups': list([group]),
  '/admin/groups/1/scope-settings': { overrides: {}, overridable: [] },
  '/admin/nodes': list([]),
  '/admin/nodes/separator': list([]),
  '/admin/servers': list([server]),
  '/admin/users': list([]),
  '/admin/traffic/top': { items: [] },
  '/admin/settings/ui': {},
  '/admin/certs': { certs: [] },
  '/admin/dns-credentials': { credentials: [] },
  '/admin/acme-accounts': { accounts: [] },
  '/admin/dns-providers': { providers: [] },
  '/admin/acme-key-types': { key_types: ['EC256'] },
}

export function installReads(overrides: Record<string, unknown> = {}) {
  api.get.mockImplementation(async (url: string) => {
    const values = { ...defaults, ...overrides }
    if (!(url in values)) throw new Error(`Unexpected GET ${url}`)
    return { data: values[url] }
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

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

export function mount(page: ReactElement) {
  return render(
    <StrictMode>
      <MemoryRouter>
        <ThemeProvider theme={theme}>{page}</ThemeProvider>
      </MemoryRouter>
    </StrictMode>,
  )
}

export async function editRow(name = 'old-name', icon = 'EditOutlinedIcon') {
  const cell = await screen.findByText(name)
  const row = cell.closest('tr')!
  const button = within(row).getByTestId(icon).closest('button')!
  await waitFor(() => expect(button.disabled).toBe(false))
  fireEvent.click(button)
  return screen.findByRole('dialog')
}

export { api, snack }
