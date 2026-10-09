/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter } from 'react-router'
import { createInstance } from 'i18next'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AppRouter from '@/router/AppRouter'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
import type { DestinationExemptionView, DestinationUserAccessView } from '@/api/accessControl'
import en from '@/locales/en-US/admin.json'
import enCommon from '@/locales/en-US/common.json'
import zh from '@/locales/zh-CN/admin.json'
import zhCommon from '@/locales/zh-CN/common.json'
import * as copy from '../../accessControl/confirmCopy'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn() }))
const translation = vi.hoisted(() => ({ instance: null as unknown as ReturnType<typeof createInstance> }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translation.instance.t.bind(translation.instance), i18n: translation.instance }) }))
import AccessTab from './AccessTab'

const at = 1791260000000
const row: DestinationExemptionView = { user_id: 13, upn: 'alice@example.invalid', reason: 'Diagnostics', created_by: 42, created_by_upn: 'admin@example.invalid', created_at: at - 10000, expires_at: at + 86400000, expired: false }
const view = (exemption: DestinationExemptionView | null = row): DestinationUserAccessView => ({ group: { id: 2, name: 'Students', mode: 'allowlist', stage: 'trial' }, exemption, hits_available: null, recent_hits: null, usage_available: null, usage_nodes: null })
beforeEach(() => {
  vi.clearAllMocks(); vi.spyOn(Date, 'now').mockReturnValue(at)
  translation.instance = createInstance()
  void translation.instance.init({ lng: 'en-US', fallbackLng: 'en-US', initAsync: false, resources: { 'en-US': { admin: en, common: enCommon }, 'zh-CN': { admin: zh, common: zhCommon } }, interpolation: { escapeValue: false } })
  useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 })
  api.get.mockResolvedValue({ data: view() }); api.delete.mockResolvedValue({ data: {} }); confirmation.mockResolvedValue(false)
})
afterEach(() => { cleanup(); vi.restoreAllMocks() })
function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createMemoryRouter([{ path: '/admin/access-control', element: <AccessTab userId={13} upn={row.upn!} /> }], { initialEntries: ['/admin/access-control'] })
  const tree = () => <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><QueryClientProvider client={client}><AppRouter router={router} /></QueryClientProvider></ThemeProvider>
  const result = render(tree())
  return { router, rerender: () => result.rerender(tree()) }
}
it('formats expiry with the interface locale and updates it without another read', async () => {
  await translation.instance.changeLanguage('zh-CN')
  const mounted = mount()
  const stamp = (language: string) => new Intl.DateTimeFormat(language, { dateStyle: 'medium', timeStyle: 'medium' }).format(row.expires_at!)
  await screen.findByText(translation.instance.t('admin:access_control.account.expires_at', { time: stamp('zh-CN') }))
  await act(async () => { await translation.instance.changeLanguage('en-US'); mounted.rerender() })
  await screen.findByText(`Expires ${stamp('en-US')}`)
  expect(api.get).toHaveBeenCalledOnce()
})
it('uses the successful read time when an exemption expires during loading', async () => {
  let finish!: (value: unknown) => void
  api.get.mockImplementation(() => new Promise(resolve => { finish = resolve }))
  mount(); await waitFor(() => expect(api.get).toHaveBeenCalledOnce())
  vi.mocked(Date.now).mockReturnValue(at + 1200000)
  finish({ data: view({ ...row, expires_at: at + 600000 }) })
  await screen.findByText(en.access_control.exemptions.expired)
  expect(api.delete).not.toHaveBeenCalled()
})
it.each([null, at - 1])('shows permanent and expired exemptions without cancelling them (expiry=%s)', async expires_at => {
  api.get.mockResolvedValue({ data: view({ ...row, expires_at }) }); mount()
  await screen.findByText(expires_at === null ? en.access_control.exemptions.expiry_never : en.access_control.exemptions.expired)
  expect(api.delete).not.toHaveBeenCalled()
})
it('uses the shared cancellation copy with the honest unavailable-ETA fallback', async () => {
  const factory = vi.spyOn(copy, 'cancelExemptionCopy')
  mount(); fireEvent.click(await screen.findByRole('button', { name: 'Cancel exemption' }))
  await waitFor(() => expect(confirmation).toHaveBeenCalledOnce())
  expect(factory).toHaveBeenCalledWith(expect.any(Function), row.upn)
  expect(confirmation.mock.calls[0][0]).toMatchObject({ title: `Cancel the exemption for ${row.upn}?`, message: 'Destination policies and allowlists will apply to this account again, expected within a few minutes.', confirmText: 'Cancel exemption' })
  expect(confirmation.mock.calls[0][0].destructive).toBeUndefined()
  expect(api.delete).not.toHaveBeenCalled()
})
it('admits one cancellation and keeps its action disabled while the write is pending', async () => {
  let finish!: (value: unknown) => void
  confirmation.mockResolvedValue(true); api.delete.mockImplementation(() => new Promise(resolve => { finish = resolve }))
  mount(); const action = await screen.findByRole('button', { name: 'Cancel exemption' })
  fireEvent.click(action); fireEvent.click(action)
  await waitFor(() => expect(api.delete).toHaveBeenCalledOnce())
  expect(action.hasAttribute('disabled')).toBe(true)
  finish({ data: {} }); await waitFor(() => expect(action.hasAttribute('disabled')).toBe(false))
  expect(api.delete.mock.calls[0][0]).toBe('/admin/dest/exemptions/13')
})
it('holds same-page account navigation until cancellation settles', async () => {
  let finish!: (value: unknown) => void
  confirmation.mockResolvedValue(true); api.delete.mockImplementation(() => new Promise(resolve => { finish = resolve }))
  const { router } = mount()
  fireEvent.click(await screen.findByRole('button', { name: 'Cancel exemption' }))
  await waitFor(() => expect(api.delete).toHaveBeenCalledOnce())
  await act(async () => { void router.navigate('/admin/access-control?user=14') })
  expect(router.state.location.search).toBe('')
  finish({ data: {} }); await waitFor(() => expect(router.state.location.search).toBe('?user=14'))
})
it('releases a failed cancellation, keeps the exemption and permits a new declined attempt', async () => {
  confirmation.mockResolvedValueOnce(true).mockResolvedValue(false)
  api.delete.mockRejectedValueOnce(new Error('offline'))
  mount(); const action = await screen.findByRole('button', { name: 'Cancel exemption' })
  fireEvent.click(action)
  await screen.findByText(/Could not cancel the exemption:/)
  await waitFor(() => expect(action.hasAttribute('disabled')).toBe(false))
  expect(screen.getByText(row.reason)).toBeTruthy()
  fireEvent.click(action); await waitFor(() => expect(confirmation).toHaveBeenCalledTimes(2))
  await waitFor(() => expect(action.hasAttribute('disabled')).toBe(false))
  expect(api.delete).toHaveBeenCalledOnce()
})
it('retries only the failed account read with a 44px action and no fabricated empty state', async () => {
  api.get.mockRejectedValueOnce(new Error('offline')); mount()
  await screen.findByText('Could not load this account’s destination access')
  expect(screen.queryByText('Not exempt')).toBeNull()
  const retry = screen.getByRole('button', { name: 'Retry' })
  expect(parseFloat(getComputedStyle(retry).minHeight)).toBeGreaterThanOrEqual(44)
  fireEvent.click(retry); await screen.findByText(row.reason)
  expect(api.get.mock.calls.map(([url]) => url)).toEqual(['/admin/dest/users/13', '/admin/dest/users/13'])
  expect(api.post).not.toHaveBeenCalled(); expect(api.delete).not.toHaveBeenCalled()
})
it.each([true, false])('provides 44px cancellation/add targets (exempt=%s)', async exempt => {
  api.get.mockResolvedValue({ data: view(exempt ? row : null) }); mount()
  const button = await screen.findByRole('button', { name: exempt ? 'Cancel exemption' : 'Exempt this account' })
  expect(parseFloat(getComputedStyle(button).minHeight)).toBeGreaterThanOrEqual(44)
  expect(parseFloat(getComputedStyle(button).minWidth)).toBeGreaterThanOrEqual(44)
})
it('opens the add dialog with the current account locked and keeps later-stage telemetry unread', async () => {
  api.get.mockResolvedValue({ data: { ...view(null), group: null } }); mount()
  await screen.findByText('No group'); await screen.findByText('Not exempt')
  fireEvent.click(screen.getByRole('button', { name: 'Exempt this account' }))
  const editor = await screen.findByRole('dialog', { name: 'Add exemption' })
  const account = within(editor).getByRole('textbox', { name: 'Account' }) as HTMLInputElement
  expect(account.value).toBe(row.upn); expect(account.readOnly).toBe(true)
  expect(api.get.mock.calls.every(([url]) => url === '/admin/dest/users/13')).toBe(true)
  expect(api.post).not.toHaveBeenCalled()
})
