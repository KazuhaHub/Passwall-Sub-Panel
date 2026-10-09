/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AppRouter from '@/router/AppRouter'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
const api = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const snack = vi.hoisted(() => vi.fn())
const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
import GlobalExceptionDialog from './GlobalExceptionDialog'
const P = 'admin:access_control.exception.'
beforeEach(() => { vi.clearAllMocks(); useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 }); api.post.mockResolvedValue({ data: { policy_id: 12, list_id: 7, entry: 'domain:example.test', created: { policy_id: 12, list_id: 7 } } }); api.get.mockResolvedValue({ data: { group: null, exemption: null } }); confirmation.mockResolvedValue(false) })
afterEach(cleanup)
function mount(userId?: number, target = 'sub.example.test') {
  const onClose = vi.fn(), router = createMemoryRouter([{ path: '/', element: <GlobalExceptionDialog target={target} userId={userId} onClose={onClose} /> }])
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><QueryClientProvider client={new QueryClient()}><AppRouter router={router} /></QueryClientProvider></ThemeProvider>)
  return onClose
}
it('keeps the global exception title separate from its close button', () => {
  mount()
  expect(within(screen.getByRole('heading', { name: `${P}title` })).queryByRole('button')).toBeNull()
})
it.each([`${P}global_summary`, 'common:actions.cancel', `${P}save`])('makes the global exception %s target touch-accessible without a write', name => {
  mount()
  const dialog = screen.getByRole('dialog', { name: `${P}title` })
  const style = getComputedStyle(within(dialog).getByRole('button', { name }))
  expect(parseFloat(style.minHeight) || 0).toBeGreaterThanOrEqual(44)
  expect(parseFloat(style.minWidth) || 0).toBeGreaterThanOrEqual(44)
  if (name === `${P}global_summary`) {
    fireEvent.click(screen.getByRole('button', { name }))
    expect(screen.getByRole('dialog', { name })).toBeTruthy()
    expect(screen.getByText(`${P}global_detail`)).toBeTruthy()
  }
  expect(api.post).not.toHaveBeenCalled()
})
it('saves a global site exception once and reports the actual first-use result', async () => {
  const close = mount(); expect(screen.getByText(`${P}global_summary`)).toBeTruthy(); expect(screen.getByText(`${P}first_use`)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: `${P}save` })); await waitFor(() => expect(close).toHaveBeenCalledOnce())
  expect(api.post).toHaveBeenCalledWith('/admin/dest/exceptions', { target: 'sub.example.test', match: 'site', scope: 'global' }, { _skipErrorToast: true })
  expect(snack).toHaveBeenCalledWith(`${P}created`, 'success'); expect(confirmation).not.toHaveBeenCalled()
})
it('uses one-address matching for IPs and does not fabricate an entire IP site', async () => {
  const close = mount(undefined, '192.0.2.7'); expect(screen.queryByRole('button', { name: `${P}site` })).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: `${P}save` })); await waitFor(() => expect(close).toHaveBeenCalled())
  expect(api.post.mock.calls[0][1].match).toBe('host')
})
it('keeps conflicts and dirty choices until explicit cancellation, preventing pending duplicates and close', async () => {
  let reject!: (error: unknown) => void; api.post.mockImplementation(() => new Promise((_, fail) => { reject = fail }))
  const close = mount(); fireEvent.click(screen.getByRole('button', { name: `${P}host` })); fireEvent.click(screen.getByRole('button', { name: `${P}save` })); fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
  expect(close).not.toHaveBeenCalled(); await waitFor(() => expect(api.post).toHaveBeenCalledOnce())
  reject({ response: { status: 409, data: { error: 'dest_exception_conflict' } } }); await screen.findByText(`${P}failed`)
  expect(screen.getByRole('button', { name: `${P}host` }).getAttribute('aria-pressed')).toBe('true')
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' })); await waitFor(() => expect(confirmation).toHaveBeenCalledOnce()); expect(close).not.toHaveBeenCalled()
})
it('requires a selected account for exemption and reuses the guarded reason/expiry dialog', async () => {
  mount(13); fireEvent.click(screen.getByRole('button', { name: `${P}account_only` }))
  await screen.findByRole('dialog', { name: 'admin:access_control.exemptions.add' })
  expect(screen.getByRole('textbox', { name: 'admin:access_control.exemptions.account' }).getAttribute('readonly')).not.toBeNull()
  expect(screen.getByText(`${P}account_summary`)).toBeTruthy()
  expect(api.post).not.toHaveBeenCalled()
})
it('closes the exception flow after saving the account exemption instead of reopening global choices', async () => {
  const close = mount(13); fireEvent.click(screen.getByRole('button', { name: `${P}account_only` }))
  fireEvent.change(await screen.findByRole('textbox', { name: 'admin:access_control.exemptions.reason' }), { target: { value: 'Diagnostics' } })
  fireEvent.click(screen.getByRole('button', { name: 'admin:access_control.exemptions.add' }))
  await waitFor(() => expect(close).toHaveBeenCalledOnce())
  expect(api.post.mock.calls[0][0]).toBe('/admin/dest/exemptions')
})
